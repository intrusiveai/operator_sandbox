//go:build linux || darwin

package hostrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/credentials"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/startup"
	"github.com/intrusiveai/operator_sandbox/internal/submission"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func TestPreparationDiagnosticUsesOnlySafeCauses(t *testing.T) {
	const secret = "SENSITIVE_AUTHORIZATION_VALUE"
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("https://user:%s@private.invalid/?key=%s: %w", secret, secret, imagerelease.ErrCompatibility), "incompatible"},
		{&os.PathError{Op: "open", Path: "/private/" + secret, Err: os.ErrNotExist}, "does not exist"},
		{&interceptor.RemoteError{Response: interceptor.Response{Status: 409, Body: json.RawMessage(`{"code":"denied","message":"permission denied; authorization: Bearer ` + secret + `","private":"` + secret + `"}`)}}, "HTTP 409"},
		{&interceptor.CallError{Kind: "invalid_response", Uncertain: true}, "invalid_response"},
		{&interceptor.CallError{Kind: secret, Uncertain: true}, "unclassified_failure"},
		{fmt.Errorf("%s: %w", secret, context.DeadlineExceeded), "deadline exceeded"},
		{errors.New(secret), "no additional safe error detail"},
		{fmt.Errorf("private wrapper: %w", errors.Join(errors.New(secret), context.DeadlineExceeded)), "no additional safe error detail"},
		{&os.PathError{Op: "open", Path: "/private/" + secret, Err: os.ErrPermission}, "permission denied"},
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
	if !errors.Is(err, primary) || !errors.Is(err, secondary) || !d.CleanupFailed || d.Stage != "Interceptor attachment" || !strings.Contains(d.Message, "transport_unavailable") || strings.Contains(err.Error(), "secret") {
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
	if !errors.Is(err, dockercontrol.ErrExecutableUnavailable) || PreparationDetails(err).Message != "Operator preparation (Docker client initialization): Docker executable unavailable" || strings.Contains(err.Error(), "private-credential") {
		t.Fatal(err)
	}
}

// A new producer needs no concrete-type case in preparationMessage.
type producerDiagnostic struct{ text string }

func (e producerDiagnostic) Error() string             { return "private underlying error" }
func (e producerDiagnostic) DiagnosticMessage() string { return e.text }

func TestPreparationForwardsProducerDiagnostic(t *testing.T) {
	primary := producerDiagnostic{"required capability feature:snapshots denied by host_policy"}
	wrapped := fmt.Errorf("private wrapper: %w", primary)
	for _, cause := range []error{primary, wrapped, errors.Join(wrapped, producerDiagnostic{"secondary cleanup failed"}), fmt.Errorf("outer: %w", errors.Join(wrapped, producerDiagnostic{"secondary cleanup failed"}))} {
		err := PreparationFailure("target preparation", cause)
		d := PreparationDetails(err)
		if d == nil || !d.Valid() || d.Message != "Operator preparation (target preparation): "+primary.text || !errors.Is(err, cause) {
			t.Fatal(d, err)
		}
	}
	err := preparationCleanup("target preparation", wrapped, errors.New("private cleanup cause"))
	d := PreparationDetails(err)
	if !d.CleanupFailed || !strings.Contains(d.Message, primary.text) || strings.Contains(d.Message, "private") {
		t.Fatal(d)
	}
}

func TestPreparationProducerCoverage(t *testing.T) {
	for _, cause := range []error{
		capabilities.ErrNative, capabilities.ErrProjection, capabilities.ErrBinding,
		capabilities.ErrBundle, capabilities.ErrCompatibility, capabilities.ErrPolicy,
		attemptadapter.ErrPolicy, targetprofile.ErrProfile, httpstarget.ErrMapping,
		hostconfig.ErrInvalid, contractstore.ErrIntegrity, submission.ErrSubmission,
		skills.ErrSkill, staging.ErrInputs, startup.ErrUnresolved, imagerelease.ErrEmbedded,
		credentials.ErrAudit,
	} {
		d := PreparationDetails(PreparationFailure("target preparation", fmt.Errorf("private context: %w", cause)))
		if d == nil || !d.Valid() || d.Message != "Operator preparation (target preparation): "+cause.Error() {
			t.Fatal(d, cause)
		}
	}
	// The configured timeout ceiling previously produced an unrecognized error.
	profile := new(targetprofile.Profile)
	cause := profile.CheckTimeoutLimit(-1)
	d := PreparationDetails(PreparationFailure("target profile loading", cause))
	if d == nil || !strings.Contains(d.Message, "target.max_operation_timeout_ms") {
		t.Fatal(d)
	}
}

func TestPreparationPreservesContractRuleAndPath(t *testing.T) {
	catalog, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	_, cause := catalog.Validate(contracts.ScenarioBundleSchema, []byte(`{}`), contracts.OrdinaryLimit)
	var argument *contracts.ArgumentError
	if !errors.As(cause, &argument) || argument.FieldPath == "" {
		t.Fatal(cause)
	}
	d := PreparationDetails(PreparationFailure("saved submission loading", fmt.Errorf("private wrapper: %w", cause)))
	if d == nil || !d.Valid() || !strings.Contains(d.Message, strconv.Quote(argument.FieldPath)) || !strings.Contains(d.Message, argument.Correction) || strings.Contains(d.Message, "private wrapper") {
		t.Fatal(d)
	}
	for _, cause := range []error{contracts.ErrCatalog, contracts.ErrSchema, contracts.ErrJSON, contracts.ErrLimit, contracts.ErrProtocol} {
		d := PreparationDetails(PreparationFailure("shared contract loading", cause))
		if d == nil || !strings.Contains(d.Message, cause.Error()) {
			t.Fatal(d)
		}
	}
}
