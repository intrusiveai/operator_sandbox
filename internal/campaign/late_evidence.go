//go:build linux || darwin

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type EvidenceResult struct {
	Outcome EvidenceOutcome `json:"outcome"`
	Archive *NativeEvidence `json:"archive,omitempty"`
}
type evidenceResultEnvelope struct {
	RecordedAt      string          `json:"recorded_at"`
	SelectionDigest string          `json:"selection_digest"`
	Result          json.RawMessage `json:"result"`
	Digest          string          `json:"digest"`
}

// BeginEvidence reads the latest verified attempt, or claims a session's first
// late collection. Existing incomplete attempts never become fresh claims.
func (r *NativeRecovery) BeginEvidence(session, selectionDigest string) (bool, *EvidenceResult, error) {
	return r.evidenceHistory(session, selectionDigest, true)
}

// ReadEvidence reads collection history without creating directories or claims.
func (r *NativeRecovery) ReadEvidence(session, selectionDigest string) (*EvidenceResult, error) {
	_, result, err := r.evidenceHistory(session, selectionDigest, false)
	return result, err
}
func (r *NativeRecovery) evidenceHistory(session, selectionDigest string, create bool) (bool, *EvidenceResult, error) {
	base := "evidence-recovery/" + session
	fresh, result, e := r.evidenceAt(session, selectionDigest, base, create)
	if e != nil || fresh {
		return fresh, result, e
	}
	names, e := r.evidenceRetries(session)
	if e != nil {
		return false, nil, e
	}
	for _, name := range names {
		// A retry directory may have been created immediately before process loss.
		// It is an interrupted attempt, not permission for this read to dispatch.
		if _, e = r.root.Lstat(base + "/retries/" + name + "/intent.json"); e != nil {
			return false, nil, ErrCorrupt
		}
		fresh, result, e = r.evidenceAt(session, selectionDigest, base+"/retries/"+name, false)
		if e != nil || fresh {
			return false, nil, ErrCorrupt
		}
	}
	return false, result, nil
}
func (r *NativeRecovery) evidenceRetries(session string) ([]string, error) {
	dir := "evidence-recovery/" + session + "/retries"
	if e := privateDir(r.root, dir); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return nil, nil
		}
		return nil, e
	}
	names, e := directoryNames(r.root, dir)
	if e != nil {
		return nil, e
	}
	if len(names) > 1000 {
		return nil, ErrQuota
	}
	for i, name := range names {
		if name != fmt.Sprintf("%04d", i+1) {
			return nil, ErrCorrupt
		}
		if e = privateDir(r.root, dir+"/"+name); e != nil {
			return nil, e
		}
	}
	return names, nil
}

