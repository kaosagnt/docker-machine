//go:build !windows

package cert

import (
	"os"

	"golang.org/x/sys/unix"
)

// flock attempts to acquire a non-blocking exclusive advisory lock on f.
// On success it returns a *fileLock that owns f; on failure it returns nil
// and leaves f untouched (the caller is responsible for closing it).
//
// LOCK_NB is used rather than a blocking Flock so the retry loop in
// newFileLockWithTimeout has an observable deadline — a blocking Flock
// would otherwise pin us in-kernel until the holder releases the lock,
// which is exactly the failure mode the timeout exists to bound.
func flock(f *os.File) *fileLock {
	if f == nil {
		return nil
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
		return &fileLock{f: f}
	}

	return nil
}
