//go:build linux || darwin

package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var ErrProvenance = errors.New("native evidence provenance is invalid or unsupported")

const evidenceJSONLimit = 64 << 20
const evidenceRecordLimit = 100000
const evidenceZeroHash = "sha256:0000000000000000000000000000000000000000000000000000000000000000"

// EvidenceIdentity is supplied by saved host attachment/restore records, never by
// the archive being checked. ParentCheckpointDigest pins restored source bytes.
type EvidenceIdentity struct {
	CampaignID             string `json:"campaign_id"`
	SessionID              string `json:"session_id"`
	EnvironmentDigest      string `json:"environment_digest"`
	ApplicationDigest      string `json:"application_digest"`
	CapabilityDigest       string `json:"capability_digest"`
	FeedbackProfile        string `json:"feedback_profile"`
	ParentSessionID        string `json:"parent_session_id,omitempty"`
	ParentCheckpointID     string `json:"parent_checkpoint_id,omitempty"`
	ParentCheckpointDigest string `json:"parent_checkpoint_digest,omitempty"`
}

type ProvenanceReceipt struct {
	Identity     EvidenceIdentity `json:"identity"`
	Transfer     EvidenceReceipt  `json:"transfer"`
	BundleDigest string           `json:"bundle_digest"`
	EventHead    string           `json:"event_head"`
	StateHead    string           `json:"state_head"`
	EventCount   int              `json:"event_count"`
	StateCount   int              `json:"state_count"`
	State        string           `json:"state"` // complete or partial; never an execution-success claim
	Gaps         []string         `json:"gaps"`
}

// VerifiedEvidence can only be constructed by semantic verification. It remains
// host-only, including protected native data. Its reader expires with Download.Close.
type VerifiedEvidence struct {
	archive *EvidenceArchive
	receipt ProvenanceReceipt
}

func (v *VerifiedEvidence) Receipt() ProvenanceReceipt {
	r := v.receipt
	r.Gaps = slices.Clone(r.Gaps)
	return r
}
func (v *VerifiedEvidence) Reader() (io.Reader, error) { return v.archive.download.Reader() }

func (i EvidenceIdentity) Valid() bool {
	if !identifier.MatchString(i.CampaignID) || !identifier.MatchString(i.SessionID) || !slices.Contains([]string{"black-box", "diagnostic", "oracle-assisted"}, i.FeedbackProfile) {
		return false
	}
	for _, d := range []string{i.EnvironmentDigest, i.ApplicationDigest, i.CapabilityDigest} {
		if !digest.MatchString(d) {
			return false
		}
	}
	if i.ParentSessionID == "" {
		return i.ParentCheckpointID == "" && i.ParentCheckpointDigest == ""
	}
	return identifier.MatchString(i.ParentSessionID) && i.ParentSessionID != i.SessionID && checkpointID(i.ParentCheckpointID) && digest.MatchString(i.ParentCheckpointDigest)
}

