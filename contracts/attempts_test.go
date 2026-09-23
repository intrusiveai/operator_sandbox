package contracts

import (
	"encoding/json"
	"os"
	"testing"
)

type attemptStep struct {
	Action         string `json:"action"`
	Raw            string `json:"raw"`
	RequestID      string `json:"request_id"`
	AttemptID      string `json:"attempt_id"`
	Index          int64  `json:"attempt_index"`
	AllocatedIndex int64  `json:"allocated_index"`
	Identity       string `json:"identity_digest"`
	Result         string `json:"result_digest"`
	Outcome        string `json:"outcome"`
	State          string `json:"state"`
	Replay         bool   `json:"replay"`
	Fresh          bool   `json:"fresh"`
	Valid          bool   `json:"valid"`
	HighWater      int64  `json:"high_watermark"`
	Admissions     int64  `json:"admissions"`
	Closed         bool   `json:"closed"`
}

func TestSharedAttemptBookkeeping(t *testing.T) {
	raw, err := os.ReadFile("../schemas/fixtures/attempt-bookkeeping.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string        `json:"name"`
		Mode      string        `json:"mode"`
		HighWater int64         `json:"high_watermark"`
		Maximum   int64         `json:"maximum"`
		Steps     []attemptStep `json:"steps"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Mode == "allocator" {
				a, err := NewAttemptAllocator(c.HighWater)
				if err != nil {
					t.Fatal(err)
				}
				water := c.HighWater
				for i, s := range c.Steps {
					got, err := a.Allocate([]byte(s.Raw), s.RequestID, s.AttemptID)
					if (err == nil) != s.Valid || got.Index != s.AllocatedIndex {
						t.Fatalf("step %d: allocation=%+v err=%v", i, got, err)
					}
					if got.Index != 0 {
						water = s.AllocatedIndex
						if got.RequestID != s.RequestID || got.AttemptID != s.AttemptID {
							t.Fatal("IDs changed")
						}
					}
					if a.HighWatermark() != water {
						t.Fatalf("step %d: unexpected allocator watermark", i)
					}
				}
				return
			}
			if c.Mode != "ledger" {
				t.Fatal("unknown fixture mode")
			}
			l, err := NewAttemptLedger(c.HighWater, c.Maximum)
			if err != nil {
				t.Fatal(err)
			}
			for i, s := range c.Steps {
				err = nil
				switch s.Action {
				case "observe":
					sub := AttemptSubmission{AttemptAllocation: AttemptAllocation{RequestID: s.RequestID, AttemptID: s.AttemptID, Index: s.Index}, IdentityDigest: s.Identity}
					r, replay, e := l.Observe(sub)
					err = e
					if err == nil {
						if replay != s.Replay || r.State != s.State || r.AttemptSubmission != sub {
							t.Fatalf("step %d: wrong replay/record", i)
						}
						saved, ok := l.Lookup(s.RequestID)
						if !ok || saved != r {
							t.Fatal("lookup differs")
						}
						// Returned value must not let a caller mutate ledger authority.
						r.State = "forged"
						unchanged, _ := l.Lookup(s.RequestID)
						if unchanged != saved {
							t.Fatal("mutable record escaped")
						}
					}
				case "admit":
					fresh, e := l.Admit(s.RequestID)
					err = e
					if err == nil && fresh != s.Fresh {
						t.Fatalf("step %d: wrong admission disposition", i)
					}
				case "resolve":
					err = l.Resolve(s.RequestID, s.Outcome, s.Result)
					if err == nil {
						r, _ := l.Lookup(s.RequestID)
						if r.State != s.Outcome || r.ResultDigest != s.Result {
							t.Fatal("result was not retained")
						}
					}
				case "close":
					l.Close()
				case "retain": // Restore/skip do not replace or mutate the ledger.
				default:
					t.Fatal("unknown fixture action")
				}
				if (err == nil) != s.Valid {
					t.Fatalf("step %d: valid=%v want=%v error=%v", i, err == nil, s.Valid, err)
				}
				if l.HighWatermark() != s.HighWater || l.Admissions() != s.Admissions || l.Closed() != s.Closed {
					t.Fatalf("step %d: incorrect counters/closure", i)
				}
			}
		})
	}
	t.Logf("%d shared attempt bookkeeping traces", len(cases))
}

func TestAttemptBookkeepingBoundaries(t *testing.T) {
	for _, seed := range []int64{-1, MaxSafeInteger + 1} {
		if _, err := NewAttemptAllocator(seed); err == nil {
			t.Fatal("invalid seed accepted")
		}
		if _, err := NewAttemptLedger(seed, 100); err == nil {
			t.Fatal("invalid ledger seed accepted")
		}
	}
	for _, maximum := range []int64{-1, 0, MaxSafeInteger + 1} {
		if _, err := NewAttemptLedger(0, maximum); err == nil {
			t.Fatal("invalid limit accepted")
		}
	}
	a, _ := NewAttemptAllocator(0)
	if got, err := a.Allocate(make([]byte, OrdinaryLimit+1), "r1", "a1"); err == nil || got.Index != 0 || a.HighWatermark() != 0 {
		t.Fatal("oversize args allocated an index")
	}
}
