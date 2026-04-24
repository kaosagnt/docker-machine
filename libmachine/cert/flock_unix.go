//go:build !windows

package cert

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// newFileLockWithTimeout opens (creating if necessary) the file at path and
// tries to acquire an exclusive lock on it, retrying until timeout elapses.
// On timeout it returns errFileLockAcquiring.
//
// The retry loop uses LOCK_NB rather than a blocking Flock so the deadline
// is observable — a blocking Flock would otherwise pin us in-kernel until
// the holder releases the lock, which is exactly the failure mode this
// timeout exists to bound.
func newFileLockWithTimeout(path string, timeout time.Duration) (*fileLock, error) {
	deadline := time.Now().Add(timeout)
	backoff := 10 * time.Millisecond

	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}

		if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return &fileLock{f: f}, nil
		}

		f.Close()

		if time.Now().After(deadline) {
			return nil, errFileLockAcquiring
		}

		time.Sleep(backoff)
	}
}
