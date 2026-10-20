package contracts

import (
	"encoding/json"
	"errors"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestSharedAttemptOrigin(t *testing.T) {
	raw, err := os.ReadFile("../schemas/fixtures/attempt-origin.json")
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Scenarios []string
		Cases     []struct {
			Name         string
			Body         map[string]any
			Valid        bool
			Reason, Path string
		}
	}
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	scenarios := map[string]bool{}
	for _, s := range f.Scenarios {
		scenarios[s] = true
	}
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			err := ValidateAttemptOrigin(c.Body, scenarios)
			if c.Valid {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var issue *ArgumentError
				if !errors.As(err, &issue) || issue.Reason != c.Reason || issue.FieldPath != c.Path {
					t.Fatal(err)
				}
			}
			c.Body["operation_id"] = "invoke"
			c.Body["input"] = map[string]any{"encoding": "json", "value": map[string]any{}}
			b, _ := json.Marshal(c.Body)
			_, err = p.ValidateToolArguments("attempt_execute", b)
			// Bundle membership is a host/runtime-context rule, not a static schema rule.
			if (err == nil) != (c.Valid || c.Reason == "unknown_scenario") {
				t.Fatal("tool validation", err)
			}
			envelope := map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 0, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": 1, "call_id": "call-1", "operation_id": "submission-1", "operation": "engine.attempt_execute", "timeout_ms": 30000, "body": c.Body}
			wire, _ := json.Marshal(envelope)
			_, err = p.ValidateRequest(wire)
			if (err == nil) != (c.Valid || c.Reason == "unknown_scenario") {
				t.Fatal("wire validation", err)
			}
		})
	}
}
