package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestSharedConclusionFixtures(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/conclusion-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string `json:"name"`
		Conclusion string `json:"conclusion_base64"`
		Valid      bool   `json:"valid"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			data, err := base64.StdEncoding.DecodeString(c.Conclusion)
			if err != nil {
				t.Fatal(err)
			}
			_, err = p.ValidateConclusion(data)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			if err != nil && !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrSchema) && !errors.Is(err, ErrJSON) && !errors.Is(err, ErrLimit) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	t.Logf("%d shared conclusion cases", len(cases))
}

func TestAssessmentByteLimits(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/conclusion-example.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, bytes.Repeat([]byte(" "), ConclusionLimit-len(raw))...)
	if _, err = p.ValidateConclusion(raw); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ValidateConclusion(append(raw, ' ')); !errors.Is(err, ErrLimit) {
		t.Fatal("conclusion byte limit not enforced", err)
	}
	raw, err = os.ReadFile("../schemas/fixtures/ordinary-protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Request  json.RawMessage `json:"request"`
		Response json.RawMessage `json:"response"`
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cases {
		if c.Name != "record hypothesis response" {
			continue
		}
		found = true
		q := append([]byte(nil), c.Request...)
		q = append(q, bytes.Repeat([]byte(" "), ControlLimit-len(q))...)
		r := append([]byte(nil), c.Response...)
		r = append(r, bytes.Repeat([]byte(" "), ControlLimit-len(r))...)
		if _, err = p.ValidateResponse(q, r); err != nil {
			t.Fatal(err)
		}
		if _, err = p.ValidateRequest(append(q, ' ')); !errors.Is(err, ErrLimit) {
			t.Fatal("record request limit not enforced", err)
		}
		if _, err = p.ValidateResponse(q, append(r, ' ')); !errors.Is(err, ErrLimit) {
			t.Fatal("record response limit not enforced", err)
		}
	}
	if !found {
		t.Fatal("missing record fixture")
	}
}

func TestConclusionErrorPaths(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile("../schemas/fixtures/conclusion-example.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, reason, path string
		edit               func(map[string]any)
	}{
		{"missing field", "invalid_structure", "/summary", func(c map[string]any) { delete(c, "summary") }},
		{"self", "self_parent", "/hypotheses/0/provenance/parent_hypothesis_id", func(c map[string]any) {
			h := c["hypotheses"].([]any)[0].(map[string]any)
			h["provenance"].(map[string]any)["parent_hypothesis_id"] = h["hypothesis_id"]
		}},
		{"overflow", "invalid_range", "/claims/0/observation_refs/0/range", func(c map[string]any) {
			c["claims"].([]any)[0].(map[string]any)["observation_refs"].([]any)[0].(map[string]any)["range"] = map[string]any{"offset": MaxSafeInteger, "length": 1}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c map[string]any
			_ = json.Unmarshal(original, &c)
			tc.edit(c)
			raw, _ := json.Marshal(c)
			_, err := p.ValidateConclusion(raw)
			var issue *ConclusionError
			if !errors.As(err, &issue) || issue.Reason != tc.reason || issue.FieldPath != tc.path {
				t.Fatal(err, issue)
			}
		})
	}
}
