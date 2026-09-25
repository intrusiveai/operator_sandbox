// Native wire types mirrored from Interceptor adaptive/contracts.go, adaptive/feedback.go,
// injection/injection.go and sandboxd/server.go. Field order and omission affect native hashes.
package interceptor

import (
	"encoding/json"
	"time"
)

type ArtifactDescriptor struct {
	Digest           string `json:"digest"`
	SizeBytes        int64  `json:"size_bytes"`
	MediaType        string `json:"media_type"`
	Canonicalization string `json:"canonicalization,omitempty"`
}

type AttemptContext struct {
	ObservationSelection  *ObservationSelection `json:"observation_selection,omitempty"`
	APIVersion            string                `json:"api_version"`
	CampaignID            string                `json:"campaign_id"`
	ThreadID              string                `json:"thread_id"`
	AttemptID             string                `json:"attempt_id"`
	ParentAttemptID       string                `json:"parent_attempt_id,omitempty"`
	Generation            uint64                `json:"generation"`
	AttemptIndex          uint64                `json:"attempt_index"`
	Payload               ArtifactDescriptor    `json:"payload"`
	Generator             Generator             `json:"generator"`
	StrategyProvenanceRef string                `json:"strategy_provenance_ref,omitempty"`
	FeedbackProfile       string                `json:"feedback_profile"`
	CreatedAt             time.Time             `json:"created_at"`
	Digest                string                `json:"digest,omitempty"`
}

type Generator struct {
	Kind          string `json:"kind"`
	ReleaseDigest string `json:"release_digest,omitempty"`
}

type Visibility string

const (
	TargetVisible  Visibility = "target_visible"
	HarnessVisible Visibility = "harness_visible"
	OperatorOnly   Visibility = "operator_only"
	Protected      Visibility = "protected"
)

