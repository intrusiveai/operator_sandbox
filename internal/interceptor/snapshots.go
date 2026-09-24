package interceptor

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxSnapshotPage = 1000
const MaxSnapshotInventory = 10000
const MaxSnapshotResponse = 4 << 20

// Checkpoint is protected native metadata, not the harness snapshot projection.
// Field order and omitempty rules must match Interceptor's hash preimage.
type Checkpoint struct {
	CampaignID           string    `json:"campaign_id,omitempty"`
	Description          string    `json:"description,omitempty"`
	SchemaVersion        int       `json:"schema_version"`
	ID                   string    `json:"id"`
	Label                string    `json:"label,omitempty"`
	SourceSessionID      string    `json:"source_session_id"`
	ParentCheckpoint     string    `json:"parent_checkpoint,omitempty"`
	EnvironmentDigest    string    `json:"environment_digest"`
	AppDigest            string    `json:"app_digest"`
	JournalSeq           uint64    `json:"journal_seq"`
	JournalHash          string    `json:"journal_hash"`
	FileManifestDigest   string    `json:"file_manifest_digest"`
	CanonicalSizeBytes   int64     `json:"canonical_size_bytes"`
	EventSeq             uint64    `json:"event_seq"`
	CreatedAt            time.Time `json:"created_at"`
	InterceptorVersion   string    `json:"interceptor_version"`
	JournalSchemaVersion int       `json:"journal_schema_version"`
	Status               string    `json:"status"`
	Hash                 string    `json:"hash"`
}

type SnapshotCreate struct {
	Label                 string `json:"label,omitempty"`
	Description           string `json:"description,omitempty"`
	MaximumCommittedBytes int64  `json:"maximum_committed_bytes"`
}

// PrepareSnapshotCreate always sends the remaining campaign byte allowance,
// including zero. Native session counters are not the campaign accounting source.
// Persist the prepared request before Execute; a retry retains this allowance.
func PrepareSnapshotCreate(q OperationRequest, input SnapshotCreate) (PreparedOperation, error) {
	if q.Operation != "" && q.Operation != "snapshot.create" || !validSnapshotCreate(input) {
		return PreparedOperation{}, ErrRequest
	}
	q.Operation = "snapshot.create"
	raw, err := json.Marshal(input)
	if err != nil {
		return PreparedOperation{}, ErrRequest
	}
	return PrepareOperation(q, raw)
}

func validSnapshotCreate(input SnapshotCreate) bool {
	return input.MaximumCommittedBytes >= 0 && utf8.ValidString(input.Label) && utf8.RuneCountInString(input.Label) <= 256 && utf8.ValidString(input.Description) && len(input.Description) <= 4096
}

// DecodeSnapshotCreated requires native 201, verifies the complete checkpoint
// hash and checks it against the saved request/target. Accounting and exactly-once
// adoption of its canonical byte charge belong to the durable host broker.
func DecodeSnapshotCreated(r Response, p PreparedOperation, target Session) (Checkpoint, error) {
	if len(p.envelope) == 0 || p.request.Operation != "snapshot.create" || target.ID != p.request.SessionID || target.CampaignID != p.request.CampaignID {
		return Checkpoint{}, ErrRequest
	}
	fields, _ := object(p.envelope)
	var input SnapshotCreate
	if decodeClosed(fields["body"], &input, []string{"maximum_committed_bytes"}, []string{"label", "description"}) != nil || !validSnapshotCreate(input) {
		return Checkpoint{}, ErrRequest
	}
	if r.Status != 201 {
		return Checkpoint{}, &RemoteError{r}
	}
	cp, err := decodeCheckpoint(r.Body, p.request.CampaignID)
	if err != nil {
		return Checkpoint{}, err
	}
	if cp.CampaignID != p.request.CampaignID || cp.SourceSessionID != p.request.SessionID || cp.Label != input.Label || cp.Description != input.Description || cp.EnvironmentDigest != target.EnvironmentDigest || cp.AppDigest != target.AppDigest || cp.CanonicalSizeBytes > input.MaximumCommittedBytes || input.MaximumCommittedBytes == 0 {
		return Checkpoint{}, invalidResponse()
	}
	return cp, nil
}

func checkpointID(id string) bool {
	if !identifier.MatchString(id) || !strings.HasPrefix(id, "cp-") || len(id) < len("cp-1-")+24 {
		return false
	}
	for _, ch := range id[3:] {
		if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch == '-') {
			return false
		}
	}
	return true
}

func decodeCheckpoint(raw []byte, campaign string) (Checkpoint, error) {
	var cp Checkpoint
	if decodeClosed(raw, &cp, []string{"schema_version", "id", "source_session_id", "environment_digest", "app_digest", "journal_seq", "journal_hash", "file_manifest_digest", "canonical_size_bytes", "event_seq", "created_at", "interceptor_version", "journal_schema_version", "status", "hash"}, []string{"campaign_id", "description", "label", "parent_checkpoint"}) != nil {
		return Checkpoint{}, invalidResponse()
	}
	if cp.SchemaVersion != 1 || cp.JournalSchemaVersion != 1 || cp.Status != "ready" || !checkpointID(cp.ID) || !identifier.MatchString(cp.SourceSessionID) || cp.ParentCheckpoint != "" && !checkpointID(cp.ParentCheckpoint) || cp.CampaignID != "" && cp.CampaignID != campaign || len(cp.Description) > 4096 || cp.CanonicalSizeBytes < 0 || cp.JournalSeq == 0 || cp.CreatedAt.IsZero() || cp.InterceptorVersion == "" {
		return Checkpoint{}, invalidResponse()
	}
	for _, d := range []string{cp.EnvironmentDigest, cp.AppDigest, cp.JournalHash, cp.FileManifestDigest, cp.Hash} {
		if !digest.MatchString(d) {
			return Checkpoint{}, invalidResponse()
		}
	}
	// Native hashing clears hash but retains its key, and uses Go struct JSON
	// rather than shared-contract canonicalization or the received wire bytes.
	preimage := cp
	preimage.Hash = ""
	encoded, err := json.Marshal(preimage)
	if err != nil || rawDigest(encoded) != cp.Hash {
		return Checkpoint{}, invalidResponse()
	}
	return cp, nil
}

