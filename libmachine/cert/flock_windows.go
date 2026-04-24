//go:build windows

package cert

import (
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// newFileLockWithTimeout opens (creating if necessary) the file at path and
// tries to acquire an exclusive lock on it, retrying until timeout elapses.
// On timeout it returns errFileLockAcquiring.
//
// LOCKFILE_FAIL_IMMEDIATELY makes the LockFileEx call non-blocking so that
// the retry loop's deadline is observable. Locks the first byte of the file
// — the range is arbitrary since we never read or write its contents, but a
// non-zero-length range is required for the lock itself to be meaningful.
func newFileLockWithTimeout(path string, timeout time.Duration) (*fileLock, error) {
	deadline := time.Now().Add(timeout)
	backoff := 10 * time.Millisecond

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}

		ol := new(windows.Overlapped)
		err = windows.LockFileEx(
			windows.Handle(f.Fd()),
			windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
			0,
			1, 0,
			ol,
		)
		if err == nil {
			return &fileLock{f: f}, nil
		}

		f.Close()

		if time.Now().After(deadline) {
			return nil, errFileLockAcquiring
		}

		time.Sleep(backoff)
	}
}
