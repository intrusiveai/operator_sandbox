// Package campaign stores host-private launch identities and durable audit records.
// It does not launch containers or authorize execution from recovered records.
package campaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

const ManifestVersion = "operator.dev/run-manifest/v1alpha1"
const BindingVersion = "operator.dev/docker-binding/v1alpha1"
const ManifestLimit = 256 << 10

var (
	ErrInvalid = errors.New("invalid campaign record")
	ErrCorrupt = errors.New("campaign evidence is incomplete or corrupt")
	ErrClosed  = errors.New("campaign writer is closed")
	ErrActive  = errors.New("campaign writer is active")
	ErrQuota   = errors.New("campaign journal budget exhausted")
	ErrStorage = errors.New("campaign persistence failed")
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var dockerIDPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ContainerID is the host-assigned shared-contract identity, not a Docker ID.
// The actual Docker identity is saved separately after create and before start.
// All fields are required. Digests use jcs-v1 except image_digest (Docker image
// config ID) and release_record_digest (the release contract's existing recipe).
type RunManifest struct {
	APIVersion           string          `json:"api_version"`
	CampaignID           string          `json:"campaign_id"`
	LaunchID             string          `json:"launch_id"`
	ContainerID          string          `json:"container_id"`
	InitialRevision      int64           `json:"initial_revision"`
	CreatedAt            string          `json:"created_at"`
	HostPlatform         string          `json:"host_platform"`
	ImagePlatform        string          `json:"image_platform"`
	Transport            string          `json:"transport"`
	RuntimeProfile       string          `json:"runtime_profile"`
	ImageDigest          string          `json:"image_digest"`
	ReleaseRecordDigest  string          `json:"release_record_digest"`
	Contract             ContractPin     `json:"contract"`
	EngineContextDigest  string          `json:"engine_context_digest"`
	InputTreeDigest      string          `json:"input_tree_digest"`
	SkillSetDigest       string          `json:"skill_set_digest"`
	ScenarioBundleDigest string          `json:"scenario_bundle_digest"`
	HostPolicyDigest     string          `json:"host_policy_digest"`
	ModelProfileDigest   string          `json:"model_profile_digest"`
	Target               TargetBinding   `json:"target"`
	RemainingLimits      json.RawMessage `json:"remaining_limits"`
	HarnessLimits        json.RawMessage `json:"harness_limits"`
	Retention            Retention       `json:"retention"`
}

type ContractPin struct {
	Version          string `json:"version"`
	Digest           string `json:"digest"`
	CatalogDigest    string `json:"catalog_digest"`
	OperationsDigest string `json:"operations_digest"`
}

type TargetBinding struct {
	Adapter                    string `json:"adapter"`
	SessionID                  string `json:"session_id"`
	WorkerInstanceID           string `json:"worker_instance_id"`
	NativeFeedbackProfile      string `json:"native_feedback_profile"`
	CapabilitySourceDigest     string `json:"capability_source_digest"`
	CapabilityProjectionDigest string `json:"capability_projection_digest"`
}

// Journal budgets include event lines and their retained content, across every
// revision. They exclude artifacts/reports and are not physical disk reservations.
type Retention struct {
	Mode            string `json:"mode"`
	MaxJournalBytes int64  `json:"max_journal_bytes"`
	MaxSegmentBytes int64  `json:"max_segment_bytes"`
}

type DockerBinding struct {
	APIVersion        string            `json:"api_version"`
	CampaignID        string            `json:"campaign_id"`
	LaunchID          string            `json:"launch_id"`
	ContainerID       string            `json:"container_id"`
	RunManifestDigest string            `json:"run_manifest_digest"`
	Endpoint          string            `json:"endpoint"`
	DaemonID          string            `json:"daemon_id"`
	DockerContainerID string            `json:"docker_container_id"`
	ImageDigest       string            `json:"image_digest"`
	Labels            map[string]string `json:"labels"`
}

func (m RunManifest) DockerLabels() map[string]string {
	return map[string]string{
		"ai.intrusive.operator.campaign":  m.CampaignID,
		"ai.intrusive.operator.launch":    m.LaunchID,
		"ai.intrusive.operator.container": m.ContainerID,
	}
}

var installedCatalog = sync.OnceValues(func() (*contracts.Catalog, error) {
	return contracts.LoadCatalog(schemas.Files)
})

func validID(s string) bool     { return idPattern.MatchString(s) }
func validDigest(s string) bool { return digestPattern.MatchString(s) }
func validTime(s string) bool {
	t, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && t.UTC().Format(time.RFC3339Nano) == s
}

func (m RunManifest) Validate() error {
	if m.APIVersion != ManifestVersion || !validID(m.CampaignID) || !validID(m.LaunchID) || !dockerIDPattern.MatchString(m.ContainerID) ||
		m.InitialRevision < 0 || m.InitialRevision > contracts.MaxSafeInteger || !validTime(m.CreatedAt) ||
		m.RuntimeProfile != "operator-container/v1" || !versionPattern.MatchString(m.Contract.Version) || len(m.Contract.Version) > 128 {
		return ErrInvalid
	}
	parts := strings.Split(m.HostPlatform, "/")
	if len(parts) != 2 || (parts[0] != "linux" && parts[0] != "darwin") || (parts[1] != "amd64" && parts[1] != "arm64") || m.ImagePlatform != "linux/"+parts[1] {
		return ErrInvalid
	}
	if (parts[0] == "linux" && m.Transport != "fifo") || (parts[0] == "darwin" && m.Transport != "spool") {
		return ErrInvalid
	}
	for _, d := range []string{m.ImageDigest, m.ReleaseRecordDigest, m.Contract.Digest, m.Contract.CatalogDigest, m.Contract.OperationsDigest,
		m.EngineContextDigest, m.InputTreeDigest, m.SkillSetDigest, m.ScenarioBundleDigest, m.HostPolicyDigest, m.ModelProfileDigest,
		m.Target.CapabilitySourceDigest, m.Target.CapabilityProjectionDigest} {
		if !validDigest(d) {
			return ErrInvalid
		}
	}
	if m.Target.Adapter != "interceptor/v1" || !validID(m.Target.SessionID) || !validID(m.Target.WorkerInstanceID) {
		return ErrInvalid
	}
	switch m.Target.NativeFeedbackProfile {
	case "black-box", "diagnostic", "oracle-assisted":
	default:
		return ErrInvalid
	}
	if m.Retention.Mode != "manual-purge" || m.Retention.MaxSegmentBytes < MaxEventBytes || m.Retention.MaxSegmentBytes > 64<<20 ||
		m.Retention.MaxJournalBytes < m.Retention.MaxSegmentBytes || m.Retention.MaxJournalBytes > contracts.MaxSafeInteger {
		return ErrInvalid
	}
	c, err := installedCatalog()
	if err != nil {
		return err
	}
	if _, err = c.Validate(contracts.RemainingLimitsSchema, m.RemainingLimits, ManifestLimit); err != nil {
		return ErrInvalid
	}
	if _, err = c.Validate(contracts.HarnessLoopLimitsSchema, m.HarnessLimits, ManifestLimit); err != nil {
		return ErrInvalid
	}
	return nil
}

func (m RunManifest) Bytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return encode(m, ManifestLimit)
}

