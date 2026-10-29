//go:build linux || darwin

package interceptor

import (
	"encoding/json"
	"slices"
)

// DatabaseEvidence contains selected administrator audit facts only. It is not
// a Harness feedback channel and does not establish injection causality.
type DatabaseEvidence struct {
	Events    []DatabaseEvent  `json:"events"`
	Oracles   []DatabaseOracle `json:"oracles"`
	Truncated bool             `json:"truncated"`
}
type DatabaseEvent struct {
	Service           string `json:"service"`
	Kind              string `json:"kind"`
	Provenance        string `json:"provenance"`
	AttemptID         string `json:"attempt_id,omitempty"`
	ObservedAttemptID string `json:"observed_attempt_id,omitempty"`
	Sequence          uint64 `json:"sequence"`
	Digest            string `json:"digest"`
}
type DatabaseOracle struct {
	ID               string `json:"id"`
	Fired            bool   `json:"fired"`
	FirstEventSeq    uint64 `json:"first_event_seq,omitempty"`
	FirstEventDigest string `json:"first_event_digest,omitempty"`
}

func (v *evidenceVerifier) summarizeDatabaseEvent(e evidenceEvent, hash string) {
	if !slices.Contains([]string{"database_ready", "database_request", "database_result", "database_error", "database_partial_result", "database_response_rewritten", "database_state_injection_requested", "database_state_injection_committed", "database_oracle", "database_transaction_status", "database_statement_outcome", "database_connection_failed", "database_connection_closed"}, e.Kind) {
		return
	}
	var semantic struct {
		Service string `json:"service"`
	}
	if json.Unmarshal(e.Semantic, &semantic) != nil || !identifier.MatchString(semantic.Service) {
		return
	}
	if v.database == nil {
		v.database = &DatabaseEvidence{Events: []DatabaseEvent{}, Oracles: []DatabaseOracle{}}
	}
	if len(v.database.Events) >= 128 {
		v.database.Truncated = true
		return
	}
	item := DatabaseEvent{Service: semantic.Service, Kind: e.Kind, Provenance: e.Provenance, Sequence: e.Seq, Digest: hash}
	if identifier.MatchString(e.AttemptID) {
		item.AttemptID = e.AttemptID
	}
	if identifier.MatchString(e.Attribution.ObservedAttemptID) {
		item.ObservedAttemptID = e.Attribution.ObservedAttemptID
	}
	v.database.Events = append(v.database.Events, item)
}
