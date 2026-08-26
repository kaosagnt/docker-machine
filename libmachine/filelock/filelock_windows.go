//go:build windows

package filelock

import (
	"os"

	"golang.org/x/sys/windows"
)

func tryLock(file *os.File) bool {
	if file == nil {
		return false
	}
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1, 0,
		new(windows.Overlapped),
	) == nil
}