func ParseManifest(raw []byte) (RunManifest, error) {
	var m RunManifest
	if err := decode(raw, &m, ManifestLimit); err != nil {
		return m, err
	}
	return m, m.Validate()
}

func (b DockerBinding) validate(m RunManifest, manifestDigest string) error {
	if b.Validate() != nil || b.CampaignID != m.CampaignID || b.LaunchID != m.LaunchID || b.ContainerID != m.ContainerID ||
		b.RunManifestDigest != manifestDigest || b.ImageDigest != m.ImageDigest || !dockerIDPattern.MatchString(b.DockerContainerID) ||
		!validID(b.DaemonID) || len(b.Labels) != 3 {
		return ErrInvalid
	}
	for k, v := range m.DockerLabels() {
		if b.Labels[k] != v {
			return ErrInvalid
		}
	}
	return nil
}

// Validate checks a detached binding before passing it to Docker. ReadDockerBinding
// additionally verifies it against the durable campaign manifest.
func (b DockerBinding) Validate() error {
	if b.APIVersion != BindingVersion || !validID(b.CampaignID) || !validID(b.LaunchID) ||
		!dockerIDPattern.MatchString(b.ContainerID) || !dockerIDPattern.MatchString(b.DockerContainerID) ||
		!validDigest(b.RunManifestDigest) || !validDigest(b.ImageDigest) || !validID(b.DaemonID) || len(b.Labels) != 3 {
		return ErrInvalid
	}
	m := RunManifest{CampaignID: b.CampaignID, LaunchID: b.LaunchID, ContainerID: b.ContainerID}
	for k, v := range m.DockerLabels() {
		if b.Labels[k] != v {
			return ErrInvalid
		}
	}
	// The selected local Unix endpoint is frozen; mutable CLI contexts, TCP/SSH
	// routing, credentials and environment-derived endpoints cannot be persisted.
	u, err := url.Parse(b.Endpoint)
	if err != nil || u.Scheme != "unix" || u.Host != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		u.RawPath != "" || !strings.HasPrefix(u.Path, "/") || u.Path == "/" || path.Clean(u.Path) != u.Path ||
		strings.ContainsAny(u.Path, "\x00\r\n") || len(b.Endpoint) > 4096 || b.Endpoint != "unix://"+u.Path {
		return ErrInvalid
	}
	return nil
}

func encode(v any, limit int) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, ErrInvalid
	}
	return contracts.Canonicalize(raw, limit)
}

// Round-trip equality rejects missing required fields and null scalar coercions,
// as well as unknown fields. Host records use exact integer JSON spellings.
func decode(raw []byte, dst any, limit int) error {
	canonical, err := contracts.Canonicalize(raw, limit)
	if err != nil {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrInvalid
	}
	encoded, err := encode(dst, limit)
	if err != nil || !bytes.Equal(canonical, encoded) {
		return ErrInvalid
	}
	return nil
}
