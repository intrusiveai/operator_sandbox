//go:build linux || darwin

package dockercontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerFailureDetailsAreBoundedAndSafe(t *testing.T) {
	for _, item := range []struct{ stderr, want string }{
		{"No such image: https://secret:credential@host/token", "image reference is not available"},
		{"unknown flag: --platform SECRET", "does not support"},
		{"Cannot connect to the Docker daemon secret", "daemon is unavailable"},
		{"permission denied secret", "access denied"},
		{strings.Repeat("SECRET", 10000), "Docker command failed"},
	} {
		dir := t.TempDir()
		script := filepath.Join(dir, "docker")
		// Fixture writes a private response to stderr; only allowlisted categories survive.
		data := filepath.Join(dir, "stderr")
		if err := os.WriteFile(data, []byte(item.stderr), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(script, []byte("#!/bin/sh\ncat '"+data+"' >&2\nexit 7\n"), 0700); err != nil {
			t.Fatal(err)
		}
		_, err := dockerCommand(script, outputLimit)(context.Background(), "unix:///fixture/socket", "image", "inspect")
		wrapped := dockerFailure(context.Background(), "selected image inspection", ErrImage, err)
		message, ok := FailureDetail(wrapped)
		if !ok || !strings.Contains(message, item.want) || !strings.Contains(message, "exit 7") || len(message) > 512 || strings.Contains(message, "SECRET") || strings.Contains(message, "credential") || strings.Contains(message, "https://") {
			t.Fatal(message)
		}
		if !errors.Is(wrapped, ErrImage) {
			t.Fatal("classification lost")
		}
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	wrapped := dockerFailure(ctx, "pinned image reference inspection", ErrImage, errors.New("unsafe arbitrary error"))
	message, _ := FailureDetail(wrapped)
	if !errors.Is(wrapped, context.DeadlineExceeded) || !strings.Contains(message, "timed out") {
		t.Fatal(message)
	}
	wrapped = dockerFailure(context.Background(), "pinned image reference inspection", ErrImage, context.DeadlineExceeded)
	message, _ = FailureDetail(wrapped)
	if !strings.Contains(message, "timed out") {
		t.Fatal(message)
	}
}
