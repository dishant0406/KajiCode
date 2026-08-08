//go:build !windows

package harness

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// lockPath returns the OS lock file used to serialize cross-process access to
// this store's state file.
func (store *Store) lockPath() string {
	return filepath.Join(store.Dir, StateFile+".lock")
}

// acquireFileLock takes an exclusive OS advisory lock (flock) on the store's
// lock file so concurrent processes sharing the same learning root serialize
// their state mutations. It blocks until the lock is available and returns a
// release function; closing the file descriptor releases the lock.
func (store *Store) acquireFileLock() (func(), error) {
	path := store.lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create learning dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open learning lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock learning state: %w", err)
	}
	return func() {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
	}, nil
}
