package contracts

import (
	"encoding/json"
	"errors"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"strings"
	"testing"
)

func TestSharedSizePolicy(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/size-policy.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Operation, FieldPath string
		Body                       map[string]any
		Limits                     SizeLimits
		Repeat                     *int
		Valid                      bool
		Path                       string `json:"field_path"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Repeat != nil {
				c.Body["input"].(map[string]any)["text"] = strings.Repeat("x", *c.Repeat)
			}
			q := map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 0, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": 1, "call_id": "call-1", "operation_id": "request-1", "operation": c.Operation, "timeout_ms": 123456, "body": c.Body}
			encoded, _ := json.Marshal(q)
			err := p.ValidateRequestSizes(encoded, c.Limits)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v error=%v", c.Valid, err)
			}
			if !c.Valid {
				var issue *ArgumentError
				if !errors.As(err, &issue) || issue.Reason != "configured_size_exceeded" || issue.FieldPath != c.Path {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestStartupOperatingPolicyRequired(t *testing.T) {
	p, _ := LoadProtocol(schemas.Files)
	raw, _ := os.ReadFile("../schemas/fixtures/engine-context-example.json")
	for _, mode := range []string{"missing-timeouts", "extra-timeout", "zero-timeout", "missing-sizes", "zero-size", "over-ceiling", "valid-custom"} {
		t.Run(mode, func(t *testing.T) {
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			timeouts := c["operation_timeouts_ms"].(map[string]any)
			sizes := c["size_limits"].(map[string]any)
			switch mode {
			case "missing-timeouts":
				delete(c, "operation_timeouts_ms")
			case "extra-timeout":
				timeouts["engine.fake"] = 1000
			case "zero-timeout":
				for name := range timeouts {
					timeouts[name] = 0
					break
				}
			case "missing-sizes":
				delete(c, "size_limits")
			case "zero-size":
				sizes["artifact_part_bytes"] = 0
			case "over-ceiling":
				sizes["direct_input_bytes"] = DirectInputLimit + 1
			case "valid-custom":
				for name := range timeouts {
					timeouts[name] = 654321
				}
				sizes["direct_input_bytes"] = 65536
			}
			encoded, _ := json.Marshal(c)
			_, err := p.ValidateEngineContext(encoded)
			if (err == nil) != (mode == "valid-custom") {
				t.Fatal(err)
			}
		})
	}
}
