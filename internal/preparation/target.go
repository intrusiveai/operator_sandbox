//go:build linux || darwin

// Package preparation freezes target policy, compatibility and campaign bytes.
package preparation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

var ErrPreparation = errors.New("campaign preparation does not match trusted target inputs")

type Input struct {
	Protocol   *contracts.Protocol
	Profile    *targetprofile.Profile
	Attachment interceptor.Attachment
	Status     interceptor.Status
	InstanceID string
	Authoring  *capabilities.Export
	Bundle     []byte
	Artifacts  []attemptadapter.Artifact
	References map[string][]byte // Submitted passive reference bytes, keyed by raw digest.
}
type Target struct {
	protocol         *contracts.Protocol
	profile          *targetprofile.Profile
	live             *capabilities.Live
	compatibility    *capabilities.Compatibility
	policy           *attemptadapter.Policy
	bundle           *capabilities.Bundle
	authoring        *capabilities.Export
	references       *referenceSet
	artifacts        map[string]attemptadapter.Artifact
	scenarioIDs      []string
	instance         string
	hostPolicy       []byte
	nativeEvidence   interceptor.EvidenceIdentity
	evidenceMaxBytes int64
}

func Build(in Input) (*Target, error) {
	if in.Protocol == nil || in.Profile == nil || in.Authoring == nil {
		return nil, ErrPreparation
	}
	if _, ok := in.Protocol.PackageIdentity(); !ok {
		return nil, ErrPreparation
	}
	live, err := capabilities.BindLive(in.Protocol.Catalog(), in.Attachment, in.Status, in.InstanceID, in.Profile.Settings().TargetID)
	if err != nil {
		return nil, err
	}
	if in.Attachment.Session.Revision == 0 {
		return nil, ErrPreparation
	}
	policy, err := in.Profile.Resolve(live)
	if err != nil {
		return nil, err
	}
	bundle, err := capabilities.ParseBundle(in.Protocol.Catalog(), in.Bundle)
	if err != nil {
		return nil, err
	}
	compat, err := capabilities.Check(bundle, in.Authoring, live, policy.CapabilityPolicy())
	if err != nil {
		return nil, err
	}
	t := &Target{protocol: in.Protocol, profile: in.Profile, live: live, compatibility: compat, policy: policy, bundle: bundle, authoring: in.Authoring, instance: in.InstanceID, artifacts: map[string]attemptadapter.Artifact{}}
	t.references, err = prepareReferences(in.Protocol, t.bundle.JSON(), in.References)
	if err != nil {
		return nil, err
	}
	t.nativeEvidence = interceptor.EvidenceIdentity{CampaignID: in.Attachment.CampaignID, SessionID: in.Attachment.Session.ID, EnvironmentDigest: in.Attachment.Session.EnvironmentDigest, ApplicationDigest: in.Attachment.Session.AppDigest, CapabilityDigest: in.Attachment.Session.CapabilityManifestDigest, FeedbackProfile: in.Attachment.Session.FeedbackProfile}
	t.evidenceMaxBytes = in.Attachment.EvidenceMaxBytes
	if len(in.Artifacts) > 4096 {
		return nil, ErrPreparation
	}
	total := int64(0)
	for _, a := range in.Artifacts {
		request, _ := json.Marshal(map[string]any{"purpose": "payload", "artifact": a.Descriptor})
		if _, err = in.Protocol.Catalog().Validate(contracts.EngineArtifactBeginRequestSchema, request, contracts.OrdinaryLimit); err != nil {
			return nil, ErrPreparation
		}
		wire, _ := json.Marshal(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 1, "campaign_id": live.CampaignID(), "launch_id": "preparation", "run_revision": live.Binding().RunRevision, "call_id": "preparation", "operation_id": a.Descriptor.Digest, "operation": "engine.artifact_begin", "timeout_ms": 1000, "body": json.RawMessage(request)})
		if a.CampaignID != live.CampaignID() || in.Protocol.ValidateArtifactContent(wire, a.Bytes) != nil {
			return nil, ErrPreparation
		}
		if old, exists := t.artifacts[a.Descriptor.Digest]; exists {
			if old.Descriptor != a.Descriptor || !bytes.Equal(old.Bytes, a.Bytes) {
				return nil, ErrPreparation
			}
			continue
		}
		total += int64(len(a.Bytes))
		if total > 64<<20 {
			return nil, ErrPreparation
		}
		a.Bytes = bytes.Clone(a.Bytes)
		t.artifacts[a.Descriptor.Digest] = a
	}
	var parsed struct {
		Scenarios []struct {
			ID string `json:"scenario_id"`
		} `json:"scenarios"`
	}
	_ = json.Unmarshal(in.Bundle, &parsed)
	for _, s := range parsed.Scenarios {
		t.scenarioIDs = append(t.scenarioIDs, s.ID)
	}
	t.hostPolicy, _ = json.Marshal(map[string]any{"target_profile": json.RawMessage(in.Profile.JSON()), "resolved_policy": json.RawMessage(policy.RecordJSON())})
	return t, nil
}
func (t *Target) Live() *capabilities.Live        { return t.live }
func (t *Target) Profile() *targetprofile.Profile { return t.profile }
func (t *Target) Protocol() *contracts.Protocol   { return t.protocol }
func (t *Target) InstanceID() string              { return t.instance }
func (t *Target) HostPolicyJSON() []byte          { return bytes.Clone(t.hostPolicy) }
func (t *Target) BundleJSON() []byte              { return t.bundle.JSON() }
func (t *Target) ReadKinds() []string             { return t.compatibility.AllowedKinds() }
func (t *Target) Binding() campaign.TargetBinding {
	b := t.live.Binding()
	return campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: b.SessionID, WorkerInstanceID: b.WorkerInstanceID, NativeFeedbackProfile: t.live.NativeProfile(), CapabilitySourceDigest: t.live.Export().SourceDigest(), CapabilityProjectionDigest: t.live.Export().ProjectionDigest()}
}

