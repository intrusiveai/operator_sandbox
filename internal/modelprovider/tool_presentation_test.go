//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/modelcodec"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

// Exercise the selected catalog through host request construction and the real
// HTTP adapter. No provider credentials or external services are used.
func TestSelectedToolPresentationAtProviderBoundary(t *testing.T) {
	protocol, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/selected-model-tools.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Codec                string
		Operations, Surfaces []string
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	providers := map[string]string{modelcodec.Chat: "openai-chat", modelcodec.Responses: "openai-responses", modelcodec.Anthropic: "anthropic-messages", modelcodec.Bedrock: "bedrock-converse", modelcodec.Gemini: "gemini-api"}
	for _, c := range cases {
		t.Run(c.Codec+"/"+strings.Join(c.Surfaces, ","), func(t *testing.T) {
			selected, err := protocol.ModelToolsSelected(c.Codec, c.Operations, c.Surfaces)
			if err != nil {
				t.Fatal(err)
			}
			var frozen any
			if err := json.Unmarshal(selected, &frozen); err != nil {
				t.Fatal(err)
			}
			policy, _ := json.Marshal(map[string]any{"codec_id": c.Codec, "tools": frozen, "request_model": "test-model", "prompt": "controlled test", "instruction_role": "developer", "max_completion_tokens": 10, "max_output_tokens": 10, "max_tokens": 10})
			request, err := modelcodec.Build(policy, []byte(`{"purpose":"generation","conversation":[]}`))
			if err != nil {
				t.Fatal(err)
			}
			var received []byte
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				received, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"controlled":true}`)
			}))
			defer server.Close()
			p := profile(t, providers[c.Codec], server.URL)
			var client *Client
			if c.Codec == modelcodec.Bedrock {
				client = &Client{profile: p.settings, http: boundedHTTP(), sign: awsSign(aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
					return aws.Credentials{AccessKeyID: "test-id", SecretAccessKey: "test-secret"}, nil
				}), "us-west-2")}
				client.profile.Endpoint = server.URL + "/model/test-model/converse"
			} else {
				client, err = New(context.Background(), p, &secretResolver{})
				if err != nil {
					t.Fatal(err)
				}
			}
			defer client.Close()
			client.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			if _, err := client.Generate(context.Background(), request); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(request, received) {
				t.Fatal("provider request changed")
			}
			var native map[string]any
			if err := json.Unmarshal(received, &native); err != nil {
				t.Fatal(err)
			}
			tools := native["tools"]
			if c.Codec == modelcodec.Bedrock {
				tools = native["toolConfig"].(map[string]any)["tools"]
			}
			encoded, _ := json.Marshal(tools)
			want, _ := json.Marshal(frozen)
			if !bytes.Equal(encoded, want) {
				t.Fatal("frozen presentation changed during request construction")
			}
			declarations := tools.([]any)
			if c.Codec == modelcodec.Gemini {
				declarations = declarations[0].(map[string]any)["functionDeclarations"].([]any)
			}
			for _, value := range declarations {
				d := value.(map[string]any)
				if c.Codec == modelcodec.Chat {
					d = d["function"].(map[string]any)
				}
				if c.Codec == modelcodec.Bedrock {
					d = d["toolSpec"].(map[string]any)
				}
				description := d["description"].(string)
				if strings.Contains(description, "Runtime enforces") || strings.Contains(description, "Advanced guidance:") {
					t.Fatal("repeated boilerplate", d["name"])
				}
				if d["name"] == "finish" && (!strings.Contains(description, "entire campaign") || strings.Contains(description, "No tests ran")) {
					t.Fatal("finish scope missing or example repeated")
				}
				if d["name"] != "attempt_execute" {
					continue
				}
				params := d["parameters"]
				switch c.Codec {
				case modelcodec.Anthropic:
					params = d["input_schema"]
				case modelcodec.Bedrock:
					params = d["inputSchema"].(map[string]any)["json"]
				case modelcodec.Gemini:
					params = d["parametersJsonSchema"]
				}
				properties := params.(map[string]any)["properties"].(map[string]any)
				for _, field := range []string{"setup", "injection", "retain_injections"} {
					v, present := properties[field]
					if present != (len(c.Surfaces) > 0) {
						t.Fatal("selection disagrees with schema", field)
					}
					if present && (v.(map[string]any)["description"] == nil || v.(map[string]any)["description"] == "") {
						t.Fatal("setup field lacks applicable guidance", field)
					}
					if strings.Contains(description, field) {
						t.Fatal("unconditional advanced guidance", field)
					}
				}
			}
		})
	}
}
