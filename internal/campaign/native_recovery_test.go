//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestNativeRecoveryClaimAndAuditIntegrity(t *testing.T) {
	for _, damage := range []string{"none", "lost result", "lost intent", "changed step", "extra file"} {
		t.Run(damage, func(t *testing.T) {
			root, w := newWriter(t)
			if _, err := OpenNativeRecovery(root, "campaign-1"); !errors.Is(err, ErrActive) {
				t.Fatal(err)
			}
			w.Close()
			a, err := OpenNativeRecovery(root, "campaign-1")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = a.Inspect(context.Background(), nil); err != nil {
				t.Fatal(err)
			}
			fresh, _, err := a.Begin()
			if err != nil || !fresh {
				t.Fatal(err)
			}
			if err = a.Record("close-intent", []byte(`{"operation":"close"}`)); err != nil {
				t.Fatal(err)
			}
			if err = a.Finish(map[string]string{"state": "unconfirmed"}); err != nil {
				t.Fatal(err)
			}
			a.Close()
			dir := filepath.Join(root, "campaigns/campaign-1/native-recovery")
			switch damage {
			case "lost result":
				os.Remove(filepath.Join(dir, "result.json"))
			case "lost intent":
				os.Remove(filepath.Join(dir, "intent.json"))
			case "changed step":
				os.WriteFile(filepath.Join(dir, "step-001.json"), []byte(`{}`), 0600)
			case "extra file":
				os.WriteFile(filepath.Join(dir, "step-002.json.pending"), []byte(`{}`), 0600)
			}
			a, err = OpenNativeRecovery(root, "campaign-1")
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			fresh, result, err := a.Begin()
			if fresh {
				t.Fatal("reclaimed completed or uncertain cleanup")
			}
			if damage == "none" {
				if err != nil || len(result) == 0 {
					t.Fatal(err)
				}
			} else if damage == "lost result" {
				if err != nil || len(result) != 0 {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("damaged audit accepted")
			}
			if err = a.Record("retry", []byte(`{}`)); err == nil {
				t.Fatal("recorded without a fresh claim")
			}
		})
	}
}
func TestNativeRecoveryPublicationFailureClosesAudit(t *testing.T) {
	root, w := newWriter(t)
	w.Close()
	a, e := OpenNativeRecovery(root, "campaign-1")
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	if _, _, e = a.Begin(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "campaigns/campaign-1/native-recovery/step-001.json.pending")
	if e = os.WriteFile(path, []byte("partial"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = a.Record("close-intent", []byte(`{}`)); e == nil {
		t.Fatal("partial record replaced")
	}
	os.Remove(path)
	if e = a.Record("retry", []byte(`{}`)); e == nil {
		t.Fatal("audit failure recovered dispatch permission")
	}
}

func TestNativeRecoveryReservesAuditAndOriginalFreeSpaceFloor(t *testing.T) {
	root, w := newWriter(t)
	if err := w.ConfigureFreeSpace(1024); err != nil {
		t.Fatal(err)
	}
	w.Close()
	a, err := OpenNativeRecovery(root, "campaign-1")
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if _, err = a.Inspect(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	a.available = func(*os.Root) (int64, error) { return nativeRecoveryBudget + 1023, nil }
	if fresh, _, err := a.Begin(); fresh || !errors.Is(err, ErrQuota) {
		t.Fatal("lost free-space floor", err)
	}
	a.available = func(*os.Root) (int64, error) { return nativeRecoveryBudget + 1024, nil }
	if fresh, _, err := a.Begin(); !fresh || err != nil {
		t.Fatal(err)
	}
	a.available = func(*os.Root) (int64, error) { return 0, nil }
	if err = a.Record("close-intent", []byte(`{}`)); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	a.available = func(*os.Root) (int64, error) { return nativeRecoveryBudget + 1024, nil }
	if err = a.Record("retry", []byte(`{}`)); err == nil {
		t.Fatal("reopened dispatch after storage failure")
	}
}
