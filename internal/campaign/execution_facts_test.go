package campaign

import (
	"fmt"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

func TestCleanupCandidatesRequireConfirmedArmAndBoundInventory(t *testing.T) {
	_, w, a, steps := nativeWriter(t)
	for i := 0; i < 66; i++ {
		id := fmt.Sprintf("arm-%03d", i)
		p, err := interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: id, OperationID: id, Operation: "injection.arm", CampaignID: "campaign-1", SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 3, AttemptID: "attempt-parent", Deadline: time.Now().Add(time.Minute)}, []byte(fmt.Sprintf(`{"definition":{"id":"injection-%03d"}}`, i)))
		if err != nil {
			t.Fatal(err)
		}
		step, _, err := steps.Begin("parent", p)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = steps.MarkDispatched(step.ID); err != nil {
			t.Fatal(err)
		}
		outcome := "succeeded"
		if i == 65 {
			outcome = "unknown"
		}
		if err = steps.Resolve(step.ID, outcome, nativeOK); err != nil {
			t.Fatal(err)
		}
	}
	if w.Fence().Err() == nil {
		t.Fatal("unknown did not close execution")
	}
	ids, total, err := a.CleanupCandidates(64)
	if err != nil || total != 65 || len(ids) != 64 || ids[0] != "injection-000" || ids[63] != "injection-063" {
		t.Fatal(ids, total, err)
	}
}
