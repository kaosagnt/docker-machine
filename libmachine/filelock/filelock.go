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
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return file.Close()
}

func Acquire(path string, timeout time.Duration) (*Lock, error) {
	deadline := time.Now().Add(timeout)
	firstAttempt := true
	for {
		if !firstAttempt && !time.Now().Before(deadline) {
			return nil, ErrAcquireTimeout
		}
		wasFirstAttempt := firstAttempt
		firstAttempt = false
		file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		locked, err := tryLock(file)
		if err != nil {
			_ = file.Close()
			return nil, err
		}
		if locked {
			if !wasFirstAttempt && time.Now().After(deadline) {
				_ = file.Close()
				return nil, ErrAcquireTimeout
			}
			return &Lock{file: file}, nil
		}
		_ = file.Close()
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, ErrAcquireTimeout
		}
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}