// VerifyProvenance checks the native Go-JSON hash recipes and cross-file binding.
// Hashes establish internal integrity, not signatures or truth of oracle assertions.
func (a *EvidenceArchive) VerifyProvenance(ctx context.Context, id EvidenceIdentity) (*VerifiedEvidence, error) {
	if a == nil || a.download == nil || !id.Valid() || ctx.Err() != nil {
		return nil, ErrProvenance
	}
	receipt := a.download.Receipt()
	if receipt.CampaignID != id.CampaignID || receipt.SessionID != id.SessionID {
		return nil, ErrProvenance
	}
	var manifest struct {
		SchemaVersion      int    `json:"schema_version"`
		SessionID          string `json:"session_id"`
		EnvironmentDigest  string `json:"environment_digest"`
		ApplicationDigest  string `json:"application_digest"`
		InterceptorVersion string `json:"interceptor_version"`
	}
	var m evidenceMetadata
	var b evidenceEvidenceBundle
	for name, out := range map[string]any{"manifest.json": &manifest, "session.json": &m, "evidence-bundle.json": &b} {
		if _, err := a.readNative(name, out); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	if manifest.SchemaVersion != 1 || manifest.SessionID != id.SessionID || manifest.EnvironmentDigest != id.EnvironmentDigest || manifest.ApplicationDigest != id.ApplicationDigest || manifest.InterceptorVersion == "" || m.SchemaVersion != 1 || m.ID != id.SessionID || m.CampaignID != id.CampaignID || m.EnvironmentDigest != id.EnvironmentDigest || m.AppDigest != id.ApplicationDigest || m.CapabilityManifestDigest != id.CapabilityDigest || m.FeedbackProfile != id.FeedbackProfile || m.OperationAPIVersion != OperationVersion || m.ParentSessionID != id.ParentSessionID || m.ParentCheckpoint != id.ParentCheckpointID || m.CreatedAt.IsZero() || m.UpdatedAt.Before(m.CreatedAt) || !slices.Contains([]string{"running", "quiescing", "stopping", "stopped", "error"}, m.Phase) {
		return nil, fmt.Errorf("identity: %w", ErrProvenance)
	}
	wantDigest := b.Digest
	b.Digest = ""
	if nativeHash(b) != wantDigest || b.APIVersion != "interceptor.dev/evidence-bundle/v1alpha1" || b.Kind != "EvidenceBundle" || b.CampaignID != id.CampaignID || b.SessionID != id.SessionID || b.EnvironmentDigest != id.EnvironmentDigest || b.ApplicationDigest != id.ApplicationDigest || b.CapabilityManifestDigest != id.CapabilityDigest || b.FeedbackProfile != id.FeedbackProfile || b.InterceptorReleaseDigest != rawDigest([]byte(manifest.InterceptorVersion)) || !b.CreatedAt.Equal(m.UpdatedAt) {
		return nil, fmt.Errorf("bundle: %w", ErrProvenance)
	}
	v := evidenceVerifier{archive: a, id: id, eventHashes: []string{evidenceZeroHash}, stateHashes: []string{evidenceZeroHash}, attemptEvents: map[string][]uint64{}}
	if err := v.events(ctx); err != nil {
		return nil, fmt.Errorf("events: %w", err)
	}
	if err := v.transactions(ctx, "state.jsonl", id.SessionID, false); err != nil {
		return nil, fmt.Errorf("state: %w", err)
	}
	if err := v.files(); err != nil {
		return nil, fmt.Errorf("files: %w", err)
	}
	if err := v.checkpoints(ctx, b); err != nil {
		return nil, fmt.Errorf("checkpoints: %w", err)
	}
	if err := v.registries(b); err != nil {
		return nil, fmt.Errorf("registries: %w", err)
	}
	gaps, err := v.execution()
	if err != nil {
		return nil, fmt.Errorf("execution: %w", err)
	}
	var report evidenceReport
	report.Oracles = []evidenceOracleResult{}
	report.Injections = []evidenceInjectionResult{}
	report.Coverage = []string{}
	_, evaluationPresent := a.members["final-evaluation.json"]
	if evaluationPresent {
		if _, err = a.readNative("final-evaluation.json", &report); err != nil {
			return nil, err
		}
	}
	report.Oracles = append([]evidenceOracleResult{}, report.Oracles...)
	report.Injections = append([]evidenceInjectionResult{}, report.Injections...)
	expectedGaps := append(append([]string{}, report.Coverage...), gaps...)
	sort.Strings(expectedGaps)
	if !equalNative(b.Oracles, report.Oracles) || !equalNative(b.Injections, report.Injections) || !equalNative(b.CoverageGaps, expectedGaps) {
		return nil, ErrProvenance
	}
	for _, o := range report.Oracles {
		if o.FirstEventSeq >= uint64(len(v.eventHashes)) || o.FirstEventDigest != "" && (o.FirstEventSeq == 0 || o.FirstEventDigest != v.eventHashes[o.FirstEventSeq]) {
			return nil, ErrProvenance
		}
	}
	state := "complete"
	if !evaluationPresent || len(gaps) > 0 {
		state = "partial"
	}
	start := uint64(0)
	if len(v.eventHashes) > 1 {
		start = 1
	}
	if b.Completeness.State != state || b.Completeness.StartSequence != start || b.Completeness.EndSequence != uint64(len(v.eventHashes)-1) || b.Completeness.Truncated != !evaluationPresent {
		return nil, ErrProvenance
	}
	// Native interruption markers and missing closure/evaluation remain explicit
	// report gaps even where the native bundle does not downgrade its own state.
	if !evaluationPresent {
		gaps = append(gaps, "evaluation_unavailable")
	}
	for _, name := range []string{"host-interruption.json", "transport-failure.json", "cleanup-pending.json"} {
		if entry, ok := a.members[name]; ok {
			if entry.Bytes > 4096 {
				return nil, ErrProvenance
			}
			var marker map[string]json.RawMessage
			if _, err = a.readNative(name, &marker); err != nil || marker == nil {
				return nil, ErrProvenance
			}
			gaps = append(gaps, strings.TrimSuffix(name, ".json"))
		}
	}
	gaps = append(gaps, report.Coverage...)
	if len(gaps) > 0 {
		state = "partial"
	}
	sort.Strings(gaps)
	gaps = slices.Compact(gaps)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &VerifiedEvidence{a, ProvenanceReceipt{id, receipt, wantDigest, v.eventHashes[len(v.eventHashes)-1], v.stateHashes[len(v.stateHashes)-1], len(v.eventHashes) - 1, len(v.stateHashes) - 1, state, gaps}}, nil
}
func nativeHash(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return rawDigest(raw)
}
func equalNative(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}
func (a *EvidenceArchive) readNative(name string, out any) ([]byte, error) {
	e, ok := a.members[name]
	if !ok || e.Bytes > evidenceJSONLimit {
		return nil, ErrProvenance
	}
	r, err := a.Reader(name)
	if err != nil {
		return nil, ErrProvenance
	}
	raw, err := io.ReadAll(r)
	if err != nil || rawDigest(raw) != e.SHA256 || strictEvidence(raw, out) != nil {
		return nil, ErrProvenance
	}
	return raw, nil
}
func strictEvidence(raw []byte, out any) error {
	value, err := contracts.Decode(raw, evidenceJSONLimit)
	if err != nil || !evidenceShape(value, reflect.TypeOf(out).Elem()) {
		return ErrProvenance
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrProvenance
	}
	return nil
}
func evidenceShape(v any, t reflect.Type) bool {
	if t == reflect.TypeFor[json.RawMessage]() || t == reflect.TypeFor[time.Time]() {
		return true
	}
	if t.Kind() == reflect.Pointer {
		return v == nil || evidenceShape(v, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := strings.Split(f.Tag.Get("json"), ",")
			name := tag[0]
			known[name] = true
			x, present := m[name]
			if !present {
				if !slices.Contains(tag, "omitempty") {
					return false
				}
				continue
			}
			if !evidenceShape(x, f.Type) {
				return false
			}
		}
		for name := range m {
			if !known[name] {
				return false
			}
		}
	case reflect.Slice:
		if v == nil {
			return true
		}
		items, ok := v.([]any)
		if !ok {
			return false
		}
		for _, x := range items {
			if !evidenceShape(x, t.Elem()) {
				return false
			}
		}
	case reflect.Map:
		m, ok := v.(map[string]any)
		if !ok {
			return false
		}
		for _, x := range m {
			if !evidenceShape(x, t.Elem()) {
				return false
			}
		}
	default:
		if v == nil {
			return false
		}
	}
	return true
}
