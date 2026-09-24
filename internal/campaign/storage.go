//go:build linux || darwin

package campaign

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

// All paths below a caller-selected, private state root are host generated.
// os.Root confines resolution even if retained files have been damaged. Private
// ownership is a prerequisite; this is not protection against the service UID.
func privateDir(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return ErrInvalid
	}
	return nil
}

func mkdir(r *os.Root, name string) error {
	if err := r.Mkdir(name, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	if err := privateDir(r, name); err != nil {
		return err
	}
	return syncDir(r, path.Dir(name))
}

func syncDir(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func readFile(r *os.Root, name string, maximum int) ([]byte, error) {
	f, err := openRegular(r, name, os.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, int64(maximum)+1))
	if err != nil || len(raw) > maximum {
		return nil, ErrCorrupt
	}
	return raw, nil
}

func openRegular(r *os.Root, name string, flags int) (*os.File, error) {
	info, err := r.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, ErrCorrupt
	}
	f, err := r.OpenFile(name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	actual, err := f.Stat()
	if err != nil || !actual.Mode().IsRegular() || !os.SameFile(info, actual) {
		f.Close()
		return nil, ErrCorrupt
	}
	return f, nil
}

// publish uses a durable temporary inode and an atomic, no-replace hard link.
// A failed publication leaves evidence for recovery and is never retried by a
// live writer. Only the committed head uses atomic replacement.
func publish(r *os.Root, name string, raw []byte, replace bool, hooks *ioHooks) error {
	tmp := name + ".pending"
	f, err := r.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if n, e := f.Write(raw); e != nil || n != len(raw) {
		f.Close()
		return errors.Join(e, io.ErrShortWrite)
	}
	if err := hooks.sync(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if replace {
		err = r.Rename(tmp, name)
	} else {
		err = r.Link(tmp, name)
	}
	if err != nil {
		return err
	}
	if !replace {
		if err := r.Remove(tmp); err != nil {
			return err
		}
	}
	return hooks.syncDir(r, path.Dir(name))
}

// Hooks are package-private and only replaced by failure-injection tests.
type ioHooks struct {
	sync      func(*os.File) error
	syncDir   func(*os.Root, string) error
	available func(*os.Root) (int64, error)
}

func diskHooks() ioHooks {
	return ioHooks{sync: (*os.File).Sync, syncDir: syncDir, available: filesystemAvailable}
}

func lock(r *os.Root, create bool) (*os.File, error) {
	var f *os.File
	var err error
	if create {
		f, err = r.OpenFile("writer.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	} else {
		f, err = openRegular(r, "writer.lock", os.O_RDWR)
	}
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrActive
		}
		return nil, err
	}
	return f, nil
}

func openCampaign(stateRoot, campaignID string) (*os.Root, error) {
	if !validID(campaignID) {
		return nil, ErrInvalid
	}
	r, err := os.OpenRoot(stateRoot)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, p := range []string{".", "campaigns", "campaigns/" + campaignID} {
		if err := privateDir(r, p); err != nil {
			return nil, err
		}
	}
	return r.OpenRoot("campaigns/" + campaignID)
}

type campaignRecord struct {
	APIVersion        string `json:"api_version"`
	CampaignID        string `json:"campaign_id"`
	LaunchID          string `json:"launch_id"`
	RunManifestDigest string `json:"run_manifest_digest"`
}

func loadManifest(r *os.Root, campaignID string) (RunManifest, string, error) {
	var empty RunManifest
	for _, dir := range []string{"launch"} {
		if err := privateDir(r, dir); err != nil {
			return empty, "", err
		}
	}
	raw, err := readFile(r, "launch/run-manifest.json", ManifestLimit)
	if err != nil {
		return empty, "", err
	}
	m, err := ParseManifest(raw)
	if err != nil || m.CampaignID != campaignID {
		return empty, "", ErrCorrupt
	}
	digest, err := contracts.CanonicalDigest(raw, ManifestLimit)
	if err != nil {
		return empty, "", ErrCorrupt
	}
	raw, err = readFile(r, "campaign.json", ManifestLimit)
	if err != nil {
		return empty, "", err
	}
	var rec campaignRecord
	if decode(raw, &rec, ManifestLimit) != nil || rec.APIVersion != "operator.dev/campaign/v1alpha1" || rec.CampaignID != campaignID || rec.LaunchID != m.LaunchID || rec.RunManifestDigest != digest {
		return empty, "", ErrCorrupt
	}
	return m, digest, nil
}

// ReadDockerBinding never acquires the worker/journal lock or reads journal data.
// Missing or corrupt identity is uncertainty, not proof that a container is absent.
// The termination caller must still inspect daemon identity, container and labels.
func ReadDockerBinding(stateRoot, campaignID string) (DockerBinding, error) {
	var b DockerBinding
	r, err := openCampaign(stateRoot, campaignID)
	if err != nil {
		return b, err
	}
	defer r.Close()
	m, digest, err := loadManifest(r, campaignID)
	if err != nil {
		return b, err
	}
	raw, err := readFile(r, "launch/docker-binding.json", ManifestLimit)
	if err != nil {
		return b, err
	}
	if decode(raw, &b, ManifestLimit) != nil || b.validate(m, digest) != nil {
		return DockerBinding{}, ErrCorrupt
	}
	return b, nil
}
