//go:build linux || darwin

// Package startrequest retains immutable start requests and one-time worker claims.
// A retained claim is never permission to resume after worker loss.
package startrequest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

const Version = "operator.dev/start-request/v1alpha1"
const Limit = 256 << 10

var ErrRecord = errors.New("invalid or incomplete start request")
var ErrConflict = errors.New("start key already binds different inputs")
var ErrRetired = errors.New("start request retired; execution cannot resume")
var ErrClaimed = errors.New("start request already claimed; execution cannot resume")
var idPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,127}$`)

type Request struct {
	APIVersion        string            `json:"api_version"`
	ConfigurationFile string            `json:"configuration_file"`
	StateRoot         string            `json:"state_root"`
	DockerEndpoint    string            `json:"docker_endpoint"`
	InputsFingerprint string            `json:"inputs_fingerprint"`
	Selection         hostrun.Selection `json:"selection"`
}

// New binds only the already-frozen host selection and resolved installation.
func New(inputs *hostrun.InstalledInputs) (Request, error) {
	if inputs == nil {
		return Request{}, ErrRecord
	}
	paths := inputs.Installation()
	r := Request{APIVersion: Version, ConfigurationFile: paths.ConfigFile, StateRoot: paths.StateRoot, DockerEndpoint: paths.DockerEndpoint, InputsFingerprint: inputs.Fingerprint(), Selection: inputs.Selection()}
	return r, r.validate()
}

func (r Request) validate() error {
	if r.APIVersion != Version || r.Selection.Validate() != nil || !absolute(r.ConfigurationFile) || !absolute(r.StateRoot) || campaign.ValidateDockerEndpoint(r.DockerEndpoint) != nil || !digestPattern.MatchString(r.InputsFingerprint) {
		return ErrRecord
	}
	return nil
}
func (r Request) Defaults() hostconfig.Paths {
	return hostconfig.Paths{ConfigFile: r.ConfigurationFile, StateRoot: r.StateRoot, DockerEndpoint: r.DockerEndpoint}
}

type Claim struct {
	RequestDigest string `json:"request_digest"`
	OwnerID       string `json:"owner_id"`
	ClaimedAt     string `json:"claimed_at"`
}

// Completion records worker completion, not experiment success or native-effect
// certainty. Detailed evidence and outcomes remain in the campaign journal.
type Completion struct {
	RequestDigest string `json:"request_digest"`
	OwnerID       string `json:"owner_id"`
	RecordedAt    string `json:"recorded_at"`
	Status        string `json:"status"` // finished or failed
	Code          string `json:"code"`   // fixed host code, never a private error string
}
type Retirement struct {
	RequestDigest string `json:"request_digest"`
	RecordedAt    string `json:"recorded_at"`
}

type Snapshot struct {
	Retired    *Retirement          `json:"retired,omitempty"`
	Service    *ServiceRegistration `json:"service,omitempty"`
	Request    Request              `json:"request"`
	Digest     string               `json:"request_digest"`
	Claim      *Claim               `json:"claim,omitempty"`
	Accepted   *hostrun.Receipt     `json:"accepted,omitempty"`
	Completion *Completion          `json:"completion,omitempty"`
}

func (s Snapshot) Phase() string {
	if s.Retired != nil {
		return "retired"
	}
	if s.Completion != nil {
		return s.Completion.Status
	}
	if s.Accepted != nil {
		return "accepted"
	}
	if s.Claim != nil {
		return "claimed"
	}
	return "submitted"
}

type Owner struct {
	lease    *campaign.RetentionLease
	root     *os.Root
	snapshot Snapshot
	mu       sync.Mutex
	closed   bool
}

func absolute(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && len(p) <= 4096 && strings.IndexFunc(p, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func privateDir(root *os.Root, name string) error {
	info, err := root.Lstat(name)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm()&0077 != 0 || (st.Uid != uint32(os.Geteuid()) && st.Uid != 0) {
		return ErrRecord
	}
	return nil
}
func syncDir(root *os.Root) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}
func open(stateRoot, id string, create bool) (*os.Root, string, error) {
	if !absolute(stateRoot) || !idPattern.MatchString(id) {
		return nil, "", ErrRecord
	}
	root, err := os.OpenRoot(stateRoot)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()
	if err := privateDir(root, "."); err != nil {
		return nil, "", err
	}
	if create {
		if err := root.Mkdir("starts", 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	if err := privateDir(root, "starts"); err != nil {
		return nil, "", err
	}
	starts, err := root.OpenRoot("starts")
	if err != nil {
		return nil, "", err
	}
	defer starts.Close()
	if create {
		if err := starts.Mkdir(id, 0700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, "", err
		}
	}
	if err := privateDir(starts, id); err != nil {
		return nil, "", err
	}
	if create {
		if err := errors.Join(syncDir(root), syncDir(starts)); err != nil {
			return nil, "", err
		}
	}
	group, err := starts.OpenRoot(id)
	return group, filepath.Join(stateRoot, "starts", id), err
}
func canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return contracts.Canonicalize(raw, Limit)
}

type envelope struct {
	Value  json.RawMessage `json:"value"`
	Digest string          `json:"digest"`
}

func encode(v any) ([]byte, string, error) {
	body, err := canonical(v)
	if err != nil {
		return nil, "", ErrRecord
	}
	digest := contracts.RawDigest(body)
	raw, err := canonical(envelope{body, digest})
	return raw, digest, err
}
func read(directory, name string, v any) (string, error) {
	raw, err := hostconfig.ReadPrivate(filepath.Join(directory, name), Limit)
	if err != nil {
		return "", err
	}
	var e envelope
	if interceptor.DecodeTypedBody(raw, &e, Limit) != nil || !digestPattern.MatchString(e.Digest) {
		return "", ErrRecord
	}
	d, err := contracts.CanonicalDigest(e.Value, Limit)
	if err != nil || d != e.Digest || interceptor.DecodeTypedBody(e.Value, v, Limit) != nil {
		return "", ErrRecord
	}
	return d, nil
}
func put(ctx context.Context, root *os.Root, name string, raw []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := root.OpenFile(name+".pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = ErrRecord
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := root.Link(name+".pending", name); err != nil {
		// A competing publisher won. This call's private temporary inode was
		// never linked into the final name, so removing it cannot erase a claim.
		if errors.Is(err, os.ErrExist) {
			return errors.Join(err, root.Remove(name+".pending"), syncDir(root))
		}
		return err
	}
	if err := root.Remove(name + ".pending"); err != nil {
		return err
	}
	return syncDir(root)
}
func pending(root *os.Root, name string) error {
	if _, err := root.Lstat(name + ".pending"); !errors.Is(err, os.ErrNotExist) {
		return ErrRecord
	}
	return nil
}

// Save uses the immutable start key as its directory key. Exact repeat requests
// return the original digest; changed inputs conflict. Partial publication remains
// uncertain and is never silently repaired or given another execution identity.
func Save(ctx context.Context, request Request) (Snapshot, error) {
	lease, err := campaign.AcquireRetentionLease(request.StateRoot, false)
	if err != nil {
		return Snapshot{}, err
	}
	defer lease.Close()
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := request.validate(); err != nil {
		return Snapshot{}, err
	}
	if err = campaign.CheckNotPurging(request.StateRoot, request.Selection.CampaignID); err != nil {
		return Snapshot{}, err
	}
	raw, digest, err := encode(request)
	if err != nil {
		return Snapshot{}, err
	}
	root, dir, err := open(request.StateRoot, request.Selection.StartRequestID, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer root.Close()
	var old Request
	got, readErr := read(dir, "request.json", &old)
	if readErr == nil {
		if pending(root, "request.json") != nil {
			return Snapshot{}, ErrRecord
		}
		if got != digest {
			return Snapshot{}, ErrConflict
		}
		return Read(request.StateRoot, request.Selection.StartRequestID)
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return Snapshot{}, readErr
	}
	if err := put(ctx, root, "request.json", raw); err != nil {
		return Snapshot{}, err
	}
	return Read(request.StateRoot, request.Selection.StartRequestID)
}

func Read(stateRoot, id string) (Snapshot, error) { return readSnapshot(stateRoot, id, false) }

func readSnapshot(stateRoot, id string, retiring bool) (Snapshot, error) {
	var s Snapshot
	root, dir, err := open(stateRoot, id, false)
	if err != nil {
		return s, err
	}
	defer root.Close()
	s.Digest, err = read(dir, "request.json", &s.Request)
	if err != nil {
		return s, err
	}
	if s.Request.validate() != nil || s.Request.StateRoot != stateRoot || s.Request.Selection.StartRequestID != id {
		return s, ErrRecord
	}
	for _, name := range []string{"request.json", "owner.json", "accepted.json", "completion.json", "retired.json", "service.json"} {
		if retiring && name == "retired.json" {
			continue
		}
		if err := pending(root, name); err != nil {
			return s, err
		}
	}
	var owner Claim
	_, err = read(dir, "owner.json", &owner)
	if err == nil {
		if owner.RequestDigest != s.Digest || !idPattern.MatchString(owner.OwnerID) || !timestamp(owner.ClaimedAt) {
			return s, ErrRecord
		}
		s.Claim = &owner
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	var accepted hostrun.Receipt
	_, err = read(dir, "accepted.json", &accepted)
	if err == nil {
		if s.Claim == nil || !matches(s, accepted) {
			return s, ErrRecord
		}
		s.Accepted = &accepted
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	var completion Completion
	_, err = read(dir, "completion.json", &completion)
	if err == nil {
		if s.Claim == nil || completion.RequestDigest != s.Digest || completion.OwnerID != s.Claim.OwnerID || !timestamp(completion.RecordedAt) || !codePattern.MatchString(completion.Code) || (completion.Status != "finished" && completion.Status != "failed") || (completion.Status == "finished" && s.Accepted == nil) {
			return s, ErrRecord
		}
		s.Completion = &completion
	} else if !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	if err := readAdministration(dir, &s); err != nil {
		return s, err
	}
	return s, nil
}
func timestamp(s string) bool {
	v, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && v.UTC().Format(time.RFC3339Nano) == s
}
func matches(s Snapshot, r hostrun.Receipt) bool {
	return r.APIVersion == "operator.dev/campaign-start/v1alpha1" && r.Status == "accepted" && r.CampaignID == s.Request.Selection.CampaignID && r.LaunchID == s.Request.Selection.LaunchID && r.StartRequestID == s.Request.Selection.StartRequestID && digestPattern.MatchString(r.ManifestDigest)
}

// ClaimOnce grants the first worker a one-time claim. A second worker, including
// one started after the first process died, receives no execution authority.
func ClaimOnce(ctx context.Context, stateRoot, id, expectedDigest string) (*Owner, error) {
	lease, err := campaign.AcquireRetentionLease(stateRoot, false)
	if err != nil {
		return nil, err
	}
	kept := false
	defer func() {
		if !kept {
			lease.Close()
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := Read(stateRoot, id)
	if err != nil {
		return nil, err
	}
	if s.Digest != expectedDigest {
		return nil, ErrConflict
	}
	if s.Retired != nil {
		return nil, ErrRetired
	}
	if s.Claim != nil || s.Completion != nil {
		return nil, ErrClaimed
	}
	root, _, err := open(stateRoot, id, false)
	if err != nil {
		return nil, err
	}
	claim := Claim{RequestDigest: s.Digest, OwnerID: termination.NewRequestID(), ClaimedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _, err := encode(claim)
	if err == nil {
		err = put(ctx, root, "owner.json", raw)
	}
	if err != nil {
		root.Close()
		if errors.Is(err, os.ErrExist) {
			return nil, ErrClaimed
		}
		return nil, err
	}
	s.Claim = &claim
	kept = true
	return &Owner{root: root, snapshot: s, lease: lease}, nil
}
func (o *Owner) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return nil
	}
	o.closed = true
	return errors.Join(o.root.Close(), o.lease.Close())
}
func (o *Owner) Accept(ctx context.Context, r hostrun.Receipt) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || o.snapshot.Completion != nil || !matches(o.snapshot, r) {
		return ErrRecord
	}
	if o.snapshot.Accepted != nil {
		if *o.snapshot.Accepted == r {
			return nil
		}
		return ErrConflict
	}
	raw, _, err := encode(r)
	if err == nil {
		err = put(ctx, o.root, "accepted.json", raw)
	}
	if err == nil {
		o.snapshot.Accepted = &r
	}
	return err
}
func (o *Owner) Finish(ctx context.Context, status, code string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed || (status != "finished" && status != "failed") || !codePattern.MatchString(code) || (status == "finished" && o.snapshot.Accepted == nil) {
		return ErrRecord
	}
	if old := o.snapshot.Completion; old != nil {
		if old.Status == status && old.Code == code {
			return nil
		}
		return ErrConflict
	}
	c := Completion{RequestDigest: o.snapshot.Digest, OwnerID: o.snapshot.Claim.OwnerID, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano), Status: status, Code: code}
	raw, _, err := encode(c)
	if err == nil {
		err = put(ctx, o.root, "completion.json", raw)
	}
	if err == nil {
		o.snapshot.Completion = &c
	}
	return err
}
