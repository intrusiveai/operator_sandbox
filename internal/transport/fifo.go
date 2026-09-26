//go:build linux || darwin

package transport

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

type fifoLane struct {
	file          *os.File
	info          os.FileInfo
	header        [4]byte
	headerN       int
	bodyRemaining int
	decoder       *contracts.FrameDecoder
	readDeadline  time.Time
	writeFrame    []byte
	written       int
}
type fifo struct {
	root  *os.Root
	lanes [4]fifoLane
}

// NewFIFO creates fresh directional FIFO inodes and opens only host read ends.
// Host writes rendezvous later, after the launcher starts the guest. Production
// selects this backend on Linux; macOS tests can exercise Unix FIFO mechanics.
func NewFIFO(dir string, c Config) (*Session, error) {
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
	d := &fifo{root: r}
	s.driver = d
	ok := false
	defer func() {
		if !ok {
			d.close()
		}
	}()
	gid, err := guestGroup(c)
	if err != nil {
		return nil, err
	}
	for i, lane := range lanes {
		// The directory is fresh and private throughout preparation; no untrusted
		// writer can replace an entry between Mkfifo and opening its root-relative FD.
		if err := syscall.Mkfifo(filepath.Join(dir, lane), 0600); err != nil {
			return nil, err
		}
		if err := r.Chown(lane, -1, gid); err != nil {
			return nil, err
		}
		mode := os.FileMode(0640)
		if i%2 == 1 {
			mode = 0620
		}
		if err := r.Chmod(lane, mode); err != nil {
			return nil, err
		}
		d.lanes[i].info, err = r.Lstat(lane)
		if err != nil {
			return nil, err
		}
		if i%2 == 1 {
			d.lanes[i].file, err = d.open(i)
			if err != nil {
				return nil, err
			}
			d.lanes[i].decoder, err = c.Protocol.NewFrameDecoder(lane)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := r.Chown(".", -1, gid); err != nil {
		return nil, err
	}
	if err := r.Chmod(".", 0750); err != nil {
		return nil, err
	}
	ok = true
	return s, nil
}
func (d *fifo) interval() time.Duration { return time.Millisecond }
func (d *fifo) close() error {
	var errs []error
	for _, l := range d.lanes {
		if l.file != nil {
			errs = append(errs, l.file.Close())
		}
	}
	errs = append(errs, d.root.Close())
	return errors.Join(errs...)
}
func (d *fifo) open(i int) (*os.File, error) {
	info, err := d.root.Lstat(lanes[i])
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(info, d.lanes[i].info) {
		return nil, ErrProtocol
	}
	flags := os.O_RDONLY
	if i%2 == 0 {
		flags = os.O_WRONLY
	}
	f, err := d.root.OpenFile(lanes[i], flags|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !os.SameFile(info, actual) || actual.Mode()&os.ModeNamedPipe == 0 {
		f.Close()
		return nil, ErrProtocol
	}
	conn, err := f.SyscallConn()
	if err != nil {
		f.Close()
		return nil, err
	}
	var setup error
	if err = conn.Control(func(fd uintptr) { setup = syscall.SetNonblock(int(fd), true) }); err != nil {
		f.Close()
		return nil, err
	}
	if setup != nil {
		f.Close()
		return nil, setup
	}
	return f, nil
}

// Control avoids os.File.Fd(), which can switch a pollable descriptor to blocking.
func fifoIO(f *os.File, b []byte, write bool) (n int, err error) {
	c, e := f.SyscallConn()
	if e != nil {
		return 0, e
	}
	e = c.Control(func(fd uintptr) {
		if write {
			n, err = syscall.Write(int(fd), b)
		} else {
			n, err = syscall.Read(int(fd), b)
		}
	})
	if e != nil {
		return 0, e
	}
	return n, err
}
func wouldBlock(err error) bool {
	return errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EINTR)
}

func (d *fifo) pump(s *Session, now time.Time) error {
	files, err := names(d.root, 4)
	if err != nil || len(files) != 4 {
		return ErrProtocol
	}
	for i, lane := range lanes {
		info, err := d.root.Lstat(lane)
		if err != nil || info.Mode()&os.ModeNamedPipe == 0 || !os.SameFile(info, d.lanes[i].info) {
			return ErrProtocol
		}
		if i%2 == 0 && d.lanes[i].file == nil {
			f, err := d.open(i)
			if errors.Is(err, syscall.ENXIO) {
				if s.peerEstablished {
					return ErrPeerLost
				}
				continue
			}
			if err != nil {
				return err
			}
			d.lanes[i].file = f
		}
	}
	if err := d.receive(s, 3, now); err != nil {
		return err
	}
	// Both write endpoints must be ready before the bootstrap can be published.
	ready := d.lanes[0].file != nil && d.lanes[2].file != nil
	if ready {
		if err := d.send(s, 2, now); err != nil {
			return err
		}
	}
	if !s.awaitingAdmission() {
		if err := d.receive(s, 1, now); err != nil {
			return err
		}
	}
	if ready {
		if err := d.send(s, 0, now); err != nil {
			return err
		}
	}
	return nil
}

func (d *fifo) receive(s *Session, i int, now time.Time) error {
	l := &d.lanes[i]
	if !l.readDeadline.IsZero() && !now.Before(l.readDeadline) {
		return ErrDeadline
	}
	if len(s.queues[i]) == slots(i) {
		s.waitFull(i, now)
		return nil
	}
	var chunk [64 << 10]byte
	for work := 0; work < 4; work++ {
		buf := chunk[:]
		if l.headerN < 4 {
			buf = l.header[l.headerN:]
		} else {
			buf = buf[:min(len(buf), l.bodyRemaining)]
		}
		n, err := fifoIO(l.file, buf, false)
		if wouldBlock(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			if s.peerEstablished || !l.readDeadline.IsZero() {
				return ErrPeerLost
			}
			return nil
		}
		if l.readDeadline.IsZero() {
			l.readDeadline = s.bound(now)
		}
		_, raw, err := l.decoder.Feed(buf[:n])
		if err != nil {
			return ErrProtocol
		}
		if l.headerN < 4 {
			l.headerN += n
			if l.headerN == 4 {
				l.bodyRemaining = int(binary.BigEndian.Uint32(l.header[:]))
			}
		} else {
			l.bodyRemaining -= n
		}
		if raw != nil {
			if !s.now().Before(l.readDeadline) {
				return ErrDeadline
			}
			if err := s.accept(i, raw, now); err != nil {
				return err
			}
			l.headerN = 0
			l.bodyRemaining = 0
			l.readDeadline = time.Time{}
			return nil
		}
	}
	return nil
}

func (d *fifo) send(s *Session, i int, now time.Time) error {
	l := &d.lanes[i]
	if len(s.queues[i]) == 0 {
		return nil
	}
	if l.writeFrame == nil {
		l.writeFrame = s.queues[i][0].raw
		binary.BigEndian.PutUint32(l.header[:], uint32(len(l.writeFrame)))
		l.written = 0
	}
	for work := 0; work < 4; work++ {
		var remaining []byte
		if l.written < 4 {
			remaining = l.header[l.written:]
		} else {
			remaining = l.writeFrame[l.written-4:]
		}
		if len(remaining) > 64<<10 {
			remaining = remaining[:64<<10]
		}
		n, err := fifoIO(l.file, remaining, true)
		if wouldBlock(err) {
			return nil
		}
		if err != nil {
			return ErrPeerLost
		}
		if n == 0 {
			return nil
		}
		l.written += n
		if l.written == len(l.writeFrame)+4 {
			if err := s.check(s.now()); err != nil {
				return err
			}
			l.writeFrame = nil
			l.written = 0
			return s.published(i, now)
		}
	}
	return nil
}
