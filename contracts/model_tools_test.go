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
					checkObjectRootParameters(t, tool["function"].(map[string]any)["parameters"], c.Codec)
					names = append(names, tool["function"].(map[string]any)["name"].(string))
				case responsesCodec:
					checkObjectRootParameters(t, tool["parameters"], c.Codec)
					names = append(names, tool["name"].(string))
				case anthropicCodec:
					checkObjectRootParameters(t, tool["input_schema"], c.Codec)
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
			original := bytes.Clone(got)
			got[0] = 'x'
			again, err := p.ModelTools(c.Codec, c.Operations)
			if err != nil || !bytes.Equal(again, original) {
				t.Fatal("returned bytes mutated installed catalog")
			}
		})
	}
}

func checkObjectRootParameters(t *testing.T, value any, codec string) {
	t.Helper()
	parameters := value.(map[string]any)
	if parameters["type"] != "object" || parameters["properties"] == nil || parameters["additionalProperties"] != false {
		t.Fatalf("%s parameters must declare a closed object", codec)
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf", "enum", "const", "not"} {
		if _, exists := parameters[key]; exists {
			t.Fatalf("%s forbids root %s", codec, key)
		}
	}
}

// Provider acceptance is not authorization: cross-field combinations accepted by
// a declaration must still fail the authoritative runtime argument validator.
func TestObjectRootProjectionAndRuntimeConstraints(t *testing.T) {
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
		ObjectRootValid  *bool `json:"object_root_valid"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"openai-chat-text-tools-v1", responsesCodec, anthropicCodec} {
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
			parameters := tool["parameters"]
			if codec == anthropicCodec {
				parameters = tool["input_schema"]
			}
			compiler := jsonschema.NewCompiler()
			compiler.DefaultDraft(jsonschema.Draft2020)
			compiler.UseLoader(offlineLoader{})
			if err := compiler.AddResource("urn:test:tool", parameters); err != nil {
				t.Fatal(err)
			}
			compiled, err := compiler.Compile("urn:test:tool")
			if err != nil {
				t.Fatal(err)
			}
			validators[tool["name"].(string)] = compiled
		}
		for _, c := range cases {
			if c.ObjectRootValid == nil {
				continue
			}
			t.Run(codec+"/"+c.Name, func(t *testing.T) {
				value, err := Decode(c.Arguments, OrdinaryLimit)
				if err != nil {
					t.Fatal(err)
				}
				if got := validators[c.Tool].Validate(value) == nil; got != *c.ObjectRootValid {
					t.Fatalf("projection valid=%v want=%v", got, *c.ObjectRootValid)
				}
				if _, err := p.ValidateToolArguments(c.Tool, c.Arguments); (err == nil) != c.Valid {
					t.Fatalf("runtime valid=%v want=%v", err == nil, c.Valid)
				}
			})
		}
	}
}
