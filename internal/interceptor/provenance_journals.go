//go:build linux || darwin

package interceptor

import (
	"bufio"
	"context"
	"encoding/json"
	"path"
	"slices"
	"strings"
)

type evidenceVerifier struct {
	archive                  *EvidenceArchive
	id                       EvidenceIdentity
	eventHashes, stateHashes []string
	attemptEvents            map[string][]uint64
	executionHead            string
}

func (v *evidenceVerifier) lines(ctx context.Context, name string, visit func([]byte) error) error {
	r, err := v.archive.Reader(name)
	if err != nil {
		return ErrProvenance
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
			return ErrProvenance
		}
		if err = visit(scanner.Bytes()); err != nil {
			return err
		}
	}
	if scanner.Err() != nil {
		return ErrProvenance
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
		if strictEvidence(raw, &e) != nil {
			return ErrProvenance
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
		return nil
	})
}
func (v *evidenceVerifier) transactions(ctx context.Context, name, session string, source bool) error {
	hashes := []string{evidenceZeroHash}
	err := v.lines(ctx, name, func(raw []byte) error {
		var tx evidenceTransaction
		if strictEvidence(raw, &tx) != nil {
			return ErrProvenance
		}
		if tx.SchemaVersion != 1 || tx.SessionID != session || tx.Seq != uint64(len(hashes)) || tx.PreviousHash != hashes[len(hashes)-1] || tx.CommittedAt.IsZero() || tx.TxID == "" || strings.TrimSpace(tx.Reason) == "" || len(tx.Operations) == 0 {
			return ErrProvenance
		}
		h := tx.Hash
		tx.Hash = ""
		if nativeHash(tx) != h {
			return ErrProvenance
		}
		hashes = append(hashes, h)
		for _, op := range tx.Operations {
			if !validEvidenceOperation(op) {
				return ErrProvenance
			}
			if op.Kind == "file_upsert" {
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
func validEvidenceOperation(o evidenceOperation) bool {
	allowed := []string{"kind"}
	p := path.IsAbs(o.Path) && path.Clean(o.Path) == o.Path && o.Path != "/"
	switch o.Kind {
	case "file_upsert":
		if !p || !digest.MatchString(o.Blob) || o.Size < 0 {
			return false
		}
		allowed = append(allowed, "path", "mode", "blob", "size")
	case "file_delete", "directory_delete":
		if !p {
			return false
		}
		allowed = append(allowed, "path")
	case "directory_ensure":
		if !p {
			return false
		}
		allowed = append(allowed, "path", "mode")
	case "symlink_set":
		if !p || o.Target == "" || path.IsAbs(o.Target) {
			return false
		}
		allowed = append(allowed, "path", "target")
	case "service_put", "service_delete":
		if o.Service == "" || o.Collection == "" || o.Key == "" {
			return false
		}
		allowed = append(allowed, "service", "collection", "key")
		if o.Kind == "service_put" {
			if !json.Valid(o.Value) {
				return false
			}
			allowed = append(allowed, "value")
		}
	case "injection_put", "artifact_put", "attempt_put":
		if o.ID == "" || !json.Valid(o.Value) {
			return false
		}
		allowed = append(allowed, "id", "value")
	case "injection_delete":
		if o.ID == "" {
			return false
		}
		allowed = append(allowed, "id")
	case "clock_set", "oracle_state_set", "budget_set", "idempotency_commit", "restored_from", "environment_materialized":
		if !json.Valid(o.Value) {
			return false
		}
		allowed = append(allowed, "value")
	default:
		return false
	}
	raw, _ := json.Marshal(o)
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	for k := range fields {
		if !slices.Contains(allowed, k) {
			return false
		}
	}
	return true
}
func (v *evidenceVerifier) files() error {
	var m evidenceManifest
	if _, err := v.archive.readNative("file-manifest.json", &m); err != nil {
		return err
	}
	d := m.Digest
	m.Digest = ""
	if m.SchemaVersion != 1 || nativeHash(m) != d {
		return ErrProvenance
	}
	previous := ""
	for _, e := range m.Entries {
		if e.Path <= previous || !path.IsAbs(e.Path) || path.Clean(e.Path) != e.Path {
			return ErrProvenance
		}
		previous = e.Path
		switch e.Type {
		case "file":
			if e.Size < 0 || e.Target != "" {
				return ErrProvenance
			}
			if err := v.blob(e.Blob, e.Size); err != nil {
				return err
			}
		case "directory":
			if e.Blob != "" || e.Target != "" {
				return ErrProvenance
			}
		case "symlink":
			if e.Target == "" || path.IsAbs(e.Target) || e.Blob != "" {
				return ErrProvenance
			}
		default:
			return ErrProvenance
		}
	}
	return nil
}
