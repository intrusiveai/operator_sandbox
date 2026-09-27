package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
)

func modelFixture(t *testing.T) (map[string]any, map[string]any, map[string]any) {
	return namedModelFixture(t, "model-codec.json", "native text and usage preserved")
}
func namedModelFixture(t *testing.T, file, name string) (map[string]any, map[string]any, map[string]any) {
	t.Helper()
	var fixtures []struct {
		Name                    string
		Policy, Request, Result map[string]any
	}
	if err := json.Unmarshal(read(t, "../../schemas/fixtures/"+file), &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		if f.Name == name {
			return f.Policy, f.Request, f.Result
		}
	}
	t.Fatal("missing model fixture")
	return nil, nil, nil
}

func TestServiceNativeCacheAccountingAndRestore(t *testing.T) {
	for _, codec := range []struct{ file, name, cache, usage string }{
		{"anthropic-model-codec.json", "anthropic: native text including cache counts", "cache_read_input_tokens", "usage"},
		{"bedrock-model-codec.json", "bedrock: native text with separate caches", "cacheReadInputTokens", "usage"},
		{"gemini-model-codec.json", "gemini: native text including thinking tokens", "promptTokenCount", "usageMetadata"},
		{"responses-model-codec.json", "responses: native text with usage details", "input_tokens", "usage"},
	} {
		t.Run(codec.file, func(t *testing.T) {
			for _, mode := range []string{"complete", "unknown-usage", "excess-input"} {
				t.Run(mode, func(t *testing.T) {
					policy, request, result := namedModelFixture(t, codec.file, codec.name)
					response := result["response"].(map[string]any)
					if mode == "unknown-usage" {
						delete(response, codec.usage)
					}
					if mode == "excess-input" {
						usage := response[codec.usage].(map[string]any)
						usage[codec.cache] = 101
						if codec.cache == "input_tokens" {
							usage[codec.cache] = 201
							usage["total_tokens"] = 261
						}
						if codec.cache == "promptTokenCount" {
							usage[codec.cache] = 201
							usage["totalTokenCount"] = 261
						}
						if codec.cache == "cacheReadInputTokens" {
							usage["totalTokens"] = 211
						}
					}
					p := &provider{response: encode(response)}
					routes := append(append([]string{}, snapshotRoutes...), "engine.model_generate")
					s, _, runtime, _, launch := serviceWithTemplate(t, 0, routes, func(c *campaignservice.Config) {
						c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
					}, func(c map[string]any) {
						digest, err := contracts.CanonicalDigest(encode(policy["tools"]), contracts.OrdinaryLimit)
						if err != nil {
							t.Fatal(err)
						}
						settings := map[string]any{"tools_digest": digest}
						for _, key := range []string{"max_tokens", "thinking", "max_output_tokens", "thinking_config", "reasoning", "response_models"} {
							if value, ok := policy[key]; ok {
								settings[key] = value
							}
						}
						c["model"] = map[string]any{"codec_id": policy["codec_id"], "profile_id": policy["profile_id"], "profile_digest": policy["profile_digest"], "model_id": policy["request_model"], "features": []string{"text", "function-tools"}, "codec_settings": settings}
					}, snapshotInput(t))
					if err := s.Admit(context.Background(), launch); err != nil {
						t.Fatal(err)
					}
					var system any = string(launch.Prompt)
					if codec.cache == "cacheReadInputTokens" {
						system = []any{map[string]any{"text": string(launch.Prompt)}}
					}
					if codec.cache == "input_tokens" {
						request["request"].(map[string]any)["instructions"] = string(launch.Prompt)
						response["instructions"] = string(launch.Prompt)
						p.response = encode(response)
					} else if codec.cache == "promptTokenCount" {
						request["request"].(map[string]any)["systemInstruction"] = map[string]any{"parts": []any{map[string]any{"text": string(launch.Prompt)}}}
					} else {
						request["request"].(map[string]any)["system"] = system
					}
					raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "messages-1", 1, request), 0)
					if mode != "complete" {
						code := "OUTCOME_UNKNOWN"
						if mode == "excess-input" {
							code = "INTERNAL_ERROR"
						}
						if err != nil || !strings.Contains(string(raw), code) {
							t.Fatal(string(raw), err)
						}
						select {
						case <-runtime.killed:
						case <-time.After(3 * time.Second):
							t.Fatal("invalid model accounting did not stop execution")
						}
						return
					}
					first := stateResult(t, raw, err)
					raw, err = s.Handle(context.Background(), stateWire("engine.model_generate", "messages-1", 1, request), 0)
					if stateResult(t, raw, err)["receipt_id"] != first["receipt_id"] || p.calls != 1 {
						t.Fatal("model replay executed twice")
					}
					raw, err = s.Handle(context.Background(), stateWire("engine.snapshot_request", "snapshot", 1, map[string]any{}), 0)
					cp := stateResult(t, raw, err)["snapshot"].(map[string]any)
					raw, err = s.Handle(context.Background(), stateWire("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]}), 0)
					remaining := stateResult(t, raw, err)["remaining_limits"].(map[string]any)
					if remaining["model_tokens"] != float64(9840) || remaining["model_turns"] != float64(2) {
						t.Fatal("native thinking/cache tokens were omitted or refunded", remaining)
					}
				})
			}
		})
	}
}

