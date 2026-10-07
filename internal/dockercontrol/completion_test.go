//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestCompletedRequiresExactStoppedZeroExitAndStableDaemon(t *testing.T) {
	b := binding()
	for _, mode := range []string{"success", "nonzero", "unknown", "wrong image", "running", "created", "daemon changed", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			item := live(b)
			item.Status, item.Running = "exited", false
			if mode == "wrong image" {
				item.Image = "sha256:" + strings.Repeat("a", 64)
			}
			if mode == "running" {
				item.Status, item.Running = "running", true
			}
			if mode == "created" {
				item.Status = "created"
			}
			infos := 0
			c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				if endpoint != b.Endpoint {
					t.Fatal("changed endpoint")
				}
				switch {
				case reflect.DeepEqual(args, []string{"info", "--format", "{{json .ID}}"}):
					infos++
					if mode == "daemon changed" && infos > 1 {
						return json.Marshal("changed")
					}
					return json.Marshal(b.DaemonID)
				case reflect.DeepEqual(args, []string{"container", "inspect", "--format", inspectFormat, b.DockerContainerID}):
					return json.Marshal(item)
				case reflect.DeepEqual(args, []string{"container", "inspect", "--format", "{{.State.ExitCode}}", b.DockerContainerID}):
					if mode == "nonzero" {
						return []byte("1\n"), nil
					}
					if mode == "unknown" {
						return []byte("null\n"), nil
					}
					return []byte("0\n"), nil
				default:
					t.Fatalf("unexpected or mutating command: %v", args)
					return nil, nil
				}
			}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			if c.Completed(ctx, b) != (mode == "success") {
				t.Fatal("exit proof did not fail closed", mode)
			}
		})
	}
}
