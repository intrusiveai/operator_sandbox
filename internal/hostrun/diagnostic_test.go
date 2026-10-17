//go:build linux || darwin

package hostrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func TestPreparationDiagnosticUsesOnlySafeCauses(t *testing.T) {
	const secret = "SENSITIVE_AUTHORIZATION_VALUE"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("https://user:%s@private.invalid/?key=%s: %w", secret, secret, imagerelease.ErrCompatibility), "incompatible"},
		{&os.PathError{Op: "open", Path: "/private/" + secret, Err: os.ErrNotExist}, "does not exist"},
		{&interceptor.RemoteError{Response: interceptor.Response{Status: 409, Body: json.RawMessage(`{"code":"` + secret + `","message":"` + secret + `"}`)}}, "HTTP 409"},
		{&interceptor.CallError{Kind: "invalid_response", Uncertain: true}, "contract validation; the target-side outcome is uncertain"},
		{&interceptor.CallError{Kind: secret, Uncertain: true}, "Interceptor request failed"},
		{fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), "deadline exceeded"},
		{errors.New(secret), "no additional safe error detail"},
	} {
		err := PreparationFailure("local Harness image and compatibility validation", tc.err)
		d := PreparationDetails(err)
		if !errors.Is(err, tc.err) || d == nil || !d.Valid() || !strings.Contains(d.Message, tc.want) || strings.Contains(err.Error(), secret) {
			t.Fatal(d, err)
		}
		raw, _ := json.Marshal(d)
		if strings.Contains(string(raw), secret) {
			t.Fatal("unsafe diagnostic", string(raw))
		}
		if errors.Is(tc.err, imagerelease.ErrCompatibility) && d.Stage != "Harness compatibility validation" {
			t.Fatal(d)
		}
	}
}

func TestPreparationCleanupPreservesPrimaryAndSecondary(t *testing.T) {
	primary := &interceptor.CallError{Kind: "transport_unavailable", Uncertain: true}
	secondary := errors.New("private cleanup secret")
	err := preparationCleanup("Interceptor attachment", primary, secondary)
	d := PreparationDetails(err)
	if !errors.Is(err, primary) || !errors.Is(err, secondary) || !d.CleanupFailed || d.Stage != "Interceptor attachment" || !strings.Contains(d.Message, "transport failed") || strings.Contains(err.Error(), "secret") {
		t.Fatal(d, err)
	}
	outer := preparationCleanup("campaign preparation", err, nil)
	if *PreparationDetails(outer) != *d {
		t.Fatal("outer stage replaced primary")
	}
}

func TestDockerInitializationCauseIsSafeAndSpecific(t *testing.T) {
	_, cause := dockercontrol.New("/missing/private-credential/Docker")
	err := PreparationFailure("Docker client initialization", cause)
	if !errors.Is(err, dockercontrol.ErrExecutableUnavailable) || PreparationDetails(err).Message != "Docker executable unavailable" || strings.Contains(err.Error(), "private-credential") {
		t.Fatal(err)
	}
}
