//go:build linux || darwin

// Package transport implements the host's bounded, launch-scoped FIFO/spool pump.
// Capturing a message is not durable admission or permission for an external effect.
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

const TransferTimeout = 5 * time.Second
const StartupTimeout = 60 * time.Second
const SpoolPoll = 10 * time.Millisecond
const DefaultSpoolBytes int64 = 512 << 20

var (
	ErrClosed     = errors.New("transport closed")
	ErrProtocol   = errors.New("transport protocol failure")
	ErrDeadline   = errors.New("transport deadline expired")
	ErrQueueFull  = errors.New("transport queue full")
	ErrNotReady   = errors.New("FIFO rendezvous incomplete")
	ErrSpoolLimit = errors.New("SPOOL_SIZE_LIMIT")
	ErrPeerLost   = errors.New("transport peer lost")
)

var lanes = []string{"ordinary-in", "ordinary-out", "control-in", "control-out"}

func laneIndex(lane string) int {
	for i, v := range lanes {
		if lane == v {
			return i
		}
	}
	return -1
}
func laneLimit(i int) int {
	if i >= 2 {
		return contracts.ControlLimit
	}
	return contracts.OrdinaryLimit
}
func slots(i int) int {
	if i >= 2 {
		return 16
	}
	return 2
}
func integer(v any) int64 { n, _ := strconv.ParseFloat(string(v.(json.Number)), 64); return int64(n) }
func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

type Config struct {
	Protocol             *contracts.Protocol
	CampaignID, LaunchID string
	Fence                *campaign.Fence
	CampaignDeadline     time.Time // Trusted absolute monotonic host deadline.
	SpoolMaxBytes        int64     // Zero selects 512 MiB.
	GuestGID             *int      // Nil selects the host's primary group; launcher must match guest access.
}

type message struct {
	raw      []byte
	seq      int64
	deadline time.Time
}
type operation struct {
	request  []byte
	deadline time.Time
	queued   bool
}
type driver interface {
	pump(*Session, time.Time) error
	close() error
	interval() time.Duration
}

// Session serializes its I/O pump and bounded queues. Fence.Stop never needs this
// mutex. Each incoming queue is consumed separately so control can bypass work.
type Session struct {
	mu                 sync.Mutex
	config             Config
	state              *contracts.TransportState
	driver             driver
	now                func() time.Time
	queues             [4][]message
	nextSend           [4]int64
	nextReceive        [4]int64
	fullSince          [4]time.Time
	phase              string
	phaseDeadline      time.Time
	active             *operation
	peerEstablished    bool
	admissionPublished bool
	closed             bool
	directory          string
	directoryInfo      os.FileInfo
	cleaned            bool
}

func newSession(c Config) (*Session, error) {
	now := time.Now()
	if c.Protocol == nil || c.Fence == nil || !c.CampaignDeadline.After(now) || c.CampaignDeadline.After(now.Add(30*time.Minute)) || c.SpoolMaxBytes < 0 {
		return nil, ErrProtocol
	}
	if c.SpoolMaxBytes == 0 {
		c.SpoolMaxBytes = DefaultSpoolBytes
	}
	state, err := c.Protocol.NewTransportState("host", c.CampaignID, c.LaunchID)
	if err != nil {
		return nil, err
	}
	if err := c.Fence.Err(); err != nil {
		return nil, err
	}
	return &Session{config: c, state: state, now: time.Now, phase: "bootstrap", phaseDeadline: earlier(now.Add(StartupTimeout), c.CampaignDeadline)}, nil
}

func (s *Session) fail(err error) error {
	s.config.Fence.Stop(err)
	s.state.Close()
	return err
}
func (s *Session) bound(now time.Time) time.Time {
	d := earlier(now.Add(TransferTimeout), s.config.CampaignDeadline)
	if s.phase != "admitted" {
		d = earlier(d, s.phaseDeadline)
	}
	return d
}
func (s *Session) check(now time.Time) error {
	if err := s.config.Fence.Err(); err != nil {
		return err
	}
	if s.closed {
		return ErrClosed
	}
	if !now.Before(s.config.CampaignDeadline) || (s.phase != "admitted" && !now.Before(s.phaseDeadline)) || (s.active != nil && !now.Before(s.active.deadline)) {
		return s.fail(ErrDeadline)
	}
	for i, q := range s.queues {
		if i%2 == 0 && len(q) > 0 && !now.Before(q[0].deadline) {
			return s.fail(ErrDeadline)
		}
		if !s.fullSince[i].IsZero() && !now.Before(s.fullSince[i]) {
			return s.fail(ErrDeadline)
		}
	}
	return nil
}

