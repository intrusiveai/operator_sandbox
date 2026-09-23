package contracts

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// ErrTransport is terminal for an established transport. It carries no peer data.
var ErrTransport = errors.New("contract transport closed")

func laneInfo(lane string) (hostWrites bool, control bool, maximum int, ok bool) {
	switch lane {
	case "ordinary-in":
		return true, false, OrdinaryLimit, true
	case "ordinary-out":
		return false, false, OrdinaryLimit, true
	case "control-in":
		return true, true, ControlLimit, true
	case "control-out":
		return false, true, ControlLimit, true
	}
	return false, false, 0, false
}

// ValidateLaneMessage checks the physical lane's closed envelope and byte ceiling.
// Response/request correlation and live admission remain the dispatcher's work.
func (p *Protocol) ValidateLaneMessage(lane string, raw []byte) (map[string]any, error) {
	host, control, _, ok := laneInfo(lane)
	if !ok {
		return nil, ErrProtocol
	}
	if control {
		direction := "guest"
		if host {
			direction = "host"
		}
		return p.ValidateControl(direction, raw)
	}
	if !host {
		return p.ValidateRequest(raw)
	}
	return p.validateResponseEnvelope(raw)
}

// EncodeFrame validates and prefixes one FIFO envelope. Spool files omit this prefix.
func (p *Protocol) EncodeFrame(lane string, raw []byte) ([]byte, error) {
	if _, err := p.ValidateLaneMessage(lane, raw); err != nil {
		return nil, err
	}
	output := make([]byte, len(raw)+4)
	binary.BigEndian.PutUint32(output, uint32(len(raw)))
	copy(output[4:], raw)
	return output, nil
}

// FrameDecoder holds at most one bounded frame. Feed returns after the first
// complete envelope; the caller retains/re-feeds unconsumed bytes. No I/O occurs.
type FrameDecoder struct {
	protocol   *Protocol
	lane       string
	maximum    int
	header     [4]byte
	headerSize int
	length     int
	body       []byte
	closed     bool
}

func (p *Protocol) NewFrameDecoder(lane string) (*FrameDecoder, error) {
	_, _, maximum, ok := laneInfo(lane)
	if !ok {
		return nil, ErrProtocol
	}
	return &FrameDecoder{protocol: p, lane: lane, maximum: maximum}, nil
}
func (d *FrameDecoder) fail() { d.closed = true; d.body = nil }

// Feed rejects an invalid size after reading only the four-byte header. A malformed
// envelope permanently closes this decoder; partial reads never reset deadlines.
func (d *FrameDecoder) Feed(chunk []byte) (consumed int, frame []byte, err error) {
	if d.closed {
		return 0, nil, ErrTransport
	}
	if d.headerSize < 4 {
		n := copy(d.header[d.headerSize:], chunk)
		d.headerSize += n
		consumed += n
		chunk = chunk[n:]
		if d.headerSize < 4 {
			return consumed, nil, nil
		}
		length := binary.BigEndian.Uint32(d.header[:])
		if length == 0 || uint64(length) > uint64(d.maximum) {
			d.fail()
			return consumed, nil, ErrLimit
		}
		d.length = int(length)
		// Allocate only once the declared bound has been verified.
		d.body = make([]byte, 0, d.length)
	}
	n := min(len(chunk), d.length-len(d.body))
	d.body = append(d.body, chunk[:n]...)
	consumed += n
	if len(d.body) < d.length {
		return consumed, nil, nil
	}
	if _, err := d.protocol.ValidateLaneMessage(d.lane, d.body); err != nil {
		d.fail()
		return consumed, nil, err
	}
	frame = d.body
	d.body = nil
	d.headerSize = 0
	d.length = 0
	return consumed, frame, nil
}

// End represents EOF/loss after rendezvous. Even a frame-boundary EOF is terminal.
// Initial FIFO no-writer handling belongs to the runtime's rendezvous state.
func (d *FrameDecoder) End() error { d.closed = true; d.body = nil; return ErrTransport }