// Context fills authoritative target, feedback and package fields in a host-owned
// context template. Prompt/skills/model/limits still come from their host builders.
func (t *Target) Context(template []byte) ([]byte, error) {
	value, err := contracts.Decode(template, contracts.EngineContextLimit)
	if err != nil {
		return nil, err
	}
	c, ok := value.(map[string]any)
	if !ok {
		return nil, ErrPreparation
	}
	if c["campaign_id"] != t.live.CampaignID() {
		return nil, ErrPreparation
	}
	c["run_revision"] = t.live.Binding().RunRevision
	pin, _ := t.protocol.PackageIdentity()
	reg := t.protocol.RegistryDigests()
	c["contract"] = map[string]any{"version": pin.Version, "digest": pin.Digest, "catalog_digest": reg["catalog_digest"], "operations_digest": reg["operations_digest"]}
	c["target"] = json.RawMessage(t.live.Export().PublicJSON())
	c["references"] = t.references.entries
	c["artifact_bindings"] = t.references.bindings
	c["omissions"] = t.references.omissions
	c["feedback"] = map[string]any{"profile": t.compatibility.EffectiveProfile(), "allowed_kinds": t.compatibility.AllowedKinds()}
	descriptor, ok := c["scenario_bundle"].(map[string]any)
	if !ok {
		return nil, ErrPreparation
	}
	descriptor["size_bytes"] = len(t.bundle.JSON())
	descriptor["digest"] = contracts.RawDigest(t.bundle.JSON())
	out, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if _, err = t.protocol.ValidateEngineContext(out); err != nil {
		return nil, err
	}
	return out, nil
}

type Stored struct {
	target    *Target
	writer    *campaign.Writer
	artifacts map[string][]campaign.ContentDescriptor
	context   []byte
}

