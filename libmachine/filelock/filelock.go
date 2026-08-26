package filelock

import (
	"errors"
	"os"
	"time"
)

var ErrAcquireTimeout = errors.New("timed out awaiting file lock acquisition")
var ErrInvalidTimeout = errors.New("file lock timeout must be non-negative")

type Lock struct {
	file *os.File
}

func (l *Lock) Unlock() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := l.file.Close(); err != nil {
		return err
	}
	l.file = nil
	return nil
}

func Acquire(path string, timeout time.Duration) (*Lock, error) {
	// timeout bounds retries caused by lock contention. The initial OpenFile and
	// non-blocking lock attempt always run, including at timeout zero; filesystem
	// syscall latency itself is outside this advisory-lock timeout contract.
	if timeout < 0 {
		return nil, ErrInvalidTimeout
	}
	started := time.Now()
	firstAttempt := true
	for {
		if !firstAttempt && time.Since(started) >= timeout {
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
			if !wasFirstAttempt && time.Since(started) >= timeout {
				_ = file.Close()
				return nil, ErrAcquireTimeout
			}
			return &Lock{file: file}, nil
		}
		_ = file.Close()
		remaining := timeout - time.Since(started)
		if remaining <= 0 {
			return nil, ErrAcquireTimeout
		}
		if remaining > 10*time.Millisecond {
			remaining = 10 * time.Millisecond
		}
		time.Sleep(remaining)
	}
}
