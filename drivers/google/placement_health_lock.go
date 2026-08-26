package google

import (
	"os"
	"path/filepath"
	"time"

	"github.com/docker/machine/libmachine/filelock"
)

func acquirePlacementHealthLock(path string, timeout time.Duration) (func() error, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	lock, err := filelock.Acquire(path, timeout)
	if err != nil {
		return nil, err
	}
	return lock.Unlock, nil
}
