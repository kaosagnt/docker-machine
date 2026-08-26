//go:build !windows

package filelock

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLock(file *os.File) bool {
	return file != nil && unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) == nil
}
