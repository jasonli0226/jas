package farm

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestDetect_Markers(t *testing.T) {
	cases := []struct {
		files []string
		check string
		glob  string
	}{
		{[]string{"go.mod"}, "go build ./... && go test ./...", "**/*_test.go"},
		{[]string{"package.json", "tsconfig.json"}, "npx tsc --noEmit", "**/*.test.*"},
		{[]string{"package.json"}, "npm test", "**/__tests__/**"},
		{[]string{"pyproject.toml"}, "pytest", "tests/**"},
		{[]string{"requirements.txt"}, "pytest", "**/test_*.py"},
		{[]string{"Cargo.toml"}, "cargo build && cargo test", "tests/**"},
	}
	for _, c := range cases {
		d := t.TempDir()
		for _, f := range c.files {
			write(t, filepath.Join(d, f), "x")
		}
		got := Detect(d)
		assert.Eq(t, len(got["check"]), 1)
		assert.Contains(t, got["check"][0], c.check)
		assert.Contains(t, strings.Join(got["test_globs"], ","), c.glob)
	}
	assert.Eq(t, len(Detect(t.TempDir())), 0)
}

func TestResolveWorktreeDir(t *testing.T) {
	repo, jasHome := t.TempDir(), t.TempDir()

	got, err := ResolveWorktreeDir(Config{}, repo, jasHome)
	assert.NoErr(t, err)
	realRepo, _ := filepath.EvalSymlinks(repo)
	assert.Eq(t, got, filepath.Join(realRepo, ".jas", "worktrees"))

	var c Config
	c.Worktree.Dir = filepath.Join(repo, ".jas", "worktrees", "mine")
	got, err = ResolveWorktreeDir(c, repo, jasHome)
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(realRepo, ".jas", "worktrees", "mine"))

	c.Worktree.Dir = filepath.Join(repo, "wt")
	_, err = ResolveWorktreeDir(c, repo, jasHome)
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "inside the repo")

	c.Worktree.Dir = filepath.Join(jasHome, "x", "y")
	_, err = ResolveWorktreeDir(c, repo, jasHome)
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "inside the jas home")

	link := filepath.Join(t.TempDir(), "link")
	assert.NoErr(t, os.Symlink(repo, link))
	c.Worktree.Dir = filepath.Join(link, "deep")
	_, err = ResolveWorktreeDir(c, repo, jasHome)
	if err == nil {
		t.Fatal("want error for symlink into repo")
	}
	assert.Contains(t, err.Error(), "inside the repo")

	ok := filepath.Join(t.TempDir(), "wts")
	c.Worktree.Dir = ok
	got, err = ResolveWorktreeDir(c, repo, jasHome)
	assert.NoErr(t, err)
	assert.Contains(t, got, "wts")
}

func TestResolveWorktreeDir_LinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "x")
	linked := filepath.Join(t.TempDir(), "linked")
	run("worktree", "add", "-q", "-b", "b", linked)
	var c Config
	c.Worktree.Dir = filepath.Join(linked, "sub")
	_, err := ResolveWorktreeDir(c, repo, t.TempDir())
	if err == nil {
		t.Fatal("want error for dir inside linked worktree")
	}
	assert.Contains(t, err.Error(), "inside the repo")
}
