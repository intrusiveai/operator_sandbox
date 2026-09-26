//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/attemptadapter"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/termination"
)

// Only the ordinary gate accesses this index. Bytes stay in immutable journal
// content; neither the upload index nor the attempt input retains a second store.
type artifactStore struct {
	uploads             map[string]*artifactUpload
	committed           map[string]*artifactUpload
	objects, bytes      int64
	conclusionAllocated bool
}
type artifactUpload struct {
	ID       string                         `json:"upload_id"`
	Purpose  string                         `json:"purpose"`
	Artifact interceptor.ArtifactDescriptor `json:"artifact"`
	Offset   int64                          `json:"next_offset"`
	Receipt  string                         `json:"artifact_receipt,omitempty"`
	Commit   *campaign.ContentDescriptor    `json:"-"`
	Begin    []byte                         `json:"-"`
	Parts    []campaign.ContentDescriptor   `json:"parts"`
}

func newArtifactStore() artifactStore {
	return artifactStore{uploads: map[string]*artifactUpload{}, committed: map[string]*artifactUpload{}}
}
func isArtifact(op string) bool {
	return slices.Contains([]string{"engine.artifact_begin", "engine.artifact_put_part", "engine.artifact_commit"}, op)
}
func (s *Service) handleArtifact(ctx context.Context, raw []byte, seq int64) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reply, err := s.artifactOperation(q, raw)
	if err != nil {
		return nil, err
	}
	response, err := s.stateEnvelope(raw, seq, reply)
	if err != nil {
		return nil, err
	}
	if q.Operation == "engine.artifact_commit" && reply.Error == nil {
		var body struct {
			ID string `json:"upload_id"`
		}
		_ = json.Unmarshal(q.Body, &body)
		u := s.artifacts.uploads[body.ID]
		if u.Commit == nil {
			refs, err := s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "artifact.receipt", Metadata: marshal(map[string]any{"upload_id": u.ID, "artifact_receipt": u.Receipt}), Content: []campaign.Content{{Role: "artifact-response", MediaType: "application/json", Bytes: response}}}, "", false)
			if err != nil {
				return nil, err
			}
			u.Commit = &refs[0]
		}
	}
	if err = s.attempts.Tools().Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, err
	}
	// Hard integrity failures close admission independently of response delivery.
	if reply.Error != nil && reply.Error.Disposition == "terminate" {
		s.Stop(campaign.ErrCorrupt)
	}
	return response, nil
}
func (s *Service) artifactOperation(q stateRequest, raw []byte) (stateReply, error) {
	if q.Operation == "engine.artifact_begin" {
		var declaration struct {
			Purpose  string                         `json:"purpose"`
			Artifact interceptor.ArtifactDescriptor `json:"artifact"`
		}
		if json.Unmarshal(q.Body, &declaration) != nil {
			return stateReply{}, ErrService
		}
		remaining := s.remaining()
		availableBytes, availableObjects := remaining["artifact_bytes"], remaining["artifact_objects"]
		if declaration.Purpose != "conclusion" && !s.artifacts.conclusionAllocated {
			// The reserve is inside the original allowance and survives target restores.
			initialObjects, initialBytes := s.config.Prepared.ArtifactUsage()
			var limits struct {
				Bytes   int64 `json:"artifact_bytes"`
				Objects int64 `json:"artifact_objects"`
			}
			_ = json.Unmarshal(s.writer.Manifest().RemainingLimits, &limits)
			if limits.Objects > initialObjects {
				availableBytes -= min(int64(1<<20), limits.Bytes-initialBytes)
				availableObjects--
			}
		}
		if availableObjects < 1 || declaration.Artifact.SizeBytes > availableBytes {
			return stateDenied("ARTIFACT_BUDGET_EXCEEDED"), nil
		}
		// Same bytes cannot acquire an ambiguous descriptor in the attempt resolver.
		initial, err := s.config.Prepared.InputsFor(s.target())
		if err != nil {
			return stateReply{}, err
		}
		if a, ok := initial.Artifacts[declaration.Artifact.Digest]; ok && a.Descriptor != declaration.Artifact {
			return stateDenied("INVALID_ARGUMENTS"), nil
		}
		for _, u := range s.artifacts.uploads {
			if u.Artifact.Digest == declaration.Artifact.Digest && u.Artifact != declaration.Artifact {
				return stateDenied("INVALID_ARGUMENTS"), nil
			}
		}
		canonical, err := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
		if err != nil {
			return stateReply{}, err
		}
		u := &artifactUpload{ID: "upload-" + termination.NewRequestID(), Purpose: declaration.Purpose, Artifact: declaration.Artifact, Begin: canonical, Parts: []campaign.ContentDescriptor{}}
		// Charge declarations before accepting bytes. Incomplete uploads retain their
		// reservations until shutdown; there is no guest abort/refund operation.
		entry := campaign.Entry{RunRevision: q.Revision, Kind: "artifact.begun", Metadata: marshal(map[string]any{"operation_id": q.ID, "upload": u, "artifact_objects": s.artifacts.objects + 1, "artifact_bytes": s.artifacts.bytes + u.Artifact.SizeBytes})}
		if _, err := s.writer.AppendStored(entry, "", false); err != nil {
			return stateReply{}, err
		}
		s.artifacts.uploads[u.ID] = u
		s.artifacts.objects++
		s.artifacts.bytes += u.Artifact.SizeBytes
		if u.Purpose == "conclusion" {
			s.artifacts.conclusionAllocated = true
		}
		return stateReply{Result: map[string]any{"upload_id": u.ID, "purpose": u.Purpose, "artifact": u.Artifact, "next_offset": 0}}, nil
	}
	var part struct {
		ID      string `json:"upload_id"`
		Offset  int64  `json:"offset"`
		Content string `json:"content"`
	}
	if json.Unmarshal(q.Body, &part) != nil {
		return stateReply{}, ErrService
	}
	u := s.artifacts.uploads[part.ID]
	if u == nil {
		return stateDenied("UPLOAD_NOT_FOUND"), nil
	}
	if q.Operation == "engine.artifact_put_part" {
		content, err := base64.StdEncoding.Strict().DecodeString(part.Content)
		if err != nil {
			return stateReply{}, err
		}
		if u.Receipt != "" || part.Offset != u.Offset || int64(len(content)) > u.Artifact.SizeBytes-u.Offset {
			return stateDenied("UPLOAD_RANGE_INVALID"), nil
		}
		next := u.Offset + int64(len(content))
		refs, err := s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "artifact.part", Metadata: marshal(map[string]any{"operation_id": q.ID, "upload_id": u.ID, "offset": u.Offset, "next_offset": next}), Content: []campaign.Content{{Role: "artifact-part", MediaType: "application/octet-stream", Bytes: content}}}, "", false)
		if err != nil {
			return stateReply{}, err
		}
		u.Parts = append(u.Parts, refs[0])
		u.Offset = next
		return stateReply{Result: map[string]any{"upload_id": u.ID, "offset": part.Offset, "raw_length": len(content), "next_offset": next}}, nil
	}
	if u.Offset != u.Artifact.SizeBytes {
		return stateDenied("UPLOAD_INCOMPLETE"), nil
	}
	if u.Receipt == "" {
		content, err := s.readUpload(u)
		if err != nil {
			return stateReply{}, err
		}
		if s.target().Protocol().ValidateArtifactContent(u.Begin, content) != nil {
			return stateReply{Error: &attemptadapter.Fault{Code: "ARTIFACT_INTEGRITY_FAILED", Message: "Uploaded content does not match its declaration.", Effect: "none", Disposition: "terminate"}}, nil
		}
		receipt := "artifact-" + termination.NewRequestID()
		// Parts are already journaled. A compact publication links them in order; a
		// 16 MiB object need not fit in the journal's 4 MiB single-content limit.
		_, err = s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "artifact.committed", Metadata: marshal(map[string]any{"operation_id": q.ID, "upload_id": u.ID, "artifact_receipt": receipt, "purpose": u.Purpose, "artifact": u.Artifact, "part_count": len(u.Parts)})}, "", false)
		if err != nil {
			return stateReply{}, err
		}
		u.Receipt = receipt
		s.artifacts.committed[u.Artifact.Digest] = u
	}
	return stateReply{Result: map[string]any{"upload_id": u.ID, "artifact_receipt": u.Receipt, "purpose": u.Purpose, "artifact": u.Artifact}}, nil
}
func (s *Service) readUpload(u *artifactUpload) ([]byte, error) {
	content := make([]byte, 0, u.Artifact.SizeBytes)
	for _, ref := range u.Parts {
		part, err := s.writer.ReadContent(ref)
		if err != nil {
			return nil, err
		}
		content = append(content, part...)
	}
	if int64(len(content)) != u.Artifact.SizeBytes {
		return nil, campaign.ErrCorrupt
	}
	return content, nil
}

// Resolve only the request's payload/carrier, never every uploaded artifact.
// The compiler checks the complete descriptor and campaign membership again.
func (s *Service) addArtifacts(raw []byte, in *attemptadapter.Inputs) error {
	var q struct {
		Payload interceptor.ArtifactDescriptor  `json:"payload"`
		Carrier *interceptor.ArtifactDescriptor `json:"carrier"`
	}
	if json.Unmarshal(raw, &q) != nil {
		return ErrService
	}
	for _, desc := range []*interceptor.ArtifactDescriptor{&q.Payload, q.Carrier} {
		if desc == nil {
			continue
		}
		u := s.artifacts.committed[desc.Digest]
		if u == nil {
			continue
		}
		content, err := s.readUpload(u)
		if err != nil {
			return err
		}
		if s.target().Protocol().ValidateArtifactContent(u.Begin, content) != nil {
			return campaign.ErrCorrupt
		}
		in.Artifacts[desc.Digest] = attemptadapter.Artifact{CampaignID: s.writer.Manifest().CampaignID, Descriptor: u.Artifact, Bytes: content}
	}
	return nil
}
