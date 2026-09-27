//go:build linux || darwin

package hostconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCampaignLimitsYAML(t *testing.T) {
	raw := minimal + "limits:\n  max_snapshot_admissions: 0\n  max_snapshot_bytes: 9007199254740991\n  max_active_seconds: 60\n  harness:\n    max_tool_calls: 40\n"
	c, err := Parse([]byte(raw), linuxDefaults(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(c.Limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"max_snapshot_admissions":0`, `"max_snapshot_bytes":9007199254740991`, `"max_active_seconds":60`, `"max_tool_calls":40`, `"max_model_turns":300`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatal("effective setting missing", want, string(encoded))
		}
	}
	for _, part := range []string{
		"  max_snapshot_bytes: 9007199254740992\n", "  max_snapshot_bytes: -1\n", "  max_snapshot_bytes: 0x20\n", "  max_snapshot_bytes: 1e3\n", "  max_snapshot_bytes: '1'\n", "  max_snapshot_bytes: null\n", "  max_snapshot_bytes: 1\n  max_snapshot_bytes: 2\n", "  max_snapshot_bytes: &n 1\n", "  unknown: 1\n", "  harness: []\n", "  harness:\n    max_model_turns: 0\n", "  harness:\n    max_model_turns: '10'\n", "  harness:\n    max_model_turns: &n 10\n", "  harness:\n    arbitrary: 10\n", "  harness:\n    harness:\n      max_model_turns: 10\n",
	} {
		if _, err := Parse([]byte(minimal+"limits:\n"+part), linuxDefaults(t)); err == nil {
			t.Fatal("ambiguous limits accepted", part)
		}
	}
}
