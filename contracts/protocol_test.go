package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"testing"
	"testing/fstest"

	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func TestSharedProtocolFixtures(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/ordinary-protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
		Ack      json.RawMessage `json:"ack"`
		Valid    bool            `json:"valid"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	covered := map[string]bool{}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var err error
			if c.Ack != nil {
				_, err = p.ValidateAck(c.Ack)
			} else if c.Response != nil {
				_, err = p.ValidateResponse(c.Request, c.Response)
				if err == nil && c.Valid {
					var req struct {
						Operation string `json:"operation"`
					}
					if err = json.Unmarshal(c.Request, &req); err != nil {
						t.Fatal(err)
					}
					covered[req.Operation] = true
				}
			} else {
				_, err = p.ValidateRequest(c.Request)
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			if err != nil && !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrSchema) && !errors.Is(err, ErrLimit) && !errors.Is(err, ErrJSON) {
				t.Fatalf("unexpected public error: %v", err)
			}
		})
	}
	for _, op := range p.Operations() {
		if !covered[op.Name] {
			t.Errorf("missing exchange for %s", op.Name)
		}
	}
	t.Logf("%d protocol cases; %d operations", len(cases), len(p.Operations()))
}

func TestRegistryAndAckGuards(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	ops := p.Operations()
	ops[0].ErrorCodes[0] = "MUTATED"
	if p.Operations()[0].ErrorCodes[0] == "MUTATED" {
		t.Fatal("mutable operation policy")
	}
	ack := []byte(`{"api_version":"operator.dev/engine-spool-ack/v1alpha1","launch_id":"launch-1","ordinary_seq":null,"control_seq":0}`)
	ack = append(ack, bytes.Repeat([]byte(" "), SpoolAckLimit-len(ack))...)
	if _, err = p.ValidateAck(ack); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ValidateAck(append(ack, ' ')); !errors.Is(err, ErrLimit) {
		t.Fatal("oversized ACK accepted", err)
	}
	for _, mutation := range []string{"duplicate", "missing-schema", "unknown-field", "unsorted", "bad-finalization-rule", "integer-exponent"} {
		t.Run(mutation, func(t *testing.T) {
			files := fstest.MapFS{}
			if err := fs.WalkDir(schemas.Files, ".", func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				raw, err := fs.ReadFile(schemas.Files, path)
				files[path] = &fstest.MapFile{Data: raw}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var registry map[string]any
			if err := json.Unmarshal(files["operations.json"].Data, &registry); err != nil {
				t.Fatal(err)
			}
			items := registry["operations"].([]any)
			switch mutation {
			case "duplicate":
				registry["operations"] = append(items, items[0])
			case "missing-schema":
				items[0].(map[string]any)["request_schema"] = "urn:missing"
			case "unknown-field":
				items[0].(map[string]any)["guest_override"] = true
			case "unsorted":
				items[0], items[1] = items[1], items[0]
			case "bad-finalization-rule":
				items[0].(map[string]any)["finalization_rule"] = "anything"
			case "integer-exponent":
				items[0].(map[string]any)["timeout_ms"] = json.Number("3e4")
			}
			raw, err := json.Marshal(registry)
			if err != nil {
				t.Fatal(err)
			}
			files["operations.json"] = &fstest.MapFile{Data: raw}
			_, err = LoadProtocol(files)
			if mutation == "integer-exponent" {
				if err != nil {
					t.Fatal("valid integer spelling rejected", err)
				}
			} else if !errors.Is(err, ErrCatalog) {
				t.Fatal("bad registry accepted", err)
			}
		})
	}
}
