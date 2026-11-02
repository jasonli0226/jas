//go:build unix

package sessions

import (
	"errors"
	"syscall"
)

// Alive reports whether a process with pid runs on this host.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
