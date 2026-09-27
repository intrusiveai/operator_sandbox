//go:build linux || darwin

package preparation

import (
	"context"
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

// RetainInputs journals the actual immutable staged tree before execution. It is
// single-use even on failure; a partial write never publishes the completion marker.
// This does not persist target adoption or authorize bootstrap by itself.
func (l *Launch) RetainInputs(ctx context.Context, w *campaign.Writer, tree *staging.Tree) (err error) {
	if l == nil || w == nil || tree == nil || !l.retained.CompareAndSwap(false, true) {
		return ErrPreparation
	}
	defer func() {
		if err != nil {
			w.Fence().Stop(err)
		}
	}()
	raw, err := l.Manifest.Bytes()
	if err != nil || contracts.RawDigest(raw) != w.ManifestDigest() {
		return ErrPreparation
	}
	receipt := tree.Receipt()
	pin := l.Manifest.Contract
	if receipt.Contract.Version != pin.Version || receipt.Contract.Digest != pin.Digest || receipt.InputTreeDigest != l.Manifest.InputTreeDigest || receipt.SkillSetDigest != l.Manifest.SkillSetDigest {
		return ErrPreparation
	}
	files := 0
	total := int64(0)
	err = tree.Visit(ctx, func(path string, data []byte) error {
		parts := max(1, (len(data)+campaign.MaxContentBytes-1)/campaign.MaxContentBytes)
		digest := contracts.RawDigest(data)
		for i := 0; i < parts; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			start := i * campaign.MaxContentBytes
			end := min(len(data), start+campaign.MaxContentBytes)
			metadata, _ := json.Marshal(map[string]any{"path": path, "size_bytes": len(data), "digest": digest, "part_index": i, "parts": parts})
			_, err := w.AppendStored(campaign.Entry{RunRevision: l.Manifest.InitialRevision, Kind: "campaign.launch-input", Metadata: metadata, Content: []campaign.Content{{Role: "input-part", MediaType: "application/octet-stream", Bytes: data[start:end]}}}, "", false)
			if err != nil {
				return err
			}
		}
		files++
		total += int64(len(data))
		return nil
	})
	if err != nil {
		return err
	}
	if files != receipt.FileCount || total != receipt.ContentBytes {
		return ErrPreparation
	}
	metadata, _ := json.Marshal(map[string]any{"input_tree_digest": receipt.InputTreeDigest, "skill_set_digest": receipt.SkillSetDigest, "files": files, "size_bytes": total})
	_, err = w.Append(campaign.Entry{RunRevision: l.Manifest.InitialRevision, Kind: "campaign.launch-inputs-retained", Metadata: metadata})
	return err
}
