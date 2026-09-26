//go:build linux || darwin

// Native evidence wire types mirrored from Interceptor. Field order and omission
// rules are part of native Go JSON hashes, not shared-contract JCS hashes.
package interceptor

import (
	"encoding/json"
	"time"
)

type evidenceAttribution struct {
	ToolCallID string `json:"tool_call_id,omitempty"`
	PID        int    `json:"pid,omitempty"`
	Confidence string `json:"confidence,omitempty"`
}

type evidenceEvent struct {
	SchemaVersion int                 `json:"schema_version"`
	SessionID     string              `json:"session_id"`
	Seq           uint64              `json:"seq"`
	ObservedAt    time.Time           `json:"observed_at"`
	Channel       string              `json:"channel"`
	Layer         string              `json:"layer"`
	Kind          string              `json:"kind"`
	Provenance    string              `json:"provenance"`
	TurnID        string              `json:"turn_id,omitempty"`
	InjectionID   string              `json:"injection_id,omitempty"`
	AttemptID     string              `json:"attempt_id,omitempty"`
	PayloadDigest string              `json:"payload_digest,omitempty"`
	Attribution   evidenceAttribution `json:"attribution,omitempty"`
	Semantic      json.RawMessage     `json:"semantic"`
	RawRef        string              `json:"raw_ref,omitempty"`
	PreviousHash  string              `json:"previous_hash"`
	Hash          string              `json:"hash"`
}

type evidenceOperation struct {
	Kind       string          `json:"kind"`
	Path       string          `json:"path,omitempty"`
	Mode       uint32          `json:"mode,omitempty"`
	Blob       string          `json:"blob,omitempty"`
	Size       int64           `json:"size,omitempty"`
	Target     string          `json:"target,omitempty"`
	Service    string          `json:"service,omitempty"`
	Collection string          `json:"collection,omitempty"`
	Key        string          `json:"key,omitempty"`
	Value      json.RawMessage `json:"value,omitempty"`
	ID         string          `json:"id,omitempty"`
}

type evidenceTransaction struct {
	SchemaVersion int                 `json:"schema_version"`
	SessionID     string              `json:"session_id"`
	Seq           uint64              `json:"seq"`
	TxID          string              `json:"tx_id"`
	CommittedAt   time.Time           `json:"committed_at"`
	Reason        string              `json:"reason"`
	Operations    []evidenceOperation `json:"operations"`
	PreviousHash  string              `json:"previous_hash"`
	Hash          string              `json:"hash"`
}

type evidenceConsumed struct {
	Turns            int64 `json:"turns"`
	Tokens           int64 `json:"tokens"`
	SnapshotAttempts int64 `json:"snapshot_attempts"`
	SnapshotBytes    int64 `json:"snapshot_bytes"`
}

type evidenceMetadata struct {
	CampaignID               string           `json:"campaign_id,omitempty"`
	SpoolMaxBytes            int64            `json:"spool_max_bytes,omitempty"`
	OperationAPIVersion      string           `json:"operation_api_version,omitempty"`
	SchemaVersion            int              `json:"schema_version"`
	ID                       string           `json:"id"`
	ParentSessionID          string           `json:"parent_session_id,omitempty"`
	ParentCheckpoint         string           `json:"parent_checkpoint,omitempty"`
	EnvironmentPath          string           `json:"environment_path"`
	EnvironmentDigest        string           `json:"environment_digest"`
	AppImage                 string           `json:"app_image"`
	AppDigest                string           `json:"app_digest"`
	CapabilityManifestDigest string           `json:"capability_manifest_digest,omitempty"`
	FeedbackProfile          string           `json:"feedback_profile,omitempty"`
	Revision                 uint64           `json:"revision"`
	Phase                    string           `json:"phase"`
	Generation               int              `json:"generation"`
	CreatedAt                time.Time        `json:"created_at"`
	UpdatedAt                time.Time        `json:"updated_at"`
	Limits                   evidenceLimits   `json:"limits"`
	Consumed                 evidenceConsumed `json:"consumed"`
	BridgeTransport          string           `json:"bridge_transport"`
	ContainerID              string           `json:"container_id,omitempty"`
	ApplicationURL           string           `json:"application_url,omitempty"`
	ControlURL               string           `json:"control_url,omitempty"`
	TerminationReason        string           `json:"termination_reason,omitempty"`
	Taints                   []string         `json:"taints"`
}

