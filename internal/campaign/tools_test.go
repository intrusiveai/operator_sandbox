//go:build linux || darwin

package campaign

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

func readTool(id string) ToolInput {
	return ToolInput{CampaignID: "campaign-1", OperationID: id, Operation: "engine.observation_read", WorkerInstanceID: "worker-1", RunRevision: 3, Body: []byte(`{"receipt_id":"receipt-1","entry_id":"entry-1","offset":0,"max_bytes":100}`)}
}
func TestToolsShareNamespaceAndCumulativeLimits(t *testing.T) {
	_, w, a := attemptWriter(t, 0)
	tools := a.Tools()
	if _, _, err := tools.Observe(readTool("one")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Observe(AttemptInput{CampaignID: "campaign-1", WorkerInstanceID: "worker-1", RunRevision: 3, Body: []byte(`{"request_id":"one","attempt_id":"attempt-1","attempt_index":1}`)}); err == nil {
		t.Fatal("tool ID reused as attempt")
	}
	if err := tools.ReserveRead("one", 100); err != nil {
		t.Fatal(err)
	}
	target := a.Target()
	target.SessionID = "replacement"
	if err := a.Rebind(4, target); !errors.Is(err, ErrActive) {
		t.Fatal("abandoned pending tool", err)
	}
	if err := tools.Finish("one", []byte(`{"result":{"ok":true}}`), 20); err != nil {
		t.Fatal(err)
	}
	if got := tools.Usage(); got.Requests != 1 || got.Bytes != 20 || got.Reserved != 0 {
		t.Fatal(got)
	}
	if err := a.Rebind(4, target); err != nil {
		t.Fatal(err)
	}
	in := readTool("one")
	in.RunRevision = 4
	in.WorkerInstanceID = "new-worker"
	if _, replay, err := tools.Observe(in); err != nil || !replay {
		t.Fatal("attribution restricted replay", err)
	}
	in.Body = []byte(`{"entry_id":"different"}`)
	if _, _, err := tools.Observe(in); !errors.Is(err, ErrConflict) {
		t.Fatal("changed body accepted", err)
	}
	a.maximumSubmissions = 1
	in = readTool("two")
	in.RunRevision = 4
	if _, _, err := tools.Observe(in); !errors.Is(err, contracts.ErrLimit) {
		t.Fatal("tool count reset on restore", err)
	}
	if w.Fence().Err() != nil {
		t.Fatal("known quota denial closed journal")
	}
}
func TestReadSettlementFailureDoesNotRefundPendingDelivery(t *testing.T) {
	_, w, a := attemptWriter(t, 0)
	tools := a.Tools()
	if _, _, err := tools.Observe(readTool("one")); err != nil {
		t.Fatal(err)
	}
	if err := tools.ReserveRead("one", 100); err != nil {
		t.Fatal(err)
	}
	original := w.hooks.sync
	w.hooks.sync = func(f *os.File) error {
		if strings.HasSuffix(f.Name(), ".bin.pending") {
			return errors.New("full disk")
		}
		return original(f)
	}
	if err := tools.Finish("one", []byte(`{"result":{"ok":true}}`), 20); err == nil {
		t.Fatal("lost settlement succeeded")
	}
	if w.Fence().Err() == nil {
		t.Fatal("not fenced")
	}
	if got := tools.Usage(); got.Bytes != 0 || got.Reserved != 100 {
		t.Fatal("refunded uncertain delivery", got)
	}
	if saved, raw, err := tools.Lookup("one"); err != nil || saved.Result != nil || raw != nil {
		t.Fatal("uncommitted reply visible", err)
	}
}

func TestLargeToolBodiesKeepDistinctIdentitiesAndBoundedMetadata(t *testing.T) {
	_, w, a := attemptWriter(t, 0)
	in := readTool("large")
	in.Operation = "engine.artifact_put_part"
	in.Body = []byte(`{"content":"` + strings.Repeat("A", 350000) + `","offset":0,"upload_id":"upload-1"}`)
	saved, replay, err := a.Tools().Observe(in)
	if err != nil || replay || saved.BodyRef == nil || len(saved.Body) != 0 {
		t.Fatal(saved, err)
	}
	raw, err := w.ReadContent(*saved.BodyRef)
	if err != nil || string(raw) != string(in.Body) {
		t.Fatal("large body not durably retained", err)
	}
	if err := a.Tools().Finish(in.OperationID, []byte(`{"result":{"next_offset":1}}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, replay, err = a.Tools().Observe(in); err != nil || !replay {
		t.Fatal(err)
	}
	in.Body = []byte(`{"content":"` + strings.Repeat("B", 350000) + `","offset":0,"upload_id":"upload-1"}`)
	if _, _, err = a.Tools().Observe(in); !errors.Is(err, ErrConflict) {
		t.Fatal("oversized identity collapsed to same digest", err)
	}
}
