//go:build linux || darwin

package startrequest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

func TestRetirementExcludesWorkersAndPermanentlyRejectsDelayedClaims(t *testing.T) {
	ctx := context.Background()
	r := requestFixture(t)
	s, e := Save(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	owner, e := ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, s.Digest)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = campaign.AcquireRetentionLease(r.StateRoot, true); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("active worker not excluded", e)
	}
	owner.Close()
	lease, e := campaign.AcquireRetentionLease(r.StateRoot, true)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Save(ctx, r); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("save raced retirement", e)
	}
	if _, e = ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, s.Digest); !errors.Is(e, campaign.ErrActive) {
		t.Fatal("claim raced retirement", e)
	}
	retired, e := Retire(ctx, lease, r.StateRoot, r.Selection.StartRequestID, s.Digest)
	if e != nil || retired.Phase() != "retired" {
		t.Fatal(retired, e)
	}
	same, e := Retire(ctx, lease, r.StateRoot, r.Selection.StartRequestID, s.Digest)
	if e != nil || *same.Retired != *retired.Retired {
		t.Fatal("retry changed retirement", e)
	}
	lease.Close()
	if _, e = ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, s.Digest); !errors.Is(e, ErrRetired) {
		t.Fatal("delayed worker claimed", e)
	}
	saved, e := Save(ctx, r)
	if e != nil || saved.Phase() != "retired" {
		t.Fatal("save reopened work", e)
	}
}
func TestRetireUnclaimedRequestAndBindServiceScope(t *testing.T) {
	ctx := context.Background()
	r := requestFixture(t)
	s, e := Save(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	id := r.Selection.StartRequestID
	if e = RegisterService(ctx, r.StateRoot, id, s.Digest, "darwin", 501); e != nil {
		t.Fatal(e)
	}
	if e = RegisterService(ctx, r.StateRoot, id, s.Digest, "darwin", 502); !errors.Is(e, ErrConflict) {
		t.Fatal("scope changed", e)
	}
	if _, e = Retire(ctx, nil, r.StateRoot, id, s.Digest); !errors.Is(e, campaign.ErrActive) {
		t.Fatal(e)
	}
	lease, e := campaign.AcquireRetentionLease(r.StateRoot, true)
	if e != nil {
		t.Fatal(e)
	}
	retired, e := Retire(ctx, lease, r.StateRoot, id, s.Digest)
	if e != nil || retired.Claim != nil || retired.Service.UID != 501 {
		t.Fatal(retired, e)
	}
	lease.Close()
	if _, e = ClaimOnce(ctx, r.StateRoot, id, s.Digest); !errors.Is(e, ErrRetired) {
		t.Fatal("unclaimed retirement reopened", e)
	}
	if e = RegisterService(ctx, r.StateRoot, id, s.Digest, "darwin", 501); !errors.Is(e, ErrRetired) {
		t.Fatal(e)
	}
}

func TestInterruptedRetirementRemainsFencedAndCanBeCompleted(t *testing.T) {
	ctx := context.Background()
	r := requestFixture(t)
	s, e := Save(ctx, r)
	if e != nil {
		t.Fatal(e)
	}
	file := filepath.Join(r.StateRoot, "starts", r.Selection.StartRequestID, "retired.json.pending")
	if e = os.WriteFile(file, []byte("interrupted write"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e = ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, s.Digest); e == nil {
		t.Fatal("incomplete retirement allowed claim")
	}
	lease, e := campaign.AcquireRetentionLease(r.StateRoot, true)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := InventoryRetiring(r.StateRoot, nil, lease)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	if _, e = Retire(ctx, lease, r.StateRoot, r.Selection.StartRequestID, s.Digest); e != nil {
		t.Fatal(e)
	}
	lease.Close()
	if _, e = ClaimOnce(ctx, r.StateRoot, r.Selection.StartRequestID, s.Digest); !errors.Is(e, ErrRetired) {
		t.Fatal("retirement reopened", e)
	}
	if _, e = os.Lstat(file); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("pending fence retained", e)
	}
}
