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

func inactivityClient(t *testing.T, b campaign.DockerBinding, listed []byte, item identity, changeDaemon bool, listError bool) *Client {
	t.Helper()
	infos := 0
	return &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
		if endpoint != b.Endpoint {
			t.Fatal("changed endpoint")
		}
		switch {
		case reflect.DeepEqual(args, []string{"info", "--format", "{{json .ID}}"}):
			infos++
			if changeDaemon && infos > 1 {
				return json.Marshal("other-daemon")
			}
			return json.Marshal(b.DaemonID)
		case reflect.DeepEqual(args, []string{"container", "ls", "--all", "--no-trunc", "--filter", "id=" + b.DockerContainerID, "--format", "{{json .ID}}"}):
			if listError {
				return nil, errors.New("unavailable")
			}
			return listed, nil
		case reflect.DeepEqual(args, []string{"container", "inspect", "--format", inspectFormat, b.DockerContainerID}):
			return json.Marshal(item)
		default:
			t.Fatalf("unexpected or mutating command: %v", args)
			return nil, nil
		}
	}}
}
func TestCheckInactiveExactAbsenceAndStoppedStates(t *testing.T) {
	b := binding()
	raw, _ := json.Marshal(b.DockerContainerID)
	for _, state := range []string{"absent", "created", "exited", "dead", "active"} {
		t.Run(state, func(t *testing.T) {
			item := live(b)
			listed := raw
			if state == "absent" {
				listed = nil
			} else if state != "active" {
				item.Running = false
				item.Status = state
			}
			c := inactivityClient(t, b, listed, item, false, false)
			got := c.CheckInactive(context.Background(), b)
			if got.State != state || got.Confirmed != (state != "active") {
				t.Fatal(got)
			}
		})
	}
}
func TestCheckInactiveUncertaintyNeverBecomesAbsence(t *testing.T) {
	b := binding()
	raw, _ := json.Marshal(b.DockerContainerID)
	stopped := live(b)
	stopped.Running = false
	stopped.Status = "exited"
	for _, tc := range []struct {
		name           string
		listed         []byte
		item           identity
		change, failed bool
	}{
		{"daemon changes with absence", nil, stopped, true, false},
		{"daemon changes with presence", raw, stopped, true, false},
		{"list failure", nil, stopped, false, true},
		{"short ID", []byte(`"bbbbbb"`), stopped, false, false},
		{"different full ID", []byte(`"` + strings.Repeat("c", 64) + `"`), stopped, false, false},
		{"multiple records", append(append([]byte{}, raw...), raw...), stopped, false, false},
		{"null", []byte(`null`), stopped, false, false},
		{"object", []byte(`{}`), stopped, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := inactivityClient(t, b, tc.listed, tc.item, tc.change, tc.failed).CheckInactive(context.Background(), b)
			if got.Confirmed || got.State != "unknown" {
				t.Fatal(got)
			}
		})
	}
	for _, alter := range []func(*identity){
		func(i *identity) { i.Image = "sha256:" + strings.Repeat("c", 64) },
		func(i *identity) { i.Labels = nil }, func(i *identity) { i.AutoRemove = true },
		func(i *identity) { i.RestartPolicy = "always" }, func(i *identity) { i.Status = "removing" },
	} {
		item := stopped
		alter(&item)
		if got := inactivityClient(t, b, raw, item, false, false).CheckInactive(context.Background(), b); got.Confirmed {
			t.Fatal(got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := inactivityClient(t, b, nil, stopped, false, false).CheckInactive(ctx, b); got.Confirmed || got.Code != "deadline_or_cancellation" {
		t.Fatal(got)
	}
}
