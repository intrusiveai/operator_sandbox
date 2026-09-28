//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const MaxEventBytes = 256 << 10
const MaxMetadataBytes = 64 << 10
const MaxContentBytes = contracts.OrdinaryLimit
const MaxContents = 8
const JournalVersion = "operator.dev/journal/v1alpha1"

const (
	IntentCommitted = "INTENT_COMMITTED"
	Dispatched      = "DISPATCHED"
	ResultCommitted = "RESULT_COMMITTED"
	Unknown         = "UNKNOWN"
)

type OperationMark struct {
	OperationID    string `json:"operation_id"`
	IdentityDigest string `json:"identity_digest"`
	State          string `json:"state"`
}

type Content struct {
	Role      string
	MediaType string
	Bytes     []byte
}

type ContentDescriptor struct {
	Role      string `json:"role"`
	MediaType string `json:"media_type"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	Digest    string `json:"digest"`
}

// Metadata is a bounded JSON object for host-verified attribution, receipts,
// dispositions and charges. Exact request/response bodies belong in Content.
// Callers must exclude credentials and validate operation-specific semantics.
type Entry struct {
	RunRevision int64
	Kind        string
	Operation   *OperationMark
	Metadata    json.RawMessage
	Content     []Content
}

type Event struct {
	APIVersion        string              `json:"api_version"`
	Sequence          int64               `json:"sequence"`
	CampaignID        string              `json:"campaign_id"`
	LaunchID          string              `json:"launch_id"`
	RunManifestDigest string              `json:"run_manifest_digest"`
	RunRevision       int64               `json:"run_revision"`
	RecordedAt        string              `json:"recorded_at"`
	Kind              string              `json:"kind"`
	Operation         *OperationMark      `json:"operation"`
	Metadata          json.RawMessage     `json:"metadata"`
	Content           []ContentDescriptor `json:"content"`
	PreviousDigest    string              `json:"previous_digest"`
}

type envelope struct {
	Event  Event  `json:"event"`
	Digest string `json:"digest"`
}
type journalHead struct {
	APIVersion string `json:"api_version"`
	Sequence   int64  `json:"sequence"`
	Digest     string `json:"digest"`
	Bytes      int64  `json:"bytes"`
}

// Writer is created once, for a fresh campaign. No API reopens a writer from
// retained evidence. Methods are serialized; the independent Docker reader is not.
type Writer struct {
	retention        *RetentionLease
	mu               sync.Mutex
	root             *os.Root
	lockFile         *os.File
	segment          *os.File
	segmentSize      int64
	segmentNumber    int
	revisionContents int
	revisionCount    int
	revision         int64
	manifest         RunManifest
	manifestDigest   string
	head             journalHead
	operations       map[string]OperationMark
	hooks            ioHooks
	failure          error
	closed           bool
	fence            *Fence
	reservations     reservationBook
	minimumFreeBytes int64
	spaceConfigured  bool
	attemptOwner     bool
}

// Create requires an existing private state root. A campaign ID is never reused,
// even when an earlier preparation failed. Failed preparations remain inspectable.
func Create(stateRoot string, manifest RunManifest) (*Writer, error) {
	lease, err := AcquireRetentionLease(stateRoot, false)
	if err != nil {
		return nil, err
	}
	kept := false
	defer func() {
		if !kept {
			lease.Close()
		}
	}()
	raw, err := manifest.Bytes()
	if err != nil {
		return nil, err
	}
	if err = CheckNotPurging(stateRoot, manifest.CampaignID); err != nil {
		return nil, err
	}
	// Freeze caller-owned maps/raw messages before retaining the manifest.
	manifest, err = ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	state, err := os.OpenRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	defer state.Close()
	if err = privateDir(state, "."); err != nil {
		return nil, err
	}
	if err = mkdir(state, "campaigns"); err != nil {
		return nil, err
	}
	campaignPath := "campaigns/" + manifest.CampaignID
	if err = state.Mkdir(campaignPath, 0700); err != nil {
		return nil, err
	}
	if err = syncDir(state, "campaigns"); err != nil {
		return nil, err
	}
	r, err := state.OpenRoot(campaignPath)
	if err != nil {
		return nil, err
	}
	w := &Writer{retention: lease, root: r, manifest: manifest, manifestDigest: contracts.RawDigest(raw), revision: manifest.InitialRevision,
		operations: map[string]OperationMark{}, hooks: diskHooks(), fence: NewFence(), reservations: newReservationBook()}
	ok := false
	defer func() {
		if !ok {
			w.Close()
		}
	}()
	if w.lockFile, err = lock(r, true); err != nil {
		return nil, err
	}
	for _, dir := range []string{"launch", "journals", "artifacts", "revisions", "reports"} {
		if err = mkdir(r, dir); err != nil {
			return nil, err
		}
	}
	if err = publish(r, "launch/run-manifest.json", raw, false, &w.hooks); err != nil {
		return nil, err
	}
	cr, err := encode(campaignRecord{"operator.dev/campaign/v1alpha1", manifest.CampaignID, manifest.LaunchID, w.manifestDigest}, ManifestLimit)
	if err != nil {
		return nil, err
	}
	if err = publish(r, "campaign.json", cr, false, &w.hooks); err != nil {
		return nil, err
	}
	w.head = journalHead{JournalVersion, 0, w.manifestDigest, 0}
	h, _ := encode(w.head, ManifestLimit)
	if err = publish(r, "journal-head.json", h, false, &w.hooks); err != nil {
		return nil, err
	}
	ok = true
	kept = true
	return w, nil
}

func (w *Writer) ManifestDigest() string { return w.manifestDigest }

// Fence can be read/stopped without acquiring the writer mutex.
func (w *Writer) Fence() *Fence { return w.fence }

func (w *Writer) ready() error {
	if w.failure != nil {
		return w.failure
	}
	if w.closed {
		return ErrClosed
	}
	return nil
}
func (w *Writer) fail(err error) error {
	w.failure = errors.Join(ErrStorage, err)
	w.fence.Stop(w.failure)
	return w.failure
}

// Failure reports the writer's latched failure under the writer mutex.
// Callers must serialize dispatch after Append; this is not a runtime kill path.
// Do not call it as a prerequisite for administrator termination.
func (w *Writer) Failure() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ready()
}

// SaveDockerBinding must succeed before Docker start. Exact repeat calls are
// idempotent; a different binding can never replace the launch's saved identity.
func (w *Writer) SaveDockerBinding(b DockerBinding) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return err
	}
	if err := b.validate(w.manifest, w.manifestDigest); err != nil {
		return err
	}
	raw, err := encode(b, ManifestLimit)
	if err != nil {
		return err
	}
	existing, err := readFile(w.root, "launch/docker-binding.json", ManifestLimit)
	if err == nil {
		if contracts.RawDigest(existing) == contracts.RawDigest(raw) {
			return nil
		}
		return ErrInvalid
	}
	if !errors.Is(err, os.ErrNotExist) {
		return w.fail(err)
	}
	if err = publish(w.root, "launch/docker-binding.json", raw, false, &w.hooks); err != nil {
		return w.fail(err)
	}
	return nil
}

func validateMetadata(raw []byte) bool {
	v, err := contracts.Decode(raw, MaxMetadataBytes)
	_, ok := v.(map[string]any)
	return err == nil && ok
}

func operationNext(old map[string]OperationMark, mark *OperationMark) bool {
	if mark == nil {
		return true
	}
	if !validID(mark.OperationID) || !validDigest(mark.IdentityDigest) {
		return false
	}
	previous, exists := old[mark.OperationID]
	if exists && previous.IdentityDigest != mark.IdentityDigest {
		return false
	}
	switch mark.State {
	case IntentCommitted:
		return !exists
	case Dispatched:
		return exists && previous.State == IntentCommitted
	case ResultCommitted, Unknown:
		return exists && (previous.State == IntentCommitted || previous.State == Dispatched)
	default:
		return false
	}
}

func contentPath(revision, seq int64, index int) string {
	return fmt.Sprintf("journals/%016d/content/event-%016d-%02d.bin", revision, seq, index)
}

// Append commits content, a hash-linked event, then a durable head. A success
// permits the caller to use the retained record; an error never permits dispatch
// or delivery based on this event. I/O/quota failures permanently fence this writer.
func (w *Writer) Append(entry Entry) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.appendLocked(entry, nil)
}

func (w *Writer) appendLocked(entry Entry, reservation *reservationChange) (string, error) {
	if err := w.ready(); err != nil {
		return "", err
	}
	metadata, err := addReservation(entry.Metadata, reservation)
	if err != nil {
		return "", err
	}
	entry.Metadata = metadata
	if reservation != nil && reservation.Action == "reserve" && !w.spaceConfigured {
		return "", ErrInvalid
	}
	if reservation != nil && reservation.Action == "reserve" {
		if err := w.fence.Err(); err != nil {
			return "", err
		}
	}
	if !validID(entry.Kind) || !validateMetadata(entry.Metadata) || entry.RunRevision < w.revision || entry.RunRevision > contracts.MaxSafeInteger ||
		len(entry.Content) > MaxContents || !operationNext(w.operations, entry.Operation) || w.head.Sequence == contracts.MaxSafeInteger {
		return "", ErrInvalid
	}
	ev := Event{APIVersion: JournalVersion, Sequence: w.head.Sequence + 1, CampaignID: w.manifest.CampaignID, LaunchID: w.manifest.LaunchID,
		RunManifestDigest: w.manifestDigest, RunRevision: entry.RunRevision, RecordedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Kind: entry.Kind, Operation: entry.Operation, Metadata: entry.Metadata, Content: []ContentDescriptor{}, PreviousDigest: w.head.Digest}
	var contentSize int64
	roles := map[string]bool{}
	for i, c := range entry.Content {
		if !validID(c.Role) || roles[c.Role] || !validMedia(c.MediaType) || len(c.Bytes) > MaxContentBytes {
			return "", ErrInvalid
		}
		roles[c.Role] = true
		contentSize += int64(len(c.Bytes))
		ev.Content = append(ev.Content, ContentDescriptor{c.Role, c.MediaType, contentPath(ev.RunRevision, ev.Sequence, i), int64(len(c.Bytes)), contracts.RawDigest(c.Bytes)})
	}
	rawEvent, err := encode(ev, MaxEventBytes)
	if err != nil {
		return "", ErrInvalid
	}
	digest := contracts.RawDigest(rawEvent)
	line, err := encode(envelope{ev, digest}, MaxEventBytes)
	if err != nil {
		return "", ErrInvalid
	}
	line = append(line, '\n')
	cost := int64(len(line)) + contentSize
	reservedTotal, err := w.reservations.prepare(reservation, cost, w.head.Bytes, w.manifest.Retention.MaxJournalBytes)
	if err != nil {
		if errors.Is(err, ErrQuota) {
			return "", w.fail(err)
		}
		return "", err
	}
	if w.spaceConfigured {
		available, err := w.hooks.available(w.root)
		if err != nil {
			return "", w.fail(err)
		}
		if available < w.minimumFreeBytes || reservedTotal > available-w.minimumFreeBytes || cost > available-w.minimumFreeBytes-reservedTotal {
			return "", w.fail(ErrQuota)
		}
	}
	contentCount := w.revisionContents
	if ev.RunRevision != w.revision {
		contentCount = 0
	}
	if contentCount+len(ev.Content) > maxDirectoryEntries {
		return "", w.fail(ErrQuota)
	}
	if err := w.prepareSegment(ev.RunRevision, int64(len(line))); err != nil {
		return "", w.fail(err)
	}
	for i, c := range entry.Content {
		if err := publish(w.root, ev.Content[i].Path, c.Bytes, false, &w.hooks); err != nil {
			return "", w.fail(err)
		}
	}
	if n, err := w.segment.Write(line); err != nil || n != len(line) {
		return "", w.fail(errors.Join(err, io.ErrShortWrite))
	}
	if err := w.hooks.sync(w.segment); err != nil {
		return "", w.fail(err)
	}
	head := journalHead{JournalVersion, ev.Sequence, digest, w.head.Bytes + cost}
	rawHead, _ := encode(head, ManifestLimit)
	if err := publish(w.root, "journal-head.json", rawHead, true, &w.hooks); err != nil {
		return "", w.fail(err)
	}
	w.head = head
	w.reservations.commit(reservation, cost, reservedTotal)
	w.revision = ev.RunRevision
	w.segmentSize += int64(len(line))
	w.revisionContents = contentCount + len(ev.Content)
	if ev.Operation != nil {
		w.operations[ev.Operation.OperationID] = *ev.Operation
	}
	return digest, nil
}

func validMedia(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if c < 32 || c > 126 {
			return false
		}
	}
	return true
}

func (w *Writer) prepareSegment(revision, size int64) error {
	if size > w.manifest.Retention.MaxSegmentBytes {
		return ErrQuota
	}
	if w.segment != nil && revision == w.revision && w.segmentSize+size <= w.manifest.Retention.MaxSegmentBytes {
		return nil
	}
	if w.segment != nil {
		if err := w.segment.Close(); err != nil {
			return err
		}
		w.segment = nil
	}
	if revision != w.revision {
		w.segmentNumber = 0
	}
	if w.segmentNumber == 0 {
		if w.revisionCount == maxDirectoryEntries {
			return ErrQuota
		}
		w.revisionCount++
	}
	w.segmentNumber++
	if w.segmentNumber >= maxDirectoryEntries {
		return ErrQuota
	}
	dir := fmt.Sprintf("journals/%016d", revision)
	for _, d := range []string{dir, dir + "/content"} {
		if err := mkdir(w.root, d); err != nil {
			return err
		}
	}
	file := fmt.Sprintf("%s/events-%06d.jsonl", dir, w.segmentNumber)
	f, err := w.root.OpenFile(file, os.O_WRONLY|os.O_APPEND|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	w.segment = f
	w.segmentSize = 0
	return w.hooks.syncDir(w.root, path.Dir(file))
}

// Close releases resources. It does not assert campaign completion, container exit,
// or successful effect resolution. A closed/failed campaign cannot be reopened.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	w.fence.Stop(ErrClosed)
	var errs []error
	if w.segment != nil {
		errs = append(errs, w.segment.Close())
	}
	if w.lockFile != nil {
		errs = append(errs, w.lockFile.Close())
	}
	if w.root != nil {
		errs = append(errs, w.root.Close())
	}
	errs = append(errs, w.retention.Close())
	return errors.Join(errs...)
}
