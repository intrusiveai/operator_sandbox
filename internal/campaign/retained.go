//go:build linux || darwin

package campaign

import (
	"fmt"
	"os"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

var retainedPath = regexp.MustCompile(`^journals/[0-9]{16}/content/event-[0-9]{16}-0[0-7]\.bin$`)

// AppendStored returns the committed descriptors under the same writer lock as
// append. Callers never derive a path from an unlocked sequence counter.
func (w *Writer) AppendStored(entry Entry, reservation string, release bool) ([]ContentDescriptor, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var change *reservationChange
	if reservation != "" {
		change = &reservationChange{ID: reservation, Action: "consume", Release: release}
	}
	if _, err := w.appendLocked(entry, change); err != nil {
		return nil, err
	}
	out := []ContentDescriptor{}
	for i, c := range entry.Content {
		out = append(out, ContentDescriptor{c.Role, c.MediaType, contentPath(entry.RunRevision, w.head.Sequence, i), int64(len(c.Bytes)), contracts.RawDigest(c.Bytes)})
	}
	return out, nil
}

// ReadContent reads a trusted retained descriptor using private no-follow files.
// The caller decides whether loss is optional feedback unavailability or critical
// journal corruption. A descriptor is never accepted directly from a guest.
func (w *Writer) ReadContent(d ContentDescriptor) ([]byte, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return nil, err
	}
	return readRetainedContent(w.root, d)
}

func readRetainedContent(root *os.Root, d ContentDescriptor) ([]byte, error) {
	if !retainedPath.MatchString(d.Path) || d.SizeBytes < 0 || d.SizeBytes > MaxContentBytes || !validDigest(d.Digest) {
		return nil, ErrInvalid
	}
	raw, err := readFile(root, d.Path, MaxContentBytes)
	if err != nil || int64(len(raw)) != d.SizeBytes || contracts.RawDigest(raw) != d.Digest {
		return nil, ErrCorrupt
	}
	return raw, nil
}
func (w *Writer) Manifest() RunManifest {
	w.mu.Lock()
	defer w.mu.Unlock()
	raw, _ := w.manifest.Bytes()
	m, _ := ParseManifest(raw)
	return m
}

func publicationReservation(id string) string {
	return "publication:" + contracts.RawDigest([]byte(id))[7:]
}
func publicationRole(index int) string { return fmt.Sprintf("feedback-%d", index) }

// Revision reports the latest journal revision for final host lifecycle records.
// It is not a live target binding and must not authorize native requests.
func (w *Writer) Revision() int64 { w.mu.Lock(); defer w.mu.Unlock(); return w.revision }
