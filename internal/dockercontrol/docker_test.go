//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
)

func binding() campaign.DockerBinding {
	b := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64),
		RunManifestDigest: contracts.RawDigest([]byte("manifest")), Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1",
		DockerContainerID: strings.Repeat("b", 64), ImageDigest: contracts.RawDigest([]byte("image"))}
	b.Labels = (campaign.RunManifest{CampaignID: b.CampaignID, LaunchID: b.LaunchID, ContainerID: b.ContainerID}).DockerLabels()
	return b
}

func live(b campaign.DockerBinding) identity {
	return identity{ID: b.DockerContainerID, Image: b.ImageDigest, Labels: b.Labels, Status: "running", Running: true, RestartPolicy: "no"}
}

type fakeDocker struct {
	t                         *testing.T
	b                         campaign.DockerBinding
	item                      identity
	daemon                    string
	kills, inspections, infos int
	killError, inspectError   bool
	stayRunning               bool
	afterKill                 func()
	mutateRaw                 func([]byte) []byte
}

func (f *fakeDocker) command(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
	f.t.Helper()
	if endpoint != f.b.Endpoint {
		f.t.Fatal("Docker endpoint drifted", endpoint)
	}
	switch args[0] {
	case "info":
		f.infos++
		if !reflect.DeepEqual(args, []string{"info", "--format", "{{json .ID}}"}) {
			f.t.Fatal(args)
		}
		return json.Marshal(f.daemon)
	case "container":
		if args[len(args)-1] != f.b.DockerContainerID {
			f.t.Fatal("not exact saved ID", args)
		}
		if args[1] == "inspect" {
			f.inspections++
			if !reflect.DeepEqual(args, []string{"container", "inspect", "--format", inspectFormat, f.b.DockerContainerID}) {
				f.t.Fatal(args)
			}
			if f.inspectError {
				return nil, errors.New("not found or unavailable")
			}
			raw, _ := json.Marshal(f.item)
			if f.mutateRaw != nil {
				raw = f.mutateRaw(raw)
			}
			return raw, nil
		}
		if !reflect.DeepEqual(args, []string{"container", "kill", "--signal", "SIGKILL", f.b.DockerContainerID}) {
			f.t.Fatal(args)
		}
		f.kills++
		if !f.stayRunning {
			f.item.Running = false
			f.item.Status = "exited"
		}
		if f.afterKill != nil {
			f.afterKill()
		}
		if f.killError {
			return nil, errors.New("lost kill response")
		}
		return []byte(f.b.DockerContainerID), nil
	default:
		f.t.Fatal("unexpected command", args)
		return nil, nil
	}
}
func fake(t *testing.T) (*Client, *fakeDocker) {
	b := binding()
	f := &fakeDocker{t: t, b: b, item: live(b), daemon: b.DaemonID}
	return &Client{run: f.command}, f
}

func TestExactKillAndRepeatedStop(t *testing.T) {
	c, f := fake(t)
	out := c.Terminate(context.Background(), f.b)
	if !out.Confirmed || !out.KillAttempted || f.kills != 1 || f.inspections != 2 || f.infos != 3 {
		t.Fatal(out, f)
	}
	out = c.Terminate(context.Background(), f.b)
	if !out.Confirmed || out.KillAttempted || f.kills != 1 {
		t.Fatal("repeat was not idempotent", out)
	}
}

func TestIdentityAndAbsenceCannotAuthorizeKill(t *testing.T) {
	for name, alter := range map[string]func(*fakeDocker){
		"daemon":   func(f *fakeDocker) { f.daemon = "other-daemon" },
		"full ID":  func(f *fakeDocker) { f.item.ID = strings.Repeat("c", 64) },
		"image":    func(f *fakeDocker) { f.item.Image = contracts.RawDigest([]byte("other")) },
		"campaign": func(f *fakeDocker) { f.item.Labels = map[string]string{"ai.intrusive.operator.campaign": "other"} },
		"missing":  func(f *fakeDocker) { f.inspectError = true },
		"null running": func(f *fakeDocker) {
			f.mutateRaw = func(raw []byte) []byte {
				return []byte(strings.Replace(string(raw), `"running":true`, `"running":null`, 1))
			}
		},
		"duplicate": func(f *fakeDocker) {
			f.mutateRaw = func(raw []byte) []byte { return append([]byte(`{"running":false,`), raw[1:]...) }
		},
		"missing field": func(f *fakeDocker) {
			f.mutateRaw = func(raw []byte) []byte { return []byte(strings.Replace(string(raw), `"paused":false,`, "", 1)) }
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, f := fake(t)
			alter(f)
			out := c.Terminate(context.Background(), f.b)
			if out.Confirmed || f.kills != 0 {
				t.Fatal(out, f.kills)
			}
		})
	}
}

