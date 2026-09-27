//go:build linux || darwin

package campaign

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHostLeaseSerializesWorkersAndReleasesAfterProcessLoss(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	lease, err := AcquireHostLease(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = AcquireHostLease(root); !errors.Is(err, ErrActive) {
		t.Fatal("second worker accepted", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal("close not idempotent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestHostLeaseChildProcess$")
	child.Env = append(os.Environ(), "OPERATOR_LEASE_TEST_ROOT="+root)
	pipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { child.Process.Kill(); child.Wait() }()
	ready, err := bufio.NewReader(pipe).ReadString('\n')
	if err != nil || ready != "held\n" {
		t.Fatal("child did not acquire", ready, err)
	}
	if _, err = AcquireHostLease(root); !errors.Is(err, ErrActive) {
		t.Fatal("cross-process serialization failed", err)
	}
	if err = child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	fresh, err := AcquireHostLease(root)
	if err != nil {
		t.Fatal("dead process kept lock", err)
	}
	fresh.Close()
	if _, err = os.Stat(filepath.Join(root, "execution.lock")); err != nil {
		t.Fatal("stable lock inode removed", err)
	}
}
func TestHostLeaseChildProcess(t *testing.T) {
	root := os.Getenv("OPERATOR_LEASE_TEST_ROOT")
	if root == "" {
		return
	}
	lease, err := AcquireHostLease(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	fmt.Println("held")
	var b [1]byte
	os.Stdin.Read(b[:])
}
func TestHostLeaseRejectsUnsafeLockFiles(t *testing.T) {
	for _, mode := range []string{"symlink", "hardlink", "directory", "public-file", "public-root"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			os.Chmod(root, 0700)
			name := filepath.Join(root, "execution.lock")
			switch mode {
			case "symlink":
				other := filepath.Join(root, "other")
				os.WriteFile(other, nil, 0600)
				os.Symlink(other, name)
			case "hardlink":
				other := filepath.Join(root, "other")
				os.WriteFile(other, nil, 0600)
				os.Link(other, name)
			case "directory":
				os.Mkdir(name, 0700)
			case "public-file":
				os.WriteFile(name, nil, 0644)
			case "public-root":
				os.Chmod(root, 0755)
			}
			lease, err := AcquireHostLease(root)
			if err == nil {
				lease.Close()
				t.Fatal("unsafe lock admitted")
			}
		})
	}
}
