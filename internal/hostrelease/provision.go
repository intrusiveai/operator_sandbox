//go:build linux || darwin

package hostrelease

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type ProvisionReceipt struct {
	APIVersion            string `json:"api_version"`
	Account               string `json:"account"`
	UID                   int    `json:"uid"`
	Created               bool   `json:"created"`
	DockerAccessRequested bool   `json:"docker_access_requested"`
	RuntimeDirectory      string `json:"runtime_directory"`
}
type provisionDependencies struct {
	goos      string
	euid      int
	query     func(context.Context, string, ...string) ([]byte, error)
	run       func(context.Context, string, ...string) error
	directory func(string, int, int) error
	tmpfiles  func() error
}

func provisionDirectory(name string, uid, gid int) error {
	if err := os.MkdirAll(name, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || (st.Uid != 0 && st.Uid != uint32(uid)) {
		return ErrInstall
	}
	return errors.Join(f.Chown(uid, gid), f.Chmod(0700), f.Sync())
}
func ProvisionLinux(ctx context.Context, grantDocker, service bool) (ProvisionReceipt, error) {
	query := func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.WaitDelay = 100 * time.Millisecond
		return cmd.Output()
	}
	run := func(ctx context.Context, bin string, args ...string) error {
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		cmd.WaitDelay = 100 * time.Millisecond
		return cmd.Run()
	}
	return provisionLinux(ctx, grantDocker, service, provisionDependencies{runtime.GOOS, os.Geteuid(), query, run, provisionDirectory, provisionTmpfiles})
}
func provisionLinux(ctx context.Context, grantDocker, service bool, d provisionDependencies) (r ProvisionReceipt, err error) {
	if d.goos != "linux" || d.euid != 0 {
		return r, ErrInstall
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	raw, err := d.query(ctx, "/usr/bin/getent", "passwd", "operator")
	created := false
	if err != nil {
		// getent status 2 means no matching account. Other failures must not mutate.
		var code interface{ ExitCode() int }
		if !errors.As(err, &code) || code.ExitCode() != 2 {
			return r, ErrInstall
		}
		if err = d.run(ctx, "/usr/sbin/useradd", "--system", "--user-group", "--no-create-home", "--home-dir", "/var/lib/operator", "--shell", "/usr/sbin/nologin", "operator"); err != nil {
			return r, ErrInstall
		}
		created = true
		raw, err = d.query(ctx, "/usr/bin/getent", "passwd", "operator")
		if err != nil {
			return r, ErrInstall
		}
	}
	fields := strings.Split(strings.TrimSpace(string(raw)), ":")
	if len(fields) != 7 || fields[0] != "operator" || fields[5] != "/var/lib/operator" || fields[6] != "/usr/sbin/nologin" {
		return r, ErrInstall
	}
	uid, e1 := strconv.Atoi(fields[2])
	gid, e2 := strconv.Atoi(fields[3])
	if e1 != nil || e2 != nil || uid <= 0 || gid <= 0 {
		return r, ErrInstall
	}
	if grantDocker {
		if _, err = d.query(ctx, "/usr/bin/getent", "group", "docker"); err != nil {
			return r, errors.New("install Docker and its local docker group before granting access")
		}
		if err = d.run(ctx, "/usr/sbin/usermod", "--append", "--groups", "docker", "operator"); err != nil {
			return r, ErrInstall
		}
	}
	for _, name := range []string{"/opt/operator", "/etc/operator", "/var/lib/operator", "/run/operator"} {
		if err = d.directory(name, uid, gid); err != nil {
			return r, err
		}
	}
	if err = d.tmpfiles(); err != nil {
		return r, err
	}
	if service {
		if err = d.run(ctx, "/usr/bin/loginctl", "enable-linger", "operator"); err != nil {
			return r, ErrInstall
		}
		if err = d.run(ctx, "/usr/bin/systemctl", "start", "user@"+strconv.Itoa(uid)+".service"); err != nil {
			return r, ErrInstall
		}
	}
	runtimeDirectory := "/run/operator"
	if service {
		runtimeDirectory = "/run/user/" + strconv.Itoa(uid)
	}
	return ProvisionReceipt{"operator.dev/linux-provision/v1alpha1", "operator", uid, created, grantDocker, runtimeDirectory}, nil
}

func provisionTmpfiles() error {
	name := "/etc/tmpfiles.d/operator.conf"
	if err := reclaimNewFiles(filepath.Dir(name)); err != nil {
		return err
	}
	raw := []byte("d /run/operator 0700 operator operator -\n")
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return writeNew(name, raw, 0644)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 {
		return ErrInstall
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != 0 || st.Nlink != 1 {
		return ErrInstall
	}
	prior, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil || string(prior) != string(raw) {
		return ErrInstall
	}
	return nil
}
