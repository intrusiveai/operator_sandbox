//go:build linux || darwin

package transport

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

type observedFile struct {
	digest   string
	consumed bool
}
type spool struct {
	root        *os.Root
	roots       [4]*os.Root
	identities  [4]os.FileInfo
	known       [4]map[int64]observedFile
	temporary   [4]map[string]time.Time
	outstanding [4][]message
	ackDirty    bool
	nextScan    time.Time
	bytes       int64
	guestGID    int
}

// NewSpool creates fresh lanes inside an existing empty private launch directory.
// The launcher supplies guest access to outbound lanes and read-only inbound mounts.
// Neither old messages nor a previous launch directory can be reopened.
func NewSpool(dir string, c Config) (*Session, error) {
	s, err := newSession(c)
	if err != nil {
		return nil, err
	}
	r, err := freshRoot(dir)
	if err != nil {
		return nil, err
	}
	s.directory, err = filepath.Abs(dir)
	if err != nil {
		r.Close()
		return nil, err
	}
	s.directoryInfo, err = os.Lstat(dir)
	if err != nil {
		r.Close()
		return nil, err
	}
	gid, err := guestGroup(c)
	if err != nil {
		r.Close()
		return nil, err
	}
	d := &spool{root: r, guestGID: gid}
	s.driver = d
	ok := false
	defer func() {
		if !ok {
			d.close()
		}
	}()
	for i, lane := range lanes {
		if err := r.Mkdir(lane, 0700); err != nil {
			return nil, err
		}
		if err := r.Chown(lane, -1, gid); err != nil {
			return nil, err
		}
		mode := os.FileMode(0750)
		if i%2 == 1 {
			mode = 0770
		}
		if err := r.Chmod(lane, mode); err != nil {
			return nil, err
		}
		d.identities[i], err = r.Lstat(lane)
		if err != nil {
			return nil, err
		}
		d.roots[i], err = r.OpenRoot(lane)
		if err != nil {
			return nil, err
		}
		d.known[i] = map[int64]observedFile{}
		d.temporary[i] = map[string]time.Time{}
	}
	if err := d.scanSize(s, s.now()); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}
func (d *spool) interval() time.Duration { return SpoolPoll }
func (d *spool) close() error {
	var errs []error
	for _, r := range d.roots {
		if r != nil {
			errs = append(errs, r.Close())
		}
	}
	errs = append(errs, d.root.Close())
	return errors.Join(errs...)
}

func (s *Session) SpoolUsage() (bytes, limit int64, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.driver.(*spool)
	if !ok {
		return 0, 0, false
	}
	return d.bytes, s.config.SpoolMaxBytes, true
}

func (d *spool) pump(s *Session, now time.Time) error {
	for i, lane := range lanes {
		info, err := d.root.Lstat(lane)
		if err != nil || !info.IsDir() || !os.SameFile(info, d.identities[i]) {
			return ErrProtocol
		}
	}
	if !now.Before(d.nextScan) {
		if err := d.scanSize(s, now); err != nil {
			return err
		}
	}
	// ACKs release capacity, but cannot revive an already expired transfer.
	if err := d.acknowledge(s, now); err != nil {
		return err
	}
	if err := d.receive(s, 3, now); err != nil {
		return err
	}
	if err := d.writeAck(s); err != nil {
		return err
	}
	if err := d.send(s, 2, now); err != nil {
		return err
	}
	if !s.awaitingAdmission() {
		if err := d.receive(s, 1, now); err != nil {
			return err
		}
	}
	if err := d.writeAck(s); err != nil {
		return err
	}
	return d.send(s, 0, now)
}

func (d *spool) acknowledge(s *Session, now time.Time) error {
	for _, i := range []int{2, 0} {
		if len(d.outstanding[i]) > 0 && !now.Before(d.outstanding[i][0].deadline) {
			return ErrDeadline
		}
	}
	raw, err := readRegular(d.roots[3], "consumed.json", contracts.SpoolAckLimit)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		for _, i := range []int{2, 0} {
			if len(d.outstanding[i]) > 0 && !s.now().Before(d.outstanding[i][0].deadline) {
				return ErrDeadline
			}
		}
		if err := s.state.ApplyAck(raw); err != nil {
			return ErrProtocol
		}
	}
	ack := s.state.Acknowledged()
	for _, i := range []int{2, 0} {
		key := "ordinary_seq"
		if i == 2 {
			key = "control_seq"
		}
		position := int64(-1)
		if ack[key] != nil {
			position = integer(ack[key])
		}
		for len(d.outstanding[i]) > 0 && d.outstanding[i][0].seq <= position {
			name, _ := contracts.SpoolMessageName(d.outstanding[i][0].seq, false)
			if err := d.roots[i].Remove(name); err != nil {
				return err
			}
			d.outstanding[i][0] = message{}
			d.outstanding[i] = d.outstanding[i][1:]
		}
		if len(d.outstanding[i]) > 0 && !now.Before(d.outstanding[i][0].deadline) {
			return ErrDeadline
		}
	}
	return nil
}

func (d *spool) send(s *Session, i int, now time.Time) error {
	if len(s.queues[i]) == 0 {
		return nil
	}
	if len(d.outstanding[i]) == slots(i) {
		return nil
	} // Existing ACK/queue deadlines still run.
	m := s.queues[i][0]
	temp, _ := contracts.SpoolMessageName(m.seq, true)
	name, _ := contracts.SpoolMessageName(m.seq, false)
	if err := publish(d.roots[i], temp, name, m.raw, false, d.guestGID); err != nil {
		return err
	}
	if !s.now().Before(m.deadline) {
		return ErrDeadline
	}
	if err := s.check(s.now()); err != nil {
		return err
	}
	if err := s.published(i, now); err != nil {
		return err
	}
	d.outstanding[i] = append(d.outstanding[i], message{seq: m.seq, deadline: s.bound(s.now())})
	return nil
}

