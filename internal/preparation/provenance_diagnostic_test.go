package preparation_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func TestLocalEvidenceRejectionSurvivesInspectionAndExport(t *testing.T) {
	p := &exportPeer{data: read(t, "../interceptor/testdata/native-evidence.tar")}
	// A known-good native export with an unsupported filesystem manifest version.
	// Retain the original evidence; only this local test copy is altered.
	r := tar.NewReader(bytes.NewReader(p.data))
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for {
		h, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		if h.Name == "file-manifest.json" {
			raw = []byte(`{"schema_version":99,"entries":[],"digest":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}`)
		}
		h.Size = int64(len(raw))
		if err = tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if _, err = tw.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	p.data = out.Bytes()
	s, _, _, w, launch := serviceWithSettings(t, 0, []string{"engine.observation_read"}, evidenceSettings(p, time.Second), nativeEvidenceInput)
	if err := s.Admit(context.Background(), launch); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	collected, err := s.Shutdown(ctx)
	if err != nil || len(collected.Evidence) != 1 {
		t.Fatal(collected, err)
	}
	result := collected.Evidence[0]
	dir, err := w.EvidenceDirectory()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	if result.State != "invalid" || result.Reason != "provenance_invalid" || !result.Recorded || result.Failure == nil || result.Failure.Component != "Operator" || result.Failure.Operation != "evidence.provenance-verification" || result.Failure.HTTPStatus != 0 {
		t.Fatalf("%+v", result)
	}
	if !strings.Contains(result.Failure.Message, "unsupported version 99") {
		t.Fatal(result.Failure)
	}
	binary := processCLI(t)
	for _, verb := range []string{"logs", "report", "export"} {
		args := []string{verb, "--state-root", root, "--campaign", "campaign-1"}
		if verb == "logs" {
			args = append([]string{"campaign"}, args...)
		}
		destination := filepath.Join(t.TempDir(), "export")
		if verb == "export" {
			args = append(args, "--output", destination)
		}
		data, err := exec.Command(binary, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s %v", verb, data, err)
		}
		if verb == "report" {
			var receipt reporting.Receipt
			if err := json.Unmarshal(data, &receipt); err != nil {
				t.Fatal(err)
			}
			result := reportResult(t, root, receipt)
			data, _ = json.Marshal(result)
		}
		if verb == "export" {
			data = append(read(t, filepath.Join(destination, "report.md")), read(t, filepath.Join(destination, "result-manifest.json"))...)
		}
		for _, want := range []string{"Operator", "evidence.provenance-verification", "file-manifest.json", "schema_version", "unsupported version 99; supported versions: 1, 2"} {
			if !bytes.Contains(data, []byte(want)) {
				t.Fatalf("%s lost %q: %s", verb, want, data)
			}
		}
	}
}
