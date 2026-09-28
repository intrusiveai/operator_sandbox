//go:build linux || darwin

package preparation_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func processText(text string) []byte {
	var v map[string]any
	_ = json.Unmarshal(processResponse(nil), &v)
	m := v["choices"].([]any)[0].(map[string]any)["message"].(map[string]any)
	m["refusal"] = nil
	m["content"] = text
	return encode(v)
}

func TestPythonProcessLargeHistoryCompaction(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		t.Run(transport, func(t *testing.T) {
			reply := func(turn int, q map[string]any) ([]byte, error) {
				switch turn {
				case 1:
					// Cross the production 1 MiB conversation threshold on the next turn,
					// while leaving space for tool-free history inside the compaction request.
					count := (1 << 20) - len(encode(q)) + 8192
					if count <= 512<<10 || count >= 1<<20 {
						return nil, fmt.Errorf("unexpected initial request size")
					}
					return processText(strings.Repeat("x", count)), nil
				case 2:
					if q["tool_choice"] != "none" {
						return nil, fmt.Errorf("compaction exposed tools")
					}
					if len(encode(q)) < 512<<10 {
						return nil, fmt.Errorf("history was silently omitted")
					}
					return processText("History contains unverified model text; the objective remains untested."), nil
				case 3:
					raw := string(encode(q))
					if !strings.Contains(raw, "model-generated-not-evidence") || !strings.Contains(raw, "omitted_complete_segments") || !strings.Contains(raw, "objective-marker") || q["tool_choice"] != "auto" {
						return nil, fmt.Errorf("lost compaction provenance or task")
					}
					return processResponse(nil), nil
				default:
					return nil, fmt.Errorf("unexpected model turn %d", turn)
				}
			}
			r := newProcessRun(t, processCase{transport: transport, prompt: "default", reply: reply})
			r.finish(t)
			if len(r.provider.requests) != 3 {
				t.Fatal("compaction did not run as a charged model turn", len(r.provider.requests))
			}
		})
	}
}
