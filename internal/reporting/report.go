//go:build linux || darwin

// Package reporting derives immutable local facts without contacting execution
// services. Native/harness data remain source observations or claims, not verdicts.
package reporting

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
)

const Version = "operator.dev/result-manifest/v1alpha1"

type Receipt struct {
	APIVersion       string `json:"api_version"`
	CampaignID       string `json:"campaign_id"`
	GenerationDigest string `json:"generation_digest"`
	ResultDigest     string `json:"result_manifest_digest"`
	Complete         bool   `json:"journal_intact"`
}
type Attempt struct {
	RequestID     string             `json:"request_id"`
	AttemptID     string             `json:"attempt_id"`
	Index         int64              `json:"attempt_index"`
	Revision      int64              `json:"run_revision"`
	State         string             `json:"state"`
	Dispatched    bool               `json:"dispatched"`
	ScenarioID    string             `json:"scenario_id,omitempty"`
	ObjectiveIDs  []string           `json:"objective_ids"`
	TargetContact string             `json:"target_contact"`
	Invocation    string             `json:"invocation_state"`
	Cleanup       string             `json:"cleanup_state"`
	Feedback      *feedback.Manifest `json:"feedback,omitempty"`
}
type Result struct {
	APIVersion            string                       `json:"api_version"`
	CampaignID            string                       `json:"campaign_id"`
	ManifestDigest        string                       `json:"run_manifest_digest"`
	ExecutionBundleDigest string                       `json:"execution_bundle_digest"`
	EvidenceSetDigest     string                       `json:"target_evidence_set_digest"`
	JournalIntact         bool                         `json:"journal_intact"`
	VerifiedEvents        int64                        `json:"verified_events"`
	Execution             string                       `json:"execution_state"`
	ContainerCleanup      string                       `json:"container_cleanup"`
	TargetClosure         string                       `json:"target_closure"`
	InjectionCleanup      string                       `json:"injection_cleanup"`
	TargetStop            string                       `json:"target_stop"`
	TargetAdapter         string                       `json:"target_adapter"`
	NativeProfile         string                       `json:"native_feedback_profile"`
	Assurance             string                       `json:"evidence_assurance"`
	Attempts              []Attempt                    `json:"attempts"`
	UntestedScenarios     []string                     `json:"untested_scenarios"`
	UntestedObjectives    []string                     `json:"untested_objectives"`
	Coverage              string                       `json:"coverage_state"`
	Usage                 map[string]int64             `json:"cumulative_usage"`
	HarnessClaims         int64                        `json:"harness_claim_records"`
	OracleVerdict         string                       `json:"oracle_verdict"`
	Gaps                  []string                     `json:"gaps"`
	UnknownOperations     []string                     `json:"unknown_operations"`
	Termination           *campaign.TerminationOutcome `json:"termination,omitempty"`
	RecoveryDigest        string                       `json:"cleanup_audit_digest,omitempty"`
	TerminationDigest     string                       `json:"termination_record_digest,omitempty"`
	UntestedOperations    []string                     `json:"untested_operation_ids"`
	Routes                []RouteCoverage              `json:"injection_routes"`
}
type sourceContent struct {
	Role       string `json:"role"`
	Bytes      int64  `json:"size_bytes"`
	Digest     string `json:"digest"`
	Path       string `json:"path,omitempty"`
	Visibility string `json:"visibility"`
}
type sourceEvent struct {
	Sequence     int64           `json:"sequence"`
	Revision     int64           `json:"run_revision"`
	Kind         string          `json:"kind"`
	RecordedAt   string          `json:"recorded_at"`
	SourceDigest string          `json:"source_event_digest"`
	Metadata     json.RawMessage `json:"metadata"`
	Content      []sourceContent `json:"content"`
}