type provider struct {
	calls    int
	response []byte
	err      error
	block    bool
	entered  chan struct{}
}

func (p *provider) Generate(ctx context.Context, request []byte) ([]byte, error) {
	p.calls++
	if p.entered != nil {
		close(p.entered)
	}
	if p.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return p.response, p.err
}
func TestServiceModelReplayPolicyAndRestoreBudgets(t *testing.T) {
	policy, request, result := modelFixture(t)
	p := &provider{response: encode(result["response"])}
	routes := append(append([]string{}, snapshotRoutes...), "engine.model_generate")
	s, _, _, _, launch := serviceWithSettings(t, 0, routes, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	}, snapshotInput(t))
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	call := func(op, id string, rev int, body any) map[string]any {
		t.Helper()
		raw, err := s.Handle(context.Background(), stateWire(op, id, rev, body), 0)
		return stateResult(t, raw, err)
	}
	first := call("engine.model_generate", "model-1", 1, request)
	if p.calls != 1 {
		t.Fatal(p.calls)
	}
	if again := call("engine.model_generate", "model-1", 1, request); again["receipt_id"] != first["receipt_id"] || p.calls != 1 {
		t.Fatal("duplicate model dispatch", again)
	}
	cp := call("engine.snapshot_request", "snapshot", 1, map[string]any{})["snapshot"].(map[string]any)
	restored := call("engine.restore_request", "restore", 1, map[string]any{"source_session": cp["source_session"], "checkpoint_id": cp["checkpoint_id"]})
	remaining := restored["remaining_limits"].(map[string]any)
	if remaining["model_turns"] != float64(2) || remaining["model_tokens"] != float64(9880) {
		t.Fatal("restore refunded usage", remaining)
	}
	call("engine.model_generate", "model-1", 2, request)
	if p.calls != 1 {
		t.Fatal("restore replay called provider")
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = "Changed prompt"
	raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "changed", 2, request), 0)
	if err != nil || !strings.Contains(string(raw), "POLICY_DENIED") || p.calls != 1 {
		t.Fatal(string(raw), err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	call("engine.model_generate", "model-2", 2, request)
	call("engine.model_generate", "model-3", 2, request)
	raw, err = s.Handle(context.Background(), stateWire("engine.model_generate", "model-4", 2, request), 0)
	if err != nil || !strings.Contains(string(raw), "LIMIT_EXCEEDED") || p.calls != 3 {
		t.Fatal(string(raw), err)
	}
}
func TestServiceModelUncertainAndInvalidResponsesTerminate(t *testing.T) {
	for _, mode := range []string{"lost-reply", "missing-usage", "bad-model", "excess-input", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			policy, request, result := modelFixture(t)
			response := result["response"].(map[string]any)
			p := &provider{}
			switch mode {
			case "lost-reply":
				p.err = errors.New("private provider details")
			case "missing-usage":
				delete(response, "usage")
			case "bad-model":
				response["model"] = "unapproved"
			case "excess-input":
				response["usage"].(map[string]any)["prompt_tokens"] = 300
				response["usage"].(map[string]any)["total_tokens"] = 320
			}
			p.response = encode(response)
			if mode == "oversize" {
				p.response = make([]byte, (4<<20)+1)
			}
			s, _, runtime, _, launch := serviceWithSettings(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
				c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
			})
			if err := s.Admit(context.Background(), launch); err != nil {
				t.Fatal(err)
			}
			request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
			raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "model", 1, request), 0)
			if err != nil || !strings.Contains(string(raw), "terminate") || strings.Contains(string(raw), "private provider") {
				t.Fatal(string(raw), err)
			}
			select {
			case <-runtime.killed:
			case <-time.After(time.Second):
				t.Fatal("provider failure left execution open")
			}
			_, _ = s.Handle(context.Background(), stateWire("engine.model_generate", "model", 1, request), 0)
			if p.calls != 1 {
				t.Fatal("retried uncertain generation")
			}
		})
	}
}
func TestServiceModelCancellationDoesNotBlockDockerTermination(t *testing.T) {
	policy, request, _ := modelFixture(t)
	p := &provider{block: true, entered: make(chan struct{})}
	s, _, runtime, _, launch := serviceWithSettings(t, 0, []string{"engine.model_generate"}, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = s.Handle(context.Background(), stateWire("engine.model_generate", "model", 1, request), 0)
	}()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("provider not entered")
	}
	s.Stop(errors.New("administrator stop"))
	select {
	case <-runtime.killed:
	case <-time.After(time.Second):
		t.Fatal("provider blocked independent kill")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("provider context not canceled")
	}
}

