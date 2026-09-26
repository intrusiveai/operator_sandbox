package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

type transportStep struct {
	Action   string          `json:"action"`
	Lane     string          `json:"lane"`
	Raw      string          `json:"raw_base64"`
	Expected json.RawMessage `json:"expected"`
	Valid    bool            `json:"valid"`
}
type transportFixture struct {
	Name      string          `json:"name"`
	Mode      string          `json:"mode"`
	Lane      string          `json:"lane"`
	Chunks    []string        `json:"chunks_base64"`
	Frames    []string        `json:"frames_base64"`
	Valid     bool            `json:"valid"`
	EOF       bool            `json:"eof"`
	Filename  string          `json:"filename"`
	Sequence  int64           `json:"sequence"`
	Temporary bool            `json:"temporary"`
	Raw       string          `json:"raw_base64"`
	Role      string          `json:"role"`
	Campaign  string          `json:"campaign_id"`
	Launch    string          `json:"launch_id"`
	Steps     []transportStep `json:"steps"`
}

func transportFixtures(t *testing.T) []transportFixture {
	t.Helper()
	raw, err := os.ReadFile("../schemas/fixtures/transport-codec.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []transportFixture
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}
func TestSharedTransport(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	cases := transportFixtures(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			decode := func(s string) []byte {
				raw, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			var err error
			switch c.Mode {
			case "frames":
				d, e := p.NewFrameDecoder(c.Lane)
				if e != nil {
					t.Fatal(e)
				}
				var frames [][]byte
				for _, encoded := range c.Chunks {
					remaining := decode(encoded)
					for first := true; first || len(remaining) > 0; first = false {
						var n int
						var frame []byte
						n, frame, err = d.Feed(remaining)
						if n < 0 || n > len(remaining) {
							t.Fatal("invalid consumed count")
						}
						if err != nil {
							break
						}
						if frame != nil {
							frames = append(frames, frame)
						}
						if n == 0 && len(remaining) > 0 {
							t.Fatal("decoder stalled")
						}
						remaining = remaining[n:]
					}
					if err != nil {
						break
					}
				}
				if err == nil && c.EOF {
					err = d.End()
				}
				if (err == nil) != c.Valid {
					t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
				}
				if len(frames) != len(c.Frames) {
					t.Fatalf("received %d frames want %d", len(frames), len(c.Frames))
				}
				for i, frame := range frames {
					if !bytes.Equal(frame, decode(c.Frames[i])) {
						t.Fatal("decoded bytes differ")
					}
					encoded, e := p.EncodeFrame(c.Lane, frame)
					if e != nil {
						t.Fatal(e)
					}
					if binary.BigEndian.Uint32(encoded[:4]) != uint32(len(frame)) || !bytes.Equal(encoded[4:], frame) {
						t.Fatal("bad encoded frame")
					}
				}
				if err != nil {
					if _, _, next := d.Feed([]byte{0}); !errors.Is(next, ErrTransport) {
						t.Fatal("decoder reopened after failure")
					}
				}
				return
			case "name":
				var seq int64
				var temporary bool
				seq, temporary, err = ParseSpoolMessageName(c.Filename)
				if err == nil {
					if seq != c.Sequence || temporary != c.Temporary {
						t.Fatal("filename interpretation differs")
					}
					name, e := SpoolMessageName(seq, temporary)
					if e != nil || name != c.Filename {
						t.Fatal("filename did not round trip")
					}
				}
			case "spool":
				_, err = p.ValidateSpoolMessage(c.Lane, c.Filename, decode(c.Raw))
			case "state":
				state, e := p.NewTransportState(c.Role, c.Campaign, c.Launch)
				if e != nil {
					t.Fatal(e)
				}
				for i, step := range c.Steps {
					err = nil
					var actual map[string]any
					switch step.Action {
					case "accept":
						err = state.Accept(step.Lane, decode(step.Raw))
					case "publish":
						err = state.RecordPublished(step.Lane, decode(step.Raw))
					case "ack":
						err = state.ApplyAck(decode(step.Raw))
					case "acknowledged":
						actual = state.Acknowledged()
					case "consumed":
						var raw []byte
						raw, err = state.AckBytes()
						if err == nil {
							ack, e := p.ValidateAck(raw)
							if e != nil {
								t.Fatal(e)
							}
							if ack["launch_id"] != c.Launch {
								t.Fatal("wrong ACK launch")
							}
							actual = map[string]any{"ordinary_seq": ack["ordinary_seq"], "control_seq": ack["control_seq"]}
						}
					case "close":
						state.Close()
					default:
						t.Fatal("unknown state action")
					}
					if (err == nil) != step.Valid {
						t.Fatalf("step %d %s valid=%v want=%v error=%v", i, step.Action, err == nil, step.Valid, err)
					}
					if actual != nil {
						expected, e := Decode(step.Expected, ControlLimit)
						if e != nil {
							t.Fatal(e)
						}
						if !wireEqual(actual, expected) {
							t.Fatalf("step %d positions differ", i)
						}
					}
				}
				return
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
		})
	}
	t.Logf("%d shared transport cases", len(cases))
}

func TestTransportBoundaries(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	cases := transportFixtures(t)
	raw, err := base64.StdEncoding.DecodeString(cases[0].Frames[0])
	if err != nil {
		t.Fatal(err)
	}
	// Every possible split exercises partial headers and partial bodies.
	wire, err := p.EncodeFrame(cases[0].Lane, raw)
	if err != nil {
		t.Fatal(err)
	}
	for split := 0; split <= len(wire); split++ {
		d, _ := p.NewFrameDecoder(cases[0].Lane)
		n, first, e := d.Feed(wire[:split])
		if e != nil || n != split {
			t.Fatalf("split %d: %v", split, e)
		}
		n, second, e := d.Feed(wire[split:])
		if e != nil || n != len(wire)-split {
			t.Fatalf("split %d remainder: %v", split, e)
		}
		if !bytes.Equal(append(first, second...), raw) {
			t.Fatal("split lost frame bytes")
		}
	}
	for _, lane := range []string{"ordinary-out", "control-in"} {
		_, _, maximum, _ := laneInfo(lane)
		d, _ := p.NewFrameDecoder(lane)
		header := make([]byte, 4)
		binary.BigEndian.PutUint32(header, uint32(maximum+1))
		n, _, err := d.Feed(header)
		if n != 4 || !errors.Is(err, ErrLimit) || len(d.body) != 0 {
			t.Fatal("oversize frame allocated payload")
		}
	}
	state, _ := p.NewTransportState("host", "campaign-1", "launch-1")
	state.received[0] = MaxSafeInteger - 1 // Test-only boundary seed; no production reset API.
	v, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	m["seq"] = json.Number("9007199254740991")
	maximum, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.Accept("ordinary-out", maximum); err != nil {
		t.Fatal(err)
	}
	if err := state.Accept("ordinary-out", raw); err == nil {
		t.Fatal("sequence wrapped")
	}
	if _, err := SpoolMessageName(-1, false); err == nil {
		t.Fatal("negative filename sequence")
	}
	if _, err := SpoolMessageName(MaxSafeInteger+1, false); err == nil {
		t.Fatal("unsafe filename sequence")
	}
	if _, err := p.NewFrameDecoder("unknown"); err == nil {
		t.Fatal("unknown lane")
	}
}
