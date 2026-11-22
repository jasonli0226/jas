package farm

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestLockFarm_RunningWhileHeld(t *testing.T) {
	home := t.TempDir()
	release, err := LockFarm(home, "f1")
	if err != nil {
		t.Fatal(err)
	}
	if !FarmRunning(home, "f1") {
		t.Fatal("not running while the lock is held")
	}
	pid, err := os.ReadFile(filepath.Join(home, runDirName, "f1.pid"))
	if err != nil || strings.TrimSpace(string(pid)) != strconv.Itoa(os.Getpid()) {
		t.Fatalf("pid file %q, %v", pid, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if FarmRunning(home, "f1") {
		t.Fatal("running after release")
	}
	if _, err := os.Stat(filepath.Join(home, runDirName, "f1.pid")); !os.IsNotExist(err) {
		t.Fatalf("pid file left after release: %v", err)
	}
}

func TestLockFarm_SecondHolderFails(t *testing.T) {
	home := t.TempDir()
	release, err := LockFarm(home, "f1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := LockFarm(home, "f1"); !errors.Is(err, core.ErrLocked) {
		t.Fatalf("second LockFarm: %v, want ErrLocked", err)
	}
}

func TestLockFarm_RetriesPastProbe(t *testing.T) {
	home := t.TempDir()
	probe, err := core.LockPath(filepath.Join(home, runDirName, "f1.lock"))
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(100 * time.Millisecond)
		_ = probe()
	}()
	release, err := LockFarm(home, "f1")
	if err != nil {
		t.Fatalf("LockFarm behind a short probe: %v", err)
	}
	_ = release()
}

func TestFarmRunning_UnknownFarm(t *testing.T) {
	home := t.TempDir()
	if FarmRunning(home, "nope") {
		t.Fatal("unknown farm reported running")
	}
	release, err := LockFarm(home, "nope")
	if err != nil {
		t.Fatalf("the probe left a held lock: %v", err)
	}
	_ = release()
}

func TestLockFarm_Permissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix modes")
	}
	home := t.TempDir()
	release, err := LockFarm(home, "f1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	for p, want := range map[string]os.FileMode{
		filepath.Join(home, runDirName):            0o700,
		filepath.Join(home, runDirName, "f1.lock"): 0o600,
		filepath.Join(home, runDirName, "f1.pid"):  0o600,
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Fatalf("%s mode %o, want %o", p, got, want)
		}
	}
}

func TestStartFarm_HoldsLock(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var pr provRig
	r := newRunRig(t, "true", twoParts(), twoWriters)
	pr.wrap(r, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	if !FarmRunning(r.home, h.FarmID()) {
		t.Fatal("farm not running while a part runs")
	}
	close(release)
	h.Wait()
	if FarmRunning(r.home, h.FarmID()) {
		t.Fatal("farm still running after Wait")
	}
}
