//go:build linux || darwin

package dockercontrol

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

// LaunchPlan freezes installed runtime policy and verified launch-owned mounts.
// Release approval and durable campaign admission are separate host gates.
type LaunchPlan struct {
	created        atomic.Bool
	started        atomic.Bool
	actualID       atomic.Value
	image          ImagePin
	manifest       campaign.RunManifest
	manifestDigest string
	tree           *staging.Tree
	channel        *transport.Session
	gid            int
	directory      string
	identity       os.FileInfo
	args           []string
}

// NewLaunchPlan writes the installed startup syscall policy to a fresh private
// directory. parent is administrator-controlled. The guest uses UID 65532 and
// the host's nonroot primary group for directional transport access.
func NewLaunchPlan(ctx context.Context, parent string, image ImagePin, m campaign.RunManifest, tree *staging.Tree, channel *transport.Session) (*LaunchPlan, error) {
	raw, err := m.Bytes()
	if err != nil || image.Validate() != nil || tree == nil || channel == nil || os.Getgid() == 0 || image.ImageID != m.ImageDigest || image.HostPlatform != m.HostPlatform || image.ImagePlatform != m.ImagePlatform {
		return nil, ErrLaunch
	}
	m, err = campaign.ParseManifest(raw)
	if err != nil {
		return nil, err
	}
	if !safeMountPath(parent) {
		return nil, ErrLaunch
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrLaunch
	}
	receipt := tree.Receipt()
	if receipt.InputTreeDigest != m.InputTreeDigest || receipt.SkillSetDigest != m.SkillSetDigest || receipt.Contract.Version != m.Contract.Version || receipt.Contract.Digest != m.Contract.Digest {
		return nil, ErrLaunch
	}
	p := &LaunchPlan{image: image, manifest: m, manifestDigest: contracts.RawDigest(raw), tree: tree, channel: channel, gid: os.Getgid()}
	p.image.RepoDigests = append([]string(nil), image.RepoDigests...)
	if err = tree.Verify(ctx); err != nil {
		return nil, err
	}
	mounts, err := channel.LaunchMounts(m.CampaignID, m.LaunchID, m.Transport, p.gid)
	if err != nil {
		return nil, err
	}
	for _, child := range []string{"input", "customer-skills", "manifests"} {
		mounts = append(mounts, transport.Mount{Source: filepath.Join(tree.Directory(), child), Target: "/run/operator/" + child, ReadOnly: true})
	}
	for _, mount := range mounts {
		if !safeMountPath(mount.Source) {
			return nil, ErrLaunch
		}
	}
	p.directory, err = os.MkdirTemp(parent, "launch-policy-")
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(p.directory)
		}
	}()
	p.identity, err = os.Lstat(p.directory)
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(p.directory, "startup-seccomp.json"), startupSeccomp, 0600); err != nil {
		return nil, err
	}
	p.args = []string{"container", "create", "--pull=never", "--platform", image.ImagePlatform, "--restart=no", "--no-healthcheck", "--ipc=private", "--cgroupns=private", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--security-opt=seccomp=" + filepath.Join(p.directory, "startup-seccomp.json"), "--user", fmt.Sprintf("65532:%d", p.gid), "--cpus=2", "--memory=4294967296", "--memory-swap=4294967296", "--pids-limit=16", "--ulimit=nofile=256:256", "--ulimit=core=0:0", "--log-driver=none", "--workdir=/run/operator/work", "--entrypoint=/usr/bin/python3", "--env=OPERATOR_TRANSPORT=" + m.Transport}
	for _, tmp := range []struct {
		path          string
		bytes, inodes int
	}{{"/run/operator/work", 128 << 20, 8192}, {"/tmp", 32 << 20, 2048}} {
		p.args = append(p.args, "--tmpfs", fmt.Sprintf("%s:rw,nodev,nosuid,noexec,size=%d,nr_inodes=%d,mode=0700,uid=65532,gid=%d", tmp.path, tmp.bytes, tmp.inodes, p.gid))
	}
	labels := m.DockerLabels()
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		p.args = append(p.args, "--label", key+"="+labels[key])
	}
	for _, mount := range mounts {
		arg := "type=bind,source=" + mount.Source + ",target=" + mount.Target + ",bind-recursive=disabled,bind-propagation=rprivate"
		if mount.ReadOnly {
			arg += ",readonly"
		}
		p.args = append(p.args, "--mount", arg)
	}
	p.args = append(p.args, image.ImageID, "-I", "-S", "-B", "/opt/operator/engine/bootstrap.py")
	ok = true
	return p, nil
}
func safeMountPath(p string) bool {
	return filepath.IsAbs(p) && filepath.Clean(p) == p && p != "/" && len(p) <= 4096 && !strings.ContainsAny(p, ",\"\x00\r\n")
}
func (p *LaunchPlan) verify(ctx context.Context) error {
	if err := p.tree.Verify(ctx); err != nil {
		return err
	}
	if _, err := p.channel.LaunchMounts(p.manifest.CampaignID, p.manifest.LaunchID, p.manifest.Transport, p.gid); err != nil {
		return err
	}
	info, err := os.Lstat(p.directory)
	if err != nil || !info.IsDir() || !os.SameFile(info, p.identity) || info.Mode().Perm() != 0700 {
		return ErrLaunch
	}
	raw, err := staging.Capture(ctx, filepath.Join(p.directory, "startup-seccomp.json"), int64(len(startupSeccomp)))
	if err != nil || !bytes.Equal(raw, startupSeccomp) {
		return ErrLaunch
	}
	return ctx.Err()
}

// Discard removes only this plan's host policy after no further create is possible.
// It must not race Create. It does not remove transport/staged input trees.
func (p *LaunchPlan) Discard() error {
	info, err := os.Lstat(p.directory)
	if err != nil {
		return err
	}
	if !info.IsDir() || !os.SameFile(info, p.identity) {
		return ErrLaunch
	}
	return os.RemoveAll(p.directory)
}
