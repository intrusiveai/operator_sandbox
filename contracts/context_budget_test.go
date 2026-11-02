package contracts

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"testing"
)

func TestContextBudgetValidation(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	l := ContextLimits{32768, 1024, 26214}
	if ContextWarningThreshold(32768, 80) != 26214 || ContextWarningThreshold(MaxSafeInteger, 99) > MaxSafeInteger || ContextWarningThreshold(1, 1) != 1 {
		t.Fatal("warning arithmetic")
	}
	// Preserve the existing input/total enforcement; do not add a new output cap.
	if _, err := NewContextBudget(l, ModelMetrics{Known: true, InputTokens: 10, OutputTokens: 1100, TotalTokens: 1110}, 1024, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, input := range []int64{0, 26213, 26214, 32768} {
		b, err := NewContextBudget(l, ModelMetrics{Known: true, InputTokens: input, OutputTokens: 1024, TotalTokens: input + 1024}, 1024, 0, 0)
		if err != nil || b.NearPromptLimit != (input >= 26214) || b.RemainingPromptTokens != 32768-input {
			t.Fatal(b, err)
		}
		raw, _ := json.Marshal(b)
		if DecodeContextBudget(raw) == nil {
			t.Fatal(string(raw))
		}
	}
	for _, m := range []ModelMetrics{{}, {Known: true, InputTokens: 32769, TotalTokens: 32769}, {Known: true, InputTokens: 10, OutputTokens: 1, TotalTokens: 12}} {
		if _, err := NewContextBudget(l, m, 1024, 0, 0); err == nil {
			t.Fatal("accepted invalid usage", m)
		}
	}
	f := modelFixtures(t)[0]
	policy, _ := Decode(f.Policy, OrdinaryLimit)
	policy.(map[string]any)["context_limits"] = l
	policyRaw, _ := json.Marshal(policy)
	result, _ := Decode(f.Result, OrdinaryLimit)
	m, err := p.ModelUsage(f.Result)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewContextBudget(l, m, 1024, 5000, 8)
	if err != nil {
		t.Fatal(err)
	}
	result.(map[string]any)["context_budget"] = b
	raw, _ := json.Marshal(result)
	if _, err = p.ValidateModelExchange(policyRaw, f.Request, raw); err != nil {
		t.Fatal(err)
	}
	old, _ := Decode(f.Result, OrdinaryLimit)
	delete(old.(map[string]any), "context_budget")
	oldRaw, _ := json.Marshal(old)
	if _, err = p.ValidateModelExchange(policyRaw, f.Request, oldRaw); err == nil {
		t.Fatal("old wire result accepted")
	}
	b.InputTokens++
	result.(map[string]any)["context_budget"] = b
	raw, _ = json.Marshal(result)
	if _, err = p.ValidateModelExchange(policyRaw, f.Request, raw); err == nil {
		t.Fatal("forged budget accepted")
	}
}
