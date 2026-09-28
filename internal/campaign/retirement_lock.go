//go:build linux || darwin

package campaign

import (
	"sync/atomic"
	"syscall"
)

// RetentionLease excludes start publication, service submission and live worker
// claims from administrative retirement. Its stable inode is never purged.
// Shared leases may nest; acquisition is nonblocking in both modes.
type RetentionLease struct {
	lease     *HostLease
	root      string
	exclusive bool
	closed    atomic.Bool
}

func AcquireRetentionLease(root string, exclusive bool) (*RetentionLease, error) {
	mode := syscall.LOCK_SH
	if exclusive {
		mode = syscall.LOCK_EX
	}
	l, err := acquireLease(root, "retention.lock", mode)
	if err != nil {
		return nil, err
	}
	return &RetentionLease{lease: l, root: root, exclusive: exclusive}, nil
}
func (l *RetentionLease) ExclusiveFor(root string) bool {
	return l != nil && l.exclusive && l.root == root && !l.closed.Load()
}
func (l *RetentionLease) Close() error {
	if l == nil {
		return nil
	}
	l.closed.Store(true)
	return l.lease.Close()
}
