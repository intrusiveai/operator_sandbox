//go:build linux || darwin

package transport

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

type fixture struct {
	p      *contracts.Protocol
	frames map[string][]byte
}

func fixtures(t *testing.T) fixture {
	t.Helper()
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../schemas/fixtures/transport-codec.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Mode, Lane string
		Valid      bool
		Frames     []string `json:"frames_base64"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	f := fixture{p: p, frames: map[string][]byte{}}
	for _, c := range cases {
		if c.Mode == "frames" && c.Valid && f.frames[c.Lane] == nil {
			b, err := base64.StdEncoding.DecodeString(c.Frames[0])
			if err != nil {
				t.Fatal(err)
			}
			f.frames[c.Lane] = b
		}
	}
	return f
}
func (f fixture) msg(lane string, seq int64) []byte {
	var m map[string]any
	json.Unmarshal(f.frames[lane], &m)
	m["seq"] = seq
	raw, _ := json.Marshal(m)
	return raw
}
func change(raw []byte, fn func(map[string]any)) []byte {
	var m map[string]any
	json.Unmarshal(raw, &m)
	fn(m)
	out, _ := json.Marshal(m)
	return out
}
func directory(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0700); err != nil {
		t.Fatal(err)
	}
	return d
}
func config(f fixture) Config {
	return Config{Protocol: f.p, CampaignID: "campaign-1", LaunchID: "launch-1", Fence: campaign.NewFence(), CampaignDeadline: time.Now().Add(20 * time.Minute)}
}
func makeSpool(t *testing.T) (fixture, *Session, string, *time.Time) {
	t.Helper()
	f := fixtures(t)
	for _, lane := range []string{"control-in", "control-out"} {
		f.frames[lane] = change(f.frames[lane], func(m map[string]any) {
			b := m["body"].(map[string]any)
			b["host_platform"] = "darwin/arm64"
			b["transport"] = "spool"
		})
	}
	dir := directory(t)
	s, err := NewSpool(dir, config(f))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Now()
	s.now = func() time.Time { return now }
	return f, s, dir, &now
}
func admitted(t *testing.T, s *Session) {
	t.Helper()
	if err := s.BeginInitialization(); err != nil {
		t.Fatal(err)
	}
	// Isolate post-startup transport behavior. The complete startup test below
	// exercises actual admission_open publication and the broker gate.
	s.admissionPublished = true
	if err := s.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
}
func pump(t *testing.T, s *Session) {
	t.Helper()
	if err := s.Pump(); err != nil {
		t.Fatal(err)
	}
}
func put(t *testing.T, dir, lane string, seq int64, raw []byte) {
	t.Helper()
	name, _ := contracts.SpoolMessageName(seq, false)
	tmp, _ := contracts.SpoolMessageName(seq, true)
	if err := os.WriteFile(filepath.Join(dir, lane, tmp), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, lane, tmp), filepath.Join(dir, lane, name)); err != nil {
		t.Fatal(err)
	}
}
func ack(t *testing.T, dir string, ordinary, control any) {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"api_version": "operator.dev/engine-spool-ack/v1alpha1", "launch_id": "launch-1", "ordinary_seq": ordinary, "control_seq": control})
	if err := os.WriteFile(filepath.Join(dir, "control-out/.consumed.tmp"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "control-out/.consumed.tmp"), filepath.Join(dir, "control-out/consumed.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSpoolExchangeAndOperationDeadline(t *testing.T) {
	f, s, dir, now := makeSpool(t)
	admitted(t, s)
	request := f.msg("ordinary-out", 0)
	put(t, dir, "ordinary-out", 0, request)
	pump(t, s)
	got, ok := s.Receive("ordinary-out")
	if !ok || !bytes.Equal(got, request) {
		t.Fatal("lost captured request")
	}
	deadline, ok := s.OperationDeadline()
	if !ok || !deadline.Equal(now.Add(30*time.Second)) {
		t.Fatal(deadline, ok)
	}
	// Mutation after capture cannot change the broker's private copy.
	got[0] = '!'
	if err := os.Remove(filepath.Join(dir, "ordinary-out/00000000000000000000.json")); err != nil {
		t.Fatal(err)
	}
	*now = now.Add(6 * time.Second)
	pump(t, s) // Consumption is not a five-second execution timeout.
	response := f.msg("ordinary-in", 0)
	if err := s.Enqueue("ordinary-in", response); err != nil {
		t.Fatal(err)
	}
	response[0] = '!'
	pump(t, s)
	raw, err := os.ReadFile(filepath.Join(dir, "ordinary-in/00000000000000000000.json"))
	if err != nil || raw[0] != '{' {
		t.Fatal(err)
	}
	if _, ok := s.OperationDeadline(); ok {
		t.Fatal("published result left operation active")
	}
	ack(t, dir, 0, nil)
	pump(t, s)
	if _, err := os.Stat(filepath.Join(dir, "ordinary-in/00000000000000000000.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ACK did not release file", err)
	}
	// A healthy revision change keeps launch-wide positions.
	second := change(f.msg("ordinary-out", 1), func(m map[string]any) {
		m["run_revision"] = 4
		m["call_id"] = "call-2"
		m["operation_id"] = "operation-2"
	})
	put(t, dir, "ordinary-out", 1, second)
	pump(t, s)
	if raw, ok := s.Receive("ordinary-out"); !ok || !bytes.Equal(raw, second) {
		t.Fatal("revision reset transport")
	}
}

func TestSpoolACKDeadlinesDoNotRenew(t *testing.T) {
	for _, kind := range []string{"missing", "stale", "late", "future", "wrong-launch"} {
		t.Run(kind, func(t *testing.T) {
			f, s, dir, now := makeSpool(t)
			if err := s.Enqueue("control-in", f.msg("control-in", 0)); err != nil {
				t.Fatal(err)
			}
			pump(t, s)
			switch kind {
			case "stale":
				*now = now.Add(4 * time.Second)
				ack(t, dir, nil, nil)
				pump(t, s)
				*now = now.Add(time.Second)
			case "late":
				*now = now.Add(5 * time.Second)
				ack(t, dir, nil, 0)
			case "future":
				ack(t, dir, nil, 1)
			case "wrong-launch":
				ack(t, dir, nil, 0)
				p := filepath.Join(dir, "control-out/consumed.json")
				b, _ := os.ReadFile(p)
				os.WriteFile(p, bytes.ReplaceAll(b, []byte("launch-1"), []byte("launch-2")), 0600)
			default:
				*now = now.Add(5 * time.Second)
			}
			if err := s.Pump(); err == nil {
				t.Fatal("accepted failed ACK", kind)
			}
			select {
			case <-s.config.Fence.Done():
			default:
				t.Fatal("failure did not signal stop")
			}
			if _, err := os.Stat(filepath.Join(dir, "control-in/00000000000000000000.json")); err != nil {
				t.Fatal("unacknowledged file deleted", err)
			}
		})
	}
}

func TestSpoolInvalidFiles(t *testing.T) {
	for name, corrupt := range map[string]func(*testing.T, fixture, string){
		"gap":               func(t *testing.T, f fixture, d string) { put(t, d, "control-out", 1, f.msg("control-out", 1)) },
		"filename mismatch": func(t *testing.T, f fixture, d string) { put(t, d, "control-out", 0, f.msg("control-out", 1)) },
		"wrong campaign": func(t *testing.T, f fixture, d string) {
			put(t, d, "control-out", 0, change(f.msg("control-out", 0), func(m map[string]any) { m["campaign_id"] = "other" }))
		},
		"unexpected name": func(t *testing.T, f fixture, d string) {
			os.WriteFile(filepath.Join(d, "ordinary-out/extra"), []byte("x"), 0600)
		},
		"symlink": func(t *testing.T, f fixture, d string) {
			os.Symlink("/etc/hosts", filepath.Join(d, "ordinary-out/00000000000000000000.json"))
		},
		"hardlink": func(t *testing.T, f fixture, d string) {
			put(t, d, "control-out", 0, f.msg("control-out", 0))
			os.Link(filepath.Join(d, "control-out/00000000000000000000.json"), filepath.Join(d, "ordinary-out/00000000000000000000.json"))
		},
		"special file": func(t *testing.T, f fixture, d string) {
			syscall.Mkfifo(filepath.Join(d, "ordinary-out/00000000000000000000.json"), 0600)
		},
		"oversized temporary": func(t *testing.T, f fixture, d string) {
			os.WriteFile(filepath.Join(d, "control-out/.00000000000000000000.tmp"), make([]byte, contracts.ControlLimit+1), 0600)
		},
		"two temporaries": func(t *testing.T, f fixture, d string) {
			os.WriteFile(filepath.Join(d, "control-out/.00000000000000000000.tmp"), nil, 0600)
			os.WriteFile(filepath.Join(d, "control-out/.consumed.tmp"), nil, 0600)
		},
		"too many messages": func(t *testing.T, f fixture, d string) {
			for i := int64(0); i < 3; i++ {
				put(t, d, "ordinary-out", i, f.msg("ordinary-out", i))
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f, s, dir, _ := makeSpool(t)
			corrupt(t, f, dir)
			if err := s.Pump(); err == nil {
				t.Fatal("accepted invalid files")
			}
		})
	}
}

func TestSpoolNoReplayAndNoChangedPendingFile(t *testing.T) {
	for _, mutate := range []bool{false, true} {
		t.Run(map[bool]string{false: "reappeared", true: "mutated"}[mutate], func(t *testing.T) {
			f, s, dir, _ := makeSpool(t)
			put(t, dir, "control-out", 0, f.msg("control-out", 0))
			pump(t, s)
			if _, ok := s.Receive("control-out"); !ok {
				t.Fatal("missing message")
			}
			pump(t, s)
			if _, ok := s.Receive("control-out"); ok {
				t.Fatal("redelivered retained file")
			}
			if !mutate {
				os.Remove(filepath.Join(dir, "control-out/00000000000000000000.json"))
				pump(t, s)
			}
			raw := f.msg("control-out", 0)
			if mutate {
				raw = append(raw, ' ')
			}
			put(t, dir, "control-out", 0, raw)
			if err := s.Pump(); err == nil {
				t.Fatal("reused sequence accepted")
			}
		})
	}
}

func TestSpoolControlContinuesWhileOrdinaryRuns(t *testing.T) {
	f, s, dir, _ := makeSpool(t)
	admitted(t, s)
	put(t, dir, "ordinary-out", 0, f.msg("ordinary-out", 0))
	pump(t, s)
	put(t, dir, "control-out", 0, f.msg("control-out", 0))
	pump(t, s)
	if _, ok := s.Receive("control-out"); !ok {
		t.Fatal("ordinary work blocked control")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "control-in/consumed.json"))
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.p.ValidateAck(raw)
	if err != nil || integer(v["ordinary_seq"]) != 0 || integer(v["control_seq"]) != 0 {
		t.Fatal(v, err)
	}
	// A second outstanding ordinary request is a protocol failure.
	os.Remove(filepath.Join(dir, "ordinary-out/00000000000000000000.json"))
	put(t, dir, "ordinary-out", 1, f.msg("ordinary-out", 1))
	if err := s.Pump(); err == nil {
		t.Fatal("accepted concurrent ordinary requests")
	}
}

func TestSpoolQueueAndTemporaryTimers(t *testing.T) {
	t.Run("temporary", func(t *testing.T) {
		_, s, dir, now := makeSpool(t)
		p := filepath.Join(dir, "control-out/.00000000000000000000.tmp")
		os.WriteFile(p, nil, 0600)
		pump(t, s)
		*now = now.Add(4 * time.Second)
		os.WriteFile(p, []byte("partial"), 0600)
		pump(t, s)
		*now = now.Add(time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("temporary progress renewed deadline")
		}
	})
	t.Run("receive full", func(t *testing.T) {
		f, s, dir, now := makeSpool(t)
		for i := int64(0); i < 16; i++ {
			put(t, dir, "control-out", i, f.msg("control-out", i))
			pump(t, s)
			name, _ := contracts.SpoolMessageName(i, false)
			os.Remove(filepath.Join(dir, "control-out", name))
		}
		put(t, dir, "control-out", 16, f.msg("control-out", 16))
		pump(t, s)
		raw, _ := os.ReadFile(filepath.Join(dir, "control-in/consumed.json"))
		ack, _ := f.p.ValidateAck(raw)
		if integer(ack["control_seq"]) != 15 {
			t.Fatal("ACK exceeded bounded queue")
		}
		*now = now.Add(5 * time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("full queue did not time out")
		}
	})
	t.Run("send full", func(t *testing.T) {
		f, s, _, now := makeSpool(t)
		for i := int64(0); i < 16; i++ {
			if err := s.Enqueue("control-in", f.msg("control-in", i)); err != nil {
				t.Fatal(err)
			}
		}
		if !errors.Is(s.Enqueue("control-in", f.msg("control-in", 16)), ErrQueueFull) {
			t.Fatal("missing backpressure")
		}
		*now = now.Add(5 * time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("full send queue did not time out")
		}
	})
}

func TestSpoolSizeAndCleanup(t *testing.T) {
	f := fixtures(t)
	dir := directory(t)
	c := config(f)
	c.SpoolMaxBytes = 1024
	s, err := NewSpool(dir, c)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	s.now = func() time.Time { return now }
	// Count unexpected nested files without reading their contents.
	os.Mkdir(filepath.Join(dir, "ordinary-out/nested"), 0700)
	os.WriteFile(filepath.Join(dir, "ordinary-out/nested/data"), make([]byte, 1025), 0600)
	now = now.Add(time.Second)
	if !errors.Is(s.Pump(), ErrSpoolLimit) {
		t.Fatal("missing spool quota failure", s.config.Fence.Err())
	}
	used, limit, ok := s.SpoolUsage()
	if !ok || used != 1025 || limit != 1024 {
		t.Fatal(used, limit, ok)
	}
	if err := s.CleanupAfterExit(true); err == nil {
		t.Fatal("cleanup while writer active")
	}
	s.Close()
	if err := s.CleanupAfterExit(false); err == nil {
		t.Fatal("cleanup without exit confirmation")
	}
	outside := directory(t)
	os.WriteFile(filepath.Join(outside, "keep"), []byte("retained"), 0600)
	os.Symlink(outside, filepath.Join(dir, "control-out/escape"))
	if err := s.CleanupAfterExit(true); err != nil {
		t.Fatal(err)
	}
	if err := s.CleanupAfterExit(true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "keep")); err != nil {
		t.Fatal("cleanup followed link", err)
	}
}

func TestStartupOperationAndCampaignDeadlines(t *testing.T) {
	t.Run("bootstrap", func(t *testing.T) {
		_, s, _, now := makeSpool(t)
		*now = s.phaseDeadline
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("bootstrap deadline missing")
		}
	})
	t.Run("initialization", func(t *testing.T) {
		_, s, _, now := makeSpool(t)
		*now = now.Add(40 * time.Second)
		if err := s.BeginInitialization(); err != nil {
			t.Fatal(err)
		}
		*now = now.Add(60 * time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("initialization deadline missing")
		}
	})
	t.Run("no renewal", func(t *testing.T) {
		_, s, _, _ := makeSpool(t)
		if err := s.BeginInitialization(); err != nil {
			t.Fatal(err)
		}
		if err := s.BeginInitialization(); err == nil {
			t.Fatal("phase renewed")
		}
	})
	t.Run("ordinary before admission", func(t *testing.T) {
		f, s, dir, _ := makeSpool(t)
		put(t, dir, "ordinary-out", 0, f.msg("ordinary-out", 0))
		if err := s.Pump(); err == nil {
			t.Fatal("ordinary pre-admission accepted")
		}
	})
	t.Run("operation", func(t *testing.T) {
		f, s, dir, now := makeSpool(t)
		admitted(t, s)
		req := change(f.msg("ordinary-out", 0), func(m map[string]any) { m["timeout_ms"] = 1000 })
		put(t, dir, "ordinary-out", 0, req)
		pump(t, s)
		*now = now.Add(time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("operation timeout missing")
		}
	})
	t.Run("native narrowing", func(t *testing.T) {
		f, s, dir, now := makeSpool(t)
		admitted(t, s)
		put(t, dir, "ordinary-out", 0, f.msg("ordinary-out", 0))
		pump(t, s)
		if err := s.TightenOperation(now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		s.TightenOperation(now.Add(time.Hour))
		*now = now.Add(time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("native timeout extended")
		}
	})
	t.Run("campaign", func(t *testing.T) {
		f, s, dir, now := makeSpool(t)
		admitted(t, s)
		*now = s.config.CampaignDeadline.Add(-time.Second)
		put(t, dir, "ordinary-out", 0, f.msg("ordinary-out", 0))
		pump(t, s)
		deadline, _ := s.OperationDeadline()
		if !deadline.Equal(s.config.CampaignDeadline) {
			t.Fatal("campaign bound lost")
		}
		*now = now.Add(time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("campaign deadline missing")
		}
	})
}

type fifoGuest [4]*os.File

func fifoSetup(t *testing.T) (fixture, *Session, string, fifoGuest, *time.Time) {
	t.Helper()
	f := fixtures(t)
	dir := directory(t)
	s, err := NewFIFO(dir, config(f))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	var guest fifoGuest
	for _, i := range []int{0, 2, 1, 3} {
		flags := os.O_RDONLY
		if i%2 == 1 {
			flags = os.O_WRONLY
		}
		guest[i], err = os.OpenFile(filepath.Join(dir, lanes[i]), flags|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, f := range guest {
			f.Close()
		}
	})
	now := time.Now()
	s.now = func() time.Time { return now }
	pump(t, s)
	return f, s, dir, guest, &now
}
func writeGuest(t *testing.T, f *os.File, raw []byte) {
	t.Helper()
	n, err := fifoIO(f, raw, true)
	if err != nil || n != len(raw) {
		t.Fatal("guest write", n, err)
	}
}

func TestFIFORealByteExchangeAndEOF(t *testing.T) {
	f, s, _, g, _ := fifoSetup(t)
	admitted(t, s)
	frame, err := f.p.EncodeFrame("ordinary-out", f.msg("ordinary-out", 0))
	if err != nil {
		t.Fatal(err)
	}
	writeGuest(t, g[1], frame[:1])
	pump(t, s)
	writeGuest(t, g[1], frame[1:3])
	pump(t, s)
	writeGuest(t, g[1], frame[3:])
	pump(t, s)
	if raw, ok := s.Receive("ordinary-out"); !ok || !bytes.Equal(raw, f.msg("ordinary-out", 0)) {
		t.Fatal("fragmented frame lost")
	}
	if err := s.Enqueue("ordinary-in", f.msg("ordinary-in", 0)); err != nil {
		t.Fatal(err)
	}
	pump(t, s)
	var buf [4096]byte
	n, err := fifoIO(g[0], buf[:], false)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := f.p.EncodeFrame("ordinary-in", f.msg("ordinary-in", 0))
	if !bytes.Equal(buf[:n], want) {
		t.Fatal("wrong FIFO bytes")
	}
	g[3].Close()
	if !errors.Is(s.Pump(), ErrPeerLost) {
		t.Fatal("established EOF not terminal")
	}
}

func TestFIFORendezvousAndDeadlines(t *testing.T) {
	t.Run("initial EOF and absent readers", func(t *testing.T) {
		f := fixtures(t)
		s, err := NewFIFO(directory(t), config(f))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		now := time.Now()
		s.now = func() time.Time { return now }
		pump(t, s)
		if !errors.Is(s.Enqueue("control-in", f.msg("control-in", 0)), ErrNotReady) {
			t.Fatal("bootstrap before rendezvous")
		}
		now = now.Add(10 * time.Second)
		pump(t, s)
		now = s.phaseDeadline
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("rendezvous never expired")
		}
	})
	t.Run("partial frame", func(t *testing.T) {
		_, s, _, g, now := fifoSetup(t)
		admitted(t, s)
		writeGuest(t, g[3], []byte{0})
		pump(t, s)
		*now = now.Add(4 * time.Second)
		writeGuest(t, g[3], []byte{0})
		pump(t, s)
		*now = now.Add(time.Second)
		if !errors.Is(s.Pump(), ErrDeadline) {
			t.Fatal("partial bytes renewed timer")
		}
	})
	t.Run("oversized header", func(t *testing.T) {
		_, s, _, g, _ := fifoSetup(t)
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], contracts.ControlLimit+1)
		writeGuest(t, g[3], hdr[:])
		if err := s.Pump(); err == nil {
			t.Fatal("oversized header accepted")
		}
		if s.driver.(*fifo).lanes[3].decoder == nil {
			t.Fatal("decoder missing")
		}
	})
	t.Run("inode replacement", func(t *testing.T) {
		_, s, dir, _, _ := fifoSetup(t)
		os.Remove(filepath.Join(dir, "control-out"))
		syscall.Mkfifo(filepath.Join(dir, "control-out"), 0600)
		if err := s.Pump(); err == nil {
			t.Fatal("replaced FIFO accepted")
		}
	})
	t.Run("blocked write", func(t *testing.T) {
		f, s, _, _, now := fifoSetup(t)
		// Fill the real kernel pipe without relying on a platform's pipe capacity.
		file := s.driver.(*fifo).lanes[2].file
		blocked := false
		for i := 0; i < 4096; i++ {
			_, err := fifoIO(file, make([]byte, 4096), true)
			if wouldBlock(err) {
				blocked = true
				break
			}
			if err != nil {
				t.Fatal(err)
			}
		}
		if !blocked {
			t.Fatal("could not establish kernel backpressure")
		}
		if err := s.Enqueue("control-in", f.msg("control-in", 0)); err != nil {
			t.Fatal(err)
		}
		pump(t, s)
		*now = now.Add(5 * time.Second)
		if err := s.Pump(); err == nil {
			t.Fatal("blocked reader not detected")
		}
	})
}

func TestRunCancellationDoesNotWaitForIOMutex(t *testing.T) {
	_, s, _, _ := makeSpool(t)
	s.mu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cancel()
	select {
	case <-s.config.Fence.Done():
	case <-time.After(time.Second):
		s.mu.Unlock()
		t.Fatal("cancellation waited for I/O")
	}
	s.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestNoReconnectAndReplacementCleanup(t *testing.T) {
	f, s, dir, _ := makeSpool(t)
	s.Close()
	if _, err := NewSpool(dir, config(f)); err == nil {
		t.Fatal("reopened old launch")
	}
	old := dir + "-old"
	if err := os.Rename(dir, old); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(old) })
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "keep"), []byte("replacement"), 0600)
	if err := s.CleanupAfterExit(true); err == nil {
		t.Fatal("deleted replacement root")
	}
	if _, err := os.Stat(filepath.Join(dir, "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemBoundsDoNotEchoGuestContent(t *testing.T) {
	_, s, dir, _ := makeSpool(t)
	os.WriteFile(filepath.Join(dir, "control-out/00000000000000000000.json"), []byte(`{"secret":"do-not-echo"}`), 0600)
	err := s.Pump()
	if err == nil || strings.Contains(err.Error(), "do-not-echo") {
		t.Fatal(err)
	}
}

func TestCompleteStartupOverBothTransports(t *testing.T) {
	for _, backend := range []string{"fifo", "spool"} {
		t.Run(backend, func(t *testing.T) {
			var f fixture
			var s *Session
			var dir string
			var guest fifoGuest
			if backend == "fifo" {
				f, s, dir, guest, _ = fifoSetup(t)
			} else {
				f, s, dir, _ = makeSpool(t)
			}
			raw, err := os.ReadFile("../../schemas/fixtures/identity-validation.json")
			if err != nil {
				t.Fatal(err)
			}
			var cases []struct{ Messages []json.RawMessage }
			if err := json.Unmarshal(raw, &cases); err != nil {
				t.Fatal(err)
			}
			messages := make([][]byte, 5)
			for i, m := range cases[0].Messages {
				messages[i] = m
				if backend == "spool" && i < 2 {
					messages[i] = change(m, func(v map[string]any) {
						b := v["body"].(map[string]any)
						b["host_platform"], b["transport"] = "darwin/arm64", "spool"
					})
				}
			}
			if err := f.p.ValidateStartup(messages); err != nil {
				t.Fatal(err)
			}
			for i, m := range messages {
				seq := int64(i / 2)
				if i%2 == 0 {
					if err := s.Enqueue("control-in", m); err != nil {
						t.Fatal(err)
					}
					if s.Published("control-in", seq) {
						t.Fatal("queued bytes reported published")
					}
					pump(t, s)
					var captured []byte
					if backend == "spool" {
						name, _ := contracts.SpoolMessageName(seq, false)
						captured, err = os.ReadFile(filepath.Join(dir, "control-in", name))
						if err != nil {
							t.Fatal(err)
						}
						ack(t, dir, nil, seq)
					} else {
						decoder, _ := f.p.NewFrameDecoder("control-in")
						for tries := 0; captured == nil && tries < 20; tries++ {
							var buf [65536]byte
							n, err := fifoIO(guest[2], buf[:], false)
							if err != nil && !wouldBlock(err) {
								t.Fatal(err)
							}
							if n > 0 {
								_, captured, err = decoder.Feed(buf[:n])
								if err != nil {
									t.Fatal(err)
								}
							}
							pump(t, s)
						}
					}
					if !bytes.Equal(captured, m) || !s.Published("control-in", seq) {
						t.Fatal("startup publication lost")
					}
					pump(t, s)
				} else {
					if backend == "spool" {
						put(t, dir, "control-out", seq, m)
					} else {
						frame, err := f.p.EncodeFrame("control-out", m)
						if err != nil {
							t.Fatal(err)
						}
						writeGuest(t, guest[3], frame)
					}
					pump(t, s)
					captured, ok := s.Receive("control-out")
					if !ok || !bytes.Equal(captured, m) {
						t.Fatal("startup capture lost")
					}
					if i == 1 {
						if err := s.BeginInitialization(); err != nil {
							t.Fatal(err)
						}
					}
				}
			}
			// The guest can respond immediately to admission_open, before the broker
			// observes publication. Keep the request pending until the trusted gate opens.
			req := f.msg("ordinary-out", 0)
			if backend == "spool" {
				put(t, dir, "ordinary-out", 0, req)
			} else {
				frame, _ := f.p.EncodeFrame("ordinary-out", req)
				writeGuest(t, guest[1], frame)
			}
			pump(t, s)
			if _, ok := s.Receive("ordinary-out"); ok {
				t.Fatal("request bypassed broker gate")
			}
			if err := s.OpenAdmission(); err != nil {
				t.Fatal(err)
			}
			pump(t, s)
			if got, ok := s.Receive("ordinary-out"); !ok || !bytes.Equal(got, req) {
				t.Fatal("fast guest request lost")
			}
		})
	}
}

func TestFIFOLostReaderIsTerminal(t *testing.T) {
	f, s, _, guest, _ := fifoSetup(t)
	guest[2].Close()
	if err := s.Enqueue("control-in", f.msg("control-in", 0)); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(s.Pump(), ErrPeerLost) {
		t.Fatal("EPIPE did not terminate transport")
	}
}

func TestSpoolChangedUnacknowledgedFile(t *testing.T) {
	f, s, dir, _ := makeSpool(t)
	for i := int64(0); i < 16; i++ {
		put(t, dir, "control-out", i, f.msg("control-out", i))
		pump(t, s)
		name, _ := contracts.SpoolMessageName(i, false)
		if err := os.Remove(filepath.Join(dir, "control-out", name)); err != nil {
			t.Fatal(err)
		}
	}
	put(t, dir, "control-out", 16, f.msg("control-out", 16))
	pump(t, s)
	mutated := append(f.msg("control-out", 16), ' ')
	put(t, dir, "control-out", 16, mutated)
	if !errors.Is(s.Pump(), ErrProtocol) {
		t.Fatal("changed unconsumed bytes accepted")
	}
}
