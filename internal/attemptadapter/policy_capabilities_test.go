package attemptadapter

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func TestResolvedAvailabilityDoesNotGrantInjection(t *testing.T) {
	c, _, in := fixture(t)
	e := in.Live.Export()
	p, err := Resolve(e, Scopes{OperationIDs: []string{"invoke"}}, []string{"target_output"}, "black-box")
	if err != nil {
		t.Fatal(err)
	}
	cp := p.CapabilityPolicy()
	for _, ref := range []string{"service:tickets", "file_namespace:records"} {
		if !cp.Allows(ref) {
			t.Fatalf("missing available resource %s", ref)
		}
	}
	if len(cp.SelectableActionRefs) != 0 || cp.Allows("action:injection:mcp_tool_result") || cp.Allows(capabilities.FeatureSnapshots) {
		t.Fatal("availability granted an unconfigured action or unsupported feature", cp)
	}
	for _, d := range []interceptor.Definition{
		{Surface: "mcp_tool_result", Selector: interceptor.Selector{Service: "tickets", ToolName: "get_ticket"}},
		{Surface: "environment_state", Selector: interceptor.Selector{FileNamespace: "records", RelativePath: "one.json"}},
	} {
		if err := p.authorizeInjection(d, nil); err == nil {
			t.Fatal("resource availability authorized an injection")
		}
	}
	raw, err := os.ReadFile("../../schemas/fixtures/capability-chain/submitted-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		ref     string
		allowed bool
	}{
		{"service:tickets", true}, {"file_namespace:records", true},
		{"action:injection:mcp_tool_result", false}, {"service:missing", false}, {"file_namespace:missing", false},
	} {
		t.Run(tc.ref, func(t *testing.T) {
			b, err := capabilities.ParseBundle(c, mutate(raw, func(m map[string]any) {
				m["scenarios"] = []any{}
				m["target_requirements"].(map[string]any)["required_capability_refs"] = []string{tc.ref}
			}))
			if err != nil {
				t.Fatal(err)
			}
			_, err = capabilities.Check(b, e, in.Live, cp)
			if (err == nil) != tc.allowed {
				t.Fatalf("admission: %v", err)
			}
		})
	}
}

func TestResolvedFeedbackIntersectsAvailableEvidence(t *testing.T) {
	c, _, in := fixture(t) // This target advertises only black-box feedback.
	e := in.Live.Export()
	raw, err := os.ReadFile("../../schemas/fixtures/capability-chain/submitted-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, kinds := range [][]string{nil, {}, {"target_output", "operation_error", "injection_delivery", "oracle_outcome"}} {
		p, err := Resolve(e, Scopes{OperationIDs: []string{"invoke"}}, kinds, "oracle-assisted")
		if err != nil {
			t.Fatal(err)
		}
		cp := p.CapabilityPolicy()
		expected := []string{}
		if slices.Contains(kinds, "target_output") {
			expected = append(expected, "target_output")
		}
		if !slices.Equal(cp.FeedbackKinds, expected) {
			t.Fatal(cp)
		}
		for _, k := range []string{"operation_error", "injection_delivery", "oracle_outcome"} {
			if cp.Allows("evidence:" + k) {
				t.Fatalf("unavailable evidence granted: %s", k)
			}
		}
		b, err := capabilities.ParseBundle(c, raw)
		if err != nil {
			t.Fatal(err)
		}
		compat, err := capabilities.Check(b, e, in.Live, cp)
		if err != nil || !slices.Equal(compat.AllowedKinds(), expected) {
			t.Fatal("feedback narrowing failed", err)
		}
		required, err := capabilities.ParseBundle(c, mutate(raw, func(m map[string]any) {
			m["target_requirements"].(map[string]any)["required_capability_refs"] = []string{"evidence:oracle_outcome"}
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = capabilities.Check(required, e, in.Live, cp); err == nil || !strings.Contains(err.Error(), "evidence:oracle_outcome") {
			t.Fatal("required missing evidence accepted", err)
		}
		var saved struct {
			Capabilities capabilities.Policy `json:"capabilities"`
		}
		if json.Unmarshal(p.RecordJSON(), &saved) != nil || !slices.Equal(saved.Capabilities.FeedbackKinds, expected) {
			t.Fatal("resolved feedback not retained")
		}
	}
	for _, kinds := range [][]string{{"operation_error", "operation_error"}, {"unknown"}} {
		if _, err := Resolve(e, Scopes{}, kinds, ""); err == nil {
			t.Fatal("invalid configured kinds silently filtered", kinds)
		}
	}
}
