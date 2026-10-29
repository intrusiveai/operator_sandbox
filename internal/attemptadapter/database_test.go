package attemptadapter

import (
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestDatabaseRoutesAndKeys(t *testing.T) {
	c, _ := contracts.LoadCatalog(schemas.Files)
	raw, err := os.ReadFile("../../schemas/fixtures/postgresql-capability-chain/interceptor-export.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := capabilities.FromNative(c, raw, "database")
	if err != nil {
		t.Fatal(err)
	}
	state := Route{Surface: "environment_state", Target: Target{Service: "database", Collection: "documents", Key: `{"id":"one/path"}`}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{"/body"}}
	response := Route{Surface: "database_response", Target: Target{Service: "database"}, Scopes: []string{"once"}, Placements: []string{"append_text"}, Pointers: []string{"/rows/0/0"}}
	create := Route{Surface: "environment_state", Target: Target{Service: "database", Collection: "refunds", Key: `{"id":"new"}`}, Scopes: []string{"once"}, Placements: []string{"replace"}, Pointers: []string{""}}
	p, err := Resolve(e, Scopes{OperationIDs: []string{"ask"}, Routes: []Route{state, response, create}}, []string{"oracle_outcome"}, "oracle-assisted")
	if err != nil {
		t.Fatal(err)
	}
	d := interceptor.Definition{Surface: "environment_state", Scope: "once", Selector: interceptor.Selector{Service: "database", Collection: "documents", Key: `{ "id" : "one/path" }`}, Placement: interceptor.Placement{Operation: "replace", Pointer: "/body"}, Payload: []byte(`"replacement"`)}
	if err = p.authorizeInjection(d, nil); err != nil {
		t.Fatal("equivalent authorized key rejected", err)
	}
	for _, key := range []string{`{"id":"other"}`, `{"id":"one/path","extra":1}`, `{"id":null}`, `{"id":"one/path","id":"one/path"}`} {
		bad := d
		bad.Selector.Key = key
		if p.authorizeInjection(bad, nil) == nil {
			t.Fatalf("accepted key %s", key)
		}
	}
	bad := state
	bad.Pointers = []string{"/id"}
	if validRoute(e.ExecutionFacts(), bad) {
		t.Fatal("key mutation authorized")
	}
	bad = state
	bad.Pointers = []string{""}
	if validRoute(e.ExecutionFacts(), bad) {
		t.Fatal("creation on readonly table authorized")
	}
	bad = response
	bad.Pointers = []string{"/columns/0"}
	if validRoute(e.ExecutionFacts(), bad) {
		t.Fatal("column mutation authorized")
	}
	d.Selector.Collection = "refunds"
	d.Selector.Key = `{"id":"new"}`
	d.Placement.Pointer = ""
	d.Payload = []byte(`{"id":"new","amount":42}`)
	if err = p.authorizeInjection(d, nil); err != nil {
		t.Fatal("permitted create rejected", err)
	}
	for _, payload := range []string{`{"id":"wrong","amount":42}`, `{"id":"new","secret":42}`, `{"amount":42}`} {
		d.Payload = []byte(payload)
		if p.authorizeInjection(d, nil) == nil {
			t.Fatalf("accepted creation %s", payload)
		}
	}
	// Composite keys compare validated JSON values, not object member order.
	f := e.ExecutionFacts()
	f.Services[0].DatabaseTables[0].Keys = []string{"tenant", "id"}
	a := state.Target
	a.Key = `{"tenant":"a","id":1}`
	b := a
	b.Key = `{ "id": 1.0, "tenant":"a" }`
	if !sameTarget(f, a, b) {
		t.Fatal("equivalent composite keys rejected")
	}
	b.Key = `{"id":1,"tenant":"b"}`
	if sameTarget(f, a, b) {
		t.Fatal("foreign key accepted")
	}
}
func TestDatabaseSurfaceSchema(t *testing.T) {
	c, _ := contracts.LoadCatalog(schemas.Files)
	request := map[string]any{"origin": "exploratory", "operation_id": "ask", "input": map[string]any{"encoding": "json", "value": map[string]any{"message": "continue"}}, "injection_input": map[string]any{"encoding": "json", "value": "benign payload"}, "pre_actions": []any{map[string]any{"action_id": "db", "action_type": "interceptor.injection/v1alpha1", "parameters": map[string]any{"surface": "database_response", "mode": "simulation", "scope": "once", "selector": map[string]any{"service": "database", "call_ordinal": "first"}, "placement": map[string]any{"operation": "append_text", "pointer": "/rows/0/0"}}}}}
	if _, err := c.Validate(contracts.EngineAttemptRequestSchema, encode(request), contracts.OrdinaryLimit); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"method", "turn_id", "tool_name"} {
		p := params(request)
		sel := p["selector"].(map[string]any)
		sel[field] = "invalid"
		if _, err := c.Validate(contracts.EngineAttemptRequestSchema, encode(request), contracts.OrdinaryLimit); err == nil {
			t.Fatal("accepted unsupported selector", field)
		}
		delete(sel, field)
	}
}
