//go:build windows

package core

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockOffset is the single byte locked. It sits far past any real file end so
// readers (ListSessions) are not blocked from reading or appending.
const lockOffset = 1 << 62

// lockFile takes an exclusive, non-blocking lock on one byte of f. The lock is
// released when f is closed.
func lockFile(f *os.File) error {
	ol := &windows.Overlapped{Offset: uint32(lockOffset & 0xffffffff), OffsetHigh: uint32(lockOffset >> 32)}
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrSessionLocked
	}
	return err
}
