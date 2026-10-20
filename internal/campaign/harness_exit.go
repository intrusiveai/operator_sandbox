package campaign

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HarnessExit is a host observation, not a Harness claim or wire message.
// Zero-valued fields in older records do not establish a successful exit.
type HarnessExit struct {
	Outcome             string `json:"outcome"`
	FinishAccepted      bool   `json:"finish_accepted"`
	AssessmentCommitted bool   `json:"assessment_committed"`
	ExitObserved        bool   `json:"exit_observed"`
	ExitCode            int    `json:"exit_code"`
	ExitUnavailable     string `json:"exit_unavailable,omitempty"`
	Forced              bool   `json:"forced_termination"`
	WorkerCause         string `json:"worker_cause"`
	Cause               string `json:"cause,omitempty"`
	Stderr              string `json:"stderr,omitempty"`
	StderrTruncated     bool   `json:"stderr_truncated,omitempty"`
	StderrState         string `json:"stderr_state"`
	CleanupComplete     bool   `json:"cleanup_complete"`
	Details             string `json:"details"`
}

func (h HarnessExit) Summary() string {
	state := "without an accepted engine.finish"
	if h.FinishAccepted {
		state = "after Operator accepted engine.finish"
	}
	var text string
	if h.ExitObserved {
		text = fmt.Sprintf("Attack Harness exited with code %d %s.", h.ExitCode, state)
	} else {
		text = "Attack Harness exit status is unconfirmed: " + h.ExitUnavailable + "."
	}
	if h.Forced {
		text += " Operator forced termination."
	}
	if h.Outcome != "succeeded" {
		if h.Cause != "" {
			text += " Cause: " + h.Cause
		} else {
			text += " No causal Harness diagnostic was available."
		}
	}
	if h.Outcome == "succeeded" && !h.CleanupComplete {
		text += " Harness execution succeeded; cleanup is incomplete."
	}
	return text
}

func (h HarnessExit) Valid() bool {
	return (h.Outcome == "succeeded" || h.Outcome == "failed" || h.Outcome == "unconfirmed") && h.ExitCode >= 0 && h.ExitCode <= 255 && len(h.Stderr) <= 8<<10 && len(h.Cause) <= 256 && len(h.WorkerCause) <= 96 && len(h.ExitUnavailable) <= 96 && len(h.StderrState) <= 96 && h.Details == "campaign journal event launch.exit" && strings.IndexFunc(h.Stderr+h.Cause+h.WorkerCause+h.ExitUnavailable, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

// An unavailable code is null on disk/output, never an apparent zero exit.
func (h HarnessExit) MarshalJSON() ([]byte, error) {
	type plain HarnessExit
	var code *int
	if h.ExitObserved {
		code = &h.ExitCode
	}
	return json.Marshal(struct {
		plain
		Code *int `json:"exit_code"`
	}{plain(h), code})
}
