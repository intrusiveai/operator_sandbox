//go:build linux || darwin

package transport

import (
	"os"
	"path/filepath"
	"syscall"
)

// Mount is a verified, launch-owned transport directory, never an arbitrary
// caller-selected host path. ReadOnly applies at the container mount boundary.
type Mount struct {
	Source, Target string
	ReadOnly       bool
}

// LaunchMounts verifies the still-live transport's identity and access group.
// It is intended before Docker create/start, not as recovery authorization.
func (s *Session) LaunchMounts(campaignID, launchID, mode string, gid int) ([]Mount, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.check(s.now()); err != nil {
		return nil, err
	}
	actualGID, err := guestGroup(s.config)
	if err != nil || gid != actualGID || s.config.CampaignID != campaignID || s.config.LaunchID != launchID {
		return nil, ErrProtocol
	}
	info, err := os.Lstat(s.directory)
	if err != nil || !info.IsDir() || !os.SameFile(info, s.directoryInfo) {
		return nil, ErrProtocol
	}
	var mounts []Mount
	switch d := s.driver.(type) {
	case *fifo:
		if mode != "fifo" || info.Mode().Perm() != 0750 {
			return nil, ErrProtocol
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Gid) != gid {
			return nil, ErrProtocol
		}
		for i, lane := range lanes {
			actual, e := d.root.Lstat(lane)
			expected := os.FileMode(0640)
			if i%2 == 1 {
				expected = 0620
			}
			if e != nil || !os.SameFile(actual, d.lanes[i].info) || actual.Mode()&os.ModeNamedPipe == 0 || actual.Mode().Perm() != expected {
				return nil, ErrProtocol
			}
			st, ok := actual.Sys().(*syscall.Stat_t)
			if !ok || int(st.Gid) != gid {
				return nil, ErrProtocol
			}
		}
		mounts = append(mounts, Mount{s.directory, "/run/operator/ipc", true})
	case *spool:
		if mode != "spool" || info.Mode().Perm() != 0700 {
			return nil, ErrProtocol
		}
		for i, lane := range lanes {
			actual, e := d.root.Lstat(lane)
			expected := os.FileMode(0750)
			if i%2 == 1 {
				expected = 0770
			}
			if e != nil || !actual.IsDir() || !os.SameFile(actual, d.identities[i]) || actual.Mode().Perm() != expected {
				return nil, ErrProtocol
			}
			st, ok := actual.Sys().(*syscall.Stat_t)
			if !ok || int(st.Gid) != gid {
				return nil, ErrProtocol
			}
			mounts = append(mounts, Mount{filepath.Join(s.directory, lane), "/run/operator/spool/" + lane, i%2 == 0})
		}
	default:
		return nil, ErrProtocol
	}
	return mounts, nil
}

// AccessGroup is the frozen group selected when the transport was constructed.
// A privileged host may choose a dedicated nonroot group using Config.GuestGID.
func (s *Session) AccessGroup() int { s.mu.Lock(); defer s.mu.Unlock(); return *s.config.GuestGID }