func (d *spool) writeAck(s *Session) error {
	if !d.ackDirty {
		return nil
	}
	raw, err := s.state.AckBytes()
	if err != nil {
		return err
	}
	deadline := s.bound(s.now())
	if err := publish(d.roots[2], ".consumed.tmp", "consumed.json", raw, true, d.guestGID); err != nil {
		return err
	}
	if !s.now().Before(deadline) {
		return ErrDeadline
	}
	d.ackDirty = false
	return nil
}

func (d *spool) receive(s *Session, i int, now time.Time) error {
	for _, deadline := range d.temporary[i] {
		if !now.Before(deadline) {
			return ErrDeadline
		}
	}
	files, err := names(d.roots[i], slots(i)+3)
	if err != nil {
		return err
	}
	sort.Strings(files)
	ready := map[int64]string{}
	present := map[int64]bool{}
	temps := map[string]bool{}
	count, total, tempCount := 0, int64(0), 0
	for _, name := range files {
		info, err := d.roots[i].Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if !regular(info) {
			return ErrProtocol
		}
		if i == 3 && (name == "consumed.json" || name == ".consumed.tmp") {
			if info.Size() > contracts.SpoolAckLimit {
				return ErrProtocol
			}
			if name == ".consumed.tmp" {
				tempCount++
				temps[name] = true
			}
			continue
		}
		seq, temp, err := contracts.ParseSpoolMessageName(name)
		if err != nil {
			return ErrProtocol
		}
		if info.Size() > int64(laneLimit(i)) {
			return ErrProtocol
		}
		count++
		total += info.Size()
		if temp {
			tempCount++
			temps[name] = true
			if seq < s.nextReceive[i] {
				return ErrProtocol
			}
			continue
		}
		present[seq] = true
		ready[seq] = name
	}
	if count > slots(i) || total > int64(slots(i)*laneLimit(i)) || tempCount > 1 {
		return ErrProtocol
	}
	for name := range d.temporary[i] {
		if !temps[name] {
			delete(d.temporary[i], name)
		}
	}
	for name := range temps {
		deadline, ok := d.temporary[i][name]
		if !ok {
			deadline = s.bound(now)
			d.temporary[i][name] = deadline
		}
		if !now.Before(deadline) {
			return ErrDeadline
		}
	}
	for seq, seen := range d.known[i] {
		if !present[seq] {
			if !seen.consumed {
				return ErrProtocol
			}
			delete(d.known[i], seq)
		}
	}
	sequences := make([]int64, 0, len(ready))
	for seq := range ready {
		sequences = append(sequences, seq)
	}
	sort.Slice(sequences, func(a, b int) bool { return sequences[a] < sequences[b] })
	for _, seq := range sequences {
		seen, known := d.known[i][seq]
		if seq < s.nextReceive[i] && !known {
			return ErrProtocol
		}
		raw, err := readRegular(d.roots[i], ready[seq], laneLimit(i))
		if errors.Is(err, os.ErrNotExist) && known && seen.consumed {
			delete(d.known[i], seq)
			continue
		}
		if err != nil {
			return err
		}
		digest := contracts.RawDigest(raw)
		if known && seen.digest != digest {
			return ErrProtocol
		}
		if _, err := s.config.Protocol.ValidateSpoolMessage(lanes[i], ready[seq], raw); err != nil {
			return ErrProtocol
		}
		if seq < s.nextReceive[i] {
			continue
		}
		if seq > s.nextReceive[i] {
			// A full receiver can leave earlier captured-but-unconsumed ready files.
			for earlier := s.nextReceive[i]; earlier < seq; earlier++ {
				if _, ok := ready[earlier]; !ok {
					return ErrProtocol
				}
			}
		}
		d.known[i][seq] = observedFile{digest: digest}
		if len(s.queues[i]) == slots(i) {
			s.waitFull(i, now)
			continue
		}
		if seq != s.nextReceive[i] {
			return ErrProtocol
		}
		if err := s.accept(i, raw, now); err != nil {
			return err
		}
		d.known[i][seq] = observedFile{digest: digest, consumed: true}
		d.ackDirty = true
	}
	return nil
}

// Valid lanes contain at most 40 entries in total. Unexpected directory floods
// fail a bounded inspection rather than growing memory or blocking termination.
func (d *spool) scanSize(s *Session, now time.Time) error {
	deadline := now.Add(TransferTimeout)
	entries := 0
	var total int64
	var scan func(string, int) error
	scan = func(dir string, depth int) error {
		if depth > 32 {
			return ErrProtocol
		}
		f, err := d.root.OpenFile(dir, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY|syscall.O_NONBLOCK, 0)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		defer f.Close()
		for {
			if err := s.config.Fence.Err(); err != nil {
				return err
			}
			if !s.now().Before(deadline) {
				return ErrDeadline
			}
			batch, err := f.ReadDir(32)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			for _, e := range batch {
				entries++
				if entries > 256 {
					return ErrProtocol
				}
				p := dir + "/" + e.Name()
				info, err := d.root.Lstat(p)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					return err
				}
				if info.IsDir() {
					if err := scan(p, depth+1); err != nil {
						return err
					}
					continue
				}
				if !regular(info) {
					return ErrProtocol
				}
				if info.Size() > s.config.SpoolMaxBytes-total {
					d.bytes = total + info.Size()
					return ErrSpoolLimit
				}
				total += info.Size()
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
		}
	}
	for _, lane := range lanes {
		if err := scan(lane, 0); err != nil {
			return err
		}
	}
	d.bytes = total
	d.nextScan = now.Add(time.Second)
	return nil
}
