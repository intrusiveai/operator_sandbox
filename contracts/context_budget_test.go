package contracts

import (
	"encoding/json"
	"testing"
)

func TestContextBudgetValidation(t *testing.T) {
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
}
