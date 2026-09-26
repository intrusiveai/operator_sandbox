//go:build linux || darwin

package campaign

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type NativeEvidence struct {
	Path       string                        `json:"path"`
	Provenance interceptor.ProvenanceReceipt `json:"provenance"`
}

// EvidenceDirectory is private campaign-local staging, never a guest mount.
// The writer lock remains held for its lifetime, excluding future purge/readers.
func (w *Writer) EvidenceDirectory() (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return "", err
	}
	if err := mkdir(w.root, "native-evidence"); err != nil {
		return "", err
	}
	return filepath.Join(w.root.Name(), "native-evidence"), nil
}

// RetainEvidence streams verified native data outside the journal byte quota,
// rechecks transfer integrity, fsyncs a no-replace archive, then commits its
// adoption event. A file without that event is an orphan, never published evidence.
// The independent terminator does not acquire the writer mutex.
func (w *Writer) RetainEvidence(ctx context.Context, v *interceptor.VerifiedEvidence, reservation string) (NativeEvidence, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return NativeEvidence{}, err
	}
	if v == nil || ctx.Err() != nil {
		return NativeEvidence{}, ErrInvalid
	}
	p := v.Receipt()
	if !p.Identity.Valid() || p.Identity.CampaignID != w.manifest.CampaignID || p.Transfer.CampaignID != w.manifest.CampaignID || p.Transfer.SessionID != p.Identity.SessionID || p.Transfer.Bytes <= 0 || !validDigest(p.Transfer.SHA256) || !validDigest(p.BundleDigest) {
		return NativeEvidence{}, ErrInvalid
	}
	directory := "native-evidence/" + p.Identity.SessionID
	for _, d := range []string{"native-evidence", directory} {
		if err := mkdir(w.root, d); err != nil {
			return NativeEvidence{}, err
		}
	}
	name := directory + "/" + strings.TrimPrefix(p.Transfer.SHA256, "sha256:") + ".tar"
	if w.spaceConfigured {
		free, err := w.hooks.available(w.root)
		if err != nil || free < w.minimumFreeBytes || p.Transfer.Bytes > free-w.minimumFreeBytes {
			return NativeEvidence{}, ErrInvalid
		}
	}
	reader, err := v.Reader()
	if err != nil {
		return NativeEvidence{}, err
	}
	temp := name + ".pending"
	f, err := w.root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return NativeEvidence{}, err
	}
	defer func() { f.Close(); _ = w.root.Remove(temp) }()
	h := sha256.New()
	n, err := io.CopyBuffer(io.MultiWriter(f, h), io.LimitReader(evidenceContextReader{ctx, reader}, p.Transfer.Bytes+1), make([]byte, 64<<10))
	if err != nil || n != p.Transfer.Bytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != p.Transfer.SHA256 {
		return NativeEvidence{}, ErrCorrupt
	}
	if err = w.hooks.sync(f); err != nil {
		return NativeEvidence{}, err
	}
	if err = f.Close(); err != nil {
		return NativeEvidence{}, err
	}
	if err = ctx.Err(); err != nil {
		return NativeEvidence{}, err
	}
	if err = w.root.Link(temp, name); err != nil {
		return NativeEvidence{}, err
	}
	if err = w.hooks.syncDir(w.root, directory); err != nil {
		return NativeEvidence{}, err
	}
	record := NativeEvidence{Path: name, Provenance: p}
	raw, err := json.Marshal(record)
	if err != nil || len(raw) > MaxContentBytes {
		return NativeEvidence{}, ErrInvalid
	}
	metadata, _ := json.Marshal(map[string]string{"session_id": p.Identity.SessionID, "state": p.State})
	var change *reservationChange
	if reservation != "" {
		change = &reservationChange{ID: reservation, Action: "consume"}
	}
	_, err = w.appendLocked(Entry{RunRevision: w.revision, Kind: "evidence.adopted", Metadata: metadata, Content: []Content{{Role: "native-evidence", MediaType: "application/json", Bytes: raw}}}, change)
	if err != nil {
		return NativeEvidence{}, err
	}
	return record, nil
}

type evidenceContextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r evidenceContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// VerifyRetainedEvidence rechecks a descriptor obtained from a committed journal
// event. It streams host-only bytes, never grants execution or guest visibility.
// Callers must retain the campaign writer/administrative lock while using it.
func (w *Writer) VerifyRetainedEvidence(ctx context.Context, record NativeEvidence) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return err
	}
	p := record.Provenance
	if !p.Identity.Valid() || p.Identity.CampaignID != w.manifest.CampaignID || !validDigest(p.Transfer.SHA256) || p.Transfer.Bytes <= 0 || record.Path != path.Join("native-evidence", p.Identity.SessionID, strings.TrimPrefix(p.Transfer.SHA256, "sha256:")+".tar") {
		return ErrInvalid
	}
	for _, d := range []string{"native-evidence", path.Dir(record.Path)} {
		if err := privateDir(w.root, d); err != nil {
			return err
		}
	}
	f, err := openRegular(w.root, record.Path, os.O_RDONLY)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.Size() != p.Transfer.Bytes {
		return ErrCorrupt
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(evidenceContextReader{ctx, f}, p.Transfer.Bytes+1), make([]byte, 64<<10))
	if err != nil {
		return errors.Join(ErrCorrupt, err)
	}
	if n != p.Transfer.Bytes || "sha256:"+hex.EncodeToString(h.Sum(nil)) != p.Transfer.SHA256 {
		return ErrCorrupt
	}
	return nil
}
