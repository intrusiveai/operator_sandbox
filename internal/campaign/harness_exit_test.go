package campaign

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHarnessExitUnknownIsNotZeroAndLegacyRemainsAbsent(t *testing.T) {
	h := HarnessExit{Outcome: "unconfirmed", ExitUnavailable: "exit_status_unavailable", StderrState: "unavailable", Details: "campaign journal event launch.exit"}
	raw, e := json.Marshal(h)
	if e != nil || !strings.Contains(string(raw), `"exit_code":null`) {
		t.Fatal(string(raw), e)
	}
	h.ExitObserved = true
	h.ExitCode = 0
	raw, e = json.Marshal(h)
	if e != nil || !strings.Contains(string(raw), `"exit_code":0`) {
		t.Fatal(string(raw), e)
	}
	var old struct {
		HarnessExit *HarnessExit `json:"harness_exit"`
	}
	if json.Unmarshal([]byte(`{"stop_accepted":true}`), &old) != nil || old.HarnessExit != nil {
		t.Fatal(old)
	}
}