// Persist binds preparation to the immutable RunManifest before broker creation.
// It retains exact source bytes and artifacts. Failure never yields a live store.
func (t *Target) Persist(w *campaign.Writer, context []byte) (*Stored, error) {
	if w == nil {
		return nil, ErrPreparation
	}
	m := w.Manifest()
	digest := func(b []byte, n int) string { d, _ := contracts.CanonicalDigest(b, n); return d }
	pin, _ := t.protocol.PackageIdentity()
	reg := t.protocol.RegistryDigests()
	if m.Target != t.Binding() || m.CampaignID != t.live.CampaignID() || m.InitialRevision != int64(t.live.Binding().RunRevision) || m.HostPolicyDigest != digest(t.hostPolicy, campaign.ManifestLimit) || m.EngineContextDigest != digest(context, contracts.EngineContextLimit) || m.ScenarioBundleDigest != digest(t.bundle.JSON(), contracts.OrdinaryLimit) || m.Contract.Version != pin.Version || m.Contract.Digest != pin.Digest || m.Contract.CatalogDigest != reg["catalog_digest"] || m.Contract.OperationsDigest != reg["operations_digest"] {
		return nil, ErrPreparation
	}
	projected, err := t.Context(context)
	if err != nil || digest(projected, contracts.EngineContextLimit) != m.EngineContextDigest {
		return nil, ErrPreparation
	}
	c, _ := t.protocol.ValidateEngineContext(context)
	if c["launch_id"] != m.LaunchID {
		return nil, ErrPreparation
	}
	if c["model"].(map[string]any)["profile_digest"] != m.ModelProfileDigest {
		return nil, ErrPreparation
	}
	release := c["release"].(map[string]any)
	if release["image_digest"] != m.ImageDigest || release["release_record_digest"] != m.ReleaseRecordDigest {
		return nil, ErrPreparation
	}
	for _, pair := range []struct {
		v   any
		raw []byte
	}{{c["remaining_limits"], m.RemainingLimits}, {c["limits"].(map[string]any)["harness"], m.HarnessLimits}} {
		b, _ := json.Marshal(pair.v)
		if digest(b, campaign.ManifestLimit) != digest(pair.raw, campaign.ManifestLimit) {
			return nil, ErrPreparation
		}
	}
	limits := c["remaining_limits"].(map[string]any)
	maxBytes, _ := limits["artifact_bytes"].(json.Number).Float64()
	maxObjects, _ := limits["artifact_objects"].(json.Number).Float64()
	total := int64(0)
	for _, a := range t.artifacts {
		total += int64(len(a.Bytes))
	}
	if total > int64(maxBytes) || int64(len(t.artifacts)) > int64(maxObjects) {
		return nil, ErrPreparation
	}
	record, _ := json.Marshal(map[string]any{"api_version": "operator.dev/campaign-preparation/v1alpha1", "instance_id": t.instance, "target": t.Binding(), "host_policy": json.RawMessage(t.hostPolicy), "compatibility": json.RawMessage(t.compatibility.RecordJSON()), "authoring_source": json.RawMessage(t.authoring.NativeJSON()), "live_source": json.RawMessage(t.live.Export().NativeJSON()), "bundle": json.RawMessage(t.bundle.JSON()), "engine_context": json.RawMessage(context)})
	if len(record) > 16<<20 {
		return nil, ErrPreparation
	}
	metadata, _ := json.Marshal(map[string]any{"digest": contracts.RawDigest(record)})
	if _, err = w.AppendStored(campaign.Entry{RunRevision: m.InitialRevision, Kind: "campaign.prepared", Metadata: metadata, Content: parts("preparation", record)}, "", false); err != nil {
		return nil, err
	}
	store := &Stored{target: t, writer: w, artifacts: map[string][]campaign.ContentDescriptor{}, context: bytes.Clone(context)}
	keys := []string{}
	for key := range t.artifacts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		a := t.artifacts[key]
		metadata, _ := json.Marshal(map[string]any{"artifact": a.Descriptor})
		refs, err := w.AppendStored(campaign.Entry{RunRevision: m.InitialRevision, Kind: "campaign.artifact-staged", Metadata: metadata, Content: parts("artifact", a.Bytes)}, "", false)
		if err != nil {
			return nil, err
		}
		store.artifacts[key] = refs
	}
	for _, entry := range t.references.entries {
		metadata, _ := json.Marshal(map[string]any{"entry": entry})
		raw := t.references.contents["input/"+entry["path"].(string)]
		if _, err := w.AppendStored(campaign.Entry{RunRevision: m.InitialRevision, Kind: "campaign.reference-staged", Metadata: metadata, Content: parts("reference", raw)}, "", false); err != nil {
			return nil, err
		}
	}
	metadata, _ = json.Marshal(map[string]any{"preparation_digest": contracts.RawDigest(record), "artifact_count": len(keys), "artifact_bytes": total})
	if _, err = w.Append(campaign.Entry{RunRevision: m.InitialRevision, Kind: "campaign.preparation-adopted", Metadata: metadata}); err != nil {
		return nil, err
	}
	return store, nil
}
func parts(role string, raw []byte) []campaign.Content {
	out := []campaign.Content{}
	for offset := 0; offset < len(raw) || offset == 0; offset += campaign.MaxContentBytes {
		end := min(len(raw), offset+campaign.MaxContentBytes)
		out = append(out, campaign.Content{Role: fmt.Sprintf("%s-%d", role, len(out)), MediaType: "application/octet-stream", Bytes: raw[offset:end]})
		if end == len(raw) {
			break
		}
	}
	return out
}
func (s *Stored) Target() *Target          { return s.target }
func (s *Stored) Context() []byte          { return bytes.Clone(s.context) }
func (s *Stored) Writer() *campaign.Writer { return s.writer }
func (s *Stored) Inputs() (attemptadapter.Inputs, error) {
	in := attemptadapter.Inputs{Live: s.target.live, Compatibility: s.target.compatibility, Policy: s.target.policy, Artifacts: map[string]attemptadapter.Artifact{}, ScenarioIDs: append([]string{}, s.target.scenarioIDs...), ReleaseDigest: s.writer.Manifest().ReleaseRecordDigest, FeedbackBytes: s.target.profile.Settings().Feedback.MaxAttemptBytes}
	for key, refs := range s.artifacts {
		a := s.target.artifacts[key]
		a.Bytes = nil
		for _, ref := range refs {
			data, err := s.writer.ReadContent(ref)
			if err != nil {
				s.writer.Fence().Stop(err)
				return in, err
			}
			a.Bytes = append(a.Bytes, data...)
		}
		if contracts.RawDigest(a.Bytes) != key {
			s.writer.Fence().Stop(campaign.ErrCorrupt)
			return in, campaign.ErrCorrupt
		}
		in.Artifacts[key] = a
	}
	return in, nil
}

func (t *Target) NativeEvidence() (interceptor.EvidenceIdentity, int64) {
	return t.nativeEvidence, t.evidenceMaxBytes
}
