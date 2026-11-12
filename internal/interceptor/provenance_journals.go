//go:build linux || darwin

package interceptor

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

type evidenceVerifier struct {
	stopped                  *SessionStopEvidence
	database                 *DatabaseEvidence
	archive                  *EvidenceArchive
	id                       EvidenceIdentity
	eventHashes, stateHashes []string
	attemptEvents            map[string][]uint64
	executionHead            string
}

func (v *evidenceVerifier) lines(ctx context.Context, name string, visit func([]byte) error) error {
	r, err := v.archive.Reader(name)
	if err != nil {
		return evidenceLocation(name, 0, err)
	}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), 32<<20)
	n := 0
	for scanner.Scan() {
		if err = ctx.Err(); err != nil {
			return err
		}
		n++
		if n > evidenceRecordLimit {
			return evidenceLocation(name, n, evidenceRule("records", fmt.Sprintf("exceeds journal record limit %d", evidenceRecordLimit)))
		}
		if err = visit(scanner.Bytes()); err != nil {
			return evidenceLocation(name, n, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return evidenceLocation(name, n+1, err)
	}
	return nil
}
func (v *evidenceVerifier) blob(d string, size int64) error {
	if !digest.MatchString(d) {
		return ErrProvenance
	}
	e, ok := v.archive.members["blobs/sha256/"+strings.TrimPrefix(d, "sha256:")]
	if !ok || e.SHA256 != d || size >= 0 && e.Bytes != size {
		return ErrProvenance
	}
	return nil
}
func (v *evidenceVerifier) references(x any) error {
	switch value := x.(type) {
	case map[string]any:
		for k, child := range value {
			if strings.HasSuffix(k, "_ref") {
				if d, ok := child.(string); ok && digest.MatchString(d) {
					if err := v.blob(d, -1); err != nil {
						return err
					}
				}
			}
			if err := v.references(child); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range value {
			if err := v.references(child); err != nil {
				return err
			}
		}
	}
	return nil
}
func (v *evidenceVerifier) events(ctx context.Context) error {
	return v.lines(ctx, "events.jsonl", func(raw []byte) error {
		var e evidenceEvent
		if err := strictEvidence(raw, &e); err != nil {
			return err
		}
		if e.SchemaVersion != 1 || e.SessionID != v.id.SessionID || e.Seq != uint64(len(v.eventHashes)) || e.PreviousHash != v.eventHashes[len(v.eventHashes)-1] || e.ObservedAt.IsZero() || e.Layer == "" || e.Kind == "" || !slices.Contains([]string{"attack", "control", "evaluation"}, e.Channel) || !slices.Contains([]string{"application", "environment_sealed", "harness_injected", "attacker_infrastructure", "sandbox_control"}, e.Provenance) {
			return ErrProvenance
		}
		h := e.Hash
		e.Hash = ""
		if nativeHash(e) != h {
			return ErrProvenance
		}
		v.eventHashes = append(v.eventHashes, h)
		v.summarizeDatabaseEvent(e, h)
		if e.Channel == "control" && e.Layer == "sandboxd" && e.Kind == "session_stopped" && e.Provenance == "sandbox_control" {
			v.stopped = &SessionStopEvidence{e.Seq, h, e.ObservedAt}
		}
		if e.RawRef != "" {
			if err := v.blob(e.RawRef, -1); err != nil {
				return err
			}
		}
		var semantic any
		if json.Unmarshal(e.Semantic, &semantic) != nil {
			return ErrProvenance
		}
		if err := v.references(semantic); err != nil {
			return err
		}
		if e.AttemptID != "" {
			if !identifier.MatchString(e.AttemptID) {
				return ErrProvenance
			}
			v.attemptEvents[e.AttemptID] = append(v.attemptEvents[e.AttemptID], e.Seq)
		}
		// Native attempt evidence also associates observations with the attempt
		// during which they were seen. This does not assert causal attribution.
		if observed := e.Attribution.ObservedAttemptID; observed != "" {
			if !identifier.MatchString(observed) || (e.Attribution.ObservedPayloadDigest != "" && !digest.MatchString(e.Attribution.ObservedPayloadDigest)) {
				return ErrProvenance
			}
			if observed != e.AttemptID {
				v.attemptEvents[observed] = append(v.attemptEvents[observed], e.Seq)
			}
		}
		return nil
	})
}
func (v *evidenceVerifier) transactions(ctx context.Context, name, session string, source bool) error {
	hashes := []string{evidenceZeroHash}
	err := v.lines(ctx, name, func(raw []byte) error {
		var tx evidenceTransaction
		if err := strictEvidence(raw, &tx); err != nil {
			return err
		}
		if tx.SchemaVersion != 1 {
			return evidenceRule("schema_version", fmt.Sprintf("unsupported version %d; supported version: 1", tx.SchemaVersion))
		}
		if tx.SessionID != session {
			return evidenceRule("session_id", "does not match the saved session identity")
		}
		if tx.Seq != uint64(len(hashes)) {
			return evidenceRule("seq", fmt.Sprintf("expected %d; observed %d", len(hashes), tx.Seq))
		}
		if tx.PreviousHash != hashes[len(hashes)-1] {
			return evidenceRule("previous_hash", "does not match the preceding verified transaction")
		}
		if tx.CommittedAt.IsZero() {
			return evidenceRule("committed_at", "requires a nonzero timestamp")
		}
		if tx.TxID == "" {
			return evidenceRule("tx_id", "must not be empty")
		}
		if strings.TrimSpace(tx.Reason) == "" {
			return evidenceRule("reason", "must not be blank")
		}
		if len(tx.Operations) == 0 {
			return evidenceRule("operations", "requires at least one operation")
		}
		h := tx.Hash
		tx.Hash = ""
		if nativeHash(tx) != h {
			return evidenceRule("hash", "does not match the serialized transaction (including ownership)")
		}
		hashes = append(hashes, h)
		for i, op := range tx.Operations {
			if err := validateEvidenceOperation(op); err != nil {
				return evidenceLocation(fmt.Sprintf("operations[%d]", i), 0, err)
			}
			if op.Kind == "file_upsert" || op.Kind == "database_checkpoint" {
				if err := v.blob(op.Blob, op.Size); err != nil {
					return err
				}
			}
			if !source && op.Kind == "idempotency_commit" {
				var value struct {
					SessionID string `json:"session_id"`
					Digest    string `json:"execution_state_digest"`
				}
				_ = json.Unmarshal(op.Value, &value)
				if value.SessionID == session && value.Digest != "" {
					v.executionHead = value.Digest
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if source {
		var cp Checkpoint
		if _, err = v.archive.readNative("restore-source/checkpoint.json", &cp); err != nil {
			return err
		}
		if cp.JournalSeq != uint64(len(hashes)-1) || cp.JournalHash != hashes[len(hashes)-1] {
			return ErrProvenance
		}
	} else {
		v.stateHashes = hashes
	}
	return nil
}
func validateEvidenceOperation(o evidenceOperation) error {
	if o.Owner != nil && *o.Owner != "supervisor" && *o.Owner != "application" {
		return evidenceRule("owner", "must be omitted, supervisor, or application")
	}
	allowed := []string{"kind"}
	p := path.IsAbs(o.Path) && path.Clean(o.Path) == o.Path && o.Path != "/"
	switch o.Kind {
	case "database_checkpoint":
		if !identifier.MatchString(o.Service) || !digest.MatchString(o.Blob) || o.Size < 0 || !validDatabaseCheckpoint(o.Value) {
			return evidenceRule("service/blob/size/value", "requires a service identifier, SHA-256 blob digest, nonnegative size and valid database checkpoint metadata")
		}
		allowed = append(allowed, "service", "blob", "size", "value")
	case "file_upsert":
		if !p || !digest.MatchString(o.Blob) || o.Size < 0 {
			return evidenceRule("path/blob/size", "requires a clean absolute non-root path, SHA-256 blob digest and nonnegative size")
		}
		allowed = append(allowed, "path", "mode", "owner", "blob", "size")
	case "file_delete", "directory_delete":
		if !p {
			return evidenceRule("path", "requires a clean absolute non-root path")
		}
		allowed = append(allowed, "path")
	case "directory_ensure":
		if !p {
			return evidenceRule("path", "requires a clean absolute non-root path")
		}
		allowed = append(allowed, "path", "mode", "owner")
	case "symlink_set":
		if !p || o.Target == "" || path.IsAbs(o.Target) {
			return evidenceRule("path/target", "requires a clean absolute non-root path and nonempty relative symlink target")
		}
		allowed = append(allowed, "path", "target", "mode", "owner")
	case "service_put", "service_delete":
		if o.Service == "" || o.Collection == "" || o.Key == "" {
			return evidenceRule("service/collection/key", "requires nonempty service, collection and key")
		}
		allowed = append(allowed, "service", "collection", "key")
		if o.Kind == "service_put" {
			if !json.Valid(o.Value) {
				return evidenceRule("value", "requires a JSON value")
			}
			allowed = append(allowed, "value")
		}
	case "injection_put", "artifact_put", "attempt_put":
		if o.ID == "" || !json.Valid(o.Value) {
			return evidenceRule("id/value", "requires a nonempty identifier and JSON value")
		}
		allowed = append(allowed, "id", "value")
	case "injection_delete":
		if o.ID == "" {
			return evidenceRule("id", "requires a nonempty identifier")
		}
		allowed = append(allowed, "id")
	case "clock_set", "oracle_state_set", "budget_set", "idempotency_commit", "restored_from", "environment_materialized":
		if !json.Valid(o.Value) {
			return evidenceRule("value", "requires a JSON value")
		}
		allowed = append(allowed, "value")
	default:
		return evidenceRule("kind", "unsupported state-journal operation kind")
	}
	raw, _ := json.Marshal(o)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for k := range fields {
		if !slices.Contains(allowed, k) {
			return evidenceRule(k, "is not permitted for this operation kind; permitted fields: "+strings.Join(allowed, ", "))
		}
	}
	return nil
}
func (v *evidenceVerifier) files() (result error) {
	defer func() {
		if result != nil {
			result = evidenceLocation("file-manifest.json", 0, result)
		}
	}()
	var m evidenceManifest
	if _, err := v.archive.readNative("file-manifest.json", &m); err != nil {
		return err
	}
	d := m.Digest
	m.Digest = ""
	if m.SchemaVersion != 1 && m.SchemaVersion != 2 {
		return evidenceRule("schema_version", fmt.Sprintf("unsupported version %d; supported versions: 1, 2", m.SchemaVersion))
	}
	if nativeHash(m) != d {
		return evidenceRule("digest", "does not match the serialized file manifest (including ownership)")
	}
	previous := ""
	for i, e := range m.Entries {
		field := fmt.Sprintf("entries[%d]", i)
		if (m.SchemaVersion == 1 && e.Owner != nil) || (m.SchemaVersion == 2 && (e.Owner == nil || (*e.Owner != "supervisor" && *e.Owner != "application"))) {
			return evidenceRule(field+".owner", fmt.Sprintf("invalid or missing ownership for schema_version %d; version 1 omits owner; version 2 requires supervisor or application", m.SchemaVersion))
		}
		if e.Mode & ^uint32(0777) != 0 {
			return evidenceRule(field+".mode", "must contain only permission bits (0..0777)")
		}
		if e.Path <= previous || !path.IsAbs(e.Path) || path.Clean(e.Path) != e.Path {
			return evidenceRule(field+".path", "requires clean absolute paths in unique lexical order")
		}
		previous = e.Path
		switch e.Type {
		case "file":
			if e.Size < 0 || e.Target != "" {
				return evidenceRule(field+".size/target", "file entries require nonnegative size and no symlink target")
			}
			if err := v.blob(e.Blob, e.Size); err != nil {
				return err
			}
		case "directory":
			if e.Blob != "" || e.Target != "" {
				return evidenceRule(field+".blob/target", "directory entries cannot contain a blob or symlink target")
			}
		case "symlink":
			if e.Target == "" || path.IsAbs(e.Target) || e.Blob != "" {
				return evidenceRule(field+".target/blob", "symlink entries require a nonempty relative target and no blob")
			}
		default:
			return evidenceRule(field+".type", "supported entry types: file, directory, symlink")
		}
	}
	return nil
}

// Engine-version compatibility is enforced by native restore, not guessed by
// the evidence reader. Here validate the recorded metadata and backup envelope.
func validDatabaseCheckpoint(raw json.RawMessage) bool {
	var value struct {
		Version         string                     `json:"version"`
		OracleBaselines map[string]json.RawMessage `json:"oracle_baselines,omitempty"`
	}
	if strictEvidence(raw, &value) != nil || len(value.Version) == 0 || len(value.Version) > 128 || strings.ContainsAny(value.Version, "\x00\r\n") {
		return false
	}
	for id, raw := range value.OracleBaselines {
		if !identifier.MatchString(id) {
			return false
		}
		var rows [][]json.RawMessage
		if json.Unmarshal(raw, &rows) != nil || rows == nil {
			return false
		}
	}
	return true
}
