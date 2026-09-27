package contracts

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

type modelFixture struct {
	Name        string           `json:"name"`
	Mode        string           `json:"mode"`
	Policy      json.RawMessage  `json:"policy"`
	Request     json.RawMessage  `json:"request"`
	Result      json.RawMessage  `json:"result"`
	ToolResults []ChatToolResult `json:"tool_results"`
	Segment     string           `json:"segment_json"`
	Valid       bool             `json:"valid"`
	Disposition string           `json:"disposition"`
	Context     json.RawMessage  `json:"context"`
	Tools       json.RawMessage  `json:"tools"`
	Prompt      string           `json:"prompt"`
	Metrics     *ModelMetrics    `json:"metrics"`
	OutputLimit int64            `json:"output_limit"`
}

func modelFixtures(t *testing.T) []modelFixture {
	t.Helper()
	var cases []modelFixture
	for _, name := range []string{"model-codec.json", "anthropic-model-codec.json"} {
		raw, err := os.ReadFile("../schemas/fixtures/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var batch []modelFixture
		if err = json.Unmarshal(raw, &batch); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, batch...)
	}
	return cases
}
func modelEnvelope(t *testing.T, body json.RawMessage, response bool) []byte {
	t.Helper()
	v := map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 0, "campaign_id": "campaign-1", "launch_id": "launch-1", "run_revision": 1, "call_id": "call-1", "operation_id": "operation-1", "operation": "engine.model_generate"}
	if response {
		v["kind"] = "response"
		v["result"] = body
	} else {
		v["timeout_ms"] = 120000
		v["body"] = body
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestSharedModelCodec(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	cases := modelFixtures(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var err error
			switch c.Mode {
			case "context":
				var policy []byte
				policy, err = p.ModelPolicyFromContext(c.Context, c.Tools, []byte(c.Prompt))
				if err == nil {
					expected, e := Canonicalize(c.Policy, OrdinaryLimit)
					if e != nil {
						t.Fatal(e)
					}
					if !bytes.Equal(policy, expected) {
						t.Fatal("startup policy differs")
					}
					if _, e = p.ValidateModelRequest(policy, c.Request); e != nil {
						t.Fatal(e)
					}
				}
			case "bound":
				var got map[string]any
				got, err = p.ValidateModelExchange(c.Policy, c.Request, c.Result)
				if err == nil {
					expected, e := Decode(c.Result, OrdinaryLimit)
					if e != nil {
						t.Fatal(e)
					}
					if !wireEqual(got, expected) {
						t.Fatal("native result changed")
					}
				}
			case "wire":
				_, err = p.ValidateResponse(modelEnvelope(t, c.Request, false), modelEnvelope(t, c.Result, true))
			case "continuation", "anthropic-continuation":
				var segment []byte
				if c.Mode == "anthropic-continuation" {
					segment, err = p.AnthropicContinuation(c.Result, c.ToolResults)
				} else {
					segment, err = p.ChatContinuation(c.Result, c.ToolResults)
				}
				if err == nil {
					if !bytes.Equal(segment, []byte(c.Segment)) {
						t.Fatal("native continuation bytes differ")
					}
					var request map[string]any
					if e := json.Unmarshal(c.Request, &request); e != nil {
						t.Fatal(e)
					}
					var messages []any
					if e := json.Unmarshal(segment, &messages); e != nil {
						t.Fatal(e)
					}
					native := request["request"].(map[string]any)
					native["messages"] = append(native["messages"].([]any), messages...)
					raw, e := json.Marshal(request)
					if e != nil {
						t.Fatal(e)
					}
					if _, e = p.ValidateModelRequest(c.Policy, raw); e != nil {
						t.Fatalf("continuation cannot be submitted: %v", e)
					}
				}
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			if c.Valid && !strings.HasSuffix(c.Mode, "continuation") && c.Mode != "context" {
				disposition, e := p.ModelDisposition(c.Result)
				if e != nil || disposition != c.Disposition {
					t.Fatalf("disposition=%s want=%s error=%v", disposition, c.Disposition, e)
				}
			}
			if c.Valid && c.Metrics != nil {
				got, e := p.ModelUsage(c.Result)
				if e != nil || got != *c.Metrics {
					t.Fatalf("metrics=%+v want=%+v err=%v", got, *c.Metrics, e)
				}
			}
			if c.Valid && c.OutputLimit > 0 {
				got, e := p.ModelOutputLimit(c.Request)
				if e != nil || got != c.OutputLimit {
					t.Fatalf("output=%v want=%v err=%v", got, c.OutputLimit, e)
				}
			}
		})
	}
	t.Logf("%d shared model codec cases", len(cases))
}

func TestModelCodecBoundsAndRegistry(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := p.operations["engine.model_generate"]
	if !ok || op.TimeoutMS != 120000 || op.Effect != "model-call" || op.FinalizationRule != "denied" || op.ReceiptPolicy != "durable" {
		t.Fatal("incorrect model admission metadata")
	}
	cases := modelFixtures(t)
	var toolCase modelFixture
	for _, c := range cases {
		if c.Name == "invalid local arguments remain exact in followup" {
			toolCase = c
		}
	}
	if toolCase.Name == "" {
		t.Fatal("missing tool fixture")
	}
	for _, content := range []string{string([]byte{0xff}), strings.Repeat("x", (1<<20)+1)} {
		if _, err := p.ChatContinuation(toolCase.Result, []ChatToolResult{{ToolCallID: "call-1", Content: content}}); err == nil {
			t.Fatal("invalid/oversize tool content accepted")
		}
	}
	var request map[string]any
	if err := json.Unmarshal(cases[0].Request, &request); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[1].(map[string]any)["content"] = strings.Repeat("x", OrdinaryLimit)
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ValidateModelRequest(cases[0].Policy, raw); err == nil {
		t.Fatal("oversize native request accepted")
	}
}