type evidenceLimits struct {
	MaxTurns       int64  `json:"max_turns"`
	MaxTokens      int64  `json:"max_tokens"`
	MaxWallSeconds int64  `json:"max_wall_seconds"`
	MaxSnapshots   int64  `json:"max_snapshots"`
	MaxBlobBytes   int64  `json:"max_blob_bytes"`
	MaxPIDs        int64  `json:"max_pids"`
	MemoryBytes    int64  `json:"memory_bytes"`
	CPUs           string `json:"cpus"`
}

type evidenceEntry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Mode   uint32 `json:"mode"`
	Blob   string `json:"blob,omitempty"`
	Size   int64  `json:"size,omitempty"`
	Target string `json:"target,omitempty"`
}

type evidenceManifest struct {
	SchemaVersion int             `json:"schema_version"`
	Entries       []evidenceEntry `json:"entries"`
	Digest        string          `json:"digest"`
}

type evidenceOracleResult struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	Fired            bool     `json:"fired"`
	FirstEventSeq    uint64   `json:"first_event_seq,omitempty"`
	Evidence         []string `json:"evidence"`
	CausalIDs        []string `json:"causal_injection_ids,omitempty"`
	Count            int      `json:"count,omitempty"`
	FirstEventDigest string   `json:"first_event_digest,omitempty"`
	Visibility       string   `json:"visibility,omitempty"`
	Causality        string   `json:"causality,omitempty"`
}

type evidenceInjectionResult struct {
	ID       string `json:"id"`
	Mode     string `json:"mode"`
	Delivery string `json:"delivery"`
	Outcome  string `json:"outcome"`
}

type evidenceReport struct {
	Oracles    []evidenceOracleResult    `json:"oracles"`
	Injections []evidenceInjectionResult `json:"injections"`
	Coverage   []string                  `json:"coverage_gaps"`
}

type evidenceEvidenceCompleteness struct {
	State         string `json:"state"`
	StartSequence uint64 `json:"start_sequence"`
	EndSequence   uint64 `json:"end_sequence"`
	Truncated     bool   `json:"truncated"`
}

type evidenceAttemptEvidence struct {
	AttemptID       string   `json:"attempt_id"`
	ParentAttemptID string   `json:"parent_attempt_id,omitempty"`
	PayloadDigest   string   `json:"payload_digest"`
	ContextDigest   string   `json:"context_digest"`
	EventSequences  []uint64 `json:"event_sequences"`
}

type evidenceSnapshotEvidence struct {
	SnapshotID         string    `json:"snapshot_id"`
	Digest             string    `json:"digest"`
	ParentSnapshot     string    `json:"parent_snapshot,omitempty"`
	CanonicalSizeBytes int64     `json:"canonical_size_bytes"`
	EventSequence      uint64    `json:"event_sequence"`
	CreatedAt          time.Time `json:"created_at"`
	Status             string    `json:"status"`
}

type evidenceRestoreEvidence struct {
	SourceSessionID    string `json:"source_session_id"`
	SnapshotID         string `json:"snapshot_id"`
	ResultingSessionID string `json:"resulting_session_id"`
}

type evidenceEvidenceBundle struct {
	APIVersion               string                       `json:"api_version"`
	Kind                     string                       `json:"kind"`
	CampaignID               string                       `json:"campaign_id,omitempty"`
	SessionID                string                       `json:"session_id"`
	EnvironmentDigest        string                       `json:"environment_digest"`
	ApplicationDigest        string                       `json:"application_digest"`
	CapabilityManifestDigest string                       `json:"capability_manifest_digest"`
	InterceptorReleaseDigest string                       `json:"interceptor_release_digest"`
	FeedbackProfile          string                       `json:"feedback_profile"`
	Completeness             evidenceEvidenceCompleteness `json:"completeness"`
	Attempts                 []evidenceAttemptEvidence    `json:"attempts"`
	Snapshots                []evidenceSnapshotEvidence   `json:"snapshots"`
	Restores                 []evidenceRestoreEvidence    `json:"restores"`
	ArtifactRefs             []ArtifactDescriptor         `json:"artifact_refs"`
	Oracles                  []evidenceOracleResult       `json:"oracles"`
	Injections               []evidenceInjectionResult    `json:"injections"`
	CoverageGaps             []string                     `json:"coverage_gaps"`
	CreatedAt                time.Time                    `json:"created_at"`
	Digest                   string                       `json:"digest"`
}

type evidenceExecutionState struct {
	ClosedAt      *time.Time                  `json:"closed_at,omitempty"`
	ClosureReason string                      `json:"closure_reason,omitempty"`
	APIVersion    string                      `json:"api_version"`
	SessionID     string                      `json:"session_id"`
	Owner         *Owner                      `json:"owner,omitempty"`
	Records       map[string]*OperationRecord `json:"records"`
}
