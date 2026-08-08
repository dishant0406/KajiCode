//go:build windows

package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
)

// lockPath returns the OS lock file used to serialize cross-process access to
// this store's state file.
func (store *Store) lockPath() string {
	return filepath.Join(store.Dir, StateFile+".lock")
}

// acquireFileLock takes an exclusive OS lock (LockFileEx) on the store's lock
// file so concurrent processes sharing the same learning root serialize their
// state mutations. It blocks until the lock is available and returns a release
// function. A transient sharing violation is retried for the same reason as
// the sessions store (antivirus / search indexers).
func (store *Store) acquireFileLock() (func(), error) {
	path := store.lockPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create learning dir: %w", err)
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open learning lock: %w", err)
	}
	handle := windows.Handle(file.Fd())
	var overlapped windows.Overlapped
	for attempts := 0; ; attempts++ {
		err = windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
		if err == nil {
			break
		}
		if err != windows.ERROR_LOCK_VIOLATION && err != windows.ERROR_SHARING_VIOLATION {
			_ = file.Close()
			return nil, fmt.Errorf("lock learning state: %w", err)
		}
		if attempts >= 1000 {
			_ = file.Close()
			return nil, fmt.Errorf("lock learning state: %w", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &overlapped)
		_ = file.Close()
	}, nil
}
