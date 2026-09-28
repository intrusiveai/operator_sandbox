//go:build linux || darwin

package nativerecovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// ImportEvidence is offline. The administrator selects the local acceptance
// ceiling; the old API transfer ceiling is not an offline archive limit.
func ImportEvidence(ctx context.Context, root, id, session, filename string, maximum int64) (campaign.ImportedEvidence, error) {
	var out campaign.ImportedEvidence
	if maximum < 0 || maximum > contracts.MaxSafeInteger {
		return out, campaign.ErrInvalid
	}
	lease, e := campaign.AcquireHostLease(root)
	if e != nil {
		return out, e
	}
	defer lease.Close()
	a, e := campaign.OpenNativeRecovery(root, id)
	if e != nil {
		return out, e
	}
	defer a.Close()
	f, e := loadFacts(ctx, a)
	if e != nil {
		return out, e
	}
	if f.manifest.Target.Adapter == "https/v1" {
		return out, campaign.ErrInvalid
	}
	facts, e := loadEvidenceFacts(ctx, a, f)
	if e != nil {
		return out, e
	}
	var identity interceptor.EvidenceIdentity
	for _, target := range facts.targets {
		if target.Identity.SessionID == session {
			identity = target.Identity
		}
	}
	if !identity.Valid() {
		return out, campaign.ErrInvalid
	}
	if maximum == 0 {
		maximum = facts.policy.MaxArchiveBytes
	}
	info, e := os.Lstat(filename)
	if e != nil {
		return out, e
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maximum {
		return out, campaign.ErrInvalid
	}
	source, e := os.OpenFile(filename, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return out, e
	}
	defer source.Close()
	actual, e := source.Stat()
	if e != nil || !os.SameFile(info, actual) || !actual.Mode().IsRegular() {
		return out, campaign.ErrInvalid
	}
	h := sha256.New()
	n, e := io.Copy(h, io.LimitReader(importReader{ctx, source}, info.Size()+1))
	if e != nil {
		return out, e
	}
	if n != info.Size() {
		return out, campaign.ErrCorrupt
	}
	if _, e = source.Seek(0, io.SeekStart); e != nil {
		return out, e
	}
	directory, e := a.ImportDirectory(info.Size())
	if e != nil {
		return out, e
	}
	receipt := interceptor.EvidenceReceipt{CampaignID: id, SessionID: session, Bytes: n, SHA256: "sha256:" + hex.EncodeToString(h.Sum(nil)), LocalMaxBytes: maximum, InterceptorMaxBytes: maximum}
	d, e := interceptor.StageEvidence(ctx, receipt, source, directory)
	if e != nil {
		return out, e
	}
	defer d.Close()
	archive, e := d.InspectArchive(ctx, interceptor.DefaultArchiveLimits(maximum))
	if e != nil {
		return out, e
	}
	verified, e := archive.VerifyProvenance(ctx, identity)
	if e != nil {
		return out, e
	}
	return a.AdoptImport(ctx, verified)
}

type importReader struct {
	ctx context.Context
	r   io.Reader
}

func (r importReader) Read(p []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(p)
}
