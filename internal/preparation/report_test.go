package preparation_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/nativerecovery"
	"github.com/intrusiveai/operator_sandbox/internal/reporting"
)

func reportResult(t *testing.T, root string, receipt reporting.Receipt) reporting.Result {
	t.Helper()
	var result reporting.Result
	name := filepath.Join(root, "campaigns", receipt.CampaignID, "reports", receipt.GenerationDigest[7:], "result-manifest.json")
	raw := read(t, name)
	if contracts.RawDigest(raw) != receipt.ResultDigest {
		t.Fatal("result digest")
	}
	if e := json.Unmarshal(raw, &result); e != nil {
		t.Fatal(e)
	}
	return result
}

func TestReportUsesHostAttemptsAndScopedFeedback(t *testing.T) {
	s, p, _, w, launch := serviceFixture(t)
	if e := s.Admit(context.Background(), launch); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Handle(context.Background(), attemptWire(p, 1, 1, w.Manifest().ReleaseRecordDigest), 0); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, e := s.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	dir, e := w.EvidenceDirectory()
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(dir)))
	w.Close()
	receipt, e := reporting.Generate(context.Background(), root, "campaign-1", "")
	if e != nil {
		t.Fatal(e)
	}
	r := reportResult(t, root, receipt)
	if len(r.Attempts) != 1 {
		t.Fatal(r)
	}
	a := r.Attempts[0]
	if a.ScenarioID != "scenario-marker" || len(a.ObjectiveIDs) == 0 || !a.Dispatched || a.Invocation != "succeeded" || a.Feedback == nil {
		t.Fatal(a)
	}
	if len(r.UntestedScenarios) != 0 || len(r.UntestedObjectives) != 0 || len(r.UntestedOperations) != 0 || r.OracleVerdict != "unknown" {
		t.Fatal(r)
	}
	if r.TargetClosure != "confirmed" {
		t.Fatal(r)
	}
}
func TestReportsAreImmutableAndExportIsSelfContained(t *testing.T) {
	root, w, p, _, target := lateFixture(t)
	if _, e := reporting.Generate(context.Background(), root, "campaign-1", ""); e == nil {
		t.Fatal("active writer accepted")
	}
	w.Close()
	before := read(t, filepath.Join(root, "campaigns/campaign-1/journal-head.json"))
	first, e := reporting.Generate(context.Background(), root, "campaign-1", "")
	if e != nil {
		t.Fatal(e)
	}
	second, e := reporting.Generate(context.Background(), root, "campaign-1", "")
	if e != nil || first != second {
		t.Fatal("unstable report", first, second, e)
	}
	result := reportResult(t, root, first)
	if !result.JournalIntact || result.Execution != "unknown" || result.OracleVerdict != "unknown" || result.Coverage != "recorded-attempts-only" {
		t.Fatal(result)
	}
	if _, e = os.Stat(filepath.Join(root, "campaigns/campaign-1/evidence-recovery")); !os.IsNotExist(e) {
		t.Fatal("report created collection intent", e)
	}
	archive := filepath.Join(t.TempDir(), "archive.tar")
	if e = os.WriteFile(archive, p.export.data, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = nativerecovery.ImportEvidence(context.Background(), root, "campaign-1", target.Identity.SessionID, archive, 0); e != nil {
		t.Fatal(e)
	}
	output := filepath.Join(t.TempDir(), "export")
	imported, e := reporting.Generate(context.Background(), root, "campaign-1", output)
	if e != nil {
		t.Fatal(e)
	}
	if imported.GenerationDigest == first.GenerationDigest {
		t.Fatal("new evidence did not change generation")
	}
	reportResult(t, root, first)
	var manifest struct {
		Files []campaign.DerivedFile `json:"files"`
	}
	if e = json.Unmarshal(read(t, filepath.Join(output, "export.json")), &manifest); e != nil {
		t.Fatal(e)
	}
	native := false
	for _, entry := range manifest.Files {
		raw := read(t, filepath.Join(output, entry.Path))
		if int64(len(raw)) != entry.Bytes || contracts.RawDigest(raw) != entry.Digest {
			t.Fatal("bad export", entry)
		}
		if strings.HasPrefix(entry.Path, "evidence/") {
			native = true
		}
		if strings.Contains(string(raw), root) || strings.Contains(string(raw), "/fixture/docker.sock") {
			t.Fatal("private host path exported", entry.Path)
		}
	}
	if !native {
		t.Fatal("no archive copied")
	}
	if _, e = reporting.Generate(context.Background(), root, "campaign-1", output); e == nil {
		t.Fatal("existing output overwritten")
	}
	if string(before) != string(read(t, filepath.Join(root, "campaigns/campaign-1/journal-head.json"))) {
		t.Fatal("journal changed")
	}
}

func TestReportsExposeIncompleteJournalAndCorruptEvidence(t *testing.T) {
	for _, mode := range []string{"journal", "archive", "report", "cancelled", "private-export"} {
		t.Run(mode, func(t *testing.T) {
			root, w, p, _, target := lateFixture(t)
			w.Close()
			archive := filepath.Join(t.TempDir(), "archive.tar")
			os.WriteFile(archive, p.export.data, 0600)
			imported, e := nativerecovery.ImportEvidence(context.Background(), root, "campaign-1", target.Identity.SessionID, archive, 0)
			if e != nil {
				t.Fatal(e)
			}
			first, e := reporting.Generate(context.Background(), root, "campaign-1", "")
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := ""
			switch mode {
			case "journal":
				os.WriteFile(filepath.Join(root, "campaigns/campaign-1/journal-head.json"), []byte("bad"), 0600)
			case "archive":
				os.WriteFile(filepath.Join(root, "campaigns/campaign-1", imported.Archive.Path), []byte("bad"), 0600)
			case "report":
				os.WriteFile(filepath.Join(root, "campaigns/campaign-1/reports", first.GenerationDigest[7:], "report.md"), []byte("bad"), 0600)
			case "cancelled":
				cancel()
			case "private-export":
				output = filepath.Join(root, "export")
			}
			next, e := reporting.Generate(ctx, root, "campaign-1", output)
			if mode == "report" || mode == "cancelled" || mode == "private-export" {
				if e == nil {
					t.Fatal("accepted", mode)
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			result := reportResult(t, root, next)
			if mode == "journal" && result.JournalIntact {
				t.Fatal("claimed intact journal")
			}
			if len(result.Gaps) == 0 {
				t.Fatal("missing evidence gap")
			}
		})
	}
}
