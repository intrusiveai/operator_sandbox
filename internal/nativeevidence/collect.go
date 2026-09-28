//go:build linux || darwin

// Package nativeevidence shares bounded transfer and provenance validation between
// live finalization and retained-evidence collection. It never controls execution.
package nativeevidence

import (
	"context"
	"errors"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type Peer interface {
	DownloadEvidence(context.Context, interceptor.EvidenceRequest, string) (*interceptor.EvidenceDownload, error)
}
type Target struct {
	Identity       interceptor.EvidenceIdentity `json:"identity"`
	NativeMaxBytes int64                        `json:"native_max_bytes"`
	Reservation    string                       `json:"reservation"`
}
type Outcome = campaign.EvidenceOutcome

func Collect(ctx context.Context, peer Peer, directory string, target Target, maximum int64, retain func(context.Context, *interceptor.VerifiedEvidence) (campaign.NativeEvidence, error)) (result Outcome) {
	out := &result
	*out = Outcome{SessionID: target.Identity.SessionID, State: "missing", LocalMaxBytes: maximum, NativeMaxBytes: target.NativeMaxBytes}
	if peer == nil || retain == nil {
		out.Reason = "export_unavailable"
		return
	}
	if target.NativeMaxBytes <= 0 {
		out.Reason = "native_limit_unavailable"
		return
	}
	deadline, _ := ctx.Deadline()
	download, err := peer.DownloadEvidence(ctx, interceptor.EvidenceRequest{CampaignID: target.Identity.CampaignID, SessionID: target.Identity.SessionID, MaxArchiveBytes: out.LocalMaxBytes, InterceptorMaxBytes: target.NativeMaxBytes, Deadline: deadline}, directory)
	if err != nil {
		out.Reason = evidenceReason(err)
		return
	}
	if download == nil {
		out.Reason = "export_unavailable"
		return
	}
	defer func() {
		if download.Close() != nil {
			out.Reason = "temporary_cleanup_failed"
		}
	}()
	archive, err := download.InspectArchive(ctx, interceptor.DefaultArchiveLimits(min(out.LocalMaxBytes, target.NativeMaxBytes)))
	if err != nil {
		out.State = "invalid"
		if ctx.Err() != nil {
			out.State = "missing"
			err = ctx.Err()
		}
		out.Reason = evidenceReason(err)
		return
	}
	verified, err := archive.VerifyProvenance(ctx, target.Identity)
	if err != nil {
		out.State = "invalid"
		if ctx.Err() != nil {
			out.State = "missing"
			err = ctx.Err()
		}
		out.Reason = evidenceReason(err)
		return
	}
	retained, err := retain(ctx, verified)
	if err != nil {
		out.Reason = "retention_failed"
		return
	}
	out.State = retained.Provenance.State
	out.ArchivePath = retained.Path
	out.ArchiveDigest = retained.Provenance.Transfer.SHA256
	return
}
func evidenceReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "collection_deadline"
	}
	if errors.Is(err, interceptor.ErrProvenance) {
		return "provenance_invalid"
	}
	var e *interceptor.EvidenceError
	if errors.As(err, &e) {
		return e.Kind
	}
	var remote *interceptor.RemoteError
	if errors.As(err, &remote) {
		if remote.Response.Status == 413 && remote.Response.Code() == "evidence_limit_exceeded" {
			return "evidence_limit_exceeded"
		}
		return "export_rejected"
	}
	return "export_unavailable"
}
