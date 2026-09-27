package preparation_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

func spoolPut(dir, lane string, seq int64, raw []byte) error {
	name, _ := contracts.SpoolMessageName(seq, false)
	temp, _ := contracts.SpoolMessageName(seq, true)
	if err := os.WriteFile(filepath.Join(dir, lane, temp), raw, 0600); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, lane, temp), filepath.Join(dir, lane, name))
}
func spoolRead(ctx context.Context, dir, lane string, seq int64) ([]byte, error) {
	name, _ := contracts.SpoolMessageName(seq, false)
	for {
		raw, err := os.ReadFile(filepath.Join(dir, lane, name))
		if err == nil {
			return raw, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Millisecond):
		}
	}
}
func spoolACK(dir string, ordinary, control any) error {
	raw := encode(map[string]any{"api_version": "operator.dev/engine-spool-ack/v1alpha1", "launch_id": "launch-1", "ordinary_seq": ordinary, "control_seq": control})
	temp := filepath.Join(dir, "control-out/.consumed.tmp")
	if err := os.WriteFile(temp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(temp, filepath.Join(dir, "control-out/consumed.json"))
}

// This simulated guest responds to what actually arrived, rather than supplying
// the harness's half of a transcript directly to the host admission routine.
func bootstrapGuest(ctx context.Context, dir, mode string) error {
	raw, err := spoolRead(ctx, dir, "control-in", 0)
	if err != nil {
		return err
	}
	var boot map[string]any
	if err = json.Unmarshal(raw, &boot); err != nil {
		return err
	}
	if err = spoolACK(dir, nil, 0); err != nil {
		return err
	}
	boot["kind"] = "confinement_ready"
	if mode == "numeric spelling" {
		boot["seq"] = json.Number("0.0")
		boot["run_revision"] = json.Number("1.0")
	}
	body := boot["body"].(map[string]any)
	delete(body, "timeout_ms")
	contract := body["contract"]
	if mode == "silent" {
		<-ctx.Done()
		return ctx.Err()
	}
	if mode == "wrong confinement" {
		body["container_id"] = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	}
	if err = spoolPut(dir, "control-out", 0, encode(boot)); err != nil {
		return err
	}
	if mode == "wrong confinement" {
		return nil
	}
	raw, err = spoolRead(ctx, dir, "control-in", 1)
	if err != nil {
		return err
	}
	var init map[string]any
	if err = json.Unmarshal(raw, &init); err != nil {
		return err
	}
	if err = spoolACK(dir, nil, 1); err != nil {
		return err
	}
	init["kind"] = "initialized"
	body = init["body"].(map[string]any)
	delete(body, "timeout_ms")
	body["contract"] = contract
	if mode == "wrong initialized" {
		body["engine_context_object_digest"] = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	}
	if err = spoolPut(dir, "control-out", 1, encode(init)); err != nil {
		return err
	}
	if mode == "wrong initialized" {
		return nil
	}
	if _, err = spoolRead(ctx, dir, "control-in", 2); err != nil {
		return err
	}
	return spoolACK(dir, nil, 2)
}
func TestLiveBootstrapGatesServiceAdmission(t *testing.T) {
	for _, mode := range []string{"success", "numeric spelling", "wrong confinement", "wrong initialized", "silent", "host check failure"} {
		t.Run(mode, func(t *testing.T) {
			service, native, _, writer, launch := serviceFixture(t)
			dir := t.TempDir()
			_ = os.Chmod(dir, 0700)
			channel, err := transport.NewSpool(dir, transport.Config{Protocol: native.input.Protocol, CampaignID: "campaign-1", LaunchID: "launch-1", Fence: writer.Fence(), CampaignDeadline: time.Now().Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			defer channel.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if mode == "silent" {
				short, stop := context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
				ctx = short
			}
			pumpDone := make(chan error, 1)
			go func() { pumpDone <- channel.Run(ctx) }()
			defer func() { cancel(); <-pumpDone }()
			guestDone := make(chan error, 1)
			go func() { guestDone <- bootstrapGuest(ctx, dir, mode) }()
			defer func() { cancel(); <-guestDone }()
			// Even valid fixture guest replies supplied by a caller cannot bypass the wire.
			launch.Messages = [][]byte{[]byte("invented transcript")}
			checks := 0
			check := func(context.Context) error {
				checks++
				if mode == "host check failure" {
					return errors.New("host interrupted")
				}
				return nil
			}
			err = hostworker.Bootstrap(ctx, native.input.Protocol, writer, channel, service, launch, check)
			if mode != "success" && mode != "numeric spelling" {
				if err == nil || writer.Fence().Err() == nil {
					t.Fatal("failure did not fence", err)
				}
				name, _ := contracts.SpoolMessageName(2, false)
				if _, e := os.Stat(filepath.Join(dir, "control-in", name)); !errors.Is(e, os.ErrNotExist) {
					t.Fatal("published admission on failure", e)
				}
				return
			}
			if err != nil || checks < 4 {
				t.Fatal("bootstrap failed", err, checks)
			}
			request := attemptWire(native, 1, 1, writer.Manifest().ReleaseRecordDigest)
			if _, err = service.Handle(ctx, request, 0); err != nil {
				t.Fatal("service not admitted", err)
			}
			if writer.Fence().Err() != nil {
				t.Fatal(writer.Fence().Err())
			}
		})
	}
}
