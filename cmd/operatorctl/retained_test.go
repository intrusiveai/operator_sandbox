//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
)

func TestRetainedCommandArgumentsAndSafeDiagnostics(t *testing.T) {
	for _, args := range [][]string{{"report"}, {"export", "--campaign", "id"}, {"report", "--campaign", "id", "--run", "/run"}, {"campaign", "evidence", "import", "--campaign", "id"}, {"campaign", "evidence", "import", "--campaign", "id", "--session", "s", "--archive", "a", "--max-archive-bytes", "-1"}, {"report", "--campaign", "id", "--state-root", "relative"}} {
		var out, err bytes.Buffer
		if code := runWithDefaults(context.Background(), args, &out, &err, hostconfig.Paths{}); code != 2 {
			t.Fatal(args, code, err.String())
		}
	}
	root := t.TempDir()
	var out, err bytes.Buffer
	code := runWithDefaults(context.Background(), []string{"report", "--campaign", "missing", "--state-root", root}, &out, &err, hostconfig.Paths{})
	if code != 1 || strings.Contains(err.String(), root) || out.Len() != 0 {
		t.Fatal(code, out.String(), err.String())
	}
}
