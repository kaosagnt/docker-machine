//go:build !windows

package google

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func acquirePlacementHealthLock(path string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return func() {
				_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		_ = file.Close()
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out acquiring placement health lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func replacePlacementHealthFile(source, destination string) error {
	return os.Rename(source, destination)
}
