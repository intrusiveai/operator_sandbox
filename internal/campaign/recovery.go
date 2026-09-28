//go:build linux || darwin

package campaign

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

type RecoveredOperation struct {
	OperationID       string
	IdentityDigest    string
	LastRecordedState string
	Outcome           string // RESULT_COMMITTED or UNKNOWN; never execution permission.
}

type Inspection struct {
	Manifest       RunManifest
	ManifestDigest string
	VerifiedEvents int64
	VerifiedBytes  int64
	JournalIntact  bool
	Operations     []RecoveredOperation
	Reservations   []Reservation
}

// Inspect takes a nonblocking writer lock and streams the committed journal.
// On corruption it returns the verified prefix plus an error, without repairing,
// truncating, dispatching, or reopening execution. Even JournalIntact=true is a
// cleanup/reporting result, never a campaign-resume grant. Visitors receive only
// verified individual events; the final result still determines overall integrity.
func Inspect(stateRoot, campaignID string, visit func(Event) error) (report Inspection, err error) {
	return inspect(context.Background(), stateRoot, campaignID, visit, false)
}

// Observation describes only the captured committed prefix. It says nothing about
// a pending tail, worker health, or permission to resume execution.
type Observation struct {
	Manifest       RunManifest
	ManifestDigest string
	VerifiedEvents int64
	VerifiedBytes  int64
	Operations     []RecoveredOperation
	Reservations   []Reservation
}

// Observe verifies one atomic journal-head snapshot without acquiring the writer
// lock. Later appends and incomplete publication are outside this observation.
// Visitors stream individually verified events; callers must also check the final
// error before treating the captured prefix as complete.
func Observe(ctx context.Context, stateRoot, campaignID string, visit func(Event) error) (Observation, error) {
	r, err := inspect(ctx, stateRoot, campaignID, visit, true)
	return Observation{r.Manifest, r.ManifestDigest, r.VerifiedEvents, r.VerifiedBytes, r.Operations, r.Reservations}, err
}

func inspect(ctx context.Context, stateRoot, campaignID string, visit func(Event) error, observing bool) (report Inspection, err error) {
	lease, e := AcquireRetentionLease(stateRoot, false)
	if e != nil {
		return report, e
	}
	defer lease.Close()
	if err := ctx.Err(); err != nil {
		return report, err
	}
	r, err := openCampaign(stateRoot, campaignID)
	if err != nil {
		return report, err
	}
	defer r.Close()
	if !observing {
		lockFile, err := lock(r, false)
		if err != nil {
			return report, err
		}
		defer lockFile.Close()
	}
	return inspectRoot(ctx, r, campaignID, visit, observing)
}

