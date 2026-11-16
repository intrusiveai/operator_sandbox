//go:build linux || darwin

package livequalification

import (
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/modelcodec"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

const probePrompt = "This is a harmless API compatibility test. Follow each user instruction. The snapshot_list tool is simulated; no target exists. After its result, reply briefly in text without calling tools."
const probeText = "Reply briefly in text that you are ready. Do not call tools yet."
const probeTool = "Now call snapshot_list exactly once with empty arguments."
const probeResult = `{"snapshots":[],"qualification_fixture":true}`

type conversation struct {
	protocol *contracts.Protocol
	policy   []byte
	codec    string
	body     map[string]any
	request  map[string]any
}

func jsonBytes(v any) []byte { raw, _ := json.Marshal(v); return raw }
func newConversation(q *Prepared) (*conversation, error) {
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		return nil, ErrPlan
	}
	s := q.profile.Settings()
	toolsRaw, err := p.ModelTools(s.Codec, []string{"engine.snapshot_list"})
	if err != nil {
		return nil, ErrPlan
	}
	policyRaw, err := q.profile.Policy(toolsRaw, []byte(probePrompt))
	if err != nil {
		return nil, err
	}
	policyValue, _ := contracts.Decode(policyRaw, contracts.OrdinaryLimit)
	policy := policyValue.(map[string]any)
	key := "max_output_tokens"
	if s.Codec == modelcodec.Chat {
		key = "max_completion_tokens"
	}
	if s.Codec == modelcodec.Anthropic || s.Codec == modelcodec.Bedrock {
		key = "max_tokens"
	}
	policy[key] = q.plan.MaximumOutputTokens
	c := &conversation{protocol: p, codec: s.Codec, policy: jsonBytes(policy)}
	c.body = map[string]any{"purpose": "generation", "conversation": []any{c.user(probeText)}}
	if err = c.build(); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *conversation) build() error {
	raw, err := modelcodec.Build(c.policy, jsonBytes(c.body))
	if err != nil {
		return err
	}
	v, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil {
		return err
	}
	c.request = v.(map[string]any)
	return nil
}

func (c *conversation) user(text string) map[string]any {
	switch c.codec {
	case "bedrock-converse-text-tools-v1":
		return map[string]any{"role": "user", "content": []any{map[string]any{"text": text}}}
	case "gemini-text-tools-v1":
		return map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}
	default:
		return map[string]any{"role": "user", "content": text}
	}
}
func (c *conversation) extend(raw []byte, askTool bool) error {
	v, err := contracts.Decode(raw, contracts.OrdinaryLimit)
	if err != nil {
		return err
	}
	history := append(c.body["conversation"].([]any), v.([]any)...)
	if askTool {
		history = append(history, c.user(probeTool))
	}
	c.body["conversation"] = history
	return c.build()
}
func (c *conversation) continuation(result []byte) ([]byte, error) {
	v, err := contracts.Decode(result, contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	parsed, err := modelcodec.Parse(c.codec, jsonBytes(v.(map[string]any)["response"]))
	if err != nil {
		return nil, err
	}
	results := []string{}
	for _, call := range parsed.Calls {
		if call.Name != "snapshot_list" {
			return nil, ErrCheck
		}
		args := jsonBytes(call.Arguments)
		if text, ok := call.Arguments.(string); ok {
			args = []byte(text)
		}
		if _, err = c.protocol.ValidateToolArguments("snapshot_list", args); err != nil {
			return nil, err
		}
		results = append(results, probeResult)
	}
	return modelcodec.Continue(c.codec, parsed, results)
}
