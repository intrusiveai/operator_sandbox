package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestSharedToolNormalization(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/tool-normalization.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Tool, Reason, Path, Correction string
		Arguments, Expected                  json.RawMessage
		Context                              ToolResolution
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := p.NormalizeToolArguments(c.Tool, c.Arguments, c.Context)
			if c.Reason != "" {
				var issue *ArgumentError
				if !errors.As(err, &issue) || issue.Reason != c.Reason || issue.FieldPath != c.Path || (c.Correction != "" && issue.Correction != c.Correction) {
					t.Fatalf("%v; want %s %s", err, c.Reason, c.Path)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(got)
			actual, _ := Canonicalize(encoded, OrdinaryLimit)
			want, _ := Canonicalize(c.Expected, OrdinaryLimit)
			if !bytes.Equal(actual, want) {
				t.Fatalf("normalization differs\n got %s\nwant %s", actual, want)
			}
			again, err := p.NormalizeToolArguments(c.Tool, c.Arguments, c.Context)
			if err != nil {
				t.Fatal(err)
			}
			encodedAgain, _ := json.Marshal(again)
			if !bytes.Equal(encoded, encodedAgain) {
				t.Fatal("unstable call normalization")
			}
			if c.Name == "derived claim receipts" {
				claim := got.Arguments["assessment"].(map[string]any)["claims"].([]any)[0].(map[string]any)
				if claim["attempt_receipt_refs"].([]any)[0] != "receipt-123" {
					t.Fatal("missing derived receipt")
				}
				if path := RemapToolPath(got.Paths, "/assessment/claims/0/attempt_receipt_refs/0"); path != "/claims/0/observations/0" {
					t.Fatal(path)
				}
			}
		})
	}
}

func TestModelToolContextNonGrowth(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/tool-context-cost.json")
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Cases []struct {
			Codec, Mode          string
			Operations, Surfaces []string
			Before               int `json:"baseline_bytes"`
			After                int `json:"revised_bytes"`
		}
	}
	if err = json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	for _, c := range report.Cases {
		got, err := p.ModelToolsSelected(c.Codec, c.Operations, c.Surfaces)
		if err != nil || len(got) != c.After || c.After > c.Before {
			t.Fatal(c.Codec, c.Mode, c.Surfaces, err)
		}
		if c.Mode == "ordinary" && c.After*4 > c.Before*3 {
			t.Fatal("ordinary schema reduction below 25 percent")
		}
	}
}
func TestArtifactPublicationCanExceedDirectInputLimit(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	value := strings.Repeat("x", DirectInputLimit+1)
	raw, _ := json.Marshal(map[string]any{"purpose": "payload", "media_type": "application/json", "json": value})
	if _, err = p.ValidateToolArguments("artifact_publish", raw); err != nil {
		t.Fatal(err)
	}
	raw, _ = json.Marshal(map[string]any{"origin": "exploratory", "operation_id": "invoke", "json": value})
	if _, err = p.ValidateToolArguments("attempt_execute", raw); err == nil {
		t.Fatal("oversized direct input admitted")
	}
}

func TestToolErrorPathsUseModelFields(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.NormalizeToolArguments("attempt_execute", []byte(`{"origin":"exploratory","operation_id":"invoke","json":{}}`), ToolResolution{CampaignID: "campaign-1", CallID: "call-1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := RemapToolPath(out.Paths, "/input"); got != "/json" {
		t.Fatal(got)
	}
}
