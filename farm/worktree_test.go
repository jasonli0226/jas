package farm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jasonli0226/jas/core"
)

func mustGit(t *testing.T, g GitRunner, dir string, args ...string) string {
	t.Helper()
	out, err := g.Run(context.Background(), dir, args...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// isolateTemp points the system temp dir (TMPDIR, and TMP/TEMP on Windows) at
// a per-test dir, so workers' jas-farm temp dirs go away with the test.
func isolateTemp(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for _, k := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(k, dir)
	}
}

func newRepo(t *testing.T) (*Worktrees, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	isolateTemp(t)
	root := t.TempDir()
	main := filepath.Join(root, "main")
	if err := os.MkdirAll(main, 0o755); err != nil {
		t.Fatal(err)
	}
	g := NewGit()
	mustGit(t, g, main, "init", "-q", "-b", "main")
	writeFile(t, filepath.Join(main, "a.txt"), "one\ntwo\nthree\n")
	writeFile(t, filepath.Join(main, ".gitignore"), ".env\n.env.test\n")
	mustGit(t, g, main, "add", "-A")
	mustGit(t, g, main, "commit", "-q", "-m", "init")
	w := &Worktrees{Git: g, Main: main, Dir: filepath.Join(root, "wt"), FarmID: "f1"}
	return w, mustGit(t, g, main, "rev-parse", "HEAD")
}

func TestWorktrees_CreateFromBase(t *testing.T) {
	w, base := newRepo(t)
	path, branch, err := w.Create(context.Background(), "core", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "jas/farm/f1/core" || path != filepath.Join(w.Dir, "f1", "part-core") {
		t.Fatalf("path=%s branch=%s", path, branch)
	}
	if got := mustGit(t, w.Git, w.Main, "rev-parse", branch); got != base {
		t.Fatalf("branch at %s, want %s", got, base)
	}
	if _, err := os.Stat(filepath.Join(path, "a.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestWorktrees_DepsMerged(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	p, _, err := w.Create(ctx, "dep", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p, "x.txt"), "X\n")
	if _, err := w.Commit(ctx, p, "dep", "add x"); err != nil {
		t.Fatal(err)
	}
	p2, _, err := w.Create(ctx, "next", base, []string{"dep"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p2, "x.txt")); err != nil {
		t.Fatalf("dep commit missing: %v", err)
	}
}

func TestWorktrees_DepConflict(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	for _, part := range []string{"d1", "d2"} {
		p, _, err := w.Create(ctx, part, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(p, "a.txt"), "one\n"+part+"\nthree\n")
		if _, err := w.Commit(ctx, p, part, "edit"); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := w.Create(ctx, "next", base, []string{"d1", "d2"})
	var mc *MergeConflictError
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
	if mc.Part != "next" || mc.Dep != "d2" || len(mc.Files) != 1 || mc.Files[0] != "a.txt" {
		t.Fatalf("%+v", mc)
	}
}

func TestWorktrees_CommitIdentityAndTrailers(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	p, _, _ := w.Create(ctx, "core", base, nil)
	writeFile(t, filepath.Join(p, "b.txt"), "b\n")
	sha, err := w.Commit(ctx, p, "core", "feat: b")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustGit(t, w.Git, p, "log", "-1", "--format=%an <%ae>|%cn <%ce>"); got != "jas farm <jas-farm@localhost>|jas farm <jas-farm@localhost>" {
		t.Fatal(got)
	}
	body := mustGit(t, w.Git, p, "log", "-1", "--format=%B")
	if !strings.Contains(body, "Jas-Farm: f1") || !strings.Contains(body, "Jas-Part: core") {
		t.Fatal(body)
	}
	if files := mustGit(t, w.Git, p, "show", "--name-only", "--format=", "HEAD"); files != "b.txt" {
		t.Fatalf("committed %q", files)
	}
	again, err := w.Commit(ctx, p, "core", "noop")
	if err != nil || again != sha {
		t.Fatalf("noop commit = %s, %v; want %s", again, err, sha)
	}
}

func TestWorktrees_ParallelCommitsNoLockError(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	parts := []string{"p1", "p2", "p3", "p4"}
	paths := map[string]string{}
	for _, part := range parts {
		p, _, err := w.Create(ctx, part, base, nil)
		if err != nil {
			t.Fatal(err)
		}
		paths[part] = p
	}
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for _, part := range parts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				writeFile(t, filepath.Join(paths[part], fmt.Sprintf("f%d.txt", i)), part)
				if _, err := w.Commit(ctx, paths[part], part, "c"); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestWorktrees_CopyRefusesDenied(t *testing.T) {
	w, base := newRepo(t)
	p, _, _ := w.Create(context.Background(), "core", base, nil)
	writeFile(t, filepath.Join(w.Main, ".env"), "S=1\n")
	writeFile(t, filepath.Join(w.Main, ".env.test"), "T=1\n")
	w.Deny = func(abs string) bool { return filepath.Base(abs) == ".env" }
	w.Copy = []string{".env"}
	if err := w.CopyFiles(p); err == nil || !strings.Contains(err.Error(), ".env") {
		t.Fatalf("err = %v", err)
	}
	w.Copy = []string{".env.test"}
	if err := w.CopyFiles(p); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(p, ".env.test")); err != nil || string(b) != "T=1\n" {
		t.Fatalf("%q %v", b, err)
	}
	w.Copy = []string{"a.txt"} // tracked, not ignored
	if err := w.CopyFiles(p); err == nil {
		t.Fatal("copied a non-ignored file")
	}
	w.Copy = []string{"missing"}
	if err := w.CopyFiles(p); err == nil {
		t.Fatal("copied a missing file")
	}
}

// A worktree made before the part- prefix (<farm>/<part>) is still found, so
// clean and retries remove it.
func TestWorktrees_RemoveLegacyPath(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	legacy := filepath.Join(w.Dir, "f1", "core")
	mustGit(t, w.Git, w.Main, "worktree", "add", "-b", w.Branch("core"), legacy, base)
	if got := w.Path("core"); got != legacy {
		t.Fatalf("Path = %s, want legacy %s", got, legacy)
	}
	if err := w.Remove(ctx, "core"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatal("legacy worktree still present")
	}
	if got := w.Path("core"); got != filepath.Join(w.Dir, "f1", "part-core") {
		t.Fatalf("Path after remove = %s", got)
	}
}

func TestWorktrees_SetupAndRemove(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	p, branch, _ := w.Create(ctx, "core", base, nil)
	if res, err := w.RunSetup(ctx, p); err != nil || res.ExitCode != 0 {
		t.Fatal(res, err)
	}
	w.Setup = "exit 3"
	if res, err := w.RunSetup(ctx, p); err == nil || res.ExitCode != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	if err := w.Remove(ctx, "core"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("worktree still present")
	}
	mustGit(t, w.Git, w.Main, "rev-parse", "--verify", branch)
	if err := Prune(ctx, w.Git, w.Main); err != nil {
		t.Fatal(err)
	}
}

func TestDirty(t *testing.T) {
	w, _ := newRepo(t)
	ctx := context.Background()
	if d, err := Dirty(ctx, w.Git, w.Main); err != nil || d {
		t.Fatal(d, err)
	}
	writeFile(t, filepath.Join(w.Main, "new.txt"), "x")
	if d, err := Dirty(ctx, w.Git, w.Main); err != nil || !d {
		t.Fatal(d, err)
	}
}

// jas rewrites its session records every turn; a repo that tracks them by
// mistake must still count as clean.
func TestDirty_IgnoresSessionRecords(t *testing.T) {
	w, _ := newRepo(t)
	ctx := context.Background()
	rec := filepath.Join(w.Main, ".jas", "sessions", "s1.json")
	writeFile(t, rec, "{}")
	mustGit(t, w.Git, w.Main, "add", "-f", rec)
	mustGit(t, w.Git, w.Main, "commit", "-q", "-m", "track a record")
	writeFile(t, rec, `{"v":2}`)
	if d, err := Dirty(ctx, w.Git, w.Main); err != nil || d {
		t.Fatal(d, err)
	}
	writeFile(t, filepath.Join(w.Main, ".jas", "farm.yaml"), "check: x\n")
	if d, err := Dirty(ctx, w.Git, w.Main); err != nil || !d {
		t.Fatal(d, err)
	}
}

func TestWorktrees_CopyRefusesEscapes(t *testing.T) {
	w, base := newRepo(t)
	p, _, _ := w.Create(context.Background(), "core", base, nil)
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "O")
	writeFile(t, filepath.Join(w.Main, ".env"), "S=1\n")
	writeFile(t, filepath.Join(w.Main, ".gitignore"), ".env\n.env.local\n.out\n")
	if err := os.Symlink(".env", filepath.Join(w.Main, ".env.local")); err != nil {
		t.Skip("symlinks unavailable")
	}
	if err := os.Symlink(outside, filepath.Join(w.Main, ".out")); err != nil {
		t.Skip("symlinks unavailable")
	}
	w.Deny = func(abs string) bool { return filepath.Base(abs) == ".env" }
	for _, c := range []string{"../x", outside, ".env.local", ".out"} {
		w.Copy = []string{c}
		if err := w.CopyFiles(p); err == nil {
			t.Errorf("CopyFiles(%q) succeeded", c)
		}
	}
}

func TestWorktrees_CopyRefusesDestEscape(t *testing.T) {
	w, base := newRepo(t)
	p, _, _ := w.Create(context.Background(), "core", base, nil)
	writeFile(t, filepath.Join(w.Main, ".gitignore"), "cfg/\n")
	writeFile(t, filepath.Join(w.Main, "cfg", "a"), "A")
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(p, "cfg")); err != nil {
		t.Skip("symlinks unavailable")
	}
	w.Copy = []string{"cfg/a"}
	if err := w.CopyFiles(p); err == nil {
		t.Fatal("copied through symlinked destination")
	}
	if _, err := os.Stat(filepath.Join(target, "a")); err == nil {
		t.Fatal("file escaped the worktree")
	}
}

func TestWorktrees_HooksNotRun(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	for _, h := range []string{"pre-merge-commit", "commit-msg", "pre-commit"} {
		writeFile(t, filepath.Join(w.Main, ".git", "hooks", h), "#!/bin/sh\nexit 1\n")
		if err := os.Chmod(filepath.Join(w.Main, ".git", "hooks", h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p, _, err := w.Create(ctx, "dep", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(p, "x.txt"), "X")
	if _, err := w.Commit(ctx, p, "dep", "x"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := w.Create(ctx, "next", base, []string{"dep"}); err != nil {
		t.Fatalf("dep merge ran hooks: %v", err)
	}
}

func TestWorktrees_CreateCleansUpAndRetries(t *testing.T) {
	w, base := newRepo(t)
	ctx := context.Background()
	for _, part := range []string{"d1", "d2"} {
		p, _, _ := w.Create(ctx, part, base, nil)
		writeFile(t, filepath.Join(p, "a.txt"), "one\n"+part+"\nthree\n")
		if _, err := w.Commit(ctx, p, part, "edit"); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := w.Create(ctx, "next", base, []string{"d1", "d2"})
	var mc *MergeConflictError
	if !errors.As(err, &mc) {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(w.Path("next")); err == nil {
		t.Fatal("worktree left behind")
	}
	if out := mustGit(t, w.Git, w.Main, "branch", "--list", w.Branch("next")); out != "" {
		t.Fatalf("branch left behind: %q", out)
	}
	if _, _, err := w.Create(ctx, "next", base, []string{"d1"}); err != nil {
		t.Fatalf("retry: %v", err)
	}
}

func TestWorktrees_CreateMakesTmpDir(t *testing.T) {
	w, base := newRepo(t) // no Setup: the worker's TMPDIR must still exist
	path, _, err := w.Create(context.Background(), "core", base, nil)
	if err != nil {
		t.Fatal(err)
	}
	tmp := TmpDir(path)
	if fi, err := os.Stat(tmp); err != nil || !fi.IsDir() {
		t.Fatalf("TMPDIR missing after Create: %v", err)
	}
	// Inside a git checkout, t.TempDir() in the project's own tests would
	// land in a repo; tests that expect a plain temp dir then fail.
	if within(tmp, path) || within(tmp, w.Main) {
		t.Fatalf("TMPDIR %s is inside a checkout", tmp)
	}
	if err := w.Remove(context.Background(), "core"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("TMPDIR left after Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(tmp)); !os.IsNotExist(err) {
		t.Fatalf("empty farm temp dir left after the last Remove: %v", err)
	}
}

func TestNewRepo_IsolatesSystemTemp(t *testing.T) {
	before := os.TempDir()
	newRepo(t)
	if os.TempDir() == before {
		t.Fatal("farm tests would leave jas-farm dirs in the real system temp dir")
	}
}

func TestWorkerPolicy_TmpDirOutsideWorktree(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "wt", "f1", "core")
	p := WorkerPolicy(core.ExecPolicy{}, Role{}, wt)
	if !slices.Contains(p.Env, "TMPDIR="+TmpDir(wt)) {
		t.Fatalf("env %v", p.Env)
	}
	if within(TmpDir(wt), wt) {
		t.Fatalf("TmpDir %s inside worktree", TmpDir(wt))
	}
}

func TestWorktrees_CreateExcludesInRepoWorktrees(t *testing.T) {
	w, base := newRepo(t)
	w.Dir = filepath.Join(w.Main, ".jas", "worktrees") // the default
	if _, _, err := w.Create(context.Background(), "core", base, nil); err != nil {
		t.Fatal(err)
	}
	if out := mustGit(t, w.Git, w.Main, "status", "--porcelain"); out != "" {
		t.Fatalf("main checkout dirty after Create: %q", out)
	}
}
