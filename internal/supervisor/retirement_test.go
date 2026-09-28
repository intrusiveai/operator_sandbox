//go:build linux || darwin

package supervisor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

type testExit int

func (e testExit) Error() string { return "manager exit" }
func (e testExit) ExitCode() int { return int(e) }

func TestRetiredServiceRequiresConfirmedAbsence(t *testing.T) {
	for _, platform := range []string{"linux", "darwin"} {
		t.Run(platform, func(t *testing.T) {
			ctx := context.Background()
			s := saved(t)
			root := s.Request.StateRoot
			id := s.Request.Selection.StartRequestID
			if e := startrequest.RegisterService(ctx, root, id, s.Digest, platform, 501); e != nil {
				t.Fatal(e)
			}
			lease, e := campaign.AcquireRetentionLease(root, true)
			if e != nil {
				t.Fatal(e)
			}
			defer lease.Close()
			calls := 0
			removed := false
			c := &Client{goos: platform, uid: 501, run: func(ctx context.Context, bin string, args ...string) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded manager call")
				}
				calls++
				removed = true
				return errors.New("lost reply")
			}}
			c.query = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
				if !strings.Contains(strings.Join(args, " "), id) {
					t.Fatal("wrong service", args)
				}
				if !removed {
					return []byte("loaded"), nil
				}
				if platform == "linux" {
					return []byte("not-found\n"), testExit(4)
				}
				return []byte("Bad request.\nCould not find service \"ai.intrusive.operator.campaign." + id + "\" in domain for user gui: 501\n"), testExit(113)
			}
			if e = c.ReconcileRetired(ctx, lease, root, id, s.Digest); !errors.Is(e, startrequest.ErrRecord) || calls != 0 {
				t.Fatal("unfenced removal", e)
			}
			if _, e = startrequest.Retire(ctx, lease, root, id, s.Digest); e != nil {
				t.Fatal(e)
			}
			if e = c.ReconcileRetired(ctx, lease, root, id, s.Digest); e != nil || calls == 0 {
				t.Fatal(e, calls)
			}
			prior := calls
			if e = c.ReconcileRetired(ctx, lease, root, id, s.Digest); e != nil || calls != prior {
				t.Fatal("absent service mutated", e, calls)
			}
			c.query = func(context.Context, string, ...string) ([]byte, error) {
				return nil, errors.New("manager unavailable")
			}
			if e = c.ReconcileRetired(ctx, lease, root, id, s.Digest); !errors.Is(e, ErrRetirement) {
				t.Fatal("failure became absence", e)
			}
			lease.Close()
			if e = c.Submit(ctx, root, id, s.Digest); !errors.Is(e, startrequest.ErrRetired) {
				t.Fatal("retired work submitted", e)
			}
		})
	}
}
