package contracts

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestModelRelayFixtures(t *testing.T) {
	p, e := LoadProtocol(schemas.Files)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../schemas/fixtures/model-relay.json")
	if e != nil {
		t.Fatal(e)
	}
	var cases []struct {
		Name                   string
		Valid                  bool
		Model, Request, Result json.RawMessage
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			_, e := p.ValidateModelExchange(c.Model, c.Request, c.Result)
			if (e == nil) != c.Valid {
				t.Fatal(e)
			}
		})
	}
	if _, e = p.ValidateModelRequest([]byte(`{"purpose":"generation","conversation":[],"conversation":[]}`)); e == nil {
		t.Fatal("duplicate key accepted")
	}
}