// Enqueue copies one validated host response/control message. ErrQueueFull means
// retry the same unsent message; it has not consumed a sequence. A non-renewing
// five-second full-queue timer remains armed until capacity becomes available.
// ErrNotReady means FIFO readers have not rendezvoused yet; keep pumping before
// retrying bootstrap. Neither retryable error consumes a sequence.
func (s *Session) Enqueue(lane string, raw []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := s.check(now); err != nil {
		return err
	}
	i := laneIndex(lane)
	if i < 0 || i%2 != 0 {
		return s.fail(ErrProtocol)
	}
	if d, ok := s.driver.(*fifo); ok && (d.lanes[0].file == nil || d.lanes[2].file == nil) {
		return ErrNotReady
	}
	if len(raw) > laneLimit(i) {
		return s.fail(ErrProtocol)
	}
	m, err := s.config.Protocol.ValidateLaneMessage(lane, raw)
	if err != nil {
		return s.fail(ErrProtocol)
	}
	if m["kind"] == "bootstrap" && !s.matchesTransport(m) {
		return s.fail(ErrProtocol)
	}
	if m["campaign_id"] != s.config.CampaignID || m["launch_id"] != s.config.LaunchID || integer(m["seq"]) != s.nextSend[i] {
		return s.fail(ErrProtocol)
	}
	if i == 0 {
		if s.active == nil || s.active.queued {
			return s.fail(ErrProtocol)
		}
		if _, err := s.config.Protocol.ValidateResponse(s.active.request, raw); err != nil {
			return s.fail(ErrProtocol)
		}
	}
	if len(s.queues[i]) == slots(i) {
		s.waitFull(i, now)
		return ErrQueueFull
	}
	s.queues[i] = append(s.queues[i], message{bytes.Clone(raw), s.nextSend[i], s.bound(now)})
	s.nextSend[i]++
	if i == 0 {
		s.active.queued = true
	}
	return nil
}

func (s *Session) waitFull(i int, now time.Time) {
	if s.fullSince[i].IsZero() {
		s.fullSince[i] = s.bound(now)
	}
}

// Receive returns a private copy from a captured incoming queue. No filesystem
// read or external effect occurs here. ACKs were issued on bounded capture.
func (s *Session) Receive(lane string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.check(s.now()) != nil {
		return nil, false
	}
	i := laneIndex(lane)
	if i < 0 || i%2 != 1 || s.closed || s.config.Fence.Err() != nil || len(s.queues[i]) == 0 {
		return nil, false
	}
	m := s.queues[i][0]
	s.queues[i][0] = message{}
	s.queues[i] = s.queues[i][1:]
	s.fullSince[i] = time.Time{}
	return bytes.Clone(m.raw), true
}

func (s *Session) accept(i int, raw []byte, now time.Time) error {
	if len(s.queues[i]) >= slots(i) {
		return ErrQueueFull
	}
	if i == 1 && (s.phase != "admitted" || s.active != nil) {
		return ErrProtocol
	}
	if err := s.state.Accept(lanes[i], raw); err != nil {
		return ErrProtocol
	}
	if i == 3 {
		m, err := s.config.Protocol.ValidateControl("guest", raw)
		if err != nil {
			return ErrProtocol
		}
		if m["kind"] == "confinement_ready" && !s.matchesTransport(m) {
			return ErrProtocol
		}
	}
	if i == 1 {
		m, err := s.config.Protocol.ValidateRequest(raw)
		if err != nil {
			return ErrProtocol
		}
		limitMS := integer(m["timeout_ms"])
		for _, op := range s.config.Protocol.Operations() {
			if op.Name == m["operation"] {
				limitMS = min(limitMS, op.TimeoutMS)
			}
		}
		limit := time.Duration(limitMS) * time.Millisecond
		s.active = &operation{request: bytes.Clone(raw), deadline: earlier(now.Add(limit), s.config.CampaignDeadline)}
	}
	s.queues[i] = append(s.queues[i], message{raw, s.nextReceive[i], time.Time{}})
	s.nextReceive[i]++
	return nil
}

func (s *Session) matchesTransport(m map[string]any) bool {
	expected := "fifo"
	if _, ok := s.driver.(*spool); ok {
		expected = "spool"
	}
	return m["body"].(map[string]any)["transport"] == expected
}

