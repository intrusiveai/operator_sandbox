//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"testing"
)

func TestReleaseAndInstallRejectMissingSelections(t *testing.T) {
	for _, args := range [][]string{{"release", "check"}, {"release", "archive"}, {"install"}, {"install", "provision-linux", "--unexpected"}, {"release", "check", "--directory", "/a", "--archive", "/b"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 {
			t.Fatalf("%v: %d %s", args, code, stderr.String())
		}
	}
}
