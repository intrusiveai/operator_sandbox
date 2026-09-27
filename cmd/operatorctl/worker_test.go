//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

func TestWorkerCommandRetainsFailureAndNeverRetriesClaim(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	request := startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: "/nonexistent/private-worker-profile.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/socket", InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: hostrun.NewSelection("/submitted/run")}
	saved, err := startrequest.Save(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"_worker", "--state-root", root, "--request-id", request.Selection.StartRequestID, "--request-digest", saved.Digest}
	for _, code := range []string{"preparation_failed", "claim_rejected"} {
		var out, stderr bytes.Buffer
		if status := run(context.Background(), args, &out, &stderr); status != 1 {
			t.Fatal(status, out.String(), stderr.String())
		}
		var result workerjob.Result
		if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Code != code {
			t.Fatal(result, err)
		}
		if strings.Contains(out.String()+stderr.String(), "private-worker-profile") {
			t.Fatal("private source leaked into diagnostics")
		}
	}
	snapshot, err := startrequest.Read(root, request.Selection.StartRequestID)
	if err != nil || snapshot.Phase() != "failed" || snapshot.Accepted != nil {
		t.Fatal(snapshot, err)
	}
}
func TestWorkerCommandRejectsUnboundRequests(t *testing.T) {
	for _, args := range [][]string{{"_worker"}, {"_worker", "--state-root", "relative", "--request-id", strings.Repeat("a", 32), "--request-digest", "sha256:" + strings.Repeat("b", 64)}, {"_worker", "--command", "anything"}} {
		var out, stderr bytes.Buffer
		if code := run(context.Background(), args, &out, &stderr); code != 2 || out.Len() != 0 {
			t.Fatal(code, out.String())
		}
	}
	var out, stderr bytes.Buffer
	if code := run(context.Background(), []string{"version"}, &out, &stderr); code != 0 || !strings.Contains(out.String(), operatorVersion) {
		t.Fatal(code, out.String())
	}
}