// Generate takes the campaign lock until reporting/export ends. The manifest is
// the publication marker. Export to an existing directory is always refused.
func Generate(ctx context.Context, root, id, output string) (Receipt, error) {
	var receipt Receipt
	if output != "" {
		var e error
		output, e = filepath.Abs(output)
		if e != nil {
			return receipt, e
		}
		state, e := filepath.EvalSymlinks(root)
		if e != nil {
			return receipt, e
		}
		parent, e := filepath.EvalSymlinks(filepath.Dir(output))
		if e != nil {
			return receipt, e
		}
		output = filepath.Join(parent, filepath.Base(output))
		rel, e := filepath.Rel(state, output)
		if e != nil || rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return receipt, campaign.ErrInvalid
		}
		if _, e = os.Lstat(output); !errors.Is(e, os.ErrNotExist) {
			return receipt, campaign.ErrInvalid
		}
	}
	a, e := campaign.OpenNativeRecovery(root, id)
	if e != nil {
		return receipt, e
	}
	defer a.Close()
	files, e := a.NewDerived()
	if e != nil {
		return receipt, e
	}
	defer files.Close()
	result := Result{APIVersion: Version, CampaignID: id, Execution: "unknown", ContainerCleanup: "unconfirmed", TargetClosure: "unconfirmed", InjectionCleanup: "unconfirmed", TargetStop: "unconfirmed", Assurance: "retained-observations-only", Attempts: []Attempt{}, UntestedObjectives: []string{}, Coverage: "unknown", Usage: map[string]int64{}, OracleVerdict: "unknown", Gaps: []string{}}
	attempts := map[string]Attempt{}
	objectives := map[string]bool{}
	scenarios := map[string][]string{}
	scenarioTested := map[string]bool{}
	bundleRetained := false
	var bundleDigest string
	var routeFacts routeCoverage
	launchInputsRetained := false
	tested := map[string]bool{}
	var batch bytes.Buffer
	parts := []campaign.DerivedFile{}
	var writeErr error
	flush := func() error {
		if batch.Len() == 0 {
			return nil
		}
		raw := batch.Bytes()
		name := fmt.Sprintf("events/%06d.jsonl", len(parts)+1)
		if e := files.Put(ctx, name, raw); e != nil {
			return e
		}
		parts = append(parts, campaign.DerivedFile{Path: name, Bytes: int64(len(raw)), Digest: contracts.RawDigest(raw)})
		batch.Reset()
		return nil
	}
	inspect, scanErr := a.Inspect(ctx, func(event campaign.Event) (projectionErr error) {
		defer func() {
			if projectionErr != nil {
				writeErr = projectionErr
			}
		}()
		if event.Sequence > 100000 {
			writeErr = campaign.ErrQuota
			return writeErr
		}
		raw, _ := json.Marshal(event)
		sourceDigest, e := contracts.CanonicalDigest(raw, campaign.MaxEventBytes)
		if e != nil {
			return e
		}
		projected := sourceEvent{Sequence: event.Sequence, Revision: event.RunRevision, Kind: event.Kind, RecordedAt: event.RecordedAt, SourceDigest: sourceDigest, Metadata: json.RawMessage(`{}`), Content: []sourceContent{}}
		metadata := map[string]json.RawMessage{}
		_ = json.Unmarshal(event.Metadata, &metadata)
		if event.Kind == "campaign.launch-inputs-retained" {
			launchInputsRetained = true
		}
		if event.Kind == "https.dispatch-intent" {
			var id string
			if json.Unmarshal(metadata["operation_id"], &id) != nil || routeFacts.operations == nil {
				return campaign.ErrCorrupt
			}
			routeFacts.operations[id] = true
		}
		if _, ok := metadata["native_step"]; ok {
			if e := routeFacts.step(a, event); e != nil {
				return e
			}
		}
		keep := map[string]json.RawMessage{}
		for _, key := range []string{"operation_id", "attempt_index_high_watermark", "attempt_admissions", "snapshot_admissions", "snapshot_bytes", "model_turns", "model_tokens", "actual_tokens", "finish_reason", "conclusion_state", "assertion_origin", "record_kind", "receipt_id", "part_index", "parts", "digest", "size_bytes"} {
			if value, ok := metadata[key]; ok {
				keep[key] = value
			}
		}
		projected.Metadata, _ = json.Marshal(keep)
		if event.Kind == "campaign.launch-input" {
			var inputPath string
			if json.Unmarshal(metadata["path"], &inputPath) == nil && filepath.IsLocal(inputPath) {
				keep["input_path"] = metadata["path"]
				projected.Metadata, _ = json.Marshal(keep)
			}
		}
		for _, key := range []string{"attempt_index_high_watermark", "attempt_admissions", "snapshot_admissions", "snapshot_bytes", "model_turns", "model_tokens"} {
			var n int64
			if json.Unmarshal(metadata[key], &n) == nil && n >= 0 {
				result.Usage[key] = max(result.Usage[key], n)
			}
		}
		if value, ok := metadata["attempt"]; ok {
			var saved campaign.SavedAttempt
			if json.Unmarshal(value, &saved) != nil {
				return campaign.ErrCorrupt
			}
			old := attempts[saved.RequestID]
			old.RequestID, old.AttemptID, old.Index, old.Revision, old.State, old.Dispatched = saved.RequestID, saved.AttemptID, saved.Index, saved.SubmittedRevision, saved.State, saved.Dispatched
			if old.ObjectiveIDs == nil {
				old.ObjectiveIDs = []string{}
			}
			if old.TargetContact == "" {
				old.TargetContact = "unknown"
				old.Invocation = "unknown"
				old.Cleanup = "unknown"
			}
			attempts[saved.RequestID] = old
		}

		if event.Kind == "campaign.prepared" {
			var joined []byte
			for _, d := range event.Content {
				if len(joined)+int(d.SizeBytes) > campaign.DerivedManifestLimit {
					return campaign.ErrCorrupt
				}
				raw, e := a.ReadContent(d)
				if e != nil {
					return e
				}
				joined = append(joined, raw...)
			}
			var preparation struct {
				Bundle     json.RawMessage `json:"bundle"`
				HostPolicy json.RawMessage `json:"host_policy"`
			}
			if json.Unmarshal(joined, &preparation) != nil {
				return campaign.ErrCorrupt
			}
			if e := routeFacts.prepare(preparation.HostPolicy); e != nil {
				return e
			}
			if len(preparation.Bundle) > 0 {
				bundleDigest, e = contracts.CanonicalDigest(preparation.Bundle, campaign.DerivedManifestLimit)
				if e != nil {
					return e
				}
				if e := readObjectives(preparation.Bundle, objectives, scenarios); e != nil {
					return e
				}
				if e := files.Put(ctx, "input/scenario-bundle.json", preparation.Bundle); e != nil {
					writeErr = e
					return e
				}
				bundleRetained = true
			}
		}
		if event.Kind == "assessment.record" {
			result.HarnessClaims++
		}
		for _, desc := range event.Content {
			item := sourceContent{Role: desc.Role, Bytes: desc.SizeBytes, Digest: desc.Digest, Visibility: "host-only-omitted"}
			raw, e := a.ReadContent(desc)
			if e != nil {
				return e
			}
			if publicContent(event.Kind, desc.Role) {
				item.Path = "content/" + desc.Digest[7:]
				item.Visibility = "administrator-evidence"
				if e = files.Put(ctx, item.Path, raw); e != nil {
					writeErr = e
					return e
				}
			}
			projected.Content = append(projected.Content, item)
			if desc.Role == "guest-request" {
				var request struct {
					RequestID  string `json:"request_id"`
					ScenarioID string `json:"scenario_id"`
				}
				if json.Unmarshal(raw, &request) == nil {
					if old, ok := attempts[request.RequestID]; ok {
						old.ScenarioID = request.ScenarioID
						old.ObjectiveIDs = append([]string{}, scenarios[request.ScenarioID]...)
						attempts[request.RequestID] = old
					}
				}
			}
			if desc.Role == "guest-result" {
				var r struct {
					RequestID     string             `json:"request_id"`
					TargetContact string             `json:"target_contact"`
					Invocation    string             `json:"invocation_state"`
					Cleanup       string             `json:"cleanup_state"`
					Feedback      *feedback.Manifest `json:"feedback"`
				}
				if json.Unmarshal(raw, &r) == nil {
					if old, ok := attempts[r.RequestID]; ok {
						old.TargetContact, old.Invocation, old.Cleanup = r.TargetContact, r.Invocation, r.Cleanup
						old.Feedback = r.Feedback
						attempts[r.RequestID] = old
					}
				}
			}
			if event.Kind == "service.terminal-result" && desc.Role == "response" {
				var r struct {
					Closure string `json:"closure"`
					Cleanup string `json:"cleanup_state"`
					Stop    string `json:"target_stop"`
				}
				if json.Unmarshal(raw, &r) == nil {
					result.TargetClosure, result.InjectionCleanup, result.TargetStop = r.Closure, r.Cleanup, r.Stop
				}
			}
			if event.Kind == "launch.terminal" && desc.Role == "launch-result" {
				var r struct {
					Cleanup string `json:"container_cleanup"`
				}
				if json.Unmarshal(raw, &r) == nil {
					result.Execution = "closed"
					result.ContainerCleanup = r.Cleanup
				}
			}

		}
		raw, e = json.Marshal(projected)
		if e != nil {
			return e
		}
		if batch.Len()+len(raw)+1 > campaign.MaxContentBytes {
			if e = flush(); e != nil {
				writeErr = e
				return e
			}
		}
		batch.Write(raw)
		batch.WriteByte('\n')
		return nil
	})
	if writeErr != nil {
		return receipt, writeErr
	}
	if e = ctx.Err(); e != nil {
		return receipt, e
	}
	if inspect.ManifestDigest == "" {
		return receipt, campaign.ErrCorrupt
	}
	if e = flush(); e != nil {
		return receipt, e
	}
	result.ManifestDigest = inspect.ManifestDigest
	result.VerifiedEvents = inspect.VerifiedEvents
	result.JournalIntact = scanErr == nil && inspect.JournalIntact
	result.TargetAdapter = inspect.Manifest.Target.Adapter
	result.NativeProfile = inspect.Manifest.Target.NativeFeedbackProfile
	if result.TargetAdapter == "https/v1" {
		result.Assurance = "declared-observer"
		result.NativeProfile = ""
		result.TargetClosure = "not-applicable"
		result.InjectionCleanup = "not-needed"
		result.TargetStop = "not-applicable"
	}
	result.UntestedOperations, result.Routes = routeFacts.summarize()
	if !launchInputsRetained {
		result.Gaps = append(result.Gaps, "launch_inputs_incomplete")
	}
	result.UnknownOperations = []string{}
	for _, operation := range inspect.Operations {
		if operation.Outcome == campaign.Unknown {
			result.UnknownOperations = append(result.UnknownOperations, operation.OperationID)
		}
	}
	if bundleRetained && bundleDigest != inspect.Manifest.ScenarioBundleDigest {
		bundleRetained = false
		result.Gaps = append(result.Gaps, "submitted_bundle_pin_mismatch")
	}
	if !result.JournalIntact {
		result.Gaps = append(result.Gaps, "journal_incomplete")
		result.Execution = "unknown"
	}
	for _, v := range attempts {
		if v.Dispatched {
			scenarioTested[v.ScenarioID] = true
			for _, id := range v.ObjectiveIDs {
				tested[id] = true
			}
		}
		result.Attempts = append(result.Attempts, v)
	}
	sort.Slice(result.Attempts, func(i, j int) bool { return result.Attempts[i].Index < result.Attempts[j].Index })
	result.UntestedScenarios = []string{}
	for id := range scenarios {
		if !scenarioTested[id] {
			result.UntestedScenarios = append(result.UntestedScenarios, id)
		}
	}
	sort.Strings(result.UntestedScenarios)
	if bundleRetained {
		result.Coverage = "recorded-attempts-only"
		for id := range objectives {
			if !tested[id] {
				result.UntestedObjectives = append(result.UntestedObjectives, id)
			}
		}
		sort.Strings(result.UntestedObjectives)
	} else {
		result.Gaps = append(result.Gaps, "objective_coverage_unavailable")
	}
	sessions, e := nativerecovery.RetainedEvidence(ctx, a)
	if e != nil {
		sessions = []nativerecovery.RetainedSession{}
		result.Gaps = append(result.Gaps, "native_evidence_unavailable")
	}
	archives := []campaign.NativeEvidence{}
	for i := range sessions {
		complete := false
		for j := range sessions[i].Archives {
			archive := &sessions[i].Archives[j]
			if archive.Verified {
				archives = append(archives, archive.Record)
				if archive.Record.Provenance.State == "complete" {
					complete = true
				}
			}
			archive.Record.Path = "evidence/" + archive.Record.Provenance.Transfer.SHA256[7:] + ".tar"
		}
		if !complete {
			result.Gaps = append(result.Gaps, "session_evidence_incomplete:"+sessions[i].Identity.SessionID)
		}
		for j := range sessions[i].Outcomes {
			v := &sessions[i].Outcomes[j]
			if v.ArchiveDigest != "" {
				v.ArchivePath = "evidence/" + v.ArchiveDigest[7:] + ".tar"
			}
		}
	}
	recovery, e := a.RecoveryResult()
	if e != nil {
		result.Gaps = append(result.Gaps, "cleanup_audit_unverified")
	} else if len(recovery) > 0 {
		result.RecoveryDigest = contracts.RawDigest(recovery)
		var v nativerecovery.Outcome
		if json.Unmarshal(recovery, &v) != nil || v.ManifestDigest != inspect.ManifestDigest || v.CampaignID != id {
			result.Gaps = append(result.Gaps, "cleanup_audit_unverified")
		} else {
			result.TargetClosure, result.InjectionCleanup, result.TargetStop = v.Closure, v.Cleanup, v.TargetStop
		}
	}
	termination, e := campaign.ReadTerminationResult(root, id)
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		result.Gaps = append(result.Gaps, "termination_audit_unverified")
	}
	if e == nil && termination != nil {
		result.Termination = termination.Outcome
		if raw, e := canonical(termination); e == nil {
			result.TerminationDigest = contracts.RawDigest(raw)
		}
	}
	evidenceRaw, e := canonical(map[string]any{"api_version": "operator.dev/target-evidence-set/v1alpha1", "campaign_id": id, "visibility": "administrator-native-evidence", "sessions": sessions})
	if e != nil {
		return receipt, e
	}
	result.EvidenceSetDigest = contracts.RawDigest(evidenceRaw)
	bundleRaw, e := canonical(map[string]any{"api_version": "operator.dev/campaign-execution-bundle/v1alpha1", "campaign_id": id, "run_manifest": inspect.Manifest, "journal_intact": result.JournalIntact, "verified_events": inspect.VerifiedEvents, "source_parts": parts, "visibility": "administrator-evidence; host control records omitted", "claims_authority": "harness assertions are non-authoritative"})
	if e != nil {
		return receipt, e
	}
	result.ExecutionBundleDigest = contracts.RawDigest(bundleRaw)
	raw, e := canonical(result)
	if e != nil {
		return receipt, e
	}
	resultDigest := contracts.RawDigest(raw)
	reportRaw, e := canonical(map[string]any{"api_version": "operator.dev/campaign-report/v1alpha1", "result_manifest_digest": resultDigest, "summary": result})
	if e != nil {
		return receipt, e
	}
	for _, file := range []struct {
		name string
		raw  []byte
	}{{"result-manifest.json", raw}, {"execution-bundle.json", bundleRaw}, {"target-evidence-set.json", evidenceRaw}, {"report.json", reportRaw}, {"report.md", markdown(result, resultDigest)}} {
		if e = files.Put(ctx, file.name, file.raw); e != nil {
			return receipt, e
		}
	}
	generation, digest, e := files.Commit(ctx)
	if e != nil {
		return receipt, e
	}
	receipt = Receipt{"operator.dev/report-receipt/v1alpha1", id, digest, resultDigest, result.JournalIntact}
	if output != "" {
		e = a.ExportDerived(ctx, generation, digest, output, archives)
	}
	return receipt, e
}
func canonical(v any) ([]byte, error) {
	raw, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return contracts.Canonicalize(raw, campaign.DerivedManifestLimit)
}
func publicContent(kind, role string) bool {
	if kind == "campaign.launch-input" || kind == "campaign.artifact-staged" || kind == "campaign.reference-staged" || kind == "assessment.record" || kind == "model.intent" || kind == "model.exchange" {
		return true
	}
	if kind == "service.request" || kind == "service.response" || kind == "artifact.part" {
		return true
	}
	return role == "guest-request" || role == "guest-result" || role == "tool-body" || role == "tool-result" || role == "publication-index" || strings.HasPrefix(role, "feedback-")
}
func readObjectives(raw []byte, objectives map[string]bool, scenarios map[string][]string) error {
	var v struct {
		Objectives []struct {
			ID string `json:"objective_id"`
		} `json:"objectives"`
		Scenarios []struct {
			ID   string   `json:"scenario_id"`
			Refs []string `json:"objective_refs"`
		} `json:"scenarios"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return campaign.ErrCorrupt
	}
	for _, o := range v.Objectives {
		objectives[o.ID] = true
	}
	for _, s := range v.Scenarios {
		scenarios[s.ID] = s.Refs
	}
	return nil
}

func markdown(r Result, digest string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Campaign report\n\nCampaign: `%s`\n\nResult: `%s`\n\n", r.CampaignID, digest)
	fmt.Fprintf(&b, "| Recorded fact | Value |\n| --- | --- |\n| Journal intact | %t |\n| Verified events | %d |\n| Execution | %s |\n| Container cleanup | %s |\n| Target closure | %s |\n| Injection cleanup | %s |\n| Target stop | %s |\n| Attempts | %d |\n| Harness claim records | %d |\n", r.JournalIntact, r.VerifiedEvents, r.Execution, r.ContainerCleanup, r.TargetClosure, r.InjectionCleanup, r.TargetStop, len(r.Attempts), r.HarnessClaims)
	b.WriteString("\nAttempt completion is not proof of exploitation. Delivery, target responses and scoped oracle observations remain source evidence. Harness conclusions are claims; missing oracle evidence is unknown. No model interpretation is performed.\n")
	if len(r.Gaps) > 0 {
		b.WriteString("\n## Evidence gaps\n\n")
		for _, gap := range r.Gaps {
			fmt.Fprintf(&b, "- `%s`\n", gap)
		}
	}
	return []byte(b.String())
}
