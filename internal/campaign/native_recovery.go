//go:build linux || darwin

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// NativeRecovery holds a dead campaign or early attachment writer lock for one
// cleanup pass. Campaign handles expose retained reads; both forms expose a
// separate bounded audit, never an execution writer.
type NativeRecovery struct {
	retention         *RetentionLease
	root              *os.Root
	lock              *os.File
	manifest          RunManifest
	digest            string
	attachmentDigest  string
	evidenceClaim     string
	evidenceIdentity  string
	evidenceDirectory string
	started, failed   bool
	sequence          int
	records           []string
	minimumFreeBytes  int64
	usedBytes         int64
	available         func(*os.Root) (int64, error)
}

const nativeRecoveryBudget = 140*(512<<10) + 2*ManifestLimit

type nativeRecoveryResult struct {
	Result  json.RawMessage `json:"result"`
	Digest  string          `json:"digest"`
	Records []string        `json:"records"`
}

func OpenNativeRecovery(stateRoot, id string) (*NativeRecovery, error) {
	lease, err := AcquireRetentionLease(stateRoot, false)
	if err != nil {
		return nil, err
	}
	kept := false
	defer func() {
		if !kept {
			lease.Close()
		}
	}()
	r, err := openCampaign(stateRoot, id)
	if err != nil {
		return nil, err
	}
	l, err := lock(r, false)
	if err != nil {
		r.Close()
		return nil, err
	}
	m, d, err := loadManifest(r, id)
	if err != nil {
		l.Close()
		r.Close()
		return nil, err
	}
	kept = true
	return &NativeRecovery{retention: lease, root: r, lock: l, manifest: m, digest: d, available: filesystemAvailable}, nil
}
func (r *NativeRecovery) Close() error {
	return errors.Join(r.lock.Close(), r.root.Close(), r.retention.Close())
}
func (r *NativeRecovery) Inspect(ctx context.Context, visit func(Event) error) (Inspection, error) {
	return inspectRoot(ctx, r.root, r.manifest.CampaignID, func(e Event) error {
		if e.Kind == "journal.storage-policy" {
			var policy struct {
				Minimum int64 `json:"minimum_free_bytes"`
			}
			if decode(e.Metadata, &policy, MaxMetadataBytes) != nil || policy.Minimum < 0 || policy.Minimum > contracts.MaxSafeInteger {
				return ErrCorrupt
			}
			r.minimumFreeBytes = policy.Minimum
		}
		if visit != nil {
			return visit(e)
		}
		return nil
	}, false)
}
func (r *NativeRecovery) ReadContent(d ContentDescriptor) ([]byte, error) {
	return readRetainedContent(r.root, d)
}

// RecoveryResult verifies an existing cleanup audit without claiming work.
func (r *NativeRecovery) RecoveryResult() ([]byte, error) {
	if e := privateDir(r.root, "native-recovery"); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil, nil
		}
		return nil, e
	}
	if _, e := readFile(r.root, "native-recovery/intent.json", ManifestLimit); e != nil {
		return nil, ErrCorrupt
	}
	fresh, raw, e := r.Begin()
	if fresh {
		return nil, ErrCorrupt
	}
	return raw, e
}

