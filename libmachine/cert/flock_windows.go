//go:build windows

package cert

import (
	"os"

	"golang.org/x/sys/windows"
)

// flock attempts to acquire a non-blocking exclusive lock on f.
// On success it returns a *fileLock that owns f; on failure it returns nil
// and leaves f untouched (the caller is responsible for closing it).
//
// LOCKFILE_FAIL_IMMEDIATELY makes the LockFileEx call non-blocking so the
// retry loop in newFileLockWithTimeout has an observable deadline. The first
// byte of the file is locked — the range is arbitrary since we never read or
// write its contents, but a non-zero-length range is required for the lock
// itself to be meaningful.
func flock(f *os.File) *fileLock {
	if f == nil {
		return nil
	}

	ol := new(windows.Overlapped)
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1, 0,
		ol,
	)
	if err == nil {
		return &fileLock{f: f}
	}

	return nil
}
