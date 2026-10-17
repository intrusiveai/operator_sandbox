//go:build linux || darwin

package dockercontrol

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// commandFailure retains only bounded, allowlisted CLI facts, never stderr,
// command arguments, endpoints, credentials or arbitrary daemon response text.
type commandFailure struct {
	category string
	exit     int
	cause    error
}

func (e *commandFailure) Error() string { return e.category }
func (e *commandFailure) Unwrap() error { return e.cause }

func classifyCommand(ctx context.Context, err error, stderr []byte, overflow bool) error {
	if err == nil && !overflow {
		return nil
	}
	e := &commandFailure{category: "Docker command failed", exit: -1, cause: err}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		e.exit = exit.ExitCode()
	}
	text := strings.ToLower(string(stderr))
	switch {
	case ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled):
		if ctx.Err() != nil {
			e.cause = ctx.Err()
		}
		if errors.Is(e.cause, context.DeadlineExceeded) {
			e.category = "Docker command timed out"
		} else {
			e.category = "Docker command canceled"
		}
	case overflow:
		e.category = "Docker response exceeded its size limit"
	case strings.Contains(text, "no such image"):
		e.category = "image reference is not available in the local Docker store"
	case strings.Contains(text, "does not provide the specified platform"), strings.Contains(text, "does not match the specified platform"):
		e.category = "requested image platform is unavailable"
	case strings.Contains(text, "unknown flag: --platform"), strings.Contains(text, "requires api version"):
		e.category = "Docker does not support the required platform inspection"
	case strings.Contains(text, "cannot connect to the docker daemon"), strings.Contains(text, "is the docker daemon running"):
		e.category = "Docker daemon is unavailable"
	case strings.Contains(text, "permission denied"):
		e.category = "Docker access denied"
	}
	return e
}

type operationFailure struct {
	operation string
	failure   *commandFailure
	cause     error
}

func (e *operationFailure) Error() string {
	if e.failure.exit >= 0 {
		return fmt.Sprintf("%s: %s (exit %d)", e.operation, e.failure.category, e.failure.exit)
	}
	return e.operation + ": " + e.failure.category
}
func (e *operationFailure) Unwrap() error { return e.cause }
func dockerFailure(ctx context.Context, operation string, sentinel, err error) error {
	var f *commandFailure
	if !errors.As(err, &f) {
		f = classifyCommand(ctx, err, nil, false).(*commandFailure)
	}
	return &operationFailure{operation, f, errors.Join(sentinel, f)}
}
func isMissingImage(err error) bool {
	var e *commandFailure
	return errors.As(err, &e) && e.category == "image reference is not available in the local Docker store"
}

// FailureDetail exposes only the operation and sanitized facts generated above.
func FailureDetail(err error) (string, bool) {
	var e *operationFailure
	if !errors.As(err, &e) {
		return "", false
	}
	return e.Error(), true
}
