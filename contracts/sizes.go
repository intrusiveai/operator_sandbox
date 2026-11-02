package contracts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// SizeLimits is host policy frozen in the verified startup context. These maxima
// protect bounded allocations and native snapshot pagination, not operating defaults.
type SizeLimits struct {
	DirectInput      int64 `json:"direct_input_bytes"`
	Assessment       int64 `json:"assessment_bytes"`
	Artifact         int64 `json:"artifact_bytes"`
	ArtifactPart     int64 `json:"artifact_part_bytes"`
	ObservationChunk int64 `json:"observation_chunk_bytes"`
	SnapshotPage     int64 `json:"snapshot_page_items"`
}

func (s SizeLimits) Valid() bool {
	return s.DirectInput > 0 && s.DirectInput <= DirectInputLimit &&
		s.Assessment > 0 && s.Assessment <= ConclusionLimit &&
		s.Artifact > 0 && s.Artifact <= 16<<20 &&
		s.ArtifactPart > 0 && s.ArtifactPart <= 1<<20 &&
		s.ObservationChunk > 0 && s.ObservationChunk <= 1<<20 &&
		s.SnapshotPage > 0 && s.SnapshotPage <= 100
}

func SizeLimitsFromContext(raw []byte) (SizeLimits, error) {
	var c struct {
		Sizes SizeLimits `json:"size_limits"`
	}
	if json.Unmarshal(raw, &c) != nil || !c.Sizes.Valid() {
		return SizeLimits{}, ErrProtocol
	}
	return c.Sizes, nil
}

func sizeIssue(path, setting string, actual, limit int64) error {
	return &ArgumentError{Reason: "configured_size_exceeded", FieldPath: path,
		Correction: fmt.Sprintf("Size %d exceeds configured %s=%d. Reduce the requested size; for direct input use a committed artifact receipt. Execution did not occur; correction is safe.", actual, setting, limit)}
}

// ValidateRequestSizes follows structural validation. No submitted content is
// included in its diagnostic; artifact receipts are resolved by the host.
func (p *Protocol) ValidateRequestSizes(raw []byte, limits SizeLimits) error {
	if !limits.Valid() {
		return ErrProtocol
	}
	q, err := p.ValidateRequest(raw)
	if err != nil {
		return err
	}
	b := q["body"].(map[string]any)
	check := func(path, name string, n, max int64) error {
		if n > max {
			return sizeIssue(path, name, n, max)
		}
		return nil
	}
	switch q["operation"] {
	case "engine.attempt_execute":
		for _, field := range []string{"input", "injection_input"} {
			if v, ok := b[field].(map[string]any); ok {
				x, e := p.ResolveDirectInput(v)
				if e != nil {
					return e
				}
				if x.ArtifactReceipt == "" {
					if e = check("/"+field, "direct_input_bytes", int64(len(x.Bytes)), limits.DirectInput); e != nil {
						return e
					}
				}
			}
		}
	case "engine.artifact_begin":
		limit, name := limits.Artifact, "artifact_bytes"
		if b["purpose"] == "conclusion" {
			limit, name = min(limit, limits.Assessment), "assessment_bytes"
		}
		return check("/artifact/size_bytes", name, number(b["artifact"].(map[string]any)["size_bytes"]), limit)
	case "engine.artifact_put_part":
		data, _ := base64.StdEncoding.DecodeString(b["content"].(string))
		return check("/content", "artifact_part_bytes", int64(len(data)), limits.ArtifactPart)
	case "engine.observation_read":
		return check("/max_bytes", "observation_chunk_bytes", number(b["max_bytes"]), limits.ObservationChunk)
	case "engine.snapshot_list":
		if n, ok := b["limit"]; ok {
			return check("/limit", "snapshot_page_items", number(n), limits.SnapshotPage)
		}
	case "engine.finish":
		if assessment, ok := b["assessment"]; ok {
			encoded, e := canonicalValue(assessment, OrdinaryLimit)
			if e != nil {
				return e
			}
			return check("/assessment", "assessment_bytes", int64(len(encoded)), limits.Assessment)
		}
	}
	return nil
}