// Begin never reclaims an existing intent. A lost process/reply remains unknown;
// a later invocation can read the result but cannot dispatch a second cleanup pass.
func (r *NativeRecovery) Begin() (fresh bool, result []byte, err error) {
	if r.started {
		return false, nil, ErrActive
	}
	if err = mkdir(r.root, "native-recovery"); err != nil {
		return false, nil, err
	}
	intent, _ := encode(map[string]string{"campaign_id": r.manifest.CampaignID, "run_manifest_digest": r.digest}, ManifestLimit)
	if r.attachmentDigest != "" {
		intent, _ = encode(map[string]string{"campaign_id": r.manifest.CampaignID, "attachment_digest": r.attachmentDigest}, ManifestLimit)
	}
	old, e := readFile(r.root, "native-recovery/intent.json", ManifestLimit)
	if e == nil {
		if string(old) != string(intent) {
			return false, nil, ErrCorrupt
		}
		raw, e := readFile(r.root, "native-recovery/result.json", ManifestLimit)
		if errors.Is(e, os.ErrNotExist) {
			return false, nil, nil
		}
		if e != nil {
			return false, nil, e
		}
		var saved nativeRecoveryResult
		if decode(raw, &saved, ManifestLimit) != nil || len(saved.Records) > 140 || saved.Records == nil {
			return false, nil, ErrCorrupt
		}
		canonical, e := contracts.Canonicalize(saved.Result, ManifestLimit)
		if e != nil || contracts.RawDigest(canonical) != saved.Digest {
			return false, nil, ErrCorrupt
		}
		names, e := directoryNames(r.root, "native-recovery")
		if e != nil || len(names) != len(saved.Records)+2 {
			return false, nil, ErrCorrupt
		}
		for i, digest := range saved.Records {
			raw, e := readFile(r.root, fmt.Sprintf("native-recovery/step-%03d.json", i+1), 512<<10)
			if e != nil || contracts.RawDigest(raw) != digest {
				return false, nil, ErrCorrupt
			}
		}
		return false, canonical, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return false, nil, e
	}
	// A missing whole intent cannot turn retained/partial recovery state into a
	// fresh grant. Only an empty namespace can begin the automatic pass.
	names, e := directoryNames(r.root, "native-recovery")
	if e != nil || len(names) != 0 {
		return false, nil, ErrCorrupt
	}
	if err = r.checkSpace(); err != nil {
		return false, nil, err
	}
	h := diskHooks()
	if err = publish(r.root, "native-recovery/intent.json", intent, false, &h); err != nil {
		return false, nil, err
	}
	r.started = true
	r.usedBytes = int64(len(intent))
	r.records = []string{}
	return true, nil, nil
}

// Record admits at most 140 records of 512 KiB, including 64 deletion pairs.
// Publication failure permanently prohibits further dispatch through this handle.
func (r *NativeRecovery) Record(kind string, payload []byte) error {
	if !r.started || r.failed {
		return ErrInvalid
	}
	if r.sequence >= 140 || !validID(kind) {
		r.failed = true
		return ErrInvalid
	}
	if err := r.checkSpace(); err != nil {
		r.failed = true
		return err
	}
	if _, err := contracts.Decode(payload, 256<<10); err != nil {
		r.failed = true
		return ErrInvalid
	}
	raw, err := encode(map[string]any{"sequence": r.sequence + 1, "kind": kind, "recorded_at": time.Now().UTC().Format(time.RFC3339Nano), "payload": json.RawMessage(payload)}, 512<<10)
	if err == nil {
		h := diskHooks()
		err = publish(r.root, fmt.Sprintf("native-recovery/step-%03d.json", r.sequence+1), raw, false, &h)
	}
	if err != nil {
		r.failed = true
		return err
	}
	r.sequence++
	r.usedBytes += int64(len(raw))
	r.records = append(r.records, contracts.RawDigest(raw))
	return nil
}

// Logical reservation and a fresh space probe protect the remaining bounded audit
// and the original free-space floor. Other host writers may still consume space.
func (r *NativeRecovery) checkSpace() error {
	available, err := r.available(r.root)
	if err != nil {
		return err
	}
	if available < r.minimumFreeBytes || nativeRecoveryBudget-r.usedBytes > available-r.minimumFreeBytes {
		return ErrQuota
	}
	return nil
}
func (r *NativeRecovery) Finish(result any) error {
	if !r.started {
		return ErrInvalid
	}
	raw, err := encode(result, ManifestLimit)
	if err != nil {
		return err
	}
	raw, err = encode(nativeRecoveryResult{raw, contracts.RawDigest(raw), r.records}, ManifestLimit)
	if err != nil {
		return err
	}
	h := diskHooks()
	err = publish(r.root, "native-recovery/result.json", raw, false, &h)
	r.started = false
	return err
}