func TestServiceModelOversizedBatchClosesExploration(t *testing.T) {
	policy, request, result := modelFixture(t)
	response := result["response"].(map[string]any)
	choice := response["choices"].([]any)[0].(map[string]any)
	choice["finish_reason"] = "tool_calls"
	calls := []any{}
	for i := 0; i < 17; i++ {
		calls = append(calls, map[string]any{"id": strings.Repeat("x", i+1), "type": "function", "function": map[string]any{"name": "snapshot_list", "arguments": "{}"}})
	}
	choice["message"].(map[string]any)["tool_calls"] = calls
	p := &provider{response: encode(response)}
	s, _, _, w, launch := serviceWithSettings(t, 0, []string{"engine.model_generate", "engine.artifact_begin"}, func(c *campaignservice.Config) {
		c.Model = &campaignservice.ModelConfig{Provider: p, ProfileDigest: policy["profile_digest"].(string), Tools: encode(policy["tools"]), MaximumPromptTokens: 200}
	})
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	request["request"].(map[string]any)["messages"].([]any)[0].(map[string]any)["content"] = string(launch.Prompt)
	raw, err := s.Handle(context.Background(), stateWire("engine.model_generate", "model", 1, request), 0)
	if err != nil || !strings.Contains(string(raw), "LIMIT_EXCEEDED") || !strings.Contains(string(raw), "known") || p.calls != 1 {
		t.Fatal(string(raw), err)
	}
	raw, err = s.Handle(context.Background(), stateWire("engine.artifact_begin", "payload", 1, map[string]any{"purpose": "payload", "artifact": descriptor([]byte(`{}`))}), 0)
	if err != nil || !strings.Contains(string(raw), "STATE_CHANGED") || w.Fence().Err() != nil {
		t.Fatal("batch prefix could execute or finalization was unavailable", string(raw), err)
	}
}
