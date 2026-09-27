//go:build linux || darwin

package hostrun

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"regexp"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

// Selection comes from a host-owned durable start request. IDs and the Operator
// version are assigned by the installed frontend; no bundle supplies these fields.
type Selection struct {
	RunDirectory     string   `json:"run_directory"`
	CampaignID       string   `json:"campaign_id"`
	LaunchID         string   `json:"launch_id"`
	ContainerID      string   `json:"container_id"`
	WorkerInstanceID string   `json:"worker_instance_id"`
	StartRequestID   string   `json:"start_request_id"`
	SkillDigests     []string `json:"skill_digests"`
	PromptMode       string   `json:"prompt_mode"`
	ReplacementFile  string   `json:"replacement_file"`
	AppendFiles      []string `json:"append_files"`
}

// InstalledRun keeps private clients alive through finalization and closes them
// after the single Run/Cancel operation. No credential value is exposed here.
type InstalledRun struct {
	session *Session
	cleanup func() error
	used    atomic.Bool
}

func (r *InstalledRun) Receipt() Receipt { return r.session.Receipt() }
func (r *InstalledRun) Run(ctx context.Context) (hostworker.Result, error) {
	if r == nil || !r.used.CompareAndSwap(false, true) {
		return hostworker.Result{}, ErrSession
	}
	result, err := r.session.Run(ctx)
	return result, errors.Join(err, r.cleanup())
}
func (r *InstalledRun) Cancel() error {
	if r == nil || !r.used.CompareAndSwap(false, true) {
		return ErrSession
	}
	return errors.Join(r.session.Cancel(), r.cleanup())
}

var selectedID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var selectedDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var fullHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate checks the closed host-selected identity and input options without I/O.
func (s Selection) Validate() error { return validateSelection(s) }

func validateSelection(s Selection) error {
	if !filepath.IsAbs(s.RunDirectory) || filepath.Clean(s.RunDirectory) != s.RunDirectory || !selectedID.MatchString(s.CampaignID) || !selectedID.MatchString(s.LaunchID) || !selectedID.MatchString(s.WorkerInstanceID) || !fullHex.MatchString(s.ContainerID) || !campaign.ValidStopRequest(s.StartRequestID, "start") || len(s.SkillDigests) > 16 || len(s.AppendFiles) > 16 {
		return ErrSession
	}
	seen := map[string]bool{}
	for _, digest := range s.SkillDigests {
		if !selectedDigest.MatchString(digest) || seen[digest] {
			return ErrSession
		}
		seen[digest] = true
	}
	if s.PromptMode != "default" && s.PromptMode != "extension" && s.PromptMode != "replacement" {
		return ErrSession
	}
	if (s.PromptMode == "replacement") != (s.ReplacementFile != "") || (s.PromptMode == "extension") != (len(s.AppendFiles) > 0) {
		return ErrSession
	}
	return nil
}

// InstalledInputs holds a frozen, offline-verified selection. Its fingerprint
// lets a durable start request detect changed inputs before any online operation.
type InstalledInputs struct {
	loaded           hostconfig.Loaded
	selected         Selection
	protocol         *contracts.Protocol
	submitted        *submission.Prepared
	profile          *targetprofile.Profile
	model            *modelprovider.Profile
	credentialConfig credentials.Config
	replacement      []byte
	appends          [][]byte
	fingerprint      string
	used             atomic.Bool
}

func (i *InstalledInputs) Fingerprint() string { return i.fingerprint }
func (i *InstalledInputs) Installation() hostconfig.Paths {
	return hostconfig.Paths{ConfigFile: i.loaded.Path, StateRoot: i.loaded.Config.State.Root, DockerEndpoint: i.loaded.Config.Docker.Endpoint}
}
func (i *InstalledInputs) Selection() Selection {
	s := i.selected
	s.SkillDigests = append([]string{}, s.SkillDigests...)
	s.AppendFiles = append([]string{}, s.AppendFiles...)
	return s
}

