package diagnostictext

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

type publicFailure string

func (e publicFailure) Error() string             { return "unrestricted private cause" }
func (e publicFailure) DiagnosticMessage() string { return string(e) }

func TestFromErrorPreservesOnlyProducerSelectedPrimaryDetail(t *testing.T) {
	primary := publicFailure("capability admission: host_policy")
	for _, err := range []error{primary, fmt.Errorf("secret wrapper: %w", primary), fmt.Errorf("secret wrapper: %w", errors.Join(primary, publicFailure("cleanup failed")))} {
		got, ok := FromError(err)
		if !ok || got != string(primary) {
			t.Fatal(got, ok)
		}
	}
	for _, err := range []error{nil, errors.New("private cause"), errors.Join(errors.New("private primary"), primary)} {
		if got, ok := FromError(err); ok || got != "" {
			t.Fatal("exposed unmarked or secondary error", got)
		}
	}
}

func TestFromErrorBoundsAndRedactsDetail(t *testing.T) {
	for _, tc := range []struct{ text, absent, omission string }{
		{"authorization: Bearer sk-private-key", "sk-private-key", ""},
		{"request to https://user:password@example.invalid", "password", ""},
		{"unsafe\x1bcontrol", "\x1b", "unsafe_control"},
		{strings.Repeat("x", 1000), strings.Repeat("x", 769), "truncated"},
	} {
		message, ok := FromError(publicFailure(tc.text))
		if !ok || !Valid(message, 850) || strings.Contains(message, tc.absent) || tc.omission != "" && !strings.Contains(message, "omitted: "+tc.omission) {
			t.Fatal(message, ok)
		}
	}
}

func TestPublicSentinelsKeepDistinctIdentities(t *testing.T) {
	a, b := New("same description"), New("same description")
	if errors.Is(a, b) || !errors.Is(fmt.Errorf("private: %w", a), a) { t.Fatal("sentinel identity changed") }
	if message, ok := FromError(a); !ok || message != "same description" { t.Fatal(message, ok) }
}
