//go:build windows

package cert

import (
	"os"

	"golang.org/x/sys/windows"
)

// newFileLock opens (creating if necessary) the file at path and acquires an
// exclusive lock on it, blocking until the lock is available. The lock is
// released by calling Unlock.
func newFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}

	// LOCKFILE_EXCLUSIVE_LOCK with LOCKFILE_FAIL_IMMEDIATELY unset means the
	// call blocks until the lock can be taken. Lock the first byte of the
	// file — the lock range is arbitrary since we never read or write its
	// contents; a non-zero-length range is required for the lock itself to
	// be meaningful.
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1, 0,
		ol,
	); err != nil {
		f.Close()
		return nil, err
	}

	return &fileLock{f: f}, nil
}