// The caller owns the root and, for strict inspection, its writer lock.
func inspectRoot(ctx context.Context, r *os.Root, campaignID string, visit func(Event) error, observing bool) (report Inspection, err error) {
	m, digest, err := loadManifest(r, campaignID)
	if err != nil {
		return report, err
	}
	report.Manifest, report.ManifestDigest = m, digest
	if err := privateDir(r, "journals"); err != nil {
		return report, err
	}
	readHead := readFile
	if observing {
		readHead = readAtomicHead
	}
	rawHead, err := readHead(r, "journal-head.json", ManifestLimit)
	if err != nil {
		return report, err
	}
	var head journalHead
	if decode(rawHead, &head, ManifestLimit) != nil || head.APIVersion != JournalVersion || head.Sequence < 0 || head.Sequence > contracts.MaxSafeInteger ||
		!validDigest(head.Digest) || head.Bytes < 0 || head.Bytes > m.Retention.MaxJournalBytes {
		return report, ErrCorrupt
	}
	operations := map[string]OperationMark{}
	reservations := newReservationBook()
	defer func() {
		for id, remaining := range reservations.remaining {
			report.Reservations = append(report.Reservations, Reservation{id, remaining})
		}
		sort.Slice(report.Reservations, func(i, j int) bool { return report.Reservations[i].ID < report.Reservations[j].ID })
		for _, op := range operations {
			outcome := Unknown
			if op.State == ResultCommitted {
				outcome = ResultCommitted
			}
			report.Operations = append(report.Operations, RecoveredOperation{op.OperationID, op.IdentityDigest, op.State, outcome})
		}
		sort.Slice(report.Operations, func(i, j int) bool { return report.Operations[i].OperationID < report.Operations[j].OperationID })
	}()
	dirs, err := directoryNames(r, "journals")
	if err != nil {
		return report, err
	}
	previous := digest
	for _, revisionName := range dirs {
		if observing && report.VerifiedEvents == head.Sequence {
			break
		}
		if err := ctx.Err(); err != nil {
			return report, err
		}
		revision, e := strconv.ParseInt(revisionName, 10, 64)
		if e != nil || revision < m.InitialRevision || revision > contracts.MaxSafeInteger || fmt.Sprintf("%016d", revision) != revisionName {
			return report, ErrCorrupt
		}
		dir := "journals/" + revisionName
		if err := privateDir(r, dir); err != nil {
			return report, ErrCorrupt
		}
		files, err := directoryNames(r, dir)
		if err != nil {
			return report, err
		}
		if len(files) < 2 || files[0] != "content" {
			return report, ErrCorrupt
		}
		if err := privateDir(r, dir+"/content"); err != nil {
			return report, ErrCorrupt
		}
		contentNames := map[string]bool{}
		for i, file := range files[1:] {
			if observing && report.VerifiedEvents == head.Sequence {
				break
			}
			if file != fmt.Sprintf("events-%06d.jsonl", i+1) {
				return report, ErrCorrupt
			}
			f, err := openRegular(r, dir+"/"+file, os.O_RDONLY)
			if err != nil {
				return report, err
			}
			info, err := f.Stat()
			if err != nil || info.Size() <= 0 || info.Size() > m.Retention.MaxSegmentBytes {
				f.Close()
				return report, ErrCorrupt
			}
			err = func() error {
				defer f.Close()
				reader := bufio.NewReaderSize(io.LimitReader(f, m.Retention.MaxSegmentBytes+1), MaxEventBytes+1)
				for {
					if err := ctx.Err(); err != nil {
						return err
					}
					if observing && report.VerifiedEvents == head.Sequence {
						return nil
					}
					line, err := reader.ReadSlice('\n')
					if errors.Is(err, io.EOF) && len(line) == 0 {
						return nil
					}
					if err != nil || len(line) > MaxEventBytes+1 {
						return ErrCorrupt
					}
					var env envelope
					if decode(line[:len(line)-1], &env, MaxEventBytes) != nil {
						return ErrCorrupt
					}
					ev := env.Event
					canonical, err := encode(ev, MaxEventBytes)
					if err != nil || env.Digest != contracts.RawDigest(canonical) || ev.APIVersion != JournalVersion || ev.CampaignID != m.CampaignID || ev.LaunchID != m.LaunchID ||
						ev.RunManifestDigest != digest || ev.PreviousDigest != previous || ev.Sequence != report.VerifiedEvents+1 || ev.Sequence > head.Sequence || ev.RunRevision != revision ||
						!validTime(ev.RecordedAt) || !validID(ev.Kind) || !validateMetadata(ev.Metadata) || ev.Content == nil || len(ev.Content) > MaxContents || !operationNext(operations, ev.Operation) {
						return ErrCorrupt
					}
					cost := int64(len(line))
					roles := map[string]bool{}
					for j, d := range ev.Content {
						if !validID(d.Role) || roles[d.Role] || !validMedia(d.MediaType) || d.Path != contentPath(revision, ev.Sequence, j) || d.SizeBytes < 0 || d.SizeBytes > MaxContentBytes || !validDigest(d.Digest) {
							return ErrCorrupt
						}
						roles[d.Role] = true
						raw, err := readFile(r, d.Path, MaxContentBytes)
						if err != nil || int64(len(raw)) != d.SizeBytes || contracts.RawDigest(raw) != d.Digest {
							return ErrCorrupt
						}
						contentNames[strings.TrimPrefix(d.Path, dir+"/content/")] = true
						cost += d.SizeBytes
					}
					if cost > head.Bytes-report.VerifiedBytes {
						return ErrCorrupt
					}
					change, err := reservationFromMetadata(ev.Metadata)
					if err != nil {
						return ErrCorrupt
					}
					total, err := reservations.prepare(change, cost, report.VerifiedBytes, m.Retention.MaxJournalBytes)
					if err != nil {
						return ErrCorrupt
					}
					reservations.commit(change, cost, total)
					report.VerifiedEvents++
					report.VerifiedBytes += cost
					previous = env.Digest
					if ev.Operation != nil {
						operations[ev.Operation.OperationID] = *ev.Operation
					}
					if visit != nil {
						if err := visit(ev); err != nil {
							return err
						}
					}
				}
			}()
			if err != nil {
				return report, err
			}
		}
		if observing {
			continue
		}
		actual, err := directoryNames(r, dir+"/content")
		if err != nil || len(actual) != len(contentNames) {
			return report, ErrCorrupt
		}
		for _, name := range actual {
			if !contentNames[name] {
				return report, ErrCorrupt
			}
		}
	}
	if report.VerifiedEvents != head.Sequence || report.VerifiedBytes != head.Bytes || previous != head.Digest {
		return report, ErrCorrupt
	}
	if observing {
		return report, ctx.Err()
	}
	// An interrupted head publication makes persistence uncertain even if the old
	// committed prefix remains valid. Recovery preserves it without silent repair.
	if _, err := r.Lstat("journal-head.json.pending"); !errors.Is(err, os.ErrNotExist) {
		return report, ErrCorrupt
	}
	report.JournalIntact = true
	return report, nil
}

// Directory scans are bounded independently of file sizes. Only the trusted host
// writes these directories; this also bounds inspection of damaged inventories.
const maxDirectoryEntries = 100000

func directoryNames(r *os.Root, dir string) ([]string, error) {
	f, err := r.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(maxDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if len(entries) > maxDirectoryEntries {
		return nil, ErrCorrupt
	}
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	sort.Strings(names)
	return names, nil
}

// Unlike immutable journal files, the head is atomically replaced. Opening first
// pins either complete inode without a false Lstat/open race during publication.
func readAtomicHead(r *os.Root, name string, maximum int) ([]byte, error) {
	f, err := r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > int64(maximum) {
		return nil, ErrCorrupt
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if err != nil || len(raw) > maximum {
		return nil, ErrCorrupt
	}
	return raw, nil
}
