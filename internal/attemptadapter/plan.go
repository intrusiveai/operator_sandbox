package attemptadapter

import (
	"bytes"
	"encoding/json"
	"regexp"
	"slices"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/capabilities"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/nativedelivery"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var digestID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type request struct {
	APIVersion            string                          `json:"api_version"`
	Kind                  string                          `json:"kind"`
	RequestID             string                          `json:"request_id"`
	Origin                string                          `json:"origin"`
	ScenarioID            string                          `json:"scenario_id"`
	ThreadID              string                          `json:"thread_id"`
	AttemptID             string                          `json:"attempt_id"`
	ParentAttemptID       string                          `json:"parent_attempt_id"`
	Generation            uint64                          `json:"generation"`
	AttemptIndex          uint64                          `json:"attempt_index"`
	Payload               interceptor.ArtifactDescriptor  `json:"payload"`
	Carrier               *interceptor.ArtifactDescriptor `json:"carrier"`
	Generator             interceptor.Generator           `json:"generator"`
	StrategyProvenanceRef string                          `json:"strategy_provenance_ref"`
	PreActions            []struct {
		ActionID   string                 `json:"action_id"`
		Parameters interceptor.Definition `json:"parameters"`
	} `json:"pre_actions"`
	Invocation struct {
		OperationID       string `json:"operation_id"`
		InputSource       string `json:"input_source"`
		MediaType         string `json:"media_type"`
		CallerPrincipalID string `json:"caller_principal_id"`
	} `json:"invocation"`
	Cleanup struct {
		Delete bool `json:"delete_actions_after_observation"`
	} `json:"cleanup"`
	Selection *interceptor.ObservationSelection `json:"observation_selection"`
}

// Artifact and Parent come from the host's verified campaign retention/current
// native lineage, never descriptors or parent assertions supplied by the harness.
type Artifact struct {
	CampaignID string
	Descriptor interceptor.ArtifactDescriptor
	Bytes      []byte
}
type Parent struct {
	SessionID          string
	Context            interceptor.AttemptContext
	CampaignGeneration uint64 // Original harness generation, when native roots were rebased.
}
type Inputs struct {
	Live                *capabilities.Live
	Compatibility       *capabilities.Compatibility
	Policy              *Policy
	Artifacts           map[string]Artifact
	Parent              *Parent
	PriorParent         *Parent // Verified campaign history absent from the restored registry.
	KnownTurnIDs        []string
	ScenarioIDs         []string
	ReleaseDigest       string
	CreatedAt, Deadline time.Time
	SessionRevision     uint64
	FeedbackBytes       int64
}
type command struct {
	Operation string          `json:"operation"`
	Body      json.RawMessage `json:"body"`
}

// Plan is pure, immutable translation. Its commands contain exact verified bytes
// and declared selectors. Rationale/technique prose never selects a native tactic.
type Plan struct {
	catalog                        *contracts.Catalog
	request                        request
	context                        interceptor.AttemptContext
	binding                        interceptor.Binding
	policy                         *feedback.Policy
	commands                       []command
	cleanup                        []command
	actions                        map[string]string
	deadline                       time.Time
	revision                       uint64
	feedbackBytes                  int64
	record                         []byte
	rawRequest                     []byte
	sourceDigest, projectionDigest string
}

func (p *Plan) RecordJSON() []byte       { return bytes.Clone(p.record) }
func (p *Plan) RequestID() string        { return p.request.RequestID }
func (p *Plan) FeedbackAllowance() int64 { return p.feedbackBytes }
func (p *Plan) ContextDigest() string    { return p.context.Digest }

func Compile(catalog *contracts.Catalog, raw []byte, in Inputs) (*Plan, error) {
	if catalog == nil || in.Live == nil || in.Compatibility == nil || in.Policy == nil || in.SessionRevision == 0 || in.CreatedAt.IsZero() || !in.Deadline.After(in.CreatedAt) || in.Deadline.Sub(in.CreatedAt) > interceptor.MaxOperationTimeout || !digestID.MatchString(in.ReleaseDigest) || in.FeedbackBytes < 0 || in.FeedbackBytes > 64*feedback.MaxArtifact {
		return nil, ErrAttempt
	}
	if _, err := catalog.Validate(contracts.EngineAttemptRequestSchema, raw, contracts.OrdinaryLimit); err != nil {
		return nil, ErrAttempt
	}
	var r request
	canonical, err := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	if err != nil || json.Unmarshal(canonical, &r) != nil {
		return nil, ErrAttempt
	}
	cp, err := in.Compatibility.ExecutionPolicy(in.Live)
	if err != nil || in.Policy.sourceDigest != in.Live.Export().SourceDigest() || !equalPolicy(cp, in.Policy.capabilityPolicy) {
		return nil, ErrPolicy
	}
	if r.Generator.ReleaseDigest != in.ReleaseDigest || r.Origin == "scenario" && !slices.Contains(in.ScenarioIDs, r.ScenarioID) {
		return nil, ErrAttempt
	}
	if r.ParentAttemptID != "" {
		parent := in.Parent
		if parent == nil {
			parent = in.PriorParent
		}
		if parent == nil || (in.Parent != nil && parent.SessionID != in.Live.Binding().SessionID) || (in.Parent == nil && (parent.SessionID == in.Live.Binding().SessionID || parent.CampaignGeneration == 0)) || parent.Context.APIVersion != "interceptor.dev/attempt-context/v1alpha1" || parent.Context.Digest != interceptor.AttemptContextDigest(parent.Context) || parent.Context.AttemptID != r.ParentAttemptID || parent.Context.CampaignID != in.Live.CampaignID() || parent.Context.ThreadID != r.ThreadID || parent.Context.AttemptIndex >= r.AttemptIndex {
			return nil, ErrAttempt
		}
		generation := parent.CampaignGeneration
		if generation == 0 {
			generation = parent.Context.Generation
		}
		if generation+1 != r.Generation {
			return nil, ErrAttempt
		}
	}
	policy, err := feedback.New(in.Compatibility.NativeProfile(), in.Compatibility.EffectiveProfile(), in.Compatibility.AllowedKinds(), r.Selection)
	if err != nil {
		return nil, err
	}
	p := &Plan{catalog: catalog, request: r, binding: in.Live.Binding(), policy: policy, actions: map[string]string{}, deadline: in.Deadline.UTC(), revision: in.SessionRevision, feedbackBytes: in.FeedbackBytes}
	p.rawRequest = bytes.Clone(raw)
	p.sourceDigest, p.projectionDigest = in.Live.Export().SourceDigest(), in.Live.Export().ProjectionDigest()
	artifactBytes := map[string][]byte{}
	for _, desc := range []*interceptor.ArtifactDescriptor{&r.Payload, r.Carrier} {
		if desc == nil {
			continue
		}
		a, ok := in.Artifacts[desc.Digest]
		if !ok || a.CampaignID != in.Live.CampaignID() || a.Descriptor != *desc || int64(len(a.Bytes)) != desc.SizeBytes || contracts.RawDigest(a.Bytes) != desc.Digest {
			return nil, ErrAttempt
		}
		if desc.Canonicalization == "jcs-v1" {
			canonical, err := contracts.Canonicalize(a.Bytes, 16<<20)
			if err != nil || !bytes.Equal(canonical, a.Bytes) {
				return nil, ErrAttempt
			}
		}
		if _, exists := artifactBytes[desc.Digest]; exists {
			continue
		}
		artifactBytes[desc.Digest] = bytes.Clone(a.Bytes)
		if err = p.add("artifact.register", struct {
			Descriptor interceptor.ArtifactDescriptor `json:"descriptor"`
			Content    []byte                         `json:"content"`
		}{*desc, a.Bytes}, false); err != nil {
			return nil, err
		}
	}
	p.context = interceptor.AttemptContext{APIVersion: "interceptor.dev/attempt-context/v1alpha1", CampaignID: in.Live.CampaignID(), ThreadID: r.ThreadID, AttemptID: r.AttemptID, ParentAttemptID: r.ParentAttemptID, Generation: r.Generation, AttemptIndex: r.AttemptIndex, Payload: r.Payload, Generator: r.Generator, StrategyProvenanceRef: r.StrategyProvenanceRef, FeedbackProfile: policy.NativeProfile(), CreatedAt: in.CreatedAt.UTC(), ObservationSelection: policy.NativeSelection()}
	if r.ParentAttemptID != "" {
		if in.Parent == nil {
			p.context.ParentAttemptID = ""
			p.context.Generation = 1
		} else {
			p.context.Generation = in.Parent.Context.Generation + 1
		}
	}
	p.context.Digest = interceptor.AttemptContextDigest(p.context)
	if err = p.add("attempt.register", p.context, false); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	if len(r.PreActions) > 0 && !r.Cleanup.Delete && !in.Policy.scopes.AllowRetainedInjections {
		return nil, ErrPolicy
	}
	for _, action := range r.PreActions {
		if seen[action.ActionID] || action.Parameters.PayloadDigest != r.Payload.Digest {
			return nil, ErrAttempt
		}
		seen[action.ActionID] = true
		d := action.Parameters
		if err = in.Policy.authorizeInjection(d, in.KnownTurnIDs); err != nil {
			return nil, err
		}
		payload := artifactBytes[r.Payload.Digest]
		if err = validatePlacementPayload(d.Placement, payload); err != nil {
			return nil, err
		}
		d.ID = opaque("injection", in.Live.CampaignID(), r.AttemptID, action.ActionID)
		d.Enabled = true
		d.AttemptID = r.AttemptID
		d.Payload = payload
		p.actions[d.ID] = action.ActionID
		if err = p.add("injection.arm", struct {
			Definition interceptor.Definition `json:"definition"`
		}{d}, false); err != nil {
			return nil, err
		}
		if err = p.add("injection.delete", map[string]string{"injection_id": d.ID}, true); err != nil {
			return nil, err
		}
	}
	input := r.Payload
	if r.Invocation.InputSource == "carrier" {
		input = *r.Carrier
	}
	if input.MediaType != r.Invocation.MediaType || !slices.Contains(in.Policy.scopes.OperationIDs, r.Invocation.OperationID) || r.Invocation.CallerPrincipalID != "" && !slices.Contains(in.Policy.scopes.CallerPrincipalIDs, r.Invocation.CallerPrincipalID) {
		return nil, ErrPolicy
	}
	var operation *capabilities.OperationCapability
	for i := range in.Policy.facts.Operations {
		op := &in.Policy.facts.Operations[i]
		if op.ID == r.Invocation.OperationID {
			operation = op
		}
	}
	if operation == nil || operation.Delivery == nil || input.MediaType != operation.Delivery.Input.MediaType || input.SizeBytes > 2<<20 || operation.MaximumInputBytes > 0 && input.SizeBytes > operation.MaximumInputBytes || nativedelivery.ValidateBytes(operation.Delivery.Input, artifactBytes[input.Digest]) != nil {
		return nil, ErrAttempt
	}
	// Inline input supports a distinct carrier. Native ArtifactDigest is restricted
	// to the attempt payload and cannot represent that carrier.
	turn := interceptor.TurnRequest{Operation: operation.ID, Input: artifactBytes[input.Digest], MediaType: input.MediaType, CallerPrincipal: r.Invocation.CallerPrincipalID, AttemptID: r.AttemptID, PayloadDigest: r.Payload.Digest}
	if err = p.add("application.invoke", turn, false); err != nil {
		return nil, err
	}
	for _, cmd := range append(append([]command{}, p.commands...), p.cleanup...) {
		if !slices.Contains(in.Policy.facts.NativeOperations, cmd.Operation) {
			return nil, ErrPolicy
		}
	}
	if policy.Collect() && (!slices.Contains(in.Policy.facts.NativeOperations, "observation.read") || !slices.Contains(in.Policy.facts.NativeOperations, "observation.content.read")) {
		return nil, ErrPolicy
	}
	p.record, err = json.Marshal(map[string]any{"api_version": "operator.dev/interceptor-attempt-plan/v1alpha1", "request_digest": contracts.RawDigest(raw), "campaign_id": p.context.CampaignID, "binding": p.binding, "initial_session_revision": p.revision, "deadline": p.deadline, "context_digest": p.context.Digest, "commands": p.commands, "cleanup": p.cleanup, "delete_after_observation": r.Cleanup.Delete, "action_handles": p.actions, "policy": json.RawMessage(in.Policy.RecordJSON()), "compatibility": json.RawMessage(in.Compatibility.RecordJSON()), "feedback_bytes": p.feedbackBytes, "native_selection": policy.NativeSelection()})
	if err != nil || len(p.record) > contracts.OrdinaryLimit {
		return nil, ErrAttempt
	}
	return p, nil
}
func (p *Plan) add(operation string, value any, cleanup bool) error {
	body, err := json.Marshal(value)
	if err != nil {
		return ErrAttempt
	}
	limit := interceptor.JSONLimit - 4096
	switch operation {
	case "injection.arm", "attempt.register":
		limit = 1 << 20
	case "application.invoke":
		limit = 3 << 20
	}
	if len(body) > limit {
		return ErrAttempt
	}
	c := command{operation, body}
	if cleanup {
		p.cleanup = append(p.cleanup, c)
	} else {
		p.commands = append(p.commands, c)
	}
	return nil
}
func opaque(prefix string, parts ...string) string {
	b, _ := json.Marshal(parts)
	return prefix + "-" + contracts.RawDigest(b)[7:]
}
func equalPolicy(a, b capabilities.Policy) bool {
	if a.FeedbackCeiling != b.FeedbackCeiling {
		return false
	}
	for _, pair := range [][2][]string{{a.AllowedRefs, b.AllowedRefs}, {a.SelectableActionRefs, b.SelectableActionRefs}, {a.FeedbackKinds, b.FeedbackKinds}} {
		x, y := slices.Clone(pair[0]), slices.Clone(pair[1])
		slices.Sort(x)
		slices.Sort(y)
		if !slices.Equal(x, y) {
			return false
		}
	}
	return true
}
func validatePlacementPayload(p interceptor.Placement, raw []byte) error {
	v, err := contracts.Decode(raw, 16<<20)
	if err != nil {
		return ErrAttempt
	}
	switch p.Operation {
	case "prepend_text", "append_text":
		if _, ok := v.(string); !ok {
			return ErrAttempt
		}
	case "merge_object":
		m, ok := v.(map[string]any)
		if !ok {
			return ErrAttempt
		}
		for k := range m {
			if !slices.Contains(p.AllowedFields, k) {
				return ErrPolicy
			}
		}
	}
	return nil
}
