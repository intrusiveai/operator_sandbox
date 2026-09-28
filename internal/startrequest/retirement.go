//go:build linux || darwin

package startrequest

import (
	"context"
	"errors"
	"io"
	"os"
	"sort"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// ServiceRegistration is saved before contacting the service manager. Absence
// in an older request does not prove it was never submitted; cleanup probes the
// deterministic legacy name in the current installed user scope as well.
type ServiceRegistration struct {
	RequestDigest string `json:"request_digest"`
	Platform      string `json:"platform"`
	UID           int    `json:"uid"`
}

func (r ServiceRegistration) valid(digest string) bool {
	return r.RequestDigest == digest && (r.Platform == "linux" || r.Platform == "darwin") && r.UID >= 0 && (r.Platform != "darwin" || r.UID > 0)
}
func readAdministration(dir string, s *Snapshot) error {
	var r Retirement
	if _, e := read(dir, "retired.json", &r); e == nil {
		if r.RequestDigest != s.Digest || !timestamp(r.RecordedAt) {
			return ErrRecord
		}
		s.Retired = &r
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	var service ServiceRegistration
	if _, e := read(dir, "service.json", &service); e == nil {
		if !service.valid(s.Digest) {
			return ErrRecord
		}
		s.Service = &service
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}
func RegisterService(ctx context.Context, stateRoot, id, digest, platform string, uid int) error {
	lease, e := campaign.AcquireRetentionLease(stateRoot, false)
	if e != nil {
		return e
	}
	defer lease.Close()
	s, e := Read(stateRoot, id)
	if e != nil {
		return e
	}
	if s.Retired != nil {
		return ErrRetired
	}
	r := ServiceRegistration{digest, platform, uid}
	if digest != s.Digest || !r.valid(s.Digest) {
		return ErrRecord
	}
	if s.Service != nil {
		if *s.Service == r {
			return nil
		}
		return ErrConflict
	}
	root, _, e := open(stateRoot, id, false)
	if e != nil {
		return e
	}
	defer root.Close()
	raw, _, e := encode(r)
	if e != nil {
		return e
	}
	return put(ctx, root, "service.json", raw)
}

// Retire permanently closes admission while the installation's exclusive
// retention lease proves that no submitter or claimed worker is still active.
// Failed manager cleanup leaves this record in place for explicit retry.
func Retire(ctx context.Context, lease *campaign.RetentionLease, stateRoot, id, digest string) (Snapshot, error) {
	if !lease.ExclusiveFor(stateRoot) {
		return Snapshot{}, campaign.ErrActive
	}
	s, e := Read(stateRoot, id)
	if e != nil {
		return s, e
	}
	if s.Digest != digest {
		return s, ErrConflict
	}
	if s.Retired != nil {
		return s, nil
	}
	root, _, e := open(stateRoot, id, false)
	if e != nil {
		return s, e
	}
	defer root.Close()
	r := Retirement{digest, time.Now().UTC().Format(time.RFC3339Nano)}
	raw, _, e := encode(r)
	if e == nil {
		e = put(ctx, root, "retired.json", raw)
	}
	if e != nil {
		return s, e
	}
	s.Retired = &r
	return s, nil
}

// Inventory enumerates only verified host-owned start groups. An unknown or
// damaged group cannot silently disappear from an all-campaign purge selection.
func Inventory(stateRoot string) ([]Snapshot, error) { return InventoryExcept(stateRoot, nil) }

// InventoryExcept skips only IDs already bound by a verified purge transaction.
func InventoryExcept(stateRoot string, excluded map[string]bool) ([]Snapshot, error) {
	root, e := os.OpenRoot(stateRoot)
	if e != nil {
		return nil, e
	}
	defer root.Close()
	if e = privateDir(root, "."); e != nil {
		return nil, e
	}
	if e = privateDir(root, "starts"); errors.Is(e, os.ErrNotExist) {
		return []Snapshot{}, nil
	} else if e != nil {
		return nil, e
	}
	dir, e := root.Open("starts")
	if e != nil {
		return nil, e
	}
	defer dir.Close()
	names, e := dir.Readdirnames(100001)
	if e != nil && !errors.Is(e, io.EOF) {
		return nil, e
	}
	if len(names) > 100000 {
		return nil, ErrRecord
	}
	sort.Strings(names)
	result := make([]Snapshot, 0, len(names))
	for _, id := range names {
		if !idPattern.MatchString(id) {
			return nil, ErrRecord
		}
		if excluded[id] {
			continue
		}
		s, e := Read(stateRoot, id)
		if e != nil {
			return nil, e
		}
		result = append(result, s)
	}
	return result, nil
}
