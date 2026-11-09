//go:build linux || darwin

package reporting

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"strings"
	"testing"
)

func TestReportEscapesAndBoundsUntrustedText(t *testing.T) {
	text := "<script>alert(1)</script>\n```\n[click](https://untrusted.invalid)\n" + strings.Repeat("x", 4096)
	r := Result{CampaignID: "campaign-1", Assessment: &AssessmentSummary{Summary: excerpt([]byte(text), 2048), Evidence: "content/abc", Event: 5}, Observations: []ObservationSummary{{Excerpt: excerpt([]byte(text), 2048), Evidence: "observations/abc"}}}
	report := string(markdown(r, "sha256:test"))
	for _, bad := range []string{"\n<script>", "\n```", "\n[click]", strings.Repeat("x", 3000)} {
		if strings.Contains(report, bad) {
			t.Fatal("unbounded/active text", bad[:min(len(bad), 20)])
		}
	}
	if !strings.Contains(report, "    <script>") {
		t.Fatal("lost quoted data")
	}
}

func TestCredentialProjectionOmitsInvalidFieldsAndKeepsTime(t *testing.T) {
	metadata := map[string]json.RawMessage{"credential_id": json.RawMessage(`"https://user:password@example.test"`), "cache_hit": json.RawMessage(`null`), "backend": json.RawMessage(`"Bearer SECRET"`)}
	record, projected := projectCredential(7, "2026-10-19T00:00:00Z", metadata)
	raw, _ := json.Marshal(projected)
	if record.CacheHit != nil || len(record.Omitted) == 0 || record.Event != 7 || record.RecordedAt == "" || strings.Contains(string(raw), "password") || strings.Contains(string(raw), "SECRET") {
		t.Fatal(record, string(raw))
	}
}

func TestReportDistinguishesExitCleanupRecoveryAndMissingFacts(t *testing.T) {
	for _, mode := range []string{"success", "nonzero", "cleanup", "missing"} {
		t.Run(mode, func(t *testing.T) {
			r := Result{CampaignID: "campaign-1", JournalIntact: true, FinishEvent: 5, ExitEvent: 9, Rejections: []SubmissionRejection{{Event: 3, Code: "CONCLUSION_INVALID", Message: "A reference was missing."}}}
			if mode != "missing" {
				r.HarnessExit = &campaign.HarnessExit{Outcome: "succeeded", FinishAccepted: true, AssessmentCommitted: true, ExitObserved: true, ExitCode: 0, CleanupComplete: true}
				if mode == "nonzero" {
					r.HarnessExit.Outcome = "failed"
					r.HarnessExit.ExitCode = 1
					r.HarnessExit.Cause = "controlled fatal error"
				}
				if mode == "cleanup" {
					r.HarnessExit.CleanupComplete = false
				}
			}
			report := string(markdown(r, "sha256:test"))
			if strings.Contains(report, "Execution completed successfully") != (mode == "success") {
				t.Fatal(report)
			}
			if !strings.Contains(report, "retained as history, not the terminal cause") {
				t.Fatal("rejection promoted to cause", report)
			}
			if mode == "nonzero" && (!strings.Contains(report, "Harness exit code: 1") || !strings.Contains(report, "controlled fatal error")) {
				t.Fatal(report)
			}
			if mode == "missing" && !strings.Contains(report, "not confirmed") {
				t.Fatal(report)
			}
		})
	}
}

func TestCollectionSuccessDoesNotConfirmOtherCleanup(t *testing.T) {
	r := Result{CampaignID: "campaign-1", TargetStop: "unconfirmed", TargetClosure: "unconfirmed", InjectionCleanup: "unconfirmed", CleanupResolutions: []CleanupResolution{{SessionID: "session-1", Collection: "complete", History: []campaign.EvidenceOutcome{{SessionID: "session-1", State: "missing", Reason: "export_rejected"}, {SessionID: "session-1", State: "complete"}}}}}
	text := string(markdown(r, "sha256:test"))
	for _, want := range []string{"Currently verified evidence collection: complete", "native rejection details were not retained", "No verified native session_stopped event", "injection cleanup: unconfirmed"} {
		if !strings.Contains(text, want) {
			t.Fatal(want, text)
		}
	}
	if strings.Contains(text, "Target stop confirmed") {
		t.Fatal("collection fabricated lifecycle evidence")
	}
}

func TestReportUsesRecordedTriggerNotCategoryPrecedence(t *testing.T) {
	model := modelprovider.Diagnostic{Provider: "openai-chat", Model: "same-model-as-target", OperationID: "model-2"}.WithFailure("timeout", "deadline_exceeded")
	workflow := campaign.NewWorkflowDiagnostic("target-1", "unknown", "delivery", "target_outcome_unknown", "", "", "")
	for _, trigger := range []string{"model", "workflow", "missing"} {
		t.Run(trigger, func(t *testing.T) {
			r := Result{CampaignID: "campaign-1", ModelFailures: []modelprovider.Diagnostic{model}, WorkflowDiagnostics: []campaign.WorkflowDiagnostic{workflow}, HarnessExit: &campaign.HarnessExit{Outcome: "failed", ExitObserved: true, ExitCode: 137, Forced: true}, CleanupActions: campaign.CleanupDiagnostic{Container: "removed", Transport: "removed", Inputs: "retained", Closure: "unconfirmed", Injections: "unconfirmed", TargetStop: "unconfirmed"}}
			if trigger != "missing" {
				op := "model-2"
				if trigger == "workflow" {
					op = "target-1"
				}
				r.FailureTrigger = &campaign.FailureTrigger{Kind: trigger, OperationID: op}
			}
			text := string(markdown(r, "sha256:test"))
			if !strings.Contains(text, "Attack Harness model request") || !strings.Contains(text, "Operator forced termination") || !strings.Contains(text, "input removal retained") || !strings.Contains(text, "failure origin unknown") {
				t.Fatal(text)
			}
			if trigger == "missing" {
				if !strings.Contains(text, "causal order was not retained") || strings.Contains(text, "Initiating operation failure") {
					t.Fatal(text)
				}
			} else {
				summary := text[strings.Index(text, "## Summary"):strings.Index(text, "## Execution outcome")]
				want := model.Summary()
				if trigger == "workflow" {
					want = workflow.Message()
				}
				if strings.Index(summary, want) < 0 || strings.Index(summary, want) > strings.Index(summary, "Attack Harness exited") {
					t.Fatal("exit displaced known cause", summary)
				}
			}
		})
	}
}