func SpoolMessageName(sequence int64, temporary bool) (string, error) {
	if sequence < 0 || sequence > MaxSafeInteger {
		return "", ErrProtocol
	}
	if temporary {
		return fmt.Sprintf(".%020d.tmp", sequence), nil
	}
	return fmt.Sprintf("%020d.json", sequence), nil
}
func ParseSpoolMessageName(name string) (sequence int64, temporary bool, err error) {
	digits := ""
	if len(name) == 25 && strings.HasSuffix(name, ".json") {
		digits = name[:20]
	} else if len(name) == 25 && name[0] == '.' && strings.HasSuffix(name, ".tmp") {
		digits = name[1:21]
		temporary = true
	} else {
		return 0, false, ErrProtocol
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false, ErrProtocol
		}
	}
	sequence, err = strconv.ParseInt(digits, 10, 64)
	if err != nil || sequence > MaxSafeInteger {
		return 0, false, ErrProtocol
	}
	return sequence, temporary, nil
}
func (p *Protocol) ValidateSpoolMessage(lane, name string, raw []byte) (map[string]any, error) {
	seq, temporary, err := ParseSpoolMessageName(name)
	if err != nil {
		return nil, err
	}
	if temporary {
		return nil, ErrProtocol
	}
	m, err := p.ValidateLaneMessage(lane, raw)
	if err != nil {
		return nil, err
	}
	if number(m["seq"]) != seq {
		return nil, ErrProtocol
	}
	return m, nil
}

var transportID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// TransportState checks direction, launch-wide sequences and consumption ACKs.
// It holds no message queue, timers or filesystem handles. The caller enforces
// bounded processing/storage queues and records acceptance only after capture.
// One event-loop owner must serialize calls; this type is not concurrency-safe.
type TransportState struct {
	protocol                          *Protocol
	host                              bool
	campaign, launch                  string
	received, published, acknowledged [2]int64
	closed                            bool
}

func (p *Protocol) NewTransportState(role, campaign, launch string) (*TransportState, error) {
	if role != "host" && role != "guest" || !transportID.MatchString(campaign) || !transportID.MatchString(launch) {
		return nil, ErrProtocol
	}
	return &TransportState{protocol: p, host: role == "host", campaign: campaign, launch: launch, received: [2]int64{-1, -1}, published: [2]int64{-1, -1}, acknowledged: [2]int64{-1, -1}}, nil
}
func (s *TransportState) Close()               { s.closed = true }
func (s *TransportState) fail(err error) error { s.Close(); return err }
func (s *TransportState) record(lane string, raw []byte, publish bool) error {
	if s.closed {
		return ErrTransport
	}
	host, control, _, ok := laneInfo(lane)
	if !ok || (host == s.host) != publish {
		return s.fail(ErrProtocol)
	}
	m, err := s.protocol.ValidateLaneMessage(lane, raw)
	if err != nil {
		return s.fail(err)
	}
	index := 0
	if control {
		index = 1
	}
	positions := &s.received
	if publish {
		positions = &s.published
	}
	if m["campaign_id"] != s.campaign || m["launch_id"] != s.launch || positions[index] == MaxSafeInteger || number(m["seq"]) != positions[index]+1 {
		return s.fail(ErrProtocol)
	}
	positions[index] = number(m["seq"])
	return nil
}

// Accept records validation/acceptance into the receiver's bounded queue. It does
// not imply operation execution and must not be called before queue capacity exists.
func (s *TransportState) Accept(lane string, raw []byte) error { return s.record(lane, raw, false) }

// RecordPublished runs only after successful FIFO transfer/spool publication.
func (s *TransportState) RecordPublished(lane string, raw []byte) error {
	return s.record(lane, raw, true)
}
func positions(values [2]int64) map[string]any {
	result := map[string]any{"ordinary_seq": nil, "control_seq": nil}
	for i, key := range []string{"ordinary_seq", "control_seq"} {
		if values[i] >= 0 {
			result[key] = json.Number(strconv.FormatInt(values[i], 10))
		}
	}
	return result
}
func (s *TransportState) Acknowledged() map[string]any { return positions(s.acknowledged) }
func (s *TransportState) AckBytes() ([]byte, error) {
	if s.closed {
		return nil, ErrTransport
	}
	ack := positions(s.received)
	ack["api_version"] = "operator.dev/engine-spool-ack/v1alpha1"
	ack["launch_id"] = s.launch
	return json.Marshal(ack)
}
func (s *TransportState) ApplyAck(raw []byte) error {
	if s.closed {
		return ErrTransport
	}
	ack, err := s.protocol.ValidateAck(raw)
	if err != nil {
		return s.fail(err)
	}
	if ack["launch_id"] != s.launch {
		return s.fail(ErrProtocol)
	}
	next := s.acknowledged
	for i, key := range []string{"ordinary_seq", "control_seq"} {
		if ack[key] == nil {
			continue
		}
		value := number(ack[key])
		if value > s.published[i] {
			return s.fail(ErrProtocol)
		}
		if value > next[i] {
			next[i] = value
		}
	}
	s.acknowledged = next
	return nil
}
