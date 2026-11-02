package sessions_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/sessions"
)

func TestStale_Rules(t *testing.T) {
	now := time.Now()
	alive := func(pid int) bool { return pid == 1 }
	cases := []struct {
		name  string
		r     sessions.Record
		stale bool
	}{
		{"fresh dead pid", sessions.Record{Host: "h", PID: 2, Heartbeat: now.Add(-30 * time.Second), State: "active"}, false},
		{"old dead pid", sessions.Record{Host: "h", PID: 2, Heartbeat: now.Add(-90 * time.Second), State: "active"}, true},
		{"old alive pid", sessions.Record{Host: "h", PID: 1, Heartbeat: now.Add(-90 * time.Second), State: "active"}, false},
		{"very old alive pid", sessions.Record{Host: "h", PID: 1, Heartbeat: now.Add(-11 * time.Minute), State: "active"}, true},
		{"other host", sessions.Record{Host: "other", PID: 2, Heartbeat: now.Add(-90 * time.Second), State: "active"}, false},
		{"ended recently", sessions.Record{Host: "h", PID: 1, Heartbeat: now.Add(-30 * time.Second), State: "ended"}, false},
		{"ended long ago", sessions.Record{Host: "h", PID: 1, Heartbeat: now.Add(-61 * time.Second), State: "ended"}, true},
	}
	for _, c := range cases {
		if got := sessions.Stale(c.r, now, "h", alive); got != c.stale {
			t.Errorf("%s: stale=%v want %v", c.name, got, c.stale)
		}
	}
}

func register(t *testing.T, dir, id string) *sessions.Handle {
	t.Helper()
	h, err := sessions.Register(dir, sessions.Record{SessionID: id, Mode: "run", Title: "t"})
	assert.NoErr(t, err)
	return h
}

func TestRegister_AtomicAndListed(t *testing.T) {
	dir := t.TempDir()
	register(t, dir, "s1")
	got, err := sessions.List(dir, time.Now())
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0].State, "active")
	assert.Eq(t, got[0].PID, os.Getpid())
	entries, _ := os.ReadDir(dir)
	assert.Eq(t, len(entries), 1)
	raw, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	assert.Contains(t, string(raw), `"session_id": "s1"`)
}

func TestHandle_TouchCaps(t *testing.T) {
	dir := t.TempDir()
	h := register(t, dir, "s1")
	for i := 0; i < 250; i++ {
		h.Touch(fmt.Sprintf("f%d.go", i))
	}
	h.Touch("f0.go") // duplicate
	assert.NoErr(t, h.Flush())
	got, _ := sessions.List(dir, time.Now())
	assert.Eq(t, len(got[0].Touched), 200)
	assert.Eq(t, got[0].TouchedTruncated, true)
}

func TestPrune_RemovesStaleAndOldEnded(t *testing.T) {
	dir := t.TempDir()
	live := register(t, dir, "live")
	_ = live
	ended := register(t, dir, "ended")
	assert.NoErr(t, ended.Close())
	n, err := sessions.Prune(dir, time.Now().Add(30*time.Second))
	assert.NoErr(t, err)
	assert.Eq(t, n, 0)
	n, err = sessions.Prune(dir, time.Now().Add(61*time.Second))
	assert.NoErr(t, err)
	assert.Eq(t, n, 1) // live keeps its pid alive; only the ended record goes
	got, _ := sessions.List(dir, time.Now())
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0].SessionID, "live")
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	git(t, root, "init", "-q")
	git(t, root, "commit", "-q", "--allow-empty", "-m", "init")
	return root
}

func TestDir_GitExcludeOnce(t *testing.T) {
	root := gitRepo(t)
	d1, err := sessions.Dir(root)
	assert.NoErr(t, err)
	_, err = sessions.Dir(root)
	assert.NoErr(t, err)
	want, _ := filepath.EvalSymlinks(root)
	got, _ := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(d1)))
	assert.Eq(t, got, want)
	ex, _ := os.ReadFile(filepath.Join(root, ".git", "info", "exclude"))
	assert.Eq(t, strings.Count(string(ex), "/.jas/sessions/"), 1)
}

func TestDir_LinkedWorktreeUsesMainCheckout(t *testing.T) {
	root := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, root, "worktree", "add", "-q", wt)
	dMain, err := sessions.Dir(root)
	assert.NoErr(t, err)
	dWT, err := sessions.Dir(wt)
	assert.NoErr(t, err)
	assert.Eq(t, dWT, dMain)
}

func TestDir_NoGit(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(root))
	d, err := sessions.Dir(root)
	assert.NoErr(t, err)
	assert.Eq(t, d, filepath.Join(root, ".jas", "sessions"))
}

func TestHandle_HeartbeatAdvances(t *testing.T) {
	dir := t.TempDir()
	h := register(t, dir, "s1")
	first, _ := sessions.List(dir, time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.StartEvery(ctx, 20*time.Millisecond)
	time.Sleep(100 * time.Millisecond)
	later, _ := sessions.List(dir, time.Now())
	if !later[0].Heartbeat.After(first[0].Heartbeat) {
		t.Fatal("heartbeat did not advance")
	}
}

func TestHandle_CloseStopsHeartbeat(t *testing.T) {
	dir := t.TempDir()
	h := register(t, dir, "s1")
	h.StartEvery(context.Background(), 10*time.Millisecond)
	assert.NoErr(t, h.Close())
	closed, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	time.Sleep(60 * time.Millisecond)
	later, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	assert.Eq(t, string(later), string(closed))
	h.Touch("x.go") // no-op after Close
	again, _ := os.ReadFile(filepath.Join(dir, "s1.json"))
	assert.Eq(t, string(again), string(closed))
}

func TestCommonDirFromOutput(t *testing.T) {
	root := t.TempDir()
	assert.Eq(t, sessions.CommonDirFromOutput(root, ".git\n"), filepath.Join(root, ".git"))
	abs := filepath.Join(root, "x", ".git")
	assert.Eq(t, sessions.CommonDirFromOutput(root, abs+"\n"), abs)
}
