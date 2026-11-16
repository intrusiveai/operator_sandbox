//go:build linux || darwin

package modelprovider

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestUsageAllowanceDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name          string
		input, output int64
		prompt, total bool
	}{
		{"input-only", 33500, 300, true, false},
		{"total-only", 32768, 4097, false, true},
		{"both", 36864, 300, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := Diagnostic{Provider: "bedrock-converse", HTTPStatus: 200}.WithUsageRejection(contracts.ModelMetrics{Known: true, InputTokens: tc.input, OutputTokens: tc.output, TotalTokens: tc.input + tc.output}, 32768, 4096, 36864)
			u := d.UsageAllowance
			if !d.Valid() || d.Reason != "usage_exceeds_reservation" || u.PromptAllowanceExceeded != tc.prompt || u.TotalReservationExceeded != tc.total {
				t.Fatal(d)
			}
			raw, _ := json.Marshal(d)
			if decoded := DecodeDiagnostic(raw); decoded == nil || *decoded != d {
				t.Fatal("numeric fields lost", string(raw))
			}
			if !strings.Contains(d.Summary(), "local token allowance") || !strings.Contains(d.Summary(), "HTTP 200") || !strings.Contains(d.UsageDetailsText(), "reserved total: 36864") {
				t.Fatal(d.Summary(), d.UsageDetailsText())
			}
		})
	}
}

func TestUsageDiagnosticRejectsUnvalidatedAndMalformedCounts(t *testing.T) {
	for _, metrics := range []contracts.ModelMetrics{
		{}, {Known: true, InputTokens: -1}, {Known: true, InputTokens: 33500, TotalTokens: 1},
		{Known: true, InputTokens: contracts.MaxSafeInteger + 1, TotalTokens: contracts.MaxSafeInteger + 1},
	} {
		d := Diagnostic{}.WithUsageRejection(metrics, 32768, 4096, 36864)
		if !d.Valid() || d.UsageAllowance != (UsageAllowance{}) || d.UsageDetailsText() != "" {
			t.Fatal("unvalidated counts retained", d)
		}
	}
	d := Diagnostic{}.WithUsageRejection(contracts.ModelMetrics{Known: true, InputTokens: 33500, OutputTokens: 300, TotalTokens: 33800}, 32768, 4096, 36864)
	raw, _ := json.Marshal(d)
	for _, field := range []string{"input_tokens", "output_tokens", "total_tokens", "maximum_prompt_tokens", "requested_completion_tokens", "reserved_tokens", "prompt_allowance_exceeded", "total_reservation_exceeded"} {
		for _, mode := range []string{"missing", "null", "wrong-type"} {
			var value map[string]any
			_ = json.Unmarshal(raw, &value)
			u := value["usage_allowance"].(map[string]any)
			switch mode {
			case "missing":
				delete(u, field)
			case "null":
				u[field] = nil
			case "wrong-type":
				u[field] = "synthetic-secret"
			}
			bad, _ := json.Marshal(value)
			if DecodeDiagnostic(bad) != nil {
				t.Fatal(field, mode)
			}
		}
	}
	for _, suffix := range []string{`,"authorization":"synthetic-secret"`, `,"input_tokens":1`} {
		bad := strings.Replace(string(raw), `"usage_allowance":{`, `"usage_allowance":{`+strings.TrimPrefix(suffix, ",")+",", 1)
		if DecodeDiagnostic([]byte(bad)) != nil {
			t.Fatal("unsafe or duplicate field accepted")
		}
	}
	// Existing records keep their original reason/message and no invented counts.
	d.UsageAllowance = UsageAllowance{}
	d = d.WithFailure("response_validation", "usage_exceeds_reservation")
	raw, _ = json.Marshal(d)
	if decoded := DecodeDiagnostic(raw); decoded == nil || decoded.UsageDetailsText() != "" {
		t.Fatal("legacy record rejected")
	}
}
