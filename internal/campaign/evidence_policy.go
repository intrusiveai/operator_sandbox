package campaign

import (
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// EvidencePolicy freezes host-only collection settings in the execution journal.
// Durations use exact integer nanoseconds to preserve the configured Go duration.
type EvidencePolicy struct {
	MaxArchiveBytes int64 `json:"max_archive_bytes"`
	TimeoutNS       int64 `json:"timeout_ns"`
	TotalTimeoutNS  int64 `json:"total_timeout_ns"`
}

func (p EvidencePolicy) Valid() bool {
	return p.MaxArchiveBytes > 0 && p.MaxArchiveBytes <= contracts.MaxSafeInteger && p.TimeoutNS > 0 && p.TimeoutNS <= int64(5*time.Minute) && p.TotalTimeoutNS > 0 && p.TotalTimeoutNS <= int64(30*time.Minute)
}

type EvidenceOutcome struct {
	Failure        *diagnostictext.Failure `json:"failure,omitempty"`
	CleanupFailure *diagnostictext.Failure `json:"cleanup_failure,omitempty"`
	NativeStatus   int                     `json:"native_http_status,omitempty"`
	NativeCode     string                  `json:"native_code,omitempty"`
	NativeOmission string                  `json:"native_detail_omission,omitempty"`
	SessionID      string                  `json:"session_id"`
	State          string                  `json:"state"` // complete, partial, missing or invalid
	Reason         string                  `json:"reason,omitempty"`
	ArchivePath    string                  `json:"archive_path,omitempty"`
	ArchiveDigest  string                  `json:"archive_digest,omitempty"`
	LocalMaxBytes  int64                   `json:"local_max_bytes"`
	NativeMaxBytes int64                   `json:"native_max_bytes"`
	Recorded       bool                    `json:"recorded"`
}

func (v EvidenceOutcome) ValidDiagnostic() bool {
	if v.Failure != nil && !v.Failure.Valid() || v.CleanupFailure != nil && !v.CleanupFailure.Valid() {
		return false
	}
	if v.NativeStatus == 0 {
		return v.NativeCode == "" && v.NativeOmission == ""
	}
	if v.NativeStatus < 100 || v.NativeStatus > 599 {
		return false
	}
	if v.NativeCode != "" {
		value, why := diagnosticID(v.NativeCode)
		if why != "" || value != v.NativeCode {
			return false
		}
	}
	return v.NativeOmission == "" || v.NativeOmission == "code_unavailable"
}
