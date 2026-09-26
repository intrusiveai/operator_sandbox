package campaign

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func publicationFixture(t *testing.T, w *Writer) (*contracts.Catalog, AttemptCompletion, Publication, []byte) {
	t.Helper()
	catalog, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("x"), MaxContentBytes+17)
	now := time.Now().UTC()
	s := feedback.Source{CampaignID: w.manifest.CampaignID, SessionID: w.manifest.Target.SessionID, AttemptID: "attempt-publish", AttemptContextDigest: contracts.RawDigest([]byte("context")), TurnID: "turn", RunRevision: 3, SessionRevision: 4}
	policy, _ := feedback.New("black-box", "black-box", []string{"target_output"}, nil)
	v := interceptor.ObservationView{APIVersion: "interceptor.dev/observation-view/v1alpha2", ReceiptID: interceptor.FeedbackReceiptID(s.SessionID, s.TurnID), CampaignID: s.CampaignID, SessionID: s.SessionID, AttemptID: s.AttemptID, AttemptContextDigest: s.AttemptContextDigest, TurnID: s.TurnID, CapturedAt: now, WindowStart: now, WindowEnd: now, SessionRevision: s.SessionRevision, FeedbackProfile: "black-box", CollectionState: "complete", Operation: interceptor.OperationView{State: "SUCCEEDED", ReceiptID: s.TurnID}, Categories: []interceptor.FeedbackCategory{{Kind: "target_output", State: "available"}, {Kind: "operation_error", State: "withheld"}, {Kind: "injection_delivery", State: "withheld"}, {Kind: "oracle_outcome", State: "withheld"}}, Entries: []interceptor.FeedbackEntry{{ID: "entry", Kind: "target_output", Visibility: interceptor.TargetVisible, Source: "application", Assurance: "target-response", Availability: "available", Artifact: &interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(data), SizeBytes: int64(len(data)), MediaType: "text/plain", Canonicalization: "raw"}, OriginalSizeBytes: int64(len(data))}}, Observations: []interceptor.Observation{}}
	v.Hash = interceptor.ObservationViewDigest(v)
	raw, _ := json.Marshal(v)
	r, err := feedback.Project(catalog, policy, s, "receipt-publish", raw, map[string][]byte{"entry": data}, nil, int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	guest, _ := json.Marshal(map[string]any{"api_version": "operator.dev/engine-attempt-result/v1alpha2", "kind": "EngineAttemptResult", "request_id": "publish", "attempt_id": s.AttemptID, "receipt_id": "receipt-publish", "status": "completed", "stage": "complete", "target_contact": "attempted", "invocation_state": "succeeded", "cleanup_state": "not-needed", "retry_disposition": "do-not-retry", "errors": []any{}, "feedback": json.RawMessage(r.ManifestJSON())})
	return catalog, AttemptCompletion{Outcome: "succeeded", Result: guest}, Publication{Receipt: r}, data
}
func preparePublication(t *testing.T, a *Attempts, n int64) {
	t.Helper()
	observe(t, a, "publish", 6)
	admit(t, a, "publish")
	if err := a.ReservePublication("publish", n); err != nil {
		t.Fatal(err)
	}
	dispatch(t, a, "publish")
}

func TestPublicationAtomicAdoptionReplayRestoreAndDiskReads(t *testing.T) {
	root, w, a := attemptWriter(t, 5)
	c, result, p, data := publicationFixture(t, w)
	preparePublication(t, a, int64(len(data)))
	if _, err := a.FindPublication("receipt-publish"); err == nil {
		t.Fatal("unpublished receipt visible")
	}
	if err := a.Publish(c, "publish", result, p); err != nil {
		t.Fatal(err)
	}
	seq := w.head.Sequence
	if err := a.Publish(c, "publish", result, p); err != nil || w.head.Sequence != seq {
		t.Fatal("replay wrote again", err)
	}
	index, err := a.FindPublication("receipt-publish")
	if err != nil {
		t.Fatal(err)
	}
	record, err := feedback.OpenRecord(c, index.Feedback)
	if err != nil {
		t.Fatal(err)
	}
	entry := record.Entries()[0].ID
	read := func() []byte {
		raw, err := record.Read("campaign-1", "receipt-publish", entry, MaxContentBytes-3, 20, true, []string{"target_output"}, func(id string, _ feedback.Artifact) ([]byte, error) { return a.ReadPublicationObject(index, id) })
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var chunk feedback.ReadResult
	_ = json.Unmarshal(read(), &chunk)
	if !chunk.EOF || chunk.RawLength != 20 || !bytes.Equal(chunk.Content, data[MaxContentBytes-3:]) {
		t.Fatal("cross-part read failed")
	}
	target := w.manifest.Target
	target.SessionID = "replacement"
	target.WorkerInstanceID = "different-worker"
	if err = a.Rebind(4, target); err != nil {
		t.Fatal(err)
	}
	if _, err = a.FindPublication("receipt-publish"); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(read(), &chunk)
	if chunk.Availability != "available" || record.Source().SessionID == target.SessionID {
		t.Fatal("restore redirected feedback")
	}
	file := filepath.Join(root, "campaigns", "campaign-1", index.Objects[entry][0].Path)
	if err = os.WriteFile(file, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(read(), &chunk)
	if chunk.Availability != "unavailable" || chunk.EOF || chunk.RawLength != 0 {
		t.Fatal("corruption returned bytes")
	}
	if w.Fence().Err() != nil {
		t.Fatal("optional feedback loss closed execution")
	}
}

func TestPublicationFailuresNeverExposeStagedReceipt(t *testing.T) {
	for stage := int64(1); stage <= 3; stage++ {
		t.Run(fmt.Sprint(stage), func(t *testing.T) {
			_, w, a := attemptWriter(t, 5)
			c, result, p, data := publicationFixture(t, w)
			preparePublication(t, a, int64(len(data)))
			failName := fmt.Sprintf("event-%016d-00.bin.pending", w.head.Sequence+stage)
			original := w.hooks.sync
			w.hooks.sync = func(f *os.File) error {
				if strings.HasSuffix(f.Name(), failName) {
					return errors.New("publication storage failure")
				}
				return original(f)
			}
			if err := a.Publish(c, "publish", result, p); err == nil {
				t.Fatal("expected storage failure")
			}
			if w.Fence().Err() == nil {
				t.Fatal("failure did not close execution")
			}
			if _, err := a.FindPublication("receipt-publish"); err == nil {
				t.Fatal("staged receipt exposed")
			}
			raw, _, err := a.Lookup("publish")
			if err == nil && raw.Result != nil {
				t.Fatal("uncommitted result visible")
			}
		})
	}
}

func TestPublicationRecoveryLinksIndexOnlyFromFinalResult(t *testing.T) {
	root, w, a := attemptWriter(t, 5)
	c, result, p, data := publicationFixture(t, w)
	preparePublication(t, a, int64(len(data)))
	if err := a.Publish(c, "publish", result, p); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()
	found := false
	inspection, err := Inspect(root, "campaign-1", func(event Event) error {
		if event.Kind == "attempt.resolved" {
			var meta struct {
				Attempt SavedAttempt `json:"attempt"`
			}
			_ = json.Unmarshal(event.Metadata, &meta)
			if meta.Attempt.Publication == nil {
				return ErrCorrupt
			}
			found = true
		}
		return nil
	})
	if err != nil || !inspection.JournalIntact || !found || len(inspection.Reservations) != 0 {
		t.Fatal(inspection, err)
	}
}
