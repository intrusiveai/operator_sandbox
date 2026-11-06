package contracts

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestModelValidationDiagnostics(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"model-codec.json", "bedrock-model-codec.json", "anthropic-model-codec.json", "gemini-model-codec.json", "responses-model-codec.json"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("../schemas/fixtures/" + name)
			if err != nil {
				t.Fatal(err)
			}
			var fixtures []struct{ Policy, Request, Result map[string]any }
			if err = json.Unmarshal(raw, &fixtures); err != nil {
				t.Fatal(err)
			}
			f := fixtures[0]
			encode := func(v any) []byte { b, _ := json.Marshal(v); return b }
			if _, err = p.ValidateNativeModelExchange(encode(f.Policy), encode(f.Request), encode(f.Result)); err != nil {
				t.Fatal("valid fixture", err)
			}
			f.Result["profile_id"] = "other-profile"
			_, err = p.ValidateNativeModelExchange(encode(f.Policy), encode(f.Request), encode(f.Result))
			var issue *ModelValidationError
			if !errors.As(err, &issue) || issue.Stage != "correlation" || issue.Reason != "request_response_mismatch" || issue.FieldPath != "/profile_id" {
				t.Fatal(err)
			}
			f.Result["profile_id"] = f.Request["profile_id"]
			f.Result["response"].(map[string]any)["private-credential-key"] = true
			_, err = p.ValidateNativeModelExchange(encode(f.Policy), encode(f.Request), encode(f.Result))
			if !errors.As(err, &issue) || issue.Stage != "schema" || issue.Reason != "unsupported_response_structure" || !issue.PathOmitted || issue.FieldPath != "" {
				t.Fatal(err, issue)
			}
		})
	}
	for _, mode := range []string{"stop", "duplicate", "usage", "reasoning", "completion"} {
		t.Run("bedrock/"+mode, func(t *testing.T) {
			raw, _ := os.ReadFile("../schemas/fixtures/bedrock-model-codec.json")
			var f []struct{ Policy, Request, Result map[string]any }
			_ = json.Unmarshal(raw, &f)
			response := f[0].Result["response"].(map[string]any)
			reason, path, stage := "inconsistent_stop_reason", "/response/stopReason", "semantics"
			switch mode {
			case "stop":
				response["stopReason"] = "tool_use"
			case "duplicate":
				response["stopReason"] = "tool_use"
				response["output"].(map[string]any)["message"].(map[string]any)["content"] = []any{map[string]any{"toolUse": map[string]any{"toolUseId": "duplicate", "name": "snapshot_list", "input": map[string]any{}}}, map[string]any{"toolUse": map[string]any{"toolUseId": "duplicate", "name": "snapshot_list", "input": map[string]any{}}}}
				reason, path = "duplicate_tool_call_identity", "/response/output/message/content"
			case "usage":
				response["usage"].(map[string]any)["totalTokens"] = 999
				reason, path = "usage_invalid", "/response/usage/totalTokens"
			case "reasoning":
				response["output"].(map[string]any)["message"].(map[string]any)["content"] = []any{map[string]any{"reasoningContent": map[string]any{"redactedContent": "invalid-base64"}}}
				reason, path = "invalid_reasoning_content", "/response/output/message/content"
			case "completion":
				response["usage"] = map[string]any{"inputTokens": 1, "outputTokens": 4096, "totalTokens": 4097}
				reason, path, stage = "completion_allowance_exceeded", "/response/usage/outputTokens", "correlation"
			}
			policy, _ := json.Marshal(f[0].Policy)
			request, _ := json.Marshal(f[0].Request)
			result, _ := json.Marshal(f[0].Result)
			_, err = p.ValidateNativeModelExchange(policy, request, result)
			var issue *ModelValidationError
			if !errors.As(err, &issue) || issue.Reason != reason || issue.FieldPath != path || issue.Stage != stage {
				t.Fatalf("%+v; %v", issue, err)
			}
		})
	}
}
