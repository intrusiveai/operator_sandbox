//go:build linux || darwin

package startrequest

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
)

// RunLink is a lookup hint bound to a verified private start request. It contains
// no authority to execute and is retained in the user-selected submission directory.
type RunLink struct {
	APIVersion     string `json:"api_version"`
	StateRoot      string `json:"state_root"`
	StartRequestID string `json:"start_request_id"`
	CampaignID     string `json:"campaign_id"`
	RequestDigest  string `json:"request_digest"`
}

type RunLock struct {
	root      *os.Root
	file      *os.File
	directory string
	mu        sync.Mutex
	closed    bool
}

// LockRun serializes default start selection for one submitted run. Hold it
// through request/link publication and supervisor submission, then release it
// before waiting for preparation. It is independent of the worker execution lease.
func LockRun(directory string) (*RunLock, error) {
	if !absolute(directory) {
		return nil, ErrRecord
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			root.Close()
		}
	}()
	if err := privateDir(root, "."); err != nil {
		return nil, err
	}
	f, err := root.OpenFile("start.lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	defer func() {
		if !success {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || st.Nlink != 1 || (st.Uid != 0 && st.Uid != uint32(os.Geteuid())) {
		return nil, ErrRecord
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, campaign.ErrActive
		}
		return nil, err
	}
	actual, err := root.Lstat("start.lock")
	if err != nil || !os.SameFile(info, actual) {
		return nil, ErrRecord
	}
	if err := syncDir(root); err != nil {
		return nil, err
	}
	success = true
	return &RunLock{root: root, file: f, directory: directory}, nil
}
func (l *RunLock) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	return errors.Join(l.file.Close(), l.root.Close())
}

// ReadLink verifies both the pointer and the full referenced request. A modified
// pointer cannot silently select a different campaign or another submitted run.
func ReadLink(directory string) (RunLink, Snapshot, error) {
	var link RunLink
	if !absolute(directory) {
		return link, Snapshot{}, ErrRecord
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return link, Snapshot{}, err
	}
	defer root.Close()
	if err := privateDir(root, "."); err != nil {
		return link, Snapshot{}, err
	}
	if err := pending(root, "start.json"); err != nil {
		return link, Snapshot{}, err
	}
	if _, err := read(directory, "start.json", &link); err != nil {
		return link, Snapshot{}, err
	}
	if link.APIVersion != "operator.dev/run-start-link/v1alpha1" || !absolute(link.StateRoot) || !idPattern.MatchString(link.StartRequestID) || !digestPattern.MatchString(link.RequestDigest) {
		return link, Snapshot{}, ErrRecord
	}
	snapshot, err := Read(link.StateRoot, link.StartRequestID)
	if err != nil {
		return link, Snapshot{}, err
	}
	if snapshot.Digest != link.RequestDigest || snapshot.Request.Selection.CampaignID != link.CampaignID || snapshot.Request.Selection.RunDirectory != directory {
		return link, Snapshot{}, ErrConflict
	}
	return link, snapshot, nil
}

// PublishLink replaces an existing run pointer only for an explicit new campaign.
// Historical start groups remain retained under their immutable keys.
func (l *RunLock) PublishLink(ctx context.Context, s Snapshot, newCampaign bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || s.Request.Selection.RunDirectory != l.directory || s.Request.validate() != nil {
		return ErrRecord
	}
	verified, err := Read(s.Request.StateRoot, s.Request.Selection.StartRequestID)
	if err != nil {
		return err
	}
	if verified.Digest != s.Digest {
		return ErrConflict
	}
	link := RunLink{APIVersion: "operator.dev/run-start-link/v1alpha1", StateRoot: s.Request.StateRoot, StartRequestID: s.Request.Selection.StartRequestID, CampaignID: s.Request.Selection.CampaignID, RequestDigest: s.Digest}
	raw, _, err := encode(link)
	if err != nil {
		return err
	}
	old, _, err := ReadLink(l.directory)
	missingLink := false
	if errors.Is(err, os.ErrNotExist) {
		_, statErr := l.root.Lstat("start.json")
		missingLink = errors.Is(statErr, os.ErrNotExist)
		if statErr != nil && !missingLink {
			return statErr
		}
		if !missingLink && !newCampaign {
			return ErrConflict
		}
	}
	if err == nil {
		if old == link {
			return nil
		}
		if !newCampaign {
			return ErrConflict
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := pending(l.root, "start.json"); err != nil {
		return err
	}
	if missingLink {
		return put(ctx, l.root, "start.json", raw)
	}
	// Replacement uses its own inode; readers see the old or new complete link.
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := l.root.OpenFile("start.json.pending", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = ErrRecord
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := l.root.Rename("start.json.pending", "start.json"); err != nil {
		return err
	}
	return syncDir(l.root)
}
