//go:build windows

package google

import (
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/machine/libmachine/filelock"
	"golang.org/x/sys/windows"
)

func acquirePlacementHealthLock(path string, timeout time.Duration) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := filelock.Acquire(path, timeout)
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Unlock() }, nil
}

func replacePlacementHealthFile(source, destination string) error {
	sourcePtr, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPtr, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(sourcePtr, destinationPtr, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
