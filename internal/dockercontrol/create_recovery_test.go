//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

func TestResolveCreateRequiresOneVerifiedCandidate(t *testing.T) {
	p, _, _ := launchFixture(t, "spool")
	intent := campaign.CreateIntent{Manifest: p.manifest, ManifestDigest: p.manifestDigest, Endpoint: p.image.Endpoint, DaemonID: p.image.DaemonID}
	b, err := intent.Binding(strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := json.Marshal(b.DockerContainerID)
	for _, kind := range []string{"created", "running", "absent", "multiple", "short ID", "object", "failed list", "inspect failed", "wrong ID", "wrong image", "wrong labels", "restart policy", "auto remove", "daemon changed", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			infos := 0
			item := live(b)
			if kind == "created" {
				item.Status = "created"
				item.Running = false
			}
			switch kind {
			case "wrong ID":
				item.ID = strings.Repeat("c", 64)
			case "wrong image":
				item.Image = "sha256:" + strings.Repeat("c", 64)
			case "wrong labels":
				item.Labels = nil
			case "restart policy":
				item.RestartPolicy = "always"
			case "auto remove":
				item.AutoRemove = true
			}
			c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				if endpoint != intent.Endpoint {
					t.Fatal("changed endpoint")
				}
				switch {
				case reflect.DeepEqual(args, []string{"info", "--format", "{{json .ID}}"}):
					infos++
					if kind == "daemon changed" && infos >= 3 {
						return json.Marshal("other-daemon")
					}
					return json.Marshal(intent.DaemonID)
				case reflect.DeepEqual(args, []string{"container", "ls", "--all", "--no-trunc", "--filter", "label=ai.intrusive.operator.campaign=" + b.CampaignID, "--filter", "label=ai.intrusive.operator.container=" + b.ContainerID, "--filter", "label=ai.intrusive.operator.launch=" + b.LaunchID, "--format", "{{json .ID}}"}):
					switch kind {
					case "absent":
						return nil, nil
					case "multiple":
						return []byte(string(id) + "\n" + string(id)), nil
					case "short ID":
						return []byte(`"bbbb"`), nil
					case "object":
						return []byte(`{}`), nil
					case "failed list":
						return nil, errors.New("unavailable")
					}
					return id, nil
				case reflect.DeepEqual(args, []string{"container", "inspect", "--format", inspectFormat, b.DockerContainerID}):
					if kind == "inspect failed" {
						return nil, errors.New("disappeared")
					}
					return json.Marshal(item)
				default:
					t.Fatalf("unexpected or mutating command: %v", args)
					return nil, ErrLaunch
				}
			}}
			ctx := context.Background()
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			got, err := c.ResolveCreate(ctx, intent)
			if kind == "created" || kind == "running" {
				if err != nil || !sameBinding(got, b) {
					t.Fatal(got, err)
				}
			} else if err == nil || got.DockerContainerID != "" {
				t.Fatal("adopted unverified container", got, err)
			}
		})
	}
}

func TestCreateKeepsCompleteIDOnFailedCommandWithoutStartPermission(t *testing.T) {
	p, root, w := launchFixture(t, "spool")
	f := &launchDocker{t: t, p: p, daemon: p.image.DaemonID, volumes: "null"}
	c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
		raw, err := f.run(ctx, endpoint, args...)
		if len(args) > 3 && args[0] == "--config" {
			return raw, errors.New("late CLI failure")
		}
		return raw, err
	}}
	b, err := c.Create(context.Background(), p)
	if err == nil || b.DockerContainerID != strings.Repeat("b", 64) {
		t.Fatal(b, err)
	}
	if err := w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := c.StartCreated(context.Background(), root, b, p); err == nil || f.started != 0 {
		t.Fatal("started after failed create", err)
	}
}
