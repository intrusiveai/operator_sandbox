package contracts

// publicError exposes a fixed contract-rule description without leaking decoder
// or validator implementation errors. Keep this package self-contained for
// distribution; the host applies its diagnostic redaction and output bounds.
type publicError struct{ message string }

func newPublicError(message string) error        { return &publicError{message} }
func (e *publicError) Error() string             { return e.message }
func (e *publicError) DiagnosticMessage() string { return e.message }
