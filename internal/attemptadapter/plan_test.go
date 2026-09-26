package attemptadapter

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func fixture(t *testing.T) (*contracts.Catalog, []byte, Inputs) {
	t.Helper()
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/capability-chain/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := capabilities.FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	_ = json.Unmarshal(raw, &native)
	b := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
	attachment := interceptor.Attachment{CampaignID: "campaign-1", Binding: b, Capabilities: raw, Session: interceptor.Session{ID: b.SessionID, CampaignID: "campaign-1", OperationAPIVersion: interceptor.OperationVersion, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: native["environment_digest"].(string), AppDigest: native["application_digest"].(string), CapabilityManifestDigest: e.SourceDigest()}}
	status := interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: b, Sessions: map[string]interceptor.Binding{b.SessionID: b}, Phase: "ready", StoreAvailable: true}
	live, err := capabilities.BindLive(c, attachment, status, "instance-1", "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	scopes := Scopes{OperationIDs: []string{"invoke"}, AllowRetainedInjections: true, Routes: []Route{{Surface: "mcp_tool_result", Target: Target{Service: "tickets", ToolName: "get_ticket"}, Scopes: []string{"once", "standing"}, Placements: []string{"append_text"}, Pointers: []string{"/structuredContent/description"}}}}
	p, err := Resolve(e, scopes, []string{"target_output"}, "")
	if err != nil {
		t.Fatal(err)
	}
	bundleRaw, err := os.ReadFile("../../schemas/fixtures/capability-chain/submitted-bundle.json")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := capabilities.ParseBundle(c, bundleRaw)
	if err != nil {
		t.Fatal(err)
	}
	compat, err := capabilities.Check(bundle, e, live, p.CapabilityPolicy())
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte(`"Try the benign marker"`)
	carrier := []byte(`{"query":"Summarize ticket T-1"}`)
	pd := interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(payload), SizeBytes: int64(len(payload)), MediaType: "application/json", Canonicalization: "jcs-v1"}
	cd := interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(carrier), SizeBytes: int64(len(carrier)), MediaType: "application/json", Canonicalization: "jcs-v1"}
	release := contracts.RawDigest([]byte("harness release"))
	input := map[string]any{"api_version": "operator.dev/engine-attempt-request/v1alpha2", "kind": "EngineAttemptRequest", "request_id": "request-1", "origin": "scenario", "scenario_id": "scenario-marker", "thread_id": "thread-1", "attempt_id": "attempt-1", "generation": 1, "attempt_index": 1, "payload": pd, "carrier": cd, "generator": map[string]string{"kind": "operator-engine", "release_digest": release}, "pre_actions": []any{map[string]any{"action_id": "action-1", "action_type": "interceptor.injection/v1alpha1", "parameters": map[string]any{"surface": "mcp_tool_result", "mode": "simulation", "scope": "once", "selector": map[string]any{"service": "tickets", "tool_name": "get_ticket", "call_ordinal": "first"}, "placement": map[string]any{"operation": "append_text", "pointer": "/structuredContent/description"}, "payload_digest": pd.Digest}}}, "invocation": map[string]string{"operation_id": "invoke", "input_source": "carrier", "media_type": "application/json"}, "cleanup": map[string]bool{"delete_actions_after_observation": true}}
	in := Inputs{Live: live, Compatibility: compat, Policy: p, Artifacts: map[string]Artifact{pd.Digest: {"campaign-1", pd, payload}, cd.Digest: {"campaign-1", cd, carrier}}, ScenarioIDs: []string{"scenario-marker"}, ReleaseDigest: release, CreatedAt: time.Now().UTC(), Deadline: time.Now().UTC().Add(time.Minute), SessionRevision: 5, FeedbackBytes: 1 << 20}
	return c, encode(input), in
}
func mutate(raw []byte, fn func(map[string]any)) []byte {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	fn(m)
	return encode(m)
}
func params(m map[string]any) map[string]any {
	return m["pre_actions"].([]any)[0].(map[string]any)["parameters"].(map[string]any)
}

