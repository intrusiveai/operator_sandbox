package operationpolicy

import (
	"errors"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

var ErrSizes = errors.New("size_limits must use documented positive integer allowances within wire safety ceilings")

// SizeDefaults are operating choices owned by Operator, never protocol constants.
func SizeDefaults() contracts.SizeLimits {
	return contracts.SizeLimits{DirectInput: 32768, Assessment: 1 << 20, Artifact: 16 << 20,
		ArtifactPart: 256 << 10, ObservationChunk: 256 << 10, SnapshotPage: 100}
}

func ResolveSizes(overrides map[string]int64) (contracts.SizeLimits, error) {
	s := SizeDefaults()
	fields := map[string]*int64{"direct_input_bytes": &s.DirectInput, "assessment_bytes": &s.Assessment,
		"artifact_bytes": &s.Artifact, "artifact_part_bytes": &s.ArtifactPart,
		"observation_chunk_bytes": &s.ObservationChunk, "snapshot_page_items": &s.SnapshotPage}
	for name, n := range overrides {
		p, ok := fields[name]
		if !ok {
			return contracts.SizeLimits{}, ErrSizes
		}
		*p = n
	}
	if !s.Valid() {
		return contracts.SizeLimits{}, ErrSizes
	}
	return s, nil
}

// ForLaunch permits zero only as the host API's explicit default selection.
func SizesForLaunch(s contracts.SizeLimits) (contracts.SizeLimits, error) {
	if s == (contracts.SizeLimits{}) {
		s = SizeDefaults()
	}
	if !s.Valid() {
		return s, ErrSizes
	}
	return s, nil
}
