package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaignlimits"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

// Require these capabilities through the production resolver, not a policy
// assembled by the test. The installed profile has no injection routes.
func requiredSnapshotResources(t *testing.T) func(*preparation.Input) {
	return func(in *preparation.Input) {
		snapshotInput(t)(in)
		e, err := capabilities.FromNative(in.Protocol.Catalog(), in.Attachment.Capabilities, "delivery-example")
		if err != nil {
			t.Fatal(err)
		}
		in.Authoring = e
		var bundle map[string]any
		if json.Unmarshal(in.Bundle, &bundle) != nil {
			t.Fatal("bundle")
		}
		requirements := bundle["target_requirements"].(map[string]any)
		requirements["capability_source_digest"] = e.SourceDigest()
		requirements["required_capability_refs"] = []string{"operation:invoke", "feature:snapshots", "service:tickets", "file_namespace:records"}
		in.Bundle = encode(bundle)
	}
}

func TestProductionSnapshotFeatureSelection(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		available, zeroBudget bool
	}{
		{"unsupported", false, false}, {"supported", true, false}, {"creation-disabled", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := fixture(t)
			if tc.available {
				requiredSnapshotResources(t)(&in)
			}
			target, err := preparation.Build(in)
			if err != nil {
				t.Fatal(err)
			}
			config := launchConfig(t, target, "openai-chat-text-tools-v1")
			if tc.zeroBudget {
				config.Limits, err = campaignlimits.ParseHost([]byte(`{"max_snapshot_admissions":0,"max_snapshot_bytes":0}`))
				if err != nil {
					t.Fatal(err)
				}
			}
			launch, err := target.BuildLaunch(config)
			if err != nil {
				t.Fatal(err)
			}
			var v struct {
				Operations []string `json:"operations"`
			}
			if json.Unmarshal(launch.Inputs.EngineContext, &v) != nil {
				t.Fatal("context")
			}
			for _, op := range []string{"engine.snapshot_request", "engine.snapshot_list", "engine.snapshot_inspect", "engine.restore_request"} {
				if slices.Contains(v.Operations, op) != tc.available {
					t.Fatalf("feature selection: %s", op)
				}
			}
			if bytes.Contains(launch.ModelTools, []byte(`"snapshot_request"`)) != tc.available {
				t.Fatal("model tool selection disagrees with operation selection")
			}
		})
	}
}

func TestUnsupportedSnapshotRequestsNeverDispatch(t *testing.T) {
	s, p, _, _, launch := serviceWithOperations(t, 0, snapshotRoutes)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	initialCalls := len(p.calls)
	p.mu.Unlock()
	for i, op := range []string{"engine.snapshot_request", "engine.snapshot_list", "engine.snapshot_inspect", "engine.restore_request"} {
		body := map[string]any{}
		if i >= 2 {
			body = map[string]any{"source_session": "session-1", "checkpoint_id": "cp-1-000000000000000000000001"}
		}
		raw, err := s.Handle(context.Background(), stateWire(op, op+"-unsupported", 1, body), 0)
		if err != nil || !strings.Contains(string(raw), "UNSUPPORTED_CAPABILITY") || !strings.Contains(string(raw), `"effect_state":"none"`) {
			t.Fatal(op, string(raw), err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.calls) != initialCalls || p.restores != 0 || len(p.allowances) != 0 {
		t.Fatal("unsupported feature reached native peer", p.calls)
	}
}

func TestPreparationNarrowsFeedbackWithoutGrantingInjection(t *testing.T) {
	in := fixture(t)
	settings := in.Profile.Settings()
	settings.Feedback.Ceiling = "oracle-assisted"
	settings.Feedback.Kinds = []string{"target_output", "operation_error", "injection_delivery", "oracle_outcome"}
	var err error
	in.Profile, err = targetprofile.Parse(encode(settings))
	if err != nil {
		t.Fatal(err)
	}
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(target.ReadKinds(), []string{"target_output"}) {
		t.Fatal(target.ReadKinds())
	}
	var record struct {
		Resolved struct {
			Capabilities capabilities.Policy `json:"capabilities"`
		} `json:"resolved_policy"`
	}
	if json.Unmarshal(target.HostPolicyJSON(), &record) != nil {
		t.Fatal("policy")
	}
	if !record.Resolved.Capabilities.Allows("service:tickets") || len(record.Resolved.Capabilities.SelectableActionRefs) != 0 || record.Resolved.Capabilities.Allows("evidence:oracle_outcome") {
		t.Fatal(record)
	}
}
