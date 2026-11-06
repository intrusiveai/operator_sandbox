package contracts

import "encoding/json"

// ContextLimits are local admission policy, not a provider's context-window size.
type ContextLimits struct {
	MaximumPromptTokens     int64 `json:"maximum_prompt_tokens"`
	MaximumCompletionTokens int64 `json:"maximum_completion_tokens"`
	WarningThresholdTokens  int64 `json:"warning_threshold_tokens"`
}

func ContextWarningThreshold(prompt, percent int64) int64 {
	// Divide first so even JSON-safe ceilings cannot overflow an int64 product.
	return max(1, prompt/100*percent+(prompt%100)*percent/100)
}

func (l ContextLimits) Valid() bool {
	return l.MaximumPromptTokens > 0 && l.MaximumPromptTokens <= MaxSafeInteger &&
		l.MaximumCompletionTokens > 0 && l.MaximumCompletionTokens <= MaxSafeInteger-l.MaximumPromptTokens &&
		l.WarningThresholdTokens > 0 && l.WarningThresholdTokens <= l.MaximumPromptTokens
}

type ContextBudget struct {
	InputTokens               int64 `json:"input_tokens"`
	OutputTokens              int64 `json:"output_tokens"`
	TotalTokens               int64 `json:"total_tokens"`
	RequestedCompletionTokens int64 `json:"requested_completion_tokens"`
	ReservedTokens            int64 `json:"reserved_tokens"`
	RemainingPromptTokens     int64 `json:"remaining_prompt_tokens"`
	NearPromptLimit           bool  `json:"near_prompt_limit"`
	RemainingModelTokens      int64 `json:"remaining_model_tokens"`
	RemainingModelTurns       int64 `json:"remaining_model_turns"`
}

func NewContextBudget(l ContextLimits, m ModelMetrics, completion, remainingTokens, remainingTurns int64) (ContextBudget, error) {
	if !l.Valid() || !m.Known || m.InputTokens < 0 || m.OutputTokens < 0 || m.InputTokens > l.MaximumPromptTokens ||
		completion < 1 || completion > l.MaximumCompletionTokens || m.TotalTokens > l.MaximumPromptTokens+completion ||
		m.TotalTokens != m.InputTokens+m.OutputTokens || remainingTokens < 0 || remainingTokens > MaxSafeInteger || remainingTurns < 0 || remainingTurns > MaxSafeInteger {
		return ContextBudget{}, ErrProtocol
	}
	return ContextBudget{m.InputTokens, m.OutputTokens, m.TotalTokens, completion, l.MaximumPromptTokens + completion,
		l.MaximumPromptTokens - m.InputTokens, m.InputTokens >= l.WarningThresholdTokens, remainingTokens, remainingTurns}, nil
}

func contextLimits(value any) (ContextLimits, error) {
	raw, err := json.Marshal(value)
	var limits ContextLimits
	if err != nil || json.Unmarshal(raw, &limits) != nil || !limits.Valid() {
		return limits, ErrProtocol
	}
	return limits, nil
}

// DecodeContextBudget is an explicit projection for retained public accounting.
// Threshold/policy binding is checked when accepting the original exchange.
func DecodeContextBudget(raw []byte) *ContextBudget {
	v, err := Decode(raw, 4096)
	if err != nil {
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 9 {
		return nil
	}
	for _, key := range []string{"input_tokens", "output_tokens", "total_tokens", "requested_completion_tokens", "reserved_tokens", "remaining_prompt_tokens", "remaining_model_tokens", "remaining_model_turns"} {
		n, ok := m[key].(json.Number)
		if !ok {
			return nil
		}
		i, e := n.Int64()
		if e != nil || i < 0 || i > MaxSafeInteger {
			return nil
		}
	}
	if _, ok := m["near_prompt_limit"].(bool); !ok {
		return nil
	}
	var b ContextBudget
	if json.Unmarshal(raw, &b) != nil || b.RequestedCompletionTokens < 1 || b.TotalTokens > b.ReservedTokens ||
		b.TotalTokens != b.InputTokens+b.OutputTokens || b.ReservedTokens != b.InputTokens+b.RemainingPromptTokens+b.RequestedCompletionTokens {
		return nil
	}
	return &b
}

// ValidateModelExchange requires the complete wire result, after native validation.
// Native provider probes and the host's pre-accounting path use the explicitly
// separate ValidateNativeModelExchange method; old wire results are not accepted.
func (p *Protocol) ValidateModelExchange(policyRaw, requestRaw, resultRaw []byte) (map[string]any, error) {
	result, err := p.ValidateNativeModelExchange(policyRaw, requestRaw, resultRaw)
	if err != nil {
		return nil, err
	}
	if _, err = p.catalog.Validate(EngineModelGenerateResultSchema, resultRaw, OrdinaryLimit); err != nil {
		return nil, err
	}
	policy, err := Decode(policyRaw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	l, err := contextLimits(policy.(map[string]any)["context_limits"])
	if err != nil {
		return nil, err
	}
	m, err := p.ModelUsage(resultRaw)
	if err != nil {
		return nil, err
	}
	completion, err := p.ModelOutputLimit(requestRaw)
	if err != nil {
		return nil, err
	}
	b := result["context_budget"].(map[string]any)
	expected, err := NewContextBudget(l, m, completion, number(b["remaining_model_tokens"]), number(b["remaining_model_turns"]))
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(expected)
	v, _ := Decode(raw, OrdinaryLimit)
	if !wireEqual(b, v) {
		return nil, modelValidation("context_budget", "context_budget_mismatch", "/context_budget")
	}
	return result, nil
}

// DecodeContextLimits projects only validated, content-free local allowances.
func DecodeContextLimits(raw []byte) *ContextLimits {
	value, err := Decode(raw, 4096)
	m, ok := value.(map[string]any)
	if err != nil || !ok || len(m) != 3 {
		return nil
	}
	for _, key := range []string{"maximum_prompt_tokens", "maximum_completion_tokens", "warning_threshold_tokens"} {
		n, ok := m[key].(json.Number)
		if !ok {
			return nil
		}
		if _, err := n.Int64(); err != nil {
			return nil
		}
	}
	l, err := contextLimits(m)
	if err != nil {
		return nil
	}
	return &l
}
