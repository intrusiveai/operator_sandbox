//go:build linux || darwin

package hostrelease

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
)

var ErrInstall = errors.New("installation failed; retain prior release and inspect installation state")
var ErrDowngrade = errors.New("lower release version requires explicit --allow-downgrade")

type InstallOptions struct {
	Root, Archive, Verifier, Keyring string
	ConfigFile, StateRoot, Image     string
	AllowDowngrade                   bool
}
type InstallReceipt struct {
	APIVersion       string `json:"api_version"`
	Status           string `json:"status"`
	Version          string `json:"version"`
	Platform         string `json:"platform"`
	ManifestDigest   string `json:"manifest_digest"`
	ReleaseDirectory string `json:"release_directory"`
	Command          string `json:"command"`
	Configuration    string `json:"configuration"`
	Template         string `json:"configuration_template"`
}

func cleanAbsolute(s string) bool {
	return len(s) <= 4096 && filepath.IsAbs(s) && filepath.Clean(s) == s && s != "/" && strings.IndexFunc(s, func(r rune) bool { return r < 32 || r == 127 }) < 0
}
func within(a, b string) bool { return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) }
func privateDirectory(name string) error {
	if err := os.MkdirAll(name, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(name)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != 0700 || st.Uid != uint32(os.Geteuid()) {
		return ErrInstall
	}
	return nil
}
func syncDirectory(name string) error {
	d, e := os.Open(name)
	if e != nil {
		return e
	}
	return errors.Join(d.Sync(), d.Close())
}
func writeNew(name string, raw []byte, mode os.FileMode) error {
	f, e := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if e != nil {
		return e
	}
	_, e = f.Write(raw)
	return errors.Join(e, f.Sync(), f.Close(), syncDirectory(filepath.Dir(name)))
}
func lowerVersion(a, b string) bool {
	av, bv := strings.Split(a, "."), strings.Split(b, ".")
	for i := range av {
		x, _ := strconv.Atoi(av[i])
		y, _ := strconv.Atoi(bv[i])
		if x != y {
			return x < y
		}
	}
	return false
}
func launcher(root string) []byte {
	quoted := "'" + strings.ReplaceAll(root, "'", "'\"'\"'") + "'"
	return []byte("#!/bin/sh\nset -eu\nbase=" + quoted + "\nrelease=$(/usr/bin/readlink \"$base/current\")\ncase \"$release\" in releases/*) ;; *) exit 1;; esac\nexec \"$base/$release/bin/operatorctl\" \"$@\"\n")
}

// Install authenticates a local archive, stages immutable content, then atomically
// switches one release pointer. Existing configuration and all state are retained.
func Install(ctx context.Context, o InstallOptions) (InstallReceipt, error) {
	return install(ctx, o, func(string) error { return nil })
}
func install(ctx context.Context, o InstallOptions, boundary func(string) error) (result InstallReceipt, err error) {
	if !cleanAbsolute(o.Root) || !cleanAbsolute(o.ConfigFile) || !cleanAbsolute(o.StateRoot) || within(o.Root, o.StateRoot) || within(o.StateRoot, o.Root) || within(o.ConfigFile, o.Root) || within(o.ConfigFile, o.StateRoot) || o.Image == "" {
		return result, ErrInstall
	}
	if err = privateDirectory(o.Root); err != nil {
		return result, err
	}
	lock, err := os.OpenFile(filepath.Join(o.Root, "install.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return result, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return result, ErrInstall
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	// Only installer-owned staging names under the private root are reclaimed.
	entries, err := os.ReadDir(o.Root)
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stage-") || strings.HasPrefix(entry.Name(), ".switch-") {
			if err = os.RemoveAll(filepath.Join(o.Root, entry.Name())); err != nil {
				return result, err
			}
		}
	}
	stage, err := os.MkdirTemp(o.Root, ".stage-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(stage)
	content := filepath.Join(stage, "content")
	if err = Extract(ctx, o.Archive, content); err != nil {
		return result, err
	}
	m, err := VerifyDirectory(ctx, content, o.Verifier, o.Keyring)
	if err != nil {
		return result, err
	}
	if m.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		return result, ErrRelease
	}
	raw, err := os.ReadFile(filepath.Join(content, "release.json"))
	if err != nil {
		return result, err
	}
	manifestDigest := contracts.RawDigest(raw)
	current := filepath.Join(o.Root, "current")
	selected, readErr := os.Readlink(current)
	if readErr == nil {
		if !strings.HasPrefix(selected, "releases/") || filepath.Clean(selected) != selected || strings.Count(selected, "/") != 1 {
			return result, ErrInstall
		}
		prior, err := VerifyDirectory(ctx, filepath.Join(o.Root, selected), o.Verifier, o.Keyring)
		if err != nil {
			return result, err
		}
		if lowerVersion(m.Version, prior.Version) && !o.AllowDowngrade {
			return result, ErrDowngrade
		}
	} else if !os.IsNotExist(readErr) {
		return result, ErrInstall
	}
	releaseName := m.Version + "-" + strings.ReplaceAll(m.Platform, "/", "-")
	releases := filepath.Join(o.Root, "releases")
	if err = privateDirectory(releases); err != nil {
		return result, err
	}
	destination := filepath.Join(releases, releaseName)
	if _, err = os.Lstat(destination); err == nil {
		prior, err := VerifyDirectory(ctx, destination, o.Verifier, o.Keyring)
		if err != nil {
			return result, err
		}
		priorRaw, err := os.ReadFile(filepath.Join(destination, "release.json"))
		if err != nil || prior.Version != m.Version || !bytes.Equal(priorRaw, raw) {
			return result, ErrRelease
		}
	} else if !os.IsNotExist(err) {
		return result, err
	} else {
		// Synchronize directory entries bottom-up before publishing the release.
		var dirs []string
		if err = filepath.WalkDir(content, func(name string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				dirs = append(dirs, name)
			}
			return nil
		}); err != nil {
			return result, err
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			if err = syncDirectory(dirs[i]); err != nil {
				return result, err
			}
		}
		if err = os.Rename(content, destination); err != nil {
			return result, err
		}
		if err = syncDirectory(releases); err != nil {
			return result, err
		}
	}
	if err = boundary("release-published"); err != nil {
		return result, err
	}
	if err = privateDirectory(o.StateRoot); err != nil {
		return result, err
	}
	if err = privateDirectory(filepath.Dir(o.ConfigFile)); err != nil {
		return result, err
	}
	config := []byte(fmt.Sprintf("engine:\n  image: %q\nstate:\n  root: %q\ncontract:\n  directory: %q\n  version: %q\n  digest: %q\n", o.Image, o.StateRoot, filepath.Join(destination, "contract"), m.Contract.Version, m.Contract.Digest))
	template := filepath.Join(filepath.Dir(o.ConfigFile), "config-"+releaseName+".yaml.example")
	if prior, e := hostconfig.ReadPrivate(template, hostconfig.MaxBytes); e == nil {
		if !bytes.Equal(prior, config) {
			return result, ErrInstall
		}
	} else if !os.IsNotExist(e) {
		return result, e
	} else if err = writeNew(template, config, 0600); err != nil {
		return result, err
	}
	// Check generated configuration before any activation; preserve custom files.
	defaults := hostconfig.Paths{ConfigFile: o.ConfigFile, StateRoot: o.StateRoot, DockerEndpoint: "unix:///var/run/docker.sock"}
	if _, err = hostconfig.Load(template, defaults); err != nil {
		return result, err
	}
	configuration := "preserved"
	if _, err = hostconfig.ReadPrivate(o.ConfigFile, hostconfig.MaxBytes); os.IsNotExist(err) {
		if err = writeNew(o.ConfigFile, config, 0600); err != nil {
			return result, err
		}
		configuration = "created"
	} else if err != nil {
		return result, err
	}
	command := filepath.Join(o.Root, "operatorctl")
	script := launcher(o.Root)
	if info, e := os.Lstat(command); e == nil && (!info.Mode().IsRegular() || info.Mode().Perm() != 0700) {
		return result, ErrInstall
	}
	if existing, e := os.ReadFile(command); e == nil {
		if !bytes.Equal(existing, script) {
			return result, ErrInstall
		}
	} else if !os.IsNotExist(e) {
		return result, e
	} else if err = writeNew(command, script, 0700); err != nil {
		return result, err
	}
	if err = boundary("before-activation"); err != nil {
		return result, err
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	tmp := filepath.Join(o.Root, ".switch-current")
	if err = os.Symlink(filepath.Join("releases", releaseName), tmp); err != nil {
		return result, err
	}
	if err = os.Rename(tmp, current); err != nil {
		return result, err
	}
	if err = syncDirectory(o.Root); err != nil {
		return result, err
	}
	result = InstallReceipt{"operator.dev/host-install/v1alpha1", "activated", m.Version, m.Platform, manifestDigest, destination, command, configuration, template}
	if err = boundary("activated"); err != nil {
		return result, err
	}
	return result, nil
}