type SnapshotListRequest struct {
	APIVersion      string `json:"api_version"`
	CampaignID      string `json:"campaign_id"`
	SourceSessionID string `json:"source_session_id,omitempty"`
	Offset          int    `json:"offset,omitempty"`
	Limit           int    `json:"limit,omitempty"`
}

type SnapshotPage struct {
	CampaignID    string
	Checkpoints   []Checkpoint
	Offset, Total int
	NextOffset    *int
}

// ListSnapshots reads one page, including retained source sessions after restore.
// Pagination is not frozen; no automatic traversal or deduplication hides changes.
// Membership/compatibility with the host's saved lineage remains a broker check.
func (c *Client) ListSnapshots(ctx context.Context, q SnapshotListRequest) (SnapshotPage, error) {
	if q.APIVersion == "" {
		q.APIVersion = LifecycleVersion
	}
	if q.APIVersion != LifecycleVersion || !identifier.MatchString(q.CampaignID) || q.SourceSessionID != "" && !identifier.MatchString(q.SourceSessionID) || q.Offset < 0 || q.Offset > MaxSnapshotInventory || q.Limit < 0 || q.Limit > MaxSnapshotPage {
		return SnapshotPage{}, ErrRequest
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	raw, _ := json.Marshal(q)
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.postBounded(ctx, "/v1/snapshots/list", raw, MaxSnapshotResponse)
	if err = success(r, err); err != nil {
		return SnapshotPage{}, err
	}
	var body struct {
		CampaignID  string            `json:"campaign_id"`
		Checkpoints []json.RawMessage `json:"checkpoints"`
		Total       int               `json:"total"`
		NextOffset  *int              `json:"next_offset,omitempty"`
	}
	if decodeClosed(r.Body, &body, []string{"campaign_id", "checkpoints", "total"}, []string{"next_offset"}) != nil || body.CampaignID != q.CampaignID || body.Total < 0 || body.Total > MaxSnapshotInventory {
		return SnapshotPage{}, invalidResponse()
	}
	start := min(q.Offset, body.Total)
	end := min(start+q.Limit, body.Total)
	fields, _ := object(r.Body)
	_, hasNext := fields["next_offset"]
	if len(body.Checkpoints) != end-start || hasNext != (end < body.Total) || hasNext && (body.NextOffset == nil || *body.NextOffset != end) {
		return SnapshotPage{}, invalidResponse()
	}
	page := SnapshotPage{CampaignID: q.CampaignID, Offset: q.Offset, Total: body.Total, NextOffset: body.NextOffset, Checkpoints: make([]Checkpoint, 0, len(body.Checkpoints))}
	seen := map[string]bool{}
	for _, raw := range body.Checkpoints {
		cp, err := decodeCheckpoint(raw, q.CampaignID)
		if err != nil {
			return SnapshotPage{}, err
		}
		key := cp.SourceSessionID + "/" + cp.ID
		if q.SourceSessionID != "" && cp.SourceSessionID != q.SourceSessionID || seen[key] {
			return SnapshotPage{}, invalidResponse()
		}
		if n := len(page.Checkpoints); n > 0 && !checkpointLess(page.Checkpoints[n-1], cp) {
			return SnapshotPage{}, invalidResponse()
		}
		seen[key] = true
		page.Checkpoints = append(page.Checkpoints, cp)
	}
	return page, nil
}

func checkpointLess(a, b Checkpoint) bool {
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	if a.SourceSessionID != b.SourceSessionID {
		return a.SourceSessionID < b.SourceSessionID
	}
	return a.ID < b.ID
}

// InspectSnapshot returns original hash-covered metadata. Legacy missing campaign
// and description fields remain missing; read scoping comes from the native API's
// source-session association, not an invented hash-covered campaign field.
func (c *Client) InspectSnapshot(ctx context.Context, campaign, source, checkpoint string) (Checkpoint, error) {
	if !identifier.MatchString(campaign) || !identifier.MatchString(source) || !checkpointID(checkpoint) {
		return Checkpoint{}, ErrRequest
	}
	raw, _ := json.Marshal(map[string]string{"api_version": LifecycleVersion, "campaign_id": campaign, "source_session_id": source, "checkpoint_id": checkpoint})
	ctx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()
	r, err := c.postBounded(ctx, "/v1/snapshots/inspect", raw, MaxSnapshotResponse)
	if err = success(r, err); err != nil {
		return Checkpoint{}, err
	}
	cp, err := decodeCheckpoint(r.Body, campaign)
	if err != nil {
		return Checkpoint{}, err
	}
	if cp.ID != checkpoint || cp.SourceSessionID != source {
		return Checkpoint{}, invalidResponse()
	}
	return cp, nil
}
