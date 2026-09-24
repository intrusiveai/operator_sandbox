//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"math"
	"os"
	"syscall"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

const reservationKey = "journal_reservation"

// Reservation directives occupy a host-reserved metadata key. Keeping them in
// metadata preserves the v1alpha1 event/digest format and existing journals.
type reservationChange struct {
	ID      string `json:"id"`
	Action  string `json:"action"`
	Bytes   int64  `json:"bytes"`
	Release bool   `json:"release"`
}

type Reservation struct {
	ID             string
	RemainingBytes int64
}

type reservationBook struct {
	remaining map[string]int64
	used      map[string]bool
	total     int64
}

func newReservationBook() reservationBook {
	return reservationBook{remaining: map[string]int64{}, used: map[string]bool{}}
}

// prepare checks without changing state; commit runs only after the journal head
// is durable. Consumed bytes become journal usage, never available quota again.
func (b *reservationBook) prepare(c *reservationChange, cost, spent, maximum int64) (int64, error) {
	total := b.total
	if c != nil {
		if !validID(c.ID) {
			return 0, ErrInvalid
		}
		switch c.Action {
		case "reserve":
			if c.Bytes < 1 || c.Bytes > contracts.MaxSafeInteger || c.Release || b.used[c.ID] {
				return 0, ErrInvalid
			}
			if c.Bytes > maximum-total {
				return 0, ErrQuota
			}
			total += c.Bytes
		case "consume":
			remaining, ok := b.remaining[c.ID]
			if !ok || c.Bytes != 0 {
				return 0, ErrInvalid
			}
			if cost > remaining {
				return 0, ErrQuota
			}
			total -= cost
			if c.Release {
				total -= remaining - cost
			}
		default:
			return 0, ErrInvalid
		}
	}
	if spent > maximum-total || cost > maximum-total-spent {
		return 0, ErrQuota
	}
	return total, nil
}

func (b *reservationBook) commit(c *reservationChange, cost, total int64) {
	b.total = total
	if c == nil {
		return
	}
	if c.Action == "reserve" {
		b.remaining[c.ID], b.used[c.ID] = c.Bytes, true
	} else if c.Release {
		delete(b.remaining, c.ID)
	} else {
		b.remaining[c.ID] -= cost
	}
}

func reservationFromMetadata(raw []byte) (*reservationChange, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, ErrInvalid
	}
	encoded, ok := object[reservationKey]
	if !ok {
		return nil, nil
	}
	var c reservationChange
	if err := decode(encoded, &c, MaxMetadataBytes); err != nil {
		return nil, err
	}
	return &c, nil
}

func addReservation(raw []byte, c *reservationChange) ([]byte, error) {
	if !validateMetadata(raw) {
		return nil, ErrInvalid
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, ErrInvalid
	}
	if _, exists := object[reservationKey]; exists {
		return nil, ErrInvalid
	}
	if c != nil {
		encoded, err := encode(c, MaxMetadataBytes)
		if err != nil {
			return nil, err
		}
		object[reservationKey] = encoded
	}
	return encode(object, MaxMetadataBytes)
}

// AppendReserving commits an entry and earmarks future journal capacity in one
// transaction. The caller supplies a trusted worst-case audit bound, not guest data.
func (w *Writer) AppendReserving(entry Entry, id string, bytes int64) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.appendLocked(entry, &reservationChange{ID: id, Action: "reserve", Bytes: bytes})
}

// AppendReserved spends only this reservation. release frees its unused remainder
// after a successful commit; it never refunds already retained journal bytes.
func (w *Writer) AppendReserved(entry Entry, id string, release bool) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.appendLocked(entry, &reservationChange{ID: id, Action: "consume", Release: release})
}

// ConfigureFreeSpace must run before the first journal event. The policy is then
// durably recorded, and must be selected before any reservation can be made.
// This is a filesystem preflight plus a journal budget reservation, not a claim
// of exclusive physical disk allocation against other host processes/campaigns.
func (w *Writer) ConfigureFreeSpace(minimumFreeBytes int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.ready(); err != nil {
		return err
	}
	if w.head.Sequence != 0 || w.spaceConfigured || minimumFreeBytes < 0 || minimumFreeBytes > contracts.MaxSafeInteger {
		return ErrInvalid
	}
	metadata, _ := encode(map[string]any{"minimum_free_bytes": minimumFreeBytes}, MaxMetadataBytes)
	if _, err := w.appendLocked(Entry{RunRevision: w.revision, Kind: "journal.storage-policy", Metadata: metadata}, nil); err != nil {
		return err
	}
	w.minimumFreeBytes, w.spaceConfigured = minimumFreeBytes, true
	return nil
}

func filesystemAvailable(r *os.Root) (int64, error) {
	f, err := r.Open(".")
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var stat syscall.Statfs_t
	if err := syscall.Fstatfs(int(f.Fd()), &stat); err != nil {
		return 0, err
	}
	if stat.Bsize <= 0 {
		return 0, ErrStorage
	}
	blocks, size := uint64(stat.Bavail), uint64(stat.Bsize)
	if blocks > math.MaxInt64/size {
		return math.MaxInt64, nil
	}
	return int64(blocks * size), nil
}
