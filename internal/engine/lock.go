package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// pollInterval is how often a waiting mutator retries the lock.
const pollInterval = 20 * time.Millisecond

// lock takes an exclusive flock on the file at path, creating it and its directory, and
// returns the function that releases it. It waits up to timeout, or until ctx ends. The lock
// is the open descriptor's, so the OS frees it when its holder dies (spec §11).
func lock(ctx context.Context, path string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := flock(ctx, f, timeout); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return func() { _ = f.Close() }, nil // closing the descriptor releases the lock
}

// flock takes an exclusive flock on f, polling until timeout or the end of ctx.
func flock(ctx context.Context, f *os.File, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return ErrLockTimeout
		case <-tick.C:
		}
	}
}
