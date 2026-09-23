package contracts

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/schemas"
)

type loopStep struct {
	Action        string          `json:"action"`
	Expected      json.RawMessage `json:"expected"`
	FinalExpected json.RawMessage `json:"final_expected"`
	Error         string          `json:"error"`
	Compaction    bool            `json:"compaction"`
	Count         int64           `json:"count"`
	Name          string          `json:"name"`
	Outcome       string          `json:"outcome"`
	SourceKind    string          `json:"source_kind"`
	SourceID      string          `json:"source_id"`
	Offset        int64           `json:"offset"`
	Size          int64           `json:"size"`
	Actual        int64           `json:"actual"`
	Result        *bool           `json:"result"`
	Receipt       string          `json:"receipt"`
	Digest        string          `json:"digest"`
	Models        int64           `json:"models"`
	Reads         int64           `json:"reads"`
	Reason        string          `json:"reason"`
	Now           int64           `json:"now"`
	Deadline      int64           `json:"deadline"`
	Remaining     int64           `json:"remaining"`
	Kind          string          `json:"kind"`
	Bytes         int64           `json:"bytes"`
}

func checkLoopProjection(t *testing.T, actual any, expected json.RawMessage) {
	t.Helper()
	if len(expected) == 0 {
		return
	}
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	a, err := Decode(raw, ControlLimit)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Decode(expected, ControlLimit)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range e.(map[string]any) {
		if !wireEqual(a.(map[string]any)[k], v) {
			t.Fatalf("%s got %v want %v", k, a.(map[string]any)[k], v)
		}
	}
}

func TestSharedLoopAccounting(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/harness-loop-accounting.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string           `json:"name"`
		Mode      string           `json:"mode"`
		Overrides string           `json:"overrides"`
		Caps      string           `json:"caps"`
		Valid     bool             `json:"valid"`
		Expected  json.RawMessage  `json:"expected"`
		Limits    map[string]int64 `json:"limits"`
		Steps     []loopStep       `json:"steps"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Mode == "config" {
				got, err := p.ResolveHarnessLimits([]byte(c.Overrides), []byte(c.Caps))
				if (err == nil) != c.Valid {
					t.Fatalf("valid=%v want=%v err=%v", err == nil, c.Valid, err)
				}
				if err == nil {
					checkLoopProjection(t, got, c.Expected)
				}
				return
			}
			if c.Mode != "loop" {
				t.Fatal("unknown fixture mode")
			}
			l, err := NewHarnessLoop(c.Limits)
			if err != nil {
				t.Fatal(err)
			}
			var budget *FinalizationBudget
			for i, s := range c.Steps {
				err = nil
				result := false
				switch s.Action {
				case "begin_model":
					err = l.BeginModel(s.Compaction)
				case "accept_response":
					err = l.AcceptResponse(s.Count)
				case "start_tool":
					err = l.StartTool(s.Name)
				case "finish_tool":
					err = l.FinishTool(s.Outcome)
				case "reserve_read":
					err = l.ReserveRead(s.SourceKind, s.SourceID, s.Offset, s.Size)
				case "settle_read":
					result, err = l.SettleRead(s.Actual)
				case "experiment":
					result, err = l.ExperimentCompleted(s.Receipt)
				case "payload":
					result, err = l.PayloadCommitted(s.Digest)
				case "end_turn":
					err = l.EndTurn()
				case "narrow":
					err = l.NarrowRemaining(s.Models, s.Reads)
				case "stop":
					err = l.Stop(s.Reason)
				case "hard_stop":
					l.HardStop()
				case "begin_finalization":
					var b *FinalizationBudget
					b, err = l.BeginFinalization(s.Now, s.Deadline, s.Remaining)
					if err == nil {
						budget = b
					}
				case "charge":
					if budget == nil {
						t.Fatal("missing budget")
					}
					err = budget.Charge(s.Kind, s.Bytes, s.Now)
				case "check_time":
					if budget == nil {
						t.Fatal("missing budget")
					}
					err = budget.CheckTime(s.Now)
				case "inspect":
				default:
					t.Fatal("unknown fixture action")
				}
				if (err == nil) != (s.Error == "") || (s.Error == "stopped" && !errors.Is(err, ErrLoopStopped)) || (s.Error == "protocol" && !errors.Is(err, ErrProtocol)) {
					t.Fatalf("step %d (%s) err=%v want=%s", i, s.Action, err, s.Error)
				}
				if s.Result != nil && result != *s.Result {
					t.Fatalf("step %d novelty=%v want=%v", i, result, *s.Result)
				}
				checkLoopProjection(t, l.Snapshot(), s.Expected)
				if len(s.FinalExpected) > 0 {
					if budget == nil {
						t.Fatal("missing finalization")
					}
					checkLoopProjection(t, budget.Snapshot(), s.FinalExpected)
				}
			}
		})
	}
	t.Logf("%d shared loop configuration/accounting cases", len(cases))
}

func TestLoopIsolationAndInvalidLimits(t *testing.T) {
	limits := harnessDefaults()
	l, err := NewHarnessLoop(limits)
	if err != nil {
		t.Fatal(err)
	}
	limits["max_model_turns"] = 1
	snapshot := l.Snapshot()
	snapshot.ModelTurns = 100
	for range 2 {
		if err := l.BeginModel(false); err != nil {
			t.Fatal(err)
		}
		if err := l.AcceptResponse(0); err != nil {
			t.Fatal(err)
		}
		if err := l.EndTurn(); err != nil {
			t.Fatal(err)
		}
	}
	if l.Snapshot().ModelTurns != 2 || l.Snapshot().Mode != "exploring" {
		t.Fatal("caller mutated accounting")
	}
	for _, invalid := range []int64{-1, 0, MaxSafeInteger + 1} {
		limits["max_model_turns"] = invalid
		if _, err := NewHarnessLoop(limits); err == nil {
			t.Fatal("invalid constructor limit accepted")
		}
	}
	delete(limits, "max_model_turns")
	limits["unexpected"] = 1
	if _, err := NewHarnessLoop(limits); err == nil {
		t.Fatal("unknown/missing constructor field accepted")
	}
}
