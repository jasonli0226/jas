package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrLocked is returned when another process already holds a lock.
var ErrLocked = errors.New("locked by another jas process")

const (
	lockFileMode = 0o600
	lockDirMode  = 0o700
)

// LockPath creates or opens path and takes a non-blocking exclusive lock on
// it, returning ErrLocked when another holder has it. release closes the file,
// which drops the lock; the file itself is left in place.
func LockPath(path string) (release func() error, err error) {
	if err := os.MkdirAll(filepath.Dir(path), lockDirMode); err != nil {
		return nil, fmt.Errorf("create lock dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		f.Close()
		if errors.Is(err, ErrLocked) {
			return nil, ErrLocked
		}
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return f.Close, nil
}
