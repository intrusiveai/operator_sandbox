//go:build linux || darwin

// Package supervisor submits fixed campaign workers to the host service manager.
// Durable start claims, rather than service names or process IDs, prevent replay.
package supervisor

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
)

var ErrSubmission = errors.New("worker service submission unconfirmed; inspect the saved start request before retrying")
var ErrInstallation = errors.New("invalid worker service installation")

type command func(context.Context, string, ...string) error

type Client struct {
	executable string
	goos       string
	uid        int
	run        command
	query      func(context.Context, string, ...string) ([]byte, error)
}

// New pins the installed executable. The service manager inherits its own
// administrator-controlled environment, never the submitting terminal's secrets.
func New(executable string) (*Client, error) {
	if !absolute(executable) {
		return nil, ErrInstallation
	}
	// An administrator may switch a stable CLI link during an update. A
	// registered worker must retain the concrete release path selected now.
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil || !absolute(resolved) {
		return nil, ErrInstallation
	}
	executable = resolved
	info, err := os.Stat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 || info.Mode().Perm()&0022 != 0 {
		return nil, ErrInstallation
	}
	return &Client{executable: executable, goos: runtime.GOOS, uid: os.Geteuid(), run: runCommand, query: queryCommand}, nil
}

func runCommand(ctx context.Context, executable string, args ...string) error {
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Run()
}

func absolute(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && len(p) <= 4096 && strings.IndexFunc(p, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

// Submit returns after the manager accepts the job, not campaign acceptance.
// Context cancellation only stops the short submission command. It cannot cancel
// a worker already owned by the manager. Every failure is potentially uncertain.
func (c *Client) Submit(ctx context.Context, stateRoot, id, digest string) error {
	lease, err := campaign.AcquireRetentionLease(stateRoot, false)
	if err != nil {
		return err
	}
	defer lease.Close()
	snapshot, err := startrequest.Read(stateRoot, id)
	if err != nil || snapshot.Digest != digest {
		return startrequest.ErrRecord
	}
	if snapshot.Retired != nil {
		return startrequest.ErrRetired
	}
	if snapshot.Claim != nil || snapshot.Completion != nil {
		return nil
	}
	if err := startrequest.RegisterService(ctx, stateRoot, id, digest, c.goos, c.uid); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	args := []string{c.executable, "_worker", "--state-root", stateRoot, "--request-id", id, "--request-digest", digest}
	switch c.goos {
	case "linux":
		manager := "--user"
		if c.uid == 0 {
			manager = "--system"
		}
		options := []string{manager, "--quiet", "--no-ask-password", "--unit=operator-campaign-" + id, "--service-type=exec", "--expand-environment=no", "--property=Restart=no", "--property=UMask=0077", "--property=TimeoutStopSec=90s", "--property=StandardOutput=null", "--property=StandardError=null", "--"}
		if c.run(ctx, "/usr/bin/systemd-run", append(options, args...)...) != nil {
			return ErrSubmission
		}
	case "darwin":
		if c.uid <= 0 {
			return ErrInstallation
		}
		label := "ai.intrusive.operator.campaign." + id
		file := filepath.Join(stateRoot, "starts", id, "worker.plist")
		if err := publish(file, plist(label, args)); err != nil {
			return err
		}
		domain := "gui/" + strconv.Itoa(c.uid)
		if c.run(ctx, "/bin/launchctl", "bootstrap", domain, file) != nil {
			return ErrSubmission
		}
		if c.run(ctx, "/bin/launchctl", "kickstart", domain+"/"+label) != nil {
			return ErrSubmission
		}
	default:
		return ErrInstallation
	}
	return nil
}

func plist(label string, args []string) []byte {
	var b bytes.Buffer
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict><key>Label</key><string>")
	xml.EscapeText(&b, []byte(label))
	b.WriteString("</string><key>ProgramArguments</key><array>")
	for _, arg := range args {
		b.WriteString("<string>")
		xml.EscapeText(&b, []byte(arg))
		b.WriteString("</string>")
	}
	b.WriteString("</array><key>RunAtLoad</key><false/><key>KeepAlive</key><false/><key>Umask</key><integer>63</integer><key>ExitTimeOut</key><integer>90</integer><key>ProcessType</key><string>Background</string></dict></plist>\n")
	return b.Bytes()
}

// Publish once in the verified request directory. An incomplete definition is
// never replaced or interpreted as a successful submission.
func publish(file string, data []byte) error {
	if existing, err := hostconfig.ReadPrivate(file, 64<<10); err == nil {
		if !bytes.Equal(existing, data) {
			return ErrInstallation
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(data)
	err = errors.Join(writeErr, f.Sync(), f.Close())
	if err != nil {
		return fmt.Errorf("worker service definition incomplete: %w", err)
	}
	d, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