// RetryEvidence grants one new, explicit read attempt after an interrupted or
// transient failure. Every earlier result and partial publication is preserved.
func (r *NativeRecovery) RetryEvidence(session, selectionDigest string) (bool, *EvidenceResult, error) {
	fresh, saved, e := r.BeginEvidence(session, selectionDigest)
	if e != nil || fresh {
		return fresh, saved, e
	}
	if saved != nil && !EvidenceRetryable(saved.Outcome) {
		return false, saved, nil
	}
	names, e := r.evidenceRetries(session)
	if e != nil {
		return false, nil, e
	}
	if len(names) >= 1000 {
		return false, nil, ErrQuota
	}
	base := "evidence-recovery/" + session + "/retries"
	if e = mkdir(r.root, base); e != nil {
		return false, nil, e
	}
	return r.beginEvidenceAt(session, selectionDigest, fmt.Sprintf("%s/%04d", base, len(names)+1))
}
func EvidenceRetryable(out EvidenceOutcome) bool {
	if out.ArchivePath != "" || out.State == "complete" || out.State == "partial" {
		return false
	}
	switch out.Reason {
	case "evidence_limit_exceeded", "archive_limit_exceeded", "native_limit_unavailable":
		return false
	}
	return true
}
func (r *NativeRecovery) beginEvidenceAt(session, selectionDigest, dir string) (bool, *EvidenceResult, error) {
	return r.evidenceAt(session, selectionDigest, dir, true)
}
func (r *NativeRecovery) evidenceAt(session, selectionDigest, dir string, create bool) (bool, *EvidenceResult, error) {
	if r.attachmentDigest != "" || r.evidenceClaim != "" || !validID(session) || !validDigest(selectionDigest) {
		return false, nil, ErrInvalid
	}
	for _, d := range []string{"evidence-recovery", "evidence-recovery/" + session, dir} {
		if !create {
			if e := privateDir(r.root, d); e != nil {
				if errors.Is(e, os.ErrNotExist) {
					return false, nil, nil
				}
				return false, nil, e
			}
			continue
		}
		if e := mkdir(r.root, d); e != nil {
			return false, nil, e
		}
	}
	intent, _ := encode(map[string]string{"campaign_id": r.manifest.CampaignID, "run_manifest_digest": r.digest, "session_id": session, "selection_digest": selectionDigest, "audit_path": dir}, ManifestLimit)
	old, e := readFile(r.root, dir+"/intent.json", ManifestLimit)
	if e == nil {
		if string(old) != string(intent) {
			return false, nil, ErrCorrupt
		}
		raw, e := readFile(r.root, dir+"/result.json", MaxContentBytes)
		if errors.Is(e, os.ErrNotExist) {
			return false, nil, nil
		}
		if e != nil {
			return false, nil, e
		}
		names, e := directoryNames(r.root, dir)
		if e != nil {
			return false, nil, e
		}
		count := 0
		for _, name := range names {
			if name == "intent.json" || name == "result.json" {
				count++
				continue
			}
			if name == "retries" && dir == "evidence-recovery/"+session {
				continue
			}
			return false, nil, ErrCorrupt
		}
		if count != 2 {
			return false, nil, ErrCorrupt
		}
		var envelope evidenceResultEnvelope
		if decode(raw, &envelope, MaxContentBytes) != nil || envelope.SelectionDigest != selectionDigest {
			return false, nil, ErrCorrupt
		}
		if when, e := time.Parse(time.RFC3339Nano, envelope.RecordedAt); e != nil || when.IsZero() {
			return false, nil, ErrCorrupt
		}
		canonical, e := contracts.Canonicalize(envelope.Result, MaxContentBytes)
		if e != nil || contracts.RawDigest(canonical) != envelope.Digest {
			return false, nil, ErrCorrupt
		}
		var result EvidenceResult
		if decode(canonical, &result, MaxContentBytes) != nil || result.Outcome.SessionID != session || !result.Outcome.Recorded {
			return false, nil, ErrCorrupt
		}
		return false, &result, nil
	}
	if !errors.Is(e, os.ErrNotExist) {
		return false, nil, e
	}
	names, e := directoryNames(r.root, dir)
	if e != nil || len(names) != 0 {
		return false, nil, ErrCorrupt
	}
	if !create {
		return false, nil, nil
	}
	if e = r.evidenceSpace(2 * MaxContentBytes); e != nil {
		return false, nil, e
	}
	h := diskHooks()
	if e = publish(r.root, dir+"/intent.json", intent, false, &h); e != nil {
		return false, nil, e
	}
	r.evidenceClaim = session
	r.evidenceIdentity = selectionDigest
	r.evidenceDirectory = dir
	return true, nil, nil
}
func (r *NativeRecovery) FinishEvidence(result EvidenceResult) error {
	if r.evidenceClaim == "" || result.Outcome.SessionID != r.evidenceClaim || !result.Outcome.Recorded {
		return ErrInvalid
	}
	dir := r.evidenceDirectory
	r.evidenceClaim = ""
	raw, e := encode(result, MaxContentBytes)
	if e != nil {
		return e
	}
	raw, e = encode(evidenceResultEnvelope{time.Now().UTC().Format(time.RFC3339Nano), r.evidenceIdentity, raw, contracts.RawDigest(raw)}, MaxContentBytes)
	if e != nil {
		return e
	}
	h := diskHooks()
	return publish(r.root, dir+"/result.json", raw, false, &h)
}
func (r *NativeRecovery) evidenceSpace(bytes int64) error {
	free, e := r.available(r.root)
	if e != nil {
		return e
	}
	if bytes < 0 || free < r.minimumFreeBytes || bytes > free-r.minimumFreeBytes {
		return ErrQuota
	}
	return nil
}

// EvidenceDirectory reserves logical space for a temporary download, retained
// copy and result metadata. Other host processes can still consume free storage.
func (r *NativeRecovery) EvidenceDirectory(maximum int64) (string, error) {
	if r.evidenceClaim == "" || maximum <= 0 || maximum > contracts.MaxSafeInteger {
		return "", ErrInvalid
	}
	if e := r.evidenceSpace(2*maximum + 2*MaxContentBytes); e != nil {
		return "", e
	}
	if e := mkdir(r.root, "native-evidence"); e != nil {
		return "", e
	}
	return filepath.Join(r.root.Name(), "native-evidence"), nil
}
func (r *NativeRecovery) RetainEvidence(ctx context.Context, v *interceptor.VerifiedEvidence) (NativeEvidence, error) {
	if r.evidenceClaim == "" || v == nil || v.Receipt().Identity.SessionID != r.evidenceClaim {
		return NativeEvidence{}, ErrInvalid
	}
	p := v.Receipt()
	// A verified redownload can identify an unadopted but complete old archive.
	// Existing bytes are rehashed and never overwritten.
	record := NativeEvidence{Path: filepath.ToSlash(filepath.Join("native-evidence", p.Identity.SessionID, p.Transfer.SHA256[7:]+".tar")), Provenance: p}
	if _, e := r.root.Lstat(record.Path); e == nil {
		if e = verifyRetainedEvidence(ctx, r.root, r.manifest.CampaignID, record); e != nil {
			return NativeEvidence{}, e
		}
		return record, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return NativeEvidence{}, e
	}
	return retainEvidence(ctx, r.root, r.manifest.CampaignID, v, r.minimumFreeBytes+2*MaxContentBytes, diskHooks())
}
func (r *NativeRecovery) VerifyRetainedEvidence(ctx context.Context, record NativeEvidence) error {
	return verifyRetainedEvidence(ctx, r.root, r.manifest.CampaignID, record)
}
