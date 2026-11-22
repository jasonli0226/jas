package farm

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jasonli0226/jas/core"
)

// A running farm holds <home>/run/<farm-id>.lock for its lifetime, so
// another process (jas attach) can tell a running farm from an ended or
// dead one: the OS drops the lock when the process exits or crashes.

const runDirName = "run"

// lockRetries and lockRetryWait let a farm start while another process
// briefly holds its lock to probe it (FarmRunning).
const (
	lockRetries   = 3
	lockRetryWait = 100 * time.Millisecond
)

func lockPath(home, id string) string { return filepath.Join(home, runDirName, id+".lock") }
func pidPath(home, id string) string  { return filepath.Join(home, runDirName, id+".pid") }
func sockPath(home, id string) string { return filepath.Join(home, runDirName, id+".sock") }

// LockFarm takes the farm's liveness lock and writes its pid beside it (the
// lock file stays empty: on Windows its byte-range lock blocks writes from
// another handle). release drops the lock and removes the pid file.
func LockFarm(home, id string) (release func() error, err error) {
	unlock, err := core.LockPath(lockPath(home, id))
	for i := 0; i < lockRetries && errors.Is(err, core.ErrLocked); i++ {
		time.Sleep(lockRetryWait)
		unlock, err = core.LockPath(lockPath(home, id))
	}
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(pidPath(home, id), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		_ = unlock()
		return nil, err
	}
	return func() error {
		rerr := os.Remove(pidPath(home, id))
		if errors.Is(rerr, os.ErrNotExist) {
			rerr = nil
		}
		return errors.Join(unlock(), rerr)
	}, nil
}

// FarmRunning reports whether a process holds the farm's lock. It probes by
// taking the lock and releasing it at once.
func FarmRunning(home, id string) bool {
	if _, err := os.Stat(lockPath(home, id)); err != nil {
		return false
	}
	unlock, err := core.LockPath(lockPath(home, id))
	if err != nil {
		return errors.Is(err, core.ErrLocked)
	}
	_ = unlock()
	return false
}
