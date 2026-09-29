//go:build linux || darwin

package supervisor

import (
	"context"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

func saved(t *testing.T) startrequest.Snapshot {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state $literal & <quotes>")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	r := startrequest.Request{APIVersion: startrequest.Version, ConfigurationFile: "/installed/config.yaml", StateRoot: root, DockerEndpoint: "unix:///saved/docker.sock", InputsFingerprint: contracts.RawDigest([]byte("inputs")), Selection: hostrun.NewSelection("/submitted/run")}
	s, err := startrequest.Save(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSupervisorUsesFixedDetachedCommands(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			s := saved(t)
			id := s.Request.Selection.StartRequestID
			var calls [][]string
			c := &Client{executable: "/installed path/operatorctl", goos: goos, uid: 501, run: func(ctx context.Context, bin string, args ...string) error {
				if _, ok := ctx.Deadline(); !ok {
					t.Fatal("unbounded submission")
				}
				calls = append(calls, append([]string{bin}, args...))
				return nil
			}}
			if err := c.Submit(context.Background(), s.Request.StateRoot, id, s.Digest); err != nil {
				t.Fatal(err)
			}
			if goos == "linux" {
				if len(calls) != 1 || calls[0][0] != "/usr/bin/systemd-run" {
					t.Fatal(calls)
				}
				joined := strings.Join(calls[0], "\n")
				for _, value := range []string{"--user", "--expand-environment=no", "--property=Restart=no", "--service-type=exec", "--state-root\n" + s.Request.StateRoot, "--request-digest\n" + s.Digest} {
					if !strings.Contains(joined, value) {
						t.Fatal(joined)
					}
				}
				for _, value := range []string{"--scope", "--wait", "--pty", "--pipe", "--collect"} {
					if strings.Contains(joined, value) {
						t.Fatal(joined)
					}
				}
			} else {
				label := "ai.intrusive.operator.campaign." + id
				want := [][]string{{"/bin/launchctl", "bootstrap", "gui/501", filepath.Join(s.Request.StateRoot, "starts", id, "worker.plist")}, {"/bin/launchctl", "kickstart", "gui/501/" + label}}
				if !reflect.DeepEqual(calls, want) {
					t.Fatal(calls)
				}
				raw, err := os.ReadFile(calls[0][3])
				if err != nil {
					t.Fatal(err)
				}
				d := xml.NewDecoder(strings.NewReader(string(raw)))
				var values []string
				for {
					tok, err := d.Token()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if v, ok := tok.(xml.StartElement); ok && v.Name.Local == "string" {
						var value string
						if err := d.DecodeElement(&value, &v); err != nil {
							t.Fatal(err)
						}
						values = append(values, value)
					}
				}
				if values[1] != c.executable || values[4] != s.Request.StateRoot || values[8] != s.Digest {
					t.Fatal(values)
				}
				if strings.Contains(string(raw), "<true/>") {
					t.Fatal("automatic execution enabled")
				}
			}
			owner, err := startrequest.ClaimOnce(context.Background(), s.Request.StateRoot, id, s.Digest)
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			calls = nil
			if err := c.Submit(context.Background(), s.Request.StateRoot, id, s.Digest); err != nil || len(calls) != 0 {
				t.Fatal("claimed work resubmitted", err, calls)
			}
		})
	}
}

func TestSupervisorFailureDoesNotGrantAcceptanceOrRetry(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s := saved(t)
			calls := 0
			c := &Client{executable: "/installed/operatorctl", goos: "darwin", uid: 501, run: func(context.Context, string, ...string) error {
				calls++
				if calls == failAt {
					return errors.New("private OS diagnostic")
				}
				return nil
			}}
			err := c.Submit(context.Background(), s.Request.StateRoot, s.Request.Selection.StartRequestID, s.Digest)
			if !errors.Is(err, ErrSubmission) || calls != failAt || strings.Contains(err.Error(), "private") {
				t.Fatal(err, calls)
			}
			now, err := startrequest.Read(s.Request.StateRoot, s.Request.Selection.StartRequestID)
			if err != nil || now.Phase() != "submitted" {
				t.Fatal(now, err)
			}
		})
	}
}

func TestSupervisorRejectsChangedDefinitionAndRequest(t *testing.T) {
	s := saved(t)
	calls := 0
	c := &Client{executable: "/installed/operatorctl", goos: "darwin", uid: 501, run: func(context.Context, string, ...string) error { calls++; return nil }}
	if err := c.Submit(context.Background(), s.Request.StateRoot, s.Request.Selection.StartRequestID, contracts.RawDigest([]byte("wrong"))); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
	file := filepath.Join(s.Request.StateRoot, "starts", s.Request.Selection.StartRequestID, "worker.plist")
	if err := os.WriteFile(file, []byte("different job"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(context.Background(), s.Request.StateRoot, s.Request.Selection.StartRequestID, s.Digest); err == nil || calls != 0 {
		t.Fatal(err, calls)
	}
}

func TestSupervisorRetainsReleasePathAcrossLinkSwitch(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, "release-one", "operatorctl")
	next := filepath.Join(root, "release-two", "operatorctl")
	for _, name := range []string{old, next} {
		if err := os.Mkdir(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("executable fixture"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "operatorctl")
	if err := os.Symlink(old, link); err != nil {
		t.Fatal(err)
	}
	client, err := New(link)
	if err != nil {
		t.Fatal(err)
	}
	// macOS temporary paths themselves can contain symlinked ancestors.
	pinned, err := filepath.EvalSymlinks(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(next, link); err != nil {
		t.Fatal(err)
	}
	client.goos, client.uid = "linux", 501
	var dispatched []string
	client.run = func(_ context.Context, _ string, args ...string) error {
		dispatched = args
		return nil
	}
	s := saved(t)
	if err := client.Submit(context.Background(), s.Request.StateRoot, s.Request.Selection.StartRequestID, s.Digest); err != nil {
		t.Fatal(err)
	}
	for i, value := range dispatched {
		if value == "--" && i+1 < len(dispatched) && dispatched[i+1] == pinned {
			return
		}
	}
	t.Fatalf("registered worker did not retain original release: %v", dispatched)
}
