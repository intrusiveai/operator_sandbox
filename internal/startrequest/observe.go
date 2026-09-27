//go:build linux || darwin

package startrequest

import (
	"context"
	"time"
)

// Observe retries a short overlapping atomic publication, without treating a
// leftover pending record as valid or granting execution authority. Permanent
// damage still fails after the bounded read window. Read remains strict for
// claim, mutation and recovery decisions.
func Observe(ctx context.Context, root, id string) (Snapshot, error) {
	return observe(ctx, func() (Snapshot, error) { return Read(root, id) })
}

func ObserveLink(ctx context.Context, directory string) (RunLink, Snapshot, error) {
	var link RunLink
	s, err := observe(ctx, func() (Snapshot, error) {
		var s Snapshot
		var err error
		link, s, err = ReadLink(directory)
		return s, err
	})
	return link, s, err
}

func observe[T any](ctx context.Context, read func() (T, error)) (value T, err error) {
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
	defer cancel()
	for {
		if parent.Err() != nil {
			return value, parent.Err()
		}
		if ctx.Err() != nil {
			if err != nil {
				return value, err
			}
			return value, ctx.Err()
		}
		value, err = read()
		if err == nil {
			return value, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return value, err
		case <-timer.C:
		}
	}
}
