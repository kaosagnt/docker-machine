package filelock

import (
	"errors"
	"os"
	"time"
)

var ErrAcquireTimeout = errors.New("timed out awaiting file lock acquisition")

type Lock struct {
	file *os.File
}

func (l *Lock) Unlock() error {
	return l.file.Close()
}

func Acquire(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if tryLock(file) {
			return &Lock{file: file}, nil
		}
		_ = file.Close()
		if time.Now().After(deadline) {
			return nil, ErrAcquireTimeout
		}
		time.Sleep(10 * time.Millisecond)
	}
}
