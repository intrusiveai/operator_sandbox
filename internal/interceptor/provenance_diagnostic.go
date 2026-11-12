//go:build linux || darwin

package interceptor

import (
	"fmt"

	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// The verifier owns these facts. Field names and archive member names are data,
// never record values; common redaction/bounds apply before public rendering.
// Wrapping retains the original error and ErrProvenance classification.
type provenanceError struct {
	location, detail string
	cause            error
}

func (e *provenanceError) Error() string        { return e.DiagnosticMessage() }
func (e *provenanceError) Unwrap() error        { return e.cause }
func (e *provenanceError) Is(target error) bool { return target == ErrProvenance }
func (e *provenanceError) DiagnosticMessage() string {
	message := e.detail
	if e.location != "" {
		message = e.location + ": " + message
	}
	text, omission := diagnostictext.Safe(message, 512)
	if omission != "" {
		text += " [omitted: " + omission + "]"
	}
	return text
}
func evidenceRule(field, expectation string) error {
	return &provenanceError{location: evidenceLabel(field), detail: expectation, cause: ErrProvenance}
}
func evidenceLabel(value string) string {
	text, omission := diagnostictext.Safe(value, 128)
	if omission != "" {
		text += " [omitted: " + omission + "]"
	}
	return fmt.Sprintf("%q", text)
}
func evidenceLocation(file string, record int, err error) error {
	location := evidenceLabel(file)
	if record > 0 {
		location += fmt.Sprintf(" record %d", record)
	}
	detail, ok := diagnostictext.FromError(err)
	if !ok {
		detail = "evidence could not be read; underlying detail is private"
	}
	return &provenanceError{location: location, detail: detail, cause: err}
}
