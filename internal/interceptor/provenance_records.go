//go:build linux || darwin

package interceptor

import (
	"bytes"
	"context"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"io"
	"mime"
	"slices"
	"sort"
	"strings"
)

func (v *evidenceVerifier) checkpoints(ctx context.Context, b evidenceEvidenceBundle) error {
	expected := map[string]evidenceSnapshotEvidence{}
	for _, e := range b.Snapshots {
		if _, ok := expected[e.SnapshotID]; ok {
			return ErrProvenance
		}
		expected[e.SnapshotID] = e
	}
	count := 0
	for name := range v.archive.members {
		if !strings.HasPrefix(name, "checkpoints/") {
			continue
		}
		count++
		var cp Checkpoint
		raw, err := v.archive.readNative(name, &cp)
		if err != nil {
			return err
		}
		cp, err = decodeCheckpoint(raw, v.id.CampaignID)
		if err != nil {
			return ErrProvenance
		}
		if name != "checkpoints/"+cp.ID+".json" || cp.SourceSessionID != v.id.SessionID || cp.EnvironmentDigest != v.id.EnvironmentDigest || cp.AppDigest != v.id.ApplicationDigest || cp.JournalSeq >= uint64(len(v.stateHashes)) || v.stateHashes[cp.JournalSeq] != cp.JournalHash || cp.EventSeq >= uint64(len(v.eventHashes)) {
			return ErrProvenance
		}
		e := evidenceSnapshotEvidence{cp.ID, cp.Hash, cp.ParentCheckpoint, cp.CanonicalSizeBytes, cp.EventSeq, cp.CreatedAt, cp.Status}
		if expected[cp.ID] != e {
			return ErrProvenance
		}
	}
	if count != len(expected) {
		return ErrProvenance
	}
	_, source := v.archive.members["restore-source/checkpoint.json"]
	if source != (v.id.ParentSessionID != "") {
		return ErrProvenance
	}
	if !source {
		if len(b.Restores) != 0 {
			return ErrProvenance
		}
		return nil
	}
	var cp Checkpoint
	raw, err := v.archive.readNative("restore-source/checkpoint.json", &cp)
	if err != nil {
		return err
	}
	cp, err = decodeCheckpoint(raw, v.id.CampaignID)
	if err != nil || cp.ID != v.id.ParentCheckpointID || cp.Hash != v.id.ParentCheckpointDigest || cp.SourceSessionID != v.id.ParentSessionID || cp.EnvironmentDigest != v.id.EnvironmentDigest || cp.AppDigest != v.id.ApplicationDigest {
		return ErrProvenance
	}
	if len(b.Restores) != 1 || b.Restores[0] != (evidenceRestoreEvidence{v.id.ParentSessionID, cp.ID, v.id.SessionID}) {
		return ErrProvenance
	}
	return v.transactions(ctx, "restore-source/state.jsonl", v.id.ParentSessionID, true)
}
func (v *evidenceVerifier) registries(b evidenceEvidenceBundle) error {
	var artifacts struct {
		SchemaVersion int                  `json:"schema_version"`
		Artifacts     []ArtifactDescriptor `json:"artifacts"`
	}
	artifacts.Artifacts = []ArtifactDescriptor{}
	if _, ok := v.archive.members["artifacts.json"]; ok {
		if _, err := v.archive.readNative("artifacts.json", &artifacts); err != nil || artifacts.SchemaVersion != 1 {
			return ErrProvenance
		}
	}
	byDigest := map[string]ArtifactDescriptor{}
	for _, a := range artifacts.Artifacts {
		if _, duplicate := byDigest[a.Digest]; duplicate {
			return ErrProvenance
		}
		if a.SizeBytes < 0 || a.Canonicalization != "raw" && a.Canonicalization != "jcs-v1" {
			return ErrProvenance
		}
		if _, _, err := mime.ParseMediaType(a.MediaType); err != nil {
			return ErrProvenance
		}
		if err := v.blob(a.Digest, a.SizeBytes); err != nil {
			return err
		}
		if a.Canonicalization == "jcs-v1" {
			if a.SizeBytes > evidenceJSONLimit {
				return ErrProvenance
			}
			reader, err := v.archive.Reader("blobs/sha256/" + strings.TrimPrefix(a.Digest, "sha256:"))
			if err != nil {
				return err
			}
			raw, err := io.ReadAll(reader)
			if err != nil {
				return err
			}
			canonical, err := contracts.Canonicalize(raw, evidenceJSONLimit)
			if err != nil || !bytes.Equal(raw, canonical) {
				return ErrProvenance
			}
		}
		byDigest[a.Digest] = a
	}
	artifacts.Artifacts = append([]ArtifactDescriptor{}, artifacts.Artifacts...)
	sort.Slice(artifacts.Artifacts, func(i, j int) bool { return artifacts.Artifacts[i].Digest < artifacts.Artifacts[j].Digest })
	if !equalNative(b.ArtifactRefs, artifacts.Artifacts) {
		return ErrProvenance
	}
	var attempts struct {
		SchemaVersion int              `json:"schema_version"`
		Attempts      []AttemptContext `json:"attempts"`
	}
	if _, ok := v.archive.members["attempts.json"]; ok {
		if _, err := v.archive.readNative("attempts.json", &attempts); err != nil || attempts.SchemaVersion != 1 || len(attempts.Attempts) > evidenceRecordLimit {
			return ErrProvenance
		}
	}
	sort.Slice(attempts.Attempts, func(i, j int) bool { return attempts.Attempts[i].AttemptIndex < attempts.Attempts[j].AttemptIndex })
	known := map[string]AttemptContext{}
	expected := []evidenceAttemptEvidence{}
	var high uint64
	for _, a := range attempts.Attempts {
		if a.APIVersion != "interceptor.dev/attempt-context/v1alpha1" || a.CampaignID != v.id.CampaignID || a.FeedbackProfile != v.id.FeedbackProfile || !identifier.MatchString(a.AttemptID) || !identifier.MatchString(a.ThreadID) || a.AttemptIndex <= high || a.CreatedAt.IsZero() || a.Digest != AttemptContextDigest(a) || byDigest[a.Payload.Digest] != a.Payload {
			return ErrProvenance
		}
		if _, duplicate := known[a.AttemptID]; duplicate {
			return ErrProvenance
		}
		if a.ParentAttemptID == "" {
			if a.Generation != 1 {
				return ErrProvenance
			}
		} else {
			parent, ok := known[a.ParentAttemptID]
			if !ok || parent.ThreadID != a.ThreadID || a.Generation != parent.Generation+1 {
				return ErrProvenance
			}
		}
		high = a.AttemptIndex
		known[a.AttemptID] = a
		expected = append(expected, evidenceAttemptEvidence{a.AttemptID, a.ParentAttemptID, a.Payload.Digest, a.Digest, append([]uint64{}, v.attemptEvents[a.AttemptID]...)})
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].AttemptID < expected[j].AttemptID })
	if !equalNative(b.Attempts, expected) {
		return ErrProvenance
	}
	return nil
}
func (v *evidenceVerifier) execution() ([]string, error) {
	var state evidenceExecutionState
	raw, err := v.archive.readNative("execution.json", &state)
	if err != nil {
		return nil, err
	}
	if state.APIVersion != "interceptor.dev/execution-state/v1alpha2" || state.SessionID != v.id.SessionID || len(state.Records) > 4098 || state.Owner == nil || state.Owner.CampaignID != v.id.CampaignID || state.Owner.BoundAt.IsZero() || !identifier.MatchString(state.Owner.Principal) || !identifier.MatchString(state.Owner.WorkerInstanceID) || state.Owner.RunRevision == 0 || rawDigest(raw) != v.executionHead {
		return nil, ErrProvenance
	}
	if (state.ClosedAt == nil) != (state.Owner.ClosedAt == nil) || state.ClosedAt != nil && (state.ClosedAt.IsZero() || state.Owner.ClosedAt.IsZero()) {
		return nil, ErrProvenance
	}
	gaps := []string{}
	if state.Owner.ClosedAt == nil {
		gaps = append(gaps, "execution_closure_unconfirmed")
	}
	for id, r := range state.Records {
		if r == nil || id != r.Request.OperationID || !identifier.MatchString(id) || r.Request.SessionID != v.id.SessionID || r.Request.CampaignID != v.id.CampaignID || r.Request.APIVersion != OperationVersion || !identifier.MatchString(r.Request.RequestID) || !identifier.MatchString(r.Request.WorkerInstanceID) || r.Request.RunRevision == 0 || !digest.MatchString(r.Request.BodyDigest) || r.Request.Deadline.IsZero() || r.Fingerprint != operationFingerprint(r.Request) || r.CommandFingerprint != "" && !digest.MatchString(r.CommandFingerprint) || r.AdmittedAt.IsZero() || !slices.Contains([]string{"running", "completed", "unknown"}, r.State) {
			return nil, ErrProvenance
		}
		if r.State == "running" {
			if r.FinishedAt != nil || r.Response.Status != 0 || r.Response.SessionRevision != 0 || len(r.Response.Body) > 0 && string(r.Response.Body) != "null" {
				return nil, ErrProvenance
			}
		} else {
			if r.FinishedAt == nil || r.FinishedAt.Before(r.AdmittedAt) || r.Response.Status < 200 || r.Response.Status > 599 || len(r.Response.Body) > 4<<20 {
				return nil, ErrProvenance
			}
		}
		if r.State != "completed" {
			gaps = append(gaps, "operation_outcome_unknown:"+id)
		}
	}
	return gaps, nil
}