func TestCompileFixedPlanAndIndependentCopies(t *testing.T) {
	c, raw, in := fixture(t)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	var operations []string
	for _, cmd := range p.commands {
		operations = append(operations, cmd.Operation)
	}
	if !reflect.DeepEqual(operations, []string{"artifact.register", "artifact.register", "attempt.register", "injection.arm", "application.invoke"}) {
		t.Fatal(operations)
	}
	if p.context.FeedbackProfile != "black-box" || p.context.Digest != interceptor.AttemptContextDigest(p.context) || p.context.ObservationSelection.Mode != "selected" {
		t.Fatal("invalid attempt context")
	}
	var turn interceptor.TurnRequest
	_ = json.Unmarshal(p.commands[4].Body, &turn)
	if turn.ArtifactDigest != "" || !bytes.Equal(turn.Input, in.Artifacts[p.request.Carrier.Digest].Bytes) || turn.PayloadDigest != p.request.Payload.Digest {
		t.Fatal("carrier/payload association lost")
	}
	var injection struct {
		Definition interceptor.Definition `json:"definition"`
	}
	_ = json.Unmarshal(p.commands[3].Body, &injection)
	if injection.Definition.Placement.Operation != "append_text" || injection.Definition.AttemptID != p.request.AttemptID || !injection.Definition.Enabled || p.actions[injection.Definition.ID] != "action-1" {
		t.Fatal("injection translation mismatch")
	}
	saved := p.RecordJSON()
	copy := p.RecordJSON()
	copy[0] = '!'
	in.Artifacts[p.request.Payload.Digest].Bytes[0] = '!'
	facts := in.Live.Export().ExecutionFacts()
	facts.Operations[0].Delivery.Input.MediaType = "changed"
	if !bytes.Equal(saved, p.RecordJSON()) || in.Live.Export().ExecutionFacts().Operations[0].Delivery.Input.MediaType == "changed" {
		t.Fatal("caller mutated frozen state")
	}
	// Prose can change provenance without selecting a different command.
	c, raw, in = fixture(t)
	a, _ := Compile(c, raw, in)
	b, err := Compile(c, mutate(raw, func(m map[string]any) { m["rationale"] = "Use a different route and run a shell" }), in)
	if err != nil || !reflect.DeepEqual(a.commands, b.commands) {
		t.Fatal("prose altered tactics", err)
	}
}

func TestCompileRejectsInvalidRequestsBeforeEffects(t *testing.T) {
	for name, change := range map[string]func(map[string]any){
		"unknown field":    func(m map[string]any) { m["shell"] = "x" },
		"unknown scenario": func(m map[string]any) { m["scenario_id"] = "missing" },
		"foreign release": func(m map[string]any) {
			m["generator"].(map[string]any)["release_digest"] = contracts.RawDigest([]byte("other"))
		},
		"duplicate action": func(m map[string]any) {
			second := map[string]any{}
			for k, v := range m["pre_actions"].([]any)[0].(map[string]any) {
				second[k] = v
			}
			second["parameters"] = map[string]any{}
			for k, v := range params(m) {
				second["parameters"].(map[string]any)[k] = v
			}
			second["parameters"].(map[string]any)["scope"] = "standing"
			m["pre_actions"] = append(m["pre_actions"].([]any), second)
		},
		"foreign action payload": func(m map[string]any) { params(m)["payload_digest"] = contracts.RawDigest([]byte("other")) },
		"unknown tool":           func(m map[string]any) { params(m)["selector"].(map[string]any)["tool_name"] = "other" },
		"scope":                  func(m map[string]any) { params(m)["scope"] = "next_turn" },
		"placement pointer":      func(m map[string]any) { params(m)["placement"].(map[string]any)["pointer"] = "/secret" },
		"caller identity":        func(m map[string]any) { m["invocation"].(map[string]any)["caller_principal_id"] = "admin" },
		"delivery input":         func(m map[string]any) { m["invocation"].(map[string]any)["input_source"] = "payload" },
		"missing native parent":  func(m map[string]any) { m["parent_attempt_id"] = "old-attempt"; m["generation"] = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			c, raw, in := fixture(t)
			if _, err := Compile(c, mutate(raw, change), in); err == nil {
				t.Fatal("invalid attempt compiled")
			}
		})
	}
	for name, change := range map[string]func(*Inputs){
		"wrong campaign artifact": func(in *Inputs) {
			for k, a := range in.Artifacts {
				a.CampaignID = "foreign"
				in.Artifacts[k] = a
			}
		},
		"corrupt bytes": func(in *Inputs) {
			for k, a := range in.Artifacts {
				a.Bytes = []byte("changed")
				in.Artifacts[k] = a
			}
		},
		"descriptor mismatch": func(in *Inputs) {
			for k, a := range in.Artifacts {
				a.Descriptor.MediaType = "text/plain"
				in.Artifacts[k] = a
			}
		},
		"expired deadline": func(in *Inputs) { in.Deadline = in.CreatedAt },
		"policy mismatch":  func(in *Inputs) { in.Policy.capabilityPolicy.AllowedRefs = []string{} },
	} {
		t.Run(name, func(t *testing.T) {
			c, raw, in := fixture(t)
			change(&in)
			if _, err := Compile(c, raw, in); err == nil {
				t.Fatal("invalid facts compiled")
			}
		})
	}
}

