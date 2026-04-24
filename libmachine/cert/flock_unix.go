//go:build !windows

package cert

import (
	"os"

	"golang.org/x/sys/unix"
)

// newFileLock opens (creating if necessary) the file at path and acquires an
// exclusive lock on it, blocking until the lock is available. The lock is
// released by calling Unlock.
func newFileLock(path string) (*fileLock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return &fileLock{f: f}, nil
}
