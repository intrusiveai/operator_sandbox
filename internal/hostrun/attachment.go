//go:build linux || darwin

package hostrun

import (
	"context"
	"errors"

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
	if err = ctx.Err(); err != nil {
		return
	}
	record, err := campaign.CreateAttachment(root, intent)
	if err != nil {
		return
	}
	defer func() { err = errors.Join(err, record.Close()) }()
	attached, err = peer.Attach(ctx, intent.CampaignID, intent.WorkerInstanceID, intent.AllowTargetStop)
	if err != nil {
		return
	}
	if err = record.Bind(attached); err != nil {
		return
	}
	status, err = peer.Status(ctx, intent.CampaignID)
	if err != nil {
		return
	}
	err = record.Verify(status)
	return
}