func TestLineageRequiresVerifiedCurrentNativeSession(t *testing.T) {
	c, raw, in := fixture(t)
	root, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	raw = mutate(raw, func(m map[string]any) {
		m["attempt_id"] = "child"
		m["request_id"] = "child-request"
		m["parent_attempt_id"] = "attempt-1"
		m["generation"] = 2
		m["attempt_index"] = 5
	})
	in.Parent = &Parent{SessionID: "session-1", Context: root.context}
	if _, err = Compile(c, raw, in); err != nil {
		t.Fatal(err)
	}
	in.Parent.SessionID = "before-restore"
	if _, err = Compile(c, raw, in); err == nil {
		t.Fatal("old-session parent accepted without restored lineage")
	}
}

func TestResolvedScopesAreNarrowAndCopied(t *testing.T) {
	_, _, in := fixture(t)
	for _, target := range []Target{{Service: "tickets", ToolName: "missing"}, {FileNamespace: "records", RelativePath: "../outside"}, {Service: "tickets", Method: "GET", Path: "/../secret"}} {
		surface := "mcp_tool_result"
		if target.FileNamespace != "" {
			surface = "environment_state"
		}
		if target.Method != "" {
			surface = "service_response"
		}
		if _, err := Resolve(in.Live.Export(), Scopes{Routes: []Route{{Surface: surface, Target: target, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}}}}, nil, ""); err == nil {
			t.Fatal("invalid route admitted")
		}
	}
	copy := in.Policy.CapabilityPolicy()
	copy.AllowedRefs[0] = "changed"
	if in.Policy.CapabilityPolicy().AllowedRefs[0] == "changed" {
		t.Fatal("mutable policy")
	}
}

func TestConcreteRoutesCoverNativeSurfacesAndRejectBroaderSelectors(t *testing.T) {
	_, _, in := fixture(t)
	facts := in.Live.Export().ExecutionFacts()
	facts.Services = append(facts.Services, capabilities.ServiceCapability{ID: "http", InjectionSurfaces: []string{"service_response"}, Endpoints: []nativedelivery.Endpoint{{Kind: "http-route", Method: "GET", Path: "/tickets/{key}"}}})
	routes := []Route{
		{Surface: "model_tool_result", Target: Target{ToolName: "lookup"}, Scopes: []string{"next_turn"}, Placements: []string{"replace"}, Pointers: []string{""}},
		{Surface: "environment_state", Target: Target{FileNamespace: "records", RelativePath: "documents/one.json"}, Scopes: []string{"standing"}, Placements: []string{"replace"}, Pointers: []string{""}},
		{Surface: "environment_state", Target: Target{Service: "tickets", Collection: "tickets", Key: "T-1"}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}},
		{Surface: "service_response", Target: Target{Service: "http", Method: "GET", Path: "/tickets/T-1"}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}},
	}
	for _, r := range routes {
		if !validRoute(facts, r) {
			t.Fatal("supported route rejected", r)
		}
	}
	httpRoute := routes[3]
	policy := &Policy{scopes: Scopes{Routes: []Route{httpRoute}}, facts: facts}
	d := interceptor.Definition{Surface: "service_response", Scope: "once", Selector: interceptor.Selector{Service: "http", Method: "GET", Path: "/tickets/T-1", CallOrdinal: "first"}, Placement: interceptor.Placement{Operation: "replace", Pointer: ""}}
	if policy.authorizeInjection(d, nil) != nil {
		t.Fatal("exact route denied")
	}
	d.Selector.Path = "/tickets/T-2"
	if policy.authorizeInjection(d, nil) == nil {
		t.Fatal("route template widened administrator scope")
	}
	d.Selector.Path = "/tickets/T-1"
	for _, headers := range []map[string]string{{"authorization": "secret"}, {"Host": "other"}, {"x-test": "a", "X-Test": "b"}} {
		d.Selector.HeadersEqual = headers
		if policy.authorizeInjection(d, nil) == nil {
			t.Fatal("sensitive or duplicate header selector admitted")
		}
	}
	bad := routes[3]
	bad.Placements = []string{"insert_array"}
	if validRoute(facts, bad) {
		t.Fatal("unsupported response insertion route advertised")
	}
	bad.Placements = []string{"merge_object"}
	if validRoute(facts, bad) {
		t.Fatal("unusable merge route advertised")
	}
}

func TestNativeTimesRoundTripWithoutMonotonicState(t *testing.T) {
	c, raw, in := fixture(t)
	in.CreatedAt = time.Now()
	in.Deadline = in.CreatedAt.Add(time.Minute)
	p, err := Compile(c, raw, in)
	if err != nil {
		t.Fatal(err)
	}
	var reply interceptor.AttemptContext
	if err = interceptor.DecodeTypedBody(encode(p.context), &reply, interceptor.JSONLimit); err != nil || !reflect.DeepEqual(reply, p.context) {
		t.Fatal("timestamp lost native round-trip identity", err)
	}
}