func TestUncertainKillAndLifecyclePolicy(t *testing.T) {
	t.Run("lost response but exit proved", func(t *testing.T) {
		c, f := fake(t)
		f.killError = true
		out := c.Terminate(context.Background(), f.b)
		if !out.Confirmed {
			t.Fatal(out)
		}
	})
	t.Run("daemon changes during kill", func(t *testing.T) {
		c, f := fake(t)
		f.afterKill = func() { f.daemon = "replacement" }
		out := c.Terminate(context.Background(), f.b)
		if out.Confirmed || out.Code != "identity_mismatch" {
			t.Fatal(out)
		}
	})
	t.Run("absent after kill", func(t *testing.T) {
		c, f := fake(t)
		f.afterKill = func() { f.inspectError = true }
		out := c.Terminate(context.Background(), f.b)
		if out.Confirmed {
			t.Fatal(out)
		}
	})
	t.Run("restart policy", func(t *testing.T) {
		c, f := fake(t)
		f.item.RestartPolicy = "always"
		out := c.Terminate(context.Background(), f.b)
		if out.Confirmed || !out.KillAttempted || out.Code != "lifecycle_policy_mismatch" {
			t.Fatal(out)
		}
	})
	t.Run("auto remove", func(t *testing.T) {
		c, f := fake(t)
		f.item.AutoRemove = true
		out := c.Terminate(context.Background(), f.b)
		if out.Confirmed || !out.KillAttempted {
			t.Fatal(out)
		}
	})
	t.Run("kill accepted still running", func(t *testing.T) {
		c, f := fake(t)
		f.stayRunning = true
		ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
		defer cancel()
		out := c.Terminate(ctx, f.b)
		if out.Confirmed || f.kills != 1 || out.Code != "deadline_or_cancellation" {
			t.Fatal(out, f.kills)
		}
	})
	for _, state := range []string{"created", "exited", "dead"} {
		t.Run(state, func(t *testing.T) {
			c, f := fake(t)
			f.item.Running = false
			f.item.Status = state
			out := c.Terminate(context.Background(), f.b)
			if !out.Confirmed || f.kills != 0 {
				t.Fatal(out)
			}
		})
	}
}

func TestBadBindingsMakeNoDockerCalls(t *testing.T) {
	for _, alter := range []func(*campaign.DockerBinding){
		func(b *campaign.DockerBinding) { b.Endpoint = "tcp://remote:2375" },
		func(b *campaign.DockerBinding) { b.DockerContainerID = "named-container" },
		func(b *campaign.DockerBinding) { b.Endpoint = "unix:///saved/../other.sock" },
		func(b *campaign.DockerBinding) { b.Labels = nil },
	} {
		c, f := fake(t)
		b := f.b
		alter(&b)
		out := c.Terminate(context.Background(), b)
		if out.Confirmed || f.infos != 0 {
			t.Fatal(out)
		}
	}
}

func TestRealCommandIgnoresDockerEnvironmentAndBoundsOutput(t *testing.T) {
	// Exercise the actual subprocess wrapper; no Docker daemon is contacted.
	for _, key := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS_VERIFY", "DOCKER_API_VERSION", "DOCKER_CONFIG", "DOCKER_CUSTOM_HEADERS", "HTTP_PROXY"} {
		t.Setenv(key, "unexpected")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ -n "$DOCKER_HOST$DOCKER_CONTEXT$DOCKER_TLS_VERIFY$DOCKER_API_VERSION$DOCKER_CONFIG$DOCKER_CUSTOM_HEADERS$HTTP_PROXY" ]; then exit 17; fi
if [ "$1" != "--host" ] || [ "$2" != "unix:///saved/docker.sock" ]; then exit 18; fi
case "$3" in
ok) printf '"daemon-1"\n'; printf 'ignored diagnostic' >&2 ;;
large) /usr/bin/head -c 70000 /dev/zero ;;
wait) exec /bin/sleep 10 ;;
*) exit 19 ;;
esac
`
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	c, err := New(bin)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.run(context.Background(), binding().Endpoint, "ok")
	if err != nil || strings.TrimSpace(string(raw)) != `"daemon-1"` {
		t.Fatal(string(raw), err)
	}
	if _, err := c.run(context.Background(), binding().Endpoint, "large"); err == nil {
		t.Fatal("unbounded output accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := c.run(ctx, binding().Endpoint, "wait"); err == nil || time.Since(start) > time.Second {
		t.Fatal("subprocess did not stop", err)
	}
}

func TestCheckRunningRequiresExactHealthyIdentity(t *testing.T) {
	for name, change := range map[string]func(*fakeDocker){
		"healthy":         func(*fakeDocker) {},
		"wrong daemon":    func(f *fakeDocker) { f.daemon = "other" },
		"wrong container": func(f *fakeDocker) { f.item.ID = strings.Repeat("c", 64) },
		"wrong image":     func(f *fakeDocker) { f.item.Image = contracts.RawDigest([]byte("other")) },
		"stopped":         func(f *fakeDocker) { f.item.Running = false; f.item.Status = "exited" },
		"paused":          func(f *fakeDocker) { f.item.Paused = true },
		"restarting":      func(f *fakeDocker) { f.item.Restarting = true },
		"restart policy":  func(f *fakeDocker) { f.item.RestartPolicy = "always" },
		"auto removal":    func(f *fakeDocker) { f.item.AutoRemove = true },
		"missing":         func(f *fakeDocker) { f.inspectError = true },
	} {
		t.Run(name, func(t *testing.T) {
			c, f := fake(t)
			change(f)
			err := c.CheckRunning(context.Background(), f.b)
			if (err == nil) != (name == "healthy") {
				t.Fatal(err)
			}
			if f.kills != 0 {
				t.Fatal("read-only check mutated Docker")
			}
		})
	}
}
