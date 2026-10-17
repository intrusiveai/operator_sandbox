//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"github.com/intrusiveai/operator_sandbox/contracts"
)

// Compatibility retains the exact validated file bytes and the independently
// resolved image identity. The record cannot contain its own final image digest.
type Compatibility struct {
	record          Record
	raw             []byte
	digest, imageID string
}

func (c Compatibility) Record() Record { return c.record }
func (c Compatibility) Bytes() []byte  { return bytes.Clone(c.raw) }
func (c Compatibility) Digest() string { return c.digest }
func (c Compatibility) Compatible(imageID string, h Requirements) error {
	if c.digest == "" || c.imageID != imageID {
		return ErrCompatibility
	}
	return c.record.Compatible(h)
}

// Validate checks a local compatibility file and binds it to its inspected image.
// Callers must read the bytes from that exact image before admitting execution.
func Validate(raw []byte, imageID string, h Requirements) (Compatibility, error) {
	if !digestPattern.MatchString(imageID) {
		return Compatibility{}, ErrRecord
	}
	record, err := Parse(raw)
	if err != nil {
		return Compatibility{}, err
	}
	if err := record.Compatible(h); err != nil {
		return Compatibility{}, err
	}
	return Compatibility{record: record, raw: bytes.Clone(raw), digest: contracts.RawDigest(raw), imageID: imageID}, nil
}
