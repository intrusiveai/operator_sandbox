//go:build linux || darwin

package hostrun

import (
	"context"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type attachmentPeer interface {
	Attach(context.Context, string, string, bool) (interceptor.Attachment, error)
	Status(context.Context, string) (interceptor.Status, error)
}

// attachRecorded runs under the installation lease. No target preparation may
// consume an attachment until both its binding and instance are durable.
func attachRecorded(ctx context.Context, root string, intent campaign.AttachmentIntent, peer attachmentPeer) (attached interceptor.Attachment, status interceptor.Status, err error) {
	stage := "Interceptor attachment intent recording"
	defer func() { err = PreparationFailure(stage, err) }()
	if err = ctx.Err(); err != nil {
		return
	}
	record, err := campaign.CreateAttachment(root, intent)
	if err != nil {
		return
	}
	defer func() { err = preparationCleanup(stage, err, record.Close()) }()
	stage = "Interceptor attachment"
	attached, err = peer.Attach(ctx, intent.CampaignID, intent.WorkerInstanceID, intent.AllowTargetStop)
	if err != nil {
		return
	}
	stage = "Interceptor attachment binding recording"
	if err = record.Bind(attached); err != nil {
		return
	}
	stage = "Interceptor status lookup"
	status, err = peer.Status(ctx, intent.CampaignID)
	if err != nil {
		return
	}
	stage = "Interceptor attachment verification"
	err = record.Verify(status)
	return
}
