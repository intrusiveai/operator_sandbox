package campaign

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTargetFailureSafetyAndRoundTrip(t *testing.T) {
	for _, text := range []string{"context deadline exceeded", "downstream inventory tool failed", "authorization=Bearer-token", strings.Repeat("é", 400), "bad\x1b[31m"} {
		f := &TargetFailure{NativeOperation: "application.invoke", Operation: "support-baseline", AttemptID: "attempt-1", TurnID: "turn-1", StepID: "step-1", ResponseDigest: "sha256:" + strings.Repeat("a", 64), HTTPStatus: 200, Category: "target_turn_failed"}
		f.SetDetail(text)
		if !f.Valid() || strings.Contains(f.Message(), "Bearer-token") || strings.Contains(f.Message(), "\x1b") {
			t.Fatalf("%+v", f)
		}
		d := NewWorkflowDiagnostic("request-1", "target", "delivery", "target_failed", "", "attempt_execute", "call-1")
		d.TargetFailure = f
		raw, _ := json.Marshal(d)
		if !d.Valid() || DecodeWorkflowDiagnostic(raw) == nil {
			t.Fatal(string(raw))
		}
	}
	old := NewWorkflowDiagnostic("request-1", "target", "delivery", "target_failed", "", "attempt_execute", "call-1")
	raw, _ := json.Marshal(old)
	if DecodeWorkflowDiagnostic(raw) == nil {
		t.Fatal("old diagnostic rejected")
	}
}