type OperationView struct {
	ResponseCode int    `json:"response_code,omitempty"`
	ExitCode     *int   `json:"exit_code,omitempty"`
	State        string `json:"state"`
	ReceiptID    string `json:"receipt_id,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}
type TargetOutput struct {
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	MediaType      string `json:"media_type,omitempty"`
	Inline         string `json:"inline,omitempty"`
}
type Observation struct {
	Kind       string     `json:"kind"`
	Visibility Visibility `json:"visibility"`
	Value      any        `json:"value,omitempty"`
}
type ObservationView struct {
	ReceiptID            string             `json:"receipt_id,omitempty"`
	CampaignID           string             `json:"campaign_id"`
	AttemptContextDigest string             `json:"attempt_context_digest"`
	TurnID               string             `json:"turn_id,omitempty"`
	CapturedAt           time.Time          `json:"captured_at"`
	WindowStart          time.Time          `json:"window_start"`
	WindowEnd            time.Time          `json:"window_end"`
	ThroughEventSeq      uint64             `json:"through_event_seq"`
	CollectionState      string             `json:"collection_state"`
	Categories           []FeedbackCategory `json:"categories"`
	Entries              []FeedbackEntry    `json:"entries"`
	Hash                 string             `json:"hash,omitempty"`
	APIVersion           string             `json:"api_version"`
	SessionID            string             `json:"session_id"`
	AttemptID            string             `json:"attempt_id"`
	FeedbackProfile      string             `json:"feedback_profile"`
	Operation            OperationView      `json:"operation"`
	TargetOutput         TargetOutput       `json:"target_output"`
	Observations         []Observation      `json:"observations"`
	SessionRevision      uint64             `json:"session_revision"`
}

type ObservationSelection struct {
	Mode  string   `json:"mode"`
	Kinds []string `json:"kinds,omitempty"`
}

type FeedbackCategory struct {
	Kind   string `json:"kind"`
	State  string `json:"state"` // available, empty, withheld, not_requested, unavailable, partial
	Reason string `json:"reason,omitempty"`
}
type FeedbackEntry struct {
	ID                string              `json:"id"`
	Kind              string              `json:"kind"`
	Visibility        Visibility          `json:"visibility"`
	Source            string              `json:"source"`
	Assurance         string              `json:"assurance"`
	Availability      string              `json:"availability"`
	Reason            string              `json:"reason,omitempty"`
	Artifact          *ArtifactDescriptor `json:"artifact,omitempty"`
	Truncated         bool                `json:"truncated"`
	OriginalSizeBytes int64               `json:"original_size_bytes"`
}

type FeedbackReadRequest struct {
	ReceiptID string `json:"receipt_id"`
	EntryID   string `json:"entry_id"`
	Offset    int64  `json:"offset"`
	MaxBytes  int    `json:"max_bytes"`
}

type Definition struct {
	ID            string          `json:"id"`
	Surface       string          `json:"surface"`
	Mode          string          `json:"mode"`
	Scope         string          `json:"scope"`
	Enabled       bool            `json:"enabled"`
	Selector      Selector        `json:"selector"`
	Placement     Placement       `json:"placement"`
	Payload       json.RawMessage `json:"payload"`
	AttemptID     string          `json:"attempt_id,omitempty"`
	PayloadDigest string          `json:"payload_digest,omitempty"`
}

type Selector struct {
	ToolName           string            `json:"tool_name"`
	CallOrdinal        string            `json:"call_ordinal"`
	ArgumentsPointer   string            `json:"arguments_pointer,omitempty"`
	ArgumentsEqual     json.RawMessage   `json:"arguments_equal,omitempty"`
	ArgumentsContain   string            `json:"arguments_contain,omitempty"`
	TurnID             string            `json:"turn_id,omitempty"`
	Service            string            `json:"service,omitempty"`
	Collection         string            `json:"collection,omitempty"`
	Key                string            `json:"key,omitempty"`
	Path               string            `json:"path,omitempty"`
	Method             string            `json:"method,omitempty"`
	QueryEqual         map[string]string `json:"query_equal,omitempty"`
	HeadersEqual       map[string]string `json:"headers_equal,omitempty"`
	RequestBodyPointer string            `json:"request_body_pointer,omitempty"`
	RequestBodyEqual   json.RawMessage   `json:"request_body_equal,omitempty"`
	RequestBodyContain string            `json:"request_body_contain,omitempty"`
	FileNamespace      string            `json:"file_namespace,omitempty"`
	RelativePath       string            `json:"relative_path,omitempty"`
}

type Placement struct {
	Operation     string   `json:"operation"`
	Pointer       string   `json:"pointer"`
	Index         int      `json:"index,omitempty"`
	AllowedFields []string `json:"allowed_fields,omitempty"`
}

type TurnRequest struct {
	Operation       string            `json:"operation,omitempty"`
	Input           []byte            `json:"input,omitempty"`
	MediaType       string            `json:"media_type,omitempty"`
	Method          string            `json:"method,omitempty"`
	Path            string            `json:"path,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	Body            []byte            `json:"body,omitempty"`
	CallerPrincipal string            `json:"caller_principal,omitempty"`
	AttemptID       string            `json:"attempt_id,omitempty"`
	PayloadDigest   string            `json:"payload_digest,omitempty"`
	ArtifactDigest  string            `json:"artifact_digest,omitempty"`
}

type Turn struct {
	OutputContractStatus string            `json:"output_contract_status,omitempty"`
	ID                   string            `json:"id"`
	Operation            string            `json:"operation,omitempty"`
	Status               string            `json:"status"`
	Code                 int               `json:"code,omitempty"`
	Headers              map[string]string `json:"headers,omitempty"`
	Body                 []byte            `json:"body,omitempty"`
	MediaType            string            `json:"media_type,omitempty"`
	ExitCode             *int              `json:"exit_code,omitempty"`
	Error                string            `json:"error,omitempty"`
	AttemptID            string            `json:"attempt_id,omitempty"`
	PayloadDigest        string            `json:"payload_digest,omitempty"`
	OutputDigest         string            `json:"output_digest,omitempty"`
	Started              time.Time         `json:"started_at"`
	Finished             time.Time         `json:"finished_at,omitempty"`
}

type FeedbackChunk struct {
	ReceiptID string        `json:"receipt_id"`
	Entry     FeedbackEntry `json:"entry"`
	Offset    int64         `json:"offset"`
	Content   []byte        `json:"content"`
	RawLength int           `json:"raw_length"`
	EOF       bool          `json:"eof"`
}
