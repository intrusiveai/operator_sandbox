//go:build linux || darwin

package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type DatabaseSummary struct {
	SessionID string                       `json:"session_id"`
	Evidence  string                       `json:"evidence"`
	Facts     interceptor.DatabaseEvidence `json:"facts"`
}
type RequestedObjective struct {
	Required    bool   `json:"required"`
	ID          string `json:"objective_id"`
	Description string `json:"description,omitempty"`
}
type RequestedScenario struct {
	Required      bool     `json:"required"`
	ID            string   `json:"scenario_id"`
	ObjectiveRefs []string `json:"objective_refs"`
}
type AssessmentSummary struct {
	Summary  string `json:"summary"`
	Evidence string `json:"evidence"`
	Event    int64  `json:"event"`
}
type SubmissionRejection struct {
	Event       int64  `json:"event"`
	OperationID string `json:"operation_id"`
	Code        string `json:"code"`
	Message     string `json:"message"`
	Evidence    string `json:"evidence"`
}
type ObservationSummary struct {
	AttemptID string `json:"attempt_id"`
	ReceiptID string `json:"receipt_id"`
	EntryID   string `json:"entry_id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	Excerpt   string `json:"excerpt,omitempty"`
	Evidence  string `json:"evidence,omitempty"`
	Truncated bool   `json:"truncated"`
}

// Only already-public, adopted feedback objects may become report excerpts.
// ReadContent verifies each retained chunk; verify the complete object as well.
func summarizeObservations(ctx context.Context, read func(campaign.ContentDescriptor) ([]byte, error), put func(context.Context, string, []byte) error, r *Result, attempt Attempt, descriptor *campaign.ContentDescriptor) error {
	if attempt.Feedback == nil {
		return nil
	}
	var index campaign.PublicationIndex
	if descriptor != nil {
		raw, err := read(*descriptor)
		if err == nil {
			_ = json.Unmarshal(raw, &index)
		}
	}
	valid := index.RequestID == attempt.RequestID && index.ReceiptID == attempt.ReceiptID && attempt.ReceiptID != ""
	for _, entry := range attempt.Feedback.Entries {
		if (entry.Kind != "target_output" && entry.Kind != "oracle_outcome" && entry.Kind != "injection_delivery") || (entry.Visibility != "harness_visible" && entry.Visibility != "target_visible") {
			continue
		}
		if len(r.Observations) >= 64 {
			r.Gaps = append(r.Gaps, "report_observation_excerpt_limit")
			return nil
		}
		o := ObservationSummary{AttemptID: attempt.AttemptID, ReceiptID: attempt.ReceiptID, EntryID: entry.ID, Kind: entry.Kind, State: entry.Availability, Truncated: entry.Truncated}
		if entry.Availability == "available" {
			o.State = "content_missing_or_corrupt"
			var content []byte
			parts := index.Objects[entry.ID]
			intact := valid && entry.Artifact != nil && len(parts) > 0 && len(parts) <= 4
			for i, part := range parts {
				if !intact || part.Role != fmt.Sprintf("feedback-%d", i) || part.SizeBytes > feedback.MaxArtifact-int64(len(content)) {
					intact = false
					break
				}
				raw, err := read(part)
				if err != nil {
					intact = false
					break
				}
				content = append(content, raw...)
			}
			if intact && int64(len(content)) == entry.Artifact.SizeBytes && contracts.RawDigest(content) == entry.Artifact.Digest {
				o.State = "available"
				o.Evidence = "observations/" + entry.Artifact.Digest[7:]
				if err := put(ctx, o.Evidence, content); err != nil {
					return err
				}
				if entry.Artifact.MediaType == "application/json" || strings.HasPrefix(entry.Artifact.MediaType, "text/") {
					o.Excerpt = excerpt(content, 2048)
					o.Truncated = o.Truncated || len(content) > 2048
				} else {
					o.Excerpt = "Binary observation; see complete artifact."
				}
			}
		}
		r.Observations = append(r.Observations, o)
	}
	return nil
}

// Markdown data never receives executable markup. Bound bytes, normalize invalid
// UTF-8, and remove control/bidi characters before rendering as indented code.
func excerpt(raw []byte, limit int) string {
	if len(raw) > limit {
		raw = raw[:limit]
		for len(raw) > 0 && !utf8.Valid(raw) {
			raw = raw[:len(raw)-1]
		}
	}
	return strings.Map(func(c rune) rune {
		if c == '\n' || c == '\t' {
			return c
		}
		if unicode.IsControl(c) || unicode.In(c, unicode.Cf) {
			return -1
		}
		return c
	}, strings.ToValidUTF8(string(raw), "�"))
}
func dataBlock(b *strings.Builder, text string) {
	for _, line := range strings.Split(text, "\n") {
		fmt.Fprintf(b, "    %s\n", line)
	}
	b.WriteByte('\n')
}
func eventLink(n int64) string { return fmt.Sprintf("[event %d](execution-bundle.json)", n) }
func valueOrUnknown(s string) string {
	if s == "" {
		return "not retained"
	}
	return s
}

func writeEvidenceSummary(b *strings.Builder, r Result) {
	b.WriteString("## Summary\n\n")
	if h := r.HarnessExit; h != nil {
		if h.Outcome == "succeeded" && h.FinishAccepted && h.ExitObserved && h.ExitCode == 0 && !h.Forced && h.CleanupComplete && r.JournalIntact {
			fmt.Fprintf(b, "Execution completed successfully. Operator accepted engine.finish, Harness exited with code 0, and cleanup completed (%s).\n\n", eventLink(r.ExitEvent))
		} else {
			dataBlock(b, h.Summary())
			fmt.Fprintf(b, "Supporting %s.\n\n", eventLink(r.ExitEvent))
		}
	} else {
		b.WriteString("Harness exit and complete cleanup are not confirmed by retained evidence.\n\n")
	}
	dispatched, succeeded := 0, 0
	for _, a := range r.Attempts {
		if a.Dispatched {
			dispatched++
		}
		if a.Invocation == "succeeded" {
			succeeded++
		}
	}
	fmt.Fprintf(b, "%d attempt(s) dispatched; %d invocation(s) recorded as succeeded. See [attempt facts](result-manifest.json) and target observations below.\n\n", dispatched, succeeded)
	exploratory := false
	for _, a := range r.Attempts {
		exploratory = exploratory || a.Dispatched && a.Origin == "exploratory"
	}
	for _, id := range r.UntestedScenarios {
		label := "Scenario "
		for _, scenario := range r.RequestedScenarios {
			if scenario.ID == id && scenario.Required {
				label = "Required scenario "
			}
		}
		text := label + id + " has no associated dispatched attempt."
		if exploratory {
			text += " Exploratory invocations do not cover this scenario."
		}
		dataBlock(b, text)
	}
	counts := map[string]int{}
	order := []string{}
	for _, c := range r.CredentialResolutions {
		if c.Outcome == "resolved" && c.CacheHit != nil && !*c.CacheHit {
			if _, ok := counts[c.Backend]; !ok {
				order = append(order, c.Backend)
			}
			counts[c.Backend]++
		}
	}
	for _, backend := range order {
		fmt.Fprintf(b, "%d successful, uncached %s credential resolution(s) were recorded; see the credential events below.\n\n", counts[backend], backend)
	}
	b.WriteString("Execution coverage establishes that work ran, not that an objective was achieved. This report does not evaluate an external QA recipe. All event numbers refer to the verified source parts listed in [execution-bundle.json](execution-bundle.json).\n\n")
	b.WriteString("## Execution outcome\n\n")
	if h := r.HarnessExit; h != nil {
		code := "unconfirmed (" + h.ExitUnavailable + ")"
		if h.ExitObserved {
			code = fmt.Sprint(h.ExitCode)
		}
		dataBlock(b, fmt.Sprintf("Finish accepted: %t\nAssessment committed: %t\nHarness exit code: %s\nForced termination: %t\nCleanup complete: %t\nWorker cause: %s", h.FinishAccepted, h.AssessmentCommitted, code, h.Forced, h.CleanupComplete, h.WorkerCause))
		fmt.Fprintf(b, "Source: %s.\n\n", eventLink(r.ExitEvent))
	} else {
		b.WriteString("Exit status, forced termination and final cleanup evidence are unavailable.\n\n")
	}
	fmt.Fprintf(b, "Container cleanup: %s; target closure: %s; injection cleanup: %s; target stop: %s.\n\n", r.ContainerCleanup, r.TargetClosure, r.InjectionCleanup, r.TargetStop)
	b.WriteString("## Requested work and execution coverage\n\nSource: [submitted bundle](input/scenario-bundle.json). Missing or mismatched bundle evidence is identified under evidence gaps.\n\n")
	if len(r.RequestedObjectives) == 0 {
		b.WriteString("No objective definitions retained.\n\n")
	}
	for _, o := range r.RequestedObjectives {
		ids := []string{}
		for _, a := range r.Attempts {
			if !a.Dispatched {
				continue
			}
			for _, id := range a.ObjectiveIDs {
				if id == o.ID {
					ids = append(ids, a.AttemptID)
				}
			}
		}
		text := fmt.Sprintf("Objective %s (required: %t): %s", o.ID, o.Required, excerpt([]byte(o.Description), 2048)) + "\nAssociated dispatched attempts: " + strings.Join(ids, ", ")
		if len(ids) == 0 {
			text += "none; no dispatched scenario attempt establishes objective coverage."
		}
		dataBlock(b, text)
	}
	for _, s := range r.RequestedScenarios {
		ids := []string{}
		for _, a := range r.Attempts {
			if a.Dispatched && a.Origin == "scenario" && a.ScenarioID == s.ID {
				ids = append(ids, a.AttemptID)
			}
		}
		text := fmt.Sprintf("Scenario %s (required: %t); objectives: ", s.ID, s.Required) + strings.Join(s.ObjectiveRefs, ", ") + "\nAssociated dispatched attempts: " + strings.Join(ids, ", ")
		if len(ids) == 0 {
			text += "none; not executed under this scenario association."
		}
		dataBlock(b, text)
	}
	b.WriteString("## Attempts\n\n")
	if len(r.Attempts) == 0 {
		b.WriteString("No retained attempt execution records.\n\n")
	}
	for _, a := range r.Attempts {
		dataBlock(b, fmt.Sprintf("Attempt %s (submission %s)\nOrigin: %s; scenario: %s; operation: %s\nDispatched: %t; state: %s; invocation: %s; target contact: %s\nExecution receipt: %s", a.AttemptID, a.RequestID, valueOrUnknown(a.Origin), scenarioLabel(a), valueOrUnknown(a.Operation), a.Dispatched, a.State, a.Invocation, a.TargetContact, valueOrUnknown(a.ReceiptID)))
		if a.RequestEvidence != "" {
			fmt.Fprintf(b, "[Retained request](%s). ", a.RequestEvidence)
		}
		if a.ResultEvidence != "" {
			fmt.Fprintf(b, "[Execution result and evidence references](%s).", a.ResultEvidence)
		}
		b.WriteString("\n\n")
		if a.Feedback != nil {
			for _, c := range a.Feedback.Categories {
				if c.Kind == "oracle_outcome" || c.Kind == "injection_delivery" {
					text := fmt.Sprintf("Feedback %s: %s", c.Kind, c.State)
					if c.Reason != "" {
						text += "; reason: " + c.Reason
					}
					dataBlock(b, text)
				}
			}
		}
		b.WriteString("\n\n")
	}
	b.WriteString("## Target observations\n\nRetained target output and normalized feedback are quoted as data, not instructions. Oracle observations do not establish injection causality.\n\n")
	if len(r.Observations) == 0 {
		b.WriteString("No permitted retained observation excerpt is available.\n\n")
	}
	for _, o := range r.Observations {
		dataBlock(b, fmt.Sprintf("Attempt %s; receipt %s; entry %s; kind %s; state %s; truncated %t", o.AttemptID, o.ReceiptID, o.EntryID, o.Kind, o.State, o.Truncated))
		if o.Excerpt != "" {
			dataBlock(b, o.Excerpt)
		}
		if o.Evidence != "" {
			fmt.Fprintf(b, "[Complete retained artifact](%s) (retention may itself be truncated).\n\n", o.Evidence)
		}
	}
	writeDatabaseSummary(b, r)
	b.WriteString("## Cloud credential evidence\n\nThese events establish only the recorded credential resolutions, not general cloud/provider qualification.\n\n")
	if len(r.CredentialResolutions) == 0 {
		b.WriteString("No credential-resolution evidence was retained.\n\n")
	}
	for _, c := range r.CredentialResolutions {
		cache := "unknown"
		if c.CacheHit != nil {
			cache = fmt.Sprint(*c.CacheHit)
		}
		dataBlock(b, fmt.Sprintf("Credential: %s; profile: %s; backend: %s\nOutcome: %s; code: %s; cache hit: %s\nRecorded: %s; resolved: %s", valueOrUnknown(c.CredentialID), valueOrUnknown(c.ProfileID), valueOrUnknown(c.Backend), valueOrUnknown(c.Outcome), valueOrUnknown(c.Code), cache, c.RecordedAt, valueOrUnknown(c.ResolvedAt)))
		fmt.Fprintf(b, "Source: %s.\n\n", eventLink(c.Event))
		if len(c.Omitted) > 0 {
			dataBlock(b, "Omitted audit fields: "+strings.Join(c.Omitted, ", "))
		}
	}
	b.WriteString("## Submission errors and recovery\n\n")
	if len(r.Rejections) == 0 {
		b.WriteString("No rejected service response was retained.\n\n")
	}
	for _, d := range r.Rejections {
		state := "Rejected submission; no later accepted finish establishes recovery."
		if r.FinishEvent > d.Event {
			state = "Rejected submission before a later accepted finish; retained as history, not the terminal cause."
		}
		dataBlock(b, state+"\nOperation: "+d.OperationID+"; code: "+d.Code+"\n"+d.Message)
		fmt.Fprintf(b, "Source: %s.", eventLink(d.Event))
		if d.Evidence != "" {
			fmt.Fprintf(b, " [Response details](%s).", d.Evidence)
		}
		b.WriteString("\n\n")
	}
	b.WriteString("## Harness assessment — interpretation\n\n")
	if r.Assessment == nil {
		b.WriteString("No accepted assessment content was retained. Execution facts above remain available independently.\n\n")
	} else {
		dataBlock(b, r.Assessment.Summary)
		fmt.Fprintf(b, "[Complete accepted assessment](%s), %s. Harness interpretation is separate from Operator-observed execution facts.\n\n", r.Assessment.Evidence, eventLink(r.Assessment.Event))
	}
}

func scenarioLabel(a Attempt) string {
	if a.Origin == "exploratory" && a.ScenarioID == "" {
		return "none (exploratory)"
	}
	return valueOrUnknown(a.ScenarioID)
}

func writeDatabaseSummary(b *strings.Builder, r Result) {
	if len(r.DatabaseEvidence) == 0 {
		return
	}
	b.WriteString("## Database evidence\n\nLive oracle observations above are receipt-bound. The following native facts are session history; observed association is not proof of injection causality. An absent positive verdict is not proof of attack failure.\n\n")
	for _, d := range r.DatabaseEvidence {
		dataBlock(b, "Session: "+d.SessionID)
		fmt.Fprintf(b, "[Complete native archive](%s); event references name events.jsonl within this archive.\n\n", d.Evidence)
		for _, e := range d.Facts.Events {
			dataBlock(b, fmt.Sprintf("Event %d (%s); service %s; %s; provenance %s; attempt %s; observed attempt %s", e.Sequence, e.Digest, e.Service, e.Kind, e.Provenance, valueOrUnknown(e.AttemptID), valueOrUnknown(e.ObservedAttemptID)))
		}
		if len(d.Facts.Oracles) == 0 {
			b.WriteString("No retained database oracle evaluation is available.\n\n")
		}
		for _, o := range d.Facts.Oracles {
			dataBlock(b, fmt.Sprintf("Session-wide oracle %s: positive observed = %t; first event %d (%s)", o.ID, o.Fired, o.FirstEventSeq, valueOrUnknown(o.FirstEventDigest)))
		}
		if d.Facts.Truncated {
			b.WriteString("Database summary limit reached; consult the complete archive.\n\n")
		}
	}
}