func (s *Session) published(i int, now time.Time) error {
	m := s.queues[i][0]
	if err := s.state.RecordPublished(lanes[i], m.raw); err != nil {
		return ErrProtocol
	}
	if i == 2 {
		control, err := s.config.Protocol.ValidateControl("host", m.raw)
		if err != nil {
			return ErrProtocol
		}
		if control["kind"] == "admission_open" {
			s.admissionPublished = true
		}
	}
	s.queues[i][0] = message{}
	s.queues[i] = s.queues[i][1:]
	s.fullSince[i] = time.Time{}
	if i == 0 {
		s.active = nil
	}
	return nil
}

// Published reports complete FIFO write or atomic spool publication, not guest
// consumption, durable admission or successful execution. Positions survive failure.
func (s *Session) Published(lane string, seq int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := laneIndex(lane)
	return i >= 0 && i%2 == 0 && seq >= 0 && seq < s.nextSend[i]-int64(len(s.queues[i]))
}

// A fast guest may send its first request before the broker observes delivery of
// admission_open. Leave those bytes in the bounded transport until OpenAdmission;
// they must neither execute early nor be mistaken for a pre-admission violation.
func (s *Session) awaitingAdmission() bool {
	return s.phase == "initialization" && s.admissionPublished
}

// BeginInitialization follows verified confinement_ready. Ordinary requests stay
// closed. A caller cannot repeatedly renew either startup phase deadline.
func (s *Session) BeginInitialization() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := s.check(now); err != nil {
		return err
	}
	if s.phase != "bootstrap" {
		return s.fail(ErrProtocol)
	}
	s.peerEstablished = true
	s.phase = "initialization"
	s.phaseDeadline = earlier(now.Add(StartupTimeout), s.config.CampaignDeadline)
	return nil
}

// OpenAdmission follows verified startup/manifest/journal gates and successful
// admission_open delivery. Transport itself does not establish those facts.
func (s *Session) OpenAdmission() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(s.now()); err != nil {
		return err
	}
	if s.phase != "initialization" || !s.admissionPublished {
		return s.fail(ErrProtocol)
	}
	s.phase = "admitted"
	return nil
}

// TightenOperation applies a native adapter's additional deadline without renewal.
func (s *Session) TightenOperation(deadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(s.now()); err != nil {
		return err
	}
	if s.active == nil {
		return ErrProtocol
	}
	s.active.deadline = earlier(s.active.deadline, deadline)
	return s.check(s.now())
}

func (s *Session) OperationDeadline() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return time.Time{}, false
	}
	return s.active.deadline, true
}

// Pump performs bounded work, with ACK/control priority. It also drives deadlines
// during idle operation. Runtime normally uses Run, not intermittent Pump calls.
func (s *Session) Pump() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if err := s.check(now); err != nil {
		return err
	}
	if err := s.driver.pump(s, now); err != nil {
		return s.fail(err)
	}
	return s.check(s.now())
}

func (s *Session) Run(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { s.config.Fence.Stop(ctx.Err()) })
	defer stop()
	ticker := time.NewTicker(s.driver.interval())
	defer ticker.Stop()
	for {
		if err := s.Pump(); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			s.config.Fence.Stop(ctx.Err())
			return ctx.Err()
		case <-s.config.Fence.Done():
			return s.config.Fence.Err()
		case <-ticker.C:
		}
	}
}

// Close signals termination before acquiring the I/O mutex. It closes host
// handles but retains transport paths until the caller confirms container exit.
func (s *Session) Close() error {
	s.config.Fence.Stop(ErrClosed)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.state.Close()
	return s.driver.close()
}

// CleanupAfterExit is for the trusted launcher after exact Docker exit confirmation
// and Close. It never follows a replacement root or links inside the spool. It may
// take time to delete hostile leftover files, so it is not part of the kill path.
func (s *Session) CleanupAfterExit(confirmedExit bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !confirmedExit || !s.closed {
		return ErrProtocol
	}
	if s.cleaned {
		return nil
	}
	r, err := os.OpenRoot(filepath.Dir(s.directory))
	if err != nil {
		return err
	}
	defer r.Close()
	name := filepath.Base(s.directory)
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(info, s.directoryInfo) {
		return ErrProtocol
	}
	if err := r.RemoveAll(name); err != nil {
		return err
	}
	s.cleaned = true
	return nil
}
