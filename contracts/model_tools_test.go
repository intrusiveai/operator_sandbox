package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestSharedModelTools(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/model-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Mode, Codec, Tool, Digest string
		Operations, Names               []string
		Size                            int `json:"size_bytes"`
		Arguments                       json.RawMessage
		Valid                           bool
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			if c.Mode == "arguments" {
				_, err := p.ValidateToolArguments(c.Tool, c.Arguments)
				if (err == nil) != c.Valid {
					t.Fatalf("valid=%v want=%v: %v", err == nil, c.Valid, err)
				}
				return
			}
			got, err := p.ModelTools(c.Codec, c.Operations)
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v: %v", err == nil, c.Valid, err)
			}
			if err != nil {
				return
			}
			if len(got) != c.Size || fmt.Sprintf("sha256:%x", sha256.Sum256(got)) != c.Digest {
				t.Fatal("projection differs from shared fixture")
			}
			var tools []map[string]any
			if err = json.Unmarshal(got, &tools); err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, tool := range tools {
				switch c.Codec {
				case "openai-chat-text-tools-v1":
					checkOpenAIParameters(t, tool["function"].(map[string]any)["parameters"])
					names = append(names, tool["function"].(map[string]any)["name"].(string))
				case responsesCodec:
					checkOpenAIParameters(t, tool["parameters"])
					names = append(names, tool["name"].(string))
				case bedrockCodec:
					names = append(names, tool["toolSpec"].(map[string]any)["name"].(string))
				case geminiCodec:
					for _, d := range tool["functionDeclarations"].([]any) {
						names = append(names, d.(map[string]any)["name"].(string))
					}
				default:
					names = append(names, tool["name"].(string))
				}
			}
			if !reflect.DeepEqual(names, c.Names) {
				t.Fatal("tool dependency selection differs")
			}
			// Projections must survive the real policy and request validators, not just
			// a comparison against another JSON generator.
			found := false
			for _, base := range modelFixtures(t) {
				if base.Mode != "context" || !base.Valid {
					continue
				}
				var policy, request map[string]any
				if err = json.Unmarshal(base.Policy, &policy); err != nil {
					t.Fatal(err)
				}
				if policy["codec_id"] != c.Codec {
					continue
				}
				found = true
				if err = json.Unmarshal(base.Request, &request); err != nil {
					t.Fatal(err)
				}
				policy["tools"] = tools
				native := request["request"].(map[string]any)
				if c.Codec == bedrockCodec {
					native["toolConfig"].(map[string]any)["tools"] = tools
				} else {
					native["tools"] = tools
				}
				policyRaw, _ := json.Marshal(policy)
				requestRaw, _ := json.Marshal(request)
				if _, err = p.ValidateModelRequest(policyRaw, requestRaw); err != nil {
					t.Fatalf("native catalog rejected: %v", err)
				}
				break
			}
			if !found {
				t.Fatal("missing codec integration fixture")
			}
			original := bytes.Clone(got)
			got[0] = 'x'
			again, err := p.ModelTools(c.Codec, c.Operations)
			if err != nil || !bytes.Equal(again, original) {
				t.Fatal("returned bytes mutated installed catalog")
			}
		})
	}
}

func checkOpenAIParameters(t *testing.T, value any) {
	t.Helper()
	parameters := value.(map[string]any)
	if parameters["type"] != "object" || parameters["properties"] == nil || parameters["additionalProperties"] != false {
		t.Fatal("OpenAI parameters must declare a closed object")
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "enum", "const", "not"} {
		if _, exists := parameters[key]; exists {
			t.Fatalf("OpenAI forbids root %s even with strict=false", key)
		}
	}
}

// Provider acceptance is not authorization: cross-field combinations accepted by
// a declaration must still fail the authoritative runtime argument validator.
func TestOpenAIProjectionAndRuntimeConstraints(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/model-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name, Mode, Tool string
		Operations       []string
		Arguments        json.RawMessage
		Valid            bool
		OpenAIValid      *bool `json:"openai_valid"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"openai-chat-text-tools-v1", responsesCodec} {
		toolsRaw, err := p.ModelTools(codec, cases[0].Operations)
		if err != nil {
			t.Fatal(err)
		}
		value, err := Decode(toolsRaw, OrdinaryLimit)
		if err != nil {
			t.Fatal(err)
		}
		validators := map[string]*jsonschema.Schema{}
		for _, value := range value.([]any) {
			tool := value.(map[string]any)
			if codec == "openai-chat-text-tools-v1" {
				tool = tool["function"].(map[string]any)
			}
			compiler := jsonschema.NewCompiler()
			compiler.DefaultDraft(jsonschema.Draft2020)
			compiler.UseLoader(offlineLoader{})
			if err := compiler.AddResource("urn:test:tool", tool["parameters"]); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile("urn:test:tool")
			if err != nil {
				t.Fatal(err)
			}
			validators[tool["name"].(string)] = compiled
		}
		for _, c := range cases {
			if c.OpenAIValid == nil {
				continue
			}
			t.Run(codec+"/"+c.Name, func(t *testing.T) {
				value, err := Decode(c.Arguments, OrdinaryLimit)
				if err != nil {
					t.Fatal(err)
				}
				if got := validators[c.Tool].Validate(value) == nil; got != *c.OpenAIValid {
					t.Fatalf("projection valid=%v want=%v", got, *c.OpenAIValid)
				}
				if _, err := p.ValidateToolArguments(c.Tool, c.Arguments); (err == nil) != c.Valid {
					t.Fatalf("runtime valid=%v want=%v", err == nil, c.Valid)
				}
			})
		}
	}
}