// LoadInputs reads and freezes installed profiles, submission and prompt bytes.
// It creates no state, resolves no credentials and contacts no external service.
func LoadInputs(ctx context.Context, configPath string, defaults hostconfig.Paths, selected Selection) (_ *InstalledInputs, err error) {
	selected.SkillDigests = append([]string{}, selected.SkillDigests...)
	selected.AppendFiles = append([]string{}, selected.AppendFiles...)
	if err = validateSelection(selected); err != nil {
		return nil, err
	}
	loaded, err := hostconfig.Load(configPath, defaults)
	if err != nil {
		return nil, err
	}
	c := loaded.Config
	installed, err := contractstore.Load(ctx, c.Contract.Directory, contracts.PackageIdentity{Version: c.Contract.Version, Digest: c.Contract.Digest})
	if err != nil {
		return nil, err
	}
	p := installed.Protocol()
	submitted, err := submission.Load(ctx, p, selected.RunDirectory)
	if err != nil {
		return nil, err
	}
	profile, err := targetprofile.Load(c.Target.ProfileFile)
	if err != nil {
		return nil, err
	}
	if profile.Settings().TargetID != submitted.TargetID() {
		return nil, ErrSession
	}
	model, err := modelprovider.Load(c.Model.ProfileFile)
	if err != nil {
		return nil, err
	}
	// Read explicit instruction sources once; no source directory is mounted.
	var replacement []byte
	appends := [][]byte{}
	if selected.ReplacementFile != "" {
		replacement, err = staging.Capture(ctx, selected.ReplacementFile, 1<<20)
		if err != nil {
			return nil, err
		}
	}
	for _, name := range selected.AppendFiles {
		raw, e := staging.Capture(ctx, name, 1<<20)
		if e != nil {
			return nil, e
		}
		appends = append(appends, raw)
	}
	// Validate text composition before native attachment or container inspection.
	if _, _, err = contracts.ComposePrompt(selected.PromptMode, []byte("validation placeholder\n"), replacement, appends); err != nil {
		return nil, err
	}
	var credentialConfig credentials.Config
	if c.Credentials.File != "" {
		credentialConfig, err = credentials.Load(c.Credentials.File)
		if err != nil {
			return nil, err
		}
	}
	if model.Settings().Authentication == "secret-store" {
		found := false
		for _, ref := range credentialConfig.Credentials {
			if ref.CredentialID == model.Settings().CredentialID {
				found = true
			}
		}
		if !found {
			return nil, ErrSession
		}
	}
	credentialRaw, err := json.Marshal(credentialConfig)
	if err != nil {
		return nil, err
	}
	promptParts := []string{}
	for _, raw := range appends {
		promptParts = append(promptParts, contracts.RawDigest(raw))
	}
	identityRaw, err := json.Marshal(map[string]any{"selection": selected, "config_path": loaded.Path, "config_digest": loaded.Digest, "effective_config": loaded.Config, "contract": submitted.Receipt().Contract, "submission": submitted.Receipt(), "target_profile_digest": profile.Digest(), "model_profile_digest": model.Digest(), "credential_configuration_digest": contracts.RawDigest(credentialRaw), "replacement_digest": contracts.RawDigest(replacement), "append_digests": promptParts})
	if err != nil {
		return nil, err
	}
	fingerprint, err := contracts.CanonicalDigest(identityRaw, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	return &InstalledInputs{loaded: loaded, selected: selected, protocol: p, submitted: submitted, profile: profile, model: model, credentialConfig: credentialConfig, replacement: replacement, appends: appends, fingerprint: fingerprint}, nil
}

// Open consumes these frozen inputs, acquires startup ownership, validates the
// local image/release and attaches Interceptor. It never starts the harness.
// A frontend MUST match Fingerprint to its durable start request before calling.
func (i *InstalledInputs) Open(ctx context.Context, operatorVersion string) (result *InstalledRun, err error) {
	if i == nil || !i.used.CompareAndSwap(false, true) {
		return nil, ErrSession
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loaded, selected, p, submitted, profile, model := i.loaded, i.selected, i.protocol, i.submitted, i.profile, i.model
	credentialConfig, replacement, appends := i.credentialConfig, i.replacement, i.appends
	c := loaded.Config
	docker, err := dockercontrol.New(c.Docker.Executable)
	if err != nil {
		return nil, err
	}
	gate, recovered, err := startup.Acquire(ctx, c.State.Root, docker)
	if err != nil {
		return nil, err
	}
	owned := true
	defer func() {
		if owned {
			gate.Close()
		}
	}()
	for _, prior := range recovered {
		if prior.CampaignID == selected.CampaignID {
			return nil, ErrSession
		}
	}
	release, err := imagerelease.Open(c.Cache.ReleaseDirectory)
	if err != nil {
		return nil, err
	}
	pin, _ := p.PackageIdentity()
	requirements := imagerelease.Requirements{OperatorVersion: operatorVersion, Contract: pin, HostPlatform: runtime.GOOS + "/" + runtime.GOARCH, RuntimeProfile: "operator-container/v1"}
	image, err := release.Prepare(ctx, docker, c.Docker.Endpoint, c.Engine.Image, requirements)
	closeErr := release.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	embedded, _, err := imagerelease.InspectEmbedded(ctx, docker, image, requirements, p)
	if err != nil {
		return nil, err
	}
	selectedSkills, err := skills.Select(ctx, p, filepath.Join(filepath.Dir(loaded.Path), "skill-signing"), filepath.Join(c.State.Root, "skills"), embedded.LoaderDigest(), selected.SkillDigests)
	if err != nil {
		return nil, err
	}
	resolver, err := credentials.New(credentialConfig, credentials.DefaultFactories())
	if err != nil {
		return nil, err
	}
	provider, err := modelprovider.New(ctx, model, resolver)
	if err != nil {
		resolver.Close()
		return nil, err
	}
	native := interceptor.New()
	cleanup := func() error { native.Close(); provider.Close(); return resolver.Close() }
	success := false
	defer func() {
		if !success {
			err = errors.Join(err, cleanup())
		}
	}()
	attached, err := native.Attach(ctx, selected.CampaignID, selected.WorkerInstanceID, profile.Settings().AllowTargetStop)
	if err != nil {
		return nil, err
	}
	status, err := native.Status(ctx, selected.CampaignID)
	if err != nil {
		return nil, err
	}
	bundle, authoring, references := submitted.Inputs()
	target, err := preparation.Build(preparation.Input{Protocol: p, Profile: profile, Attachment: attached, Status: status, InstanceID: status.InstanceID, Authoring: authoring, Bundle: bundle, References: references})
	if err != nil {
		return nil, err
	}
	session, err := Prepare(ctx, Config{StateRoot: c.State.Root, Target: target,
		Launch:       preparation.LaunchConfig{LaunchID: selected.LaunchID, ContainerID: selected.ContainerID, CreatedAt: time.Now().UTC().Truncate(time.Second), Image: image, Embedded: embedded, Model: model, Skills: selectedSkills, Limits: c.Limits, Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: c.Journal.MaxBytes, MaxSegmentBytes: c.Journal.SegmentBytes}, PromptMode: selected.PromptMode, Replacement: replacement, Appends: appends},
		Requirements: requirements, Peer: native, Docker: docker, Provider: provider, MinimumFreeBytes: c.Journal.MinimumFreeBytes, SpoolMaxBytes: c.Spool.MaxBytes, EvidenceMaxBytes: c.Evidence.MaxArchiveBytes, StartRequestID: selected.StartRequestID, Gate: gate, Recovery: recovered})
	if err != nil {
		return nil, err
	}
	owned = false
	metadata, _ := json.Marshal(map[string]string{"configuration_digest": loaded.Digest, "private_model_profile_digest": model.Digest(), "bundle_digest": submitted.Receipt().BundleDigest})
	if _, err = session.writer.Append(campaign.Entry{RunRevision: session.writer.Revision(), Kind: "campaign.installation-selected", Metadata: metadata}); err != nil {
		return nil, errors.Join(err, session.Cancel())
	}
	success = true
	return &InstalledRun{session: session, cleanup: cleanup}, nil
}

// NewSelection assigns fresh opaque host identities. Reuse is handled by a durable
// frontend request, never by regenerating IDs after a lost acknowledgement.
func NewSelection(runDirectory string) Selection {
	// Container identity uses 256 bits, independently of the actual Docker ID.
	a, b := randomID(), randomID()
	return Selection{RunDirectory: runDirectory, CampaignID: "campaign-" + randomID(), LaunchID: "launch-" + randomID(), ContainerID: a + b, WorkerInstanceID: "worker-" + randomID(), StartRequestID: randomID(), PromptMode: "default", SkillDigests: []string{}, AppendFiles: []string{}}
}
func randomID() string { return termination.NewRequestID() }
