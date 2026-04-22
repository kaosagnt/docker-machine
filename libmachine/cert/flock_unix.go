//go:build !windows

package cert

import (
	"os"

	"golang.org/x/sys/unix"
)

// fileLock is an exclusive, blocking, cross-process advisory lock backed by a
// file. It is used to serialise concurrent invocations of
// BootstrapCertificates so that simultaneous `docker-machine create`
// subprocesses do not race on CA/client certificate generation.
type fileLock struct {
	f *os.File
}

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

// Unlock releases the lock. Closing the underlying file descriptor is
// sufficient: the kernel releases any flock held on the last close of the
// open file description.
func (l *fileLock) Unlock() error {
	return l.f.Close()
}
