package attemptadapter

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestInterceptorContractModelResponseExchanges(t *testing.T) {
	for _, codec := range []string{"anthropic_messages", "aws_bedrock_converse", "google_generate_content", "openai_chat_completions", "openai_responses"} {
		t.Run(codec, func(t *testing.T) {
			route := Route{Surface: "model_response", Target: Target{ModelCodec: codec}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}}
			// Valid JSON, deliberately not a valid provider response. The target SDK,
			// rather than Operator's Harness model validator, is the experiment subject.
			p, raw := injectionPlan(t, route, []byte(`{"tool_calls":[{"arguments":"{invalid","name":"unknown"}]}`), "model-response-injection")
			e, _, _, peer := executionSetup(t, raw, p)
			d, err := contractcheck.Load("../interceptor/openapi.json")
			if err != nil {
				t.Fatal(err)
			}
			peer.contract = d
			result, err := e.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if result.Receipt == nil || len(result.Injections) != 1 || !result.Injections[0].Deleted {
				t.Fatal("missing receipt/cleanup")
			}
			calls := []string{"artifact.register", "artifact.register", "attempt.register", "injection.arm", "application.invoke", "observation.read", "observation.content.read", "injection.delete"}
			if !reflect.DeepEqual(peer.calls, calls) {
				t.Fatal(peer.calls)
			}
			if _, err = e.Run(context.Background()); err == nil || !reflect.DeepEqual(peer.calls, calls) {
				t.Fatal("replayed attempt")
			}
		})
	}
}

func TestModelResponseAuthorization(t *testing.T) {
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/model-response-injection/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	export, err := capabilities.FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Surface: "model_response", Target: Target{ModelCodec: "openai_responses"}, Scopes: []string{"once", "standing"}, Placements: []string{"replace"}, Pointers: []string{"/output"}}
	p, err := Resolve(export, Scopes{OperationIDs: []string{"invoke"}, Routes: []Route{route}}, []string{"target_output"}, "")
	if err != nil {
		t.Fatal(err)
	}
	def := interceptor.Definition{Surface: route.Surface, Scope: "once", Selector: interceptor.Selector{ModelCodec: route.Target.ModelCodec, CallOrdinal: "first"}, Placement: interceptor.Placement{Operation: "replace", Pointer: "/output"}}
	if err = p.authorizeInjection(def, nil); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*interceptor.Definition){
		"codec":        func(d *interceptor.Definition) { d.Selector.ModelCodec = "anthropic_messages" },
		"pointer":      func(d *interceptor.Definition) { d.Placement.Pointer = "" },
		"scope":        func(d *interceptor.Definition) { d.Scope = "next_turn" },
		"unknown turn": func(d *interceptor.Definition) { d.Selector.TurnID = "unobserved" },
		"service":      func(d *interceptor.Definition) { d.Selector.Service = "svc" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := def
			change(&bad)
			if p.authorizeInjection(bad, nil) == nil {
				t.Fatal("unauthorized injection")
			}
		})
	}
	for _, target := range []Target{{}, {ModelCodec: "embeddings"}, {ModelCodec: "openai_responses", ToolName: "lookup"}} {
		bad := route
		bad.Target = target
		if _, err = Resolve(export, Scopes{OperationIDs: []string{"invoke"}, Routes: []Route{bad}}, []string{"target_output"}, ""); err == nil {
			t.Fatal("invalid route admitted")
		}
	}
}
