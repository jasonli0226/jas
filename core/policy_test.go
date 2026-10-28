package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	assert.NoErr(t, err)
	return r
}

func TestFindRoot_GitAndPlain(t *testing.T) {
	repo := realpath(t, t.TempDir())
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("git unavailable: %v %s", err, out)
	}
	sub := filepath.Join(repo, "a", "b")
	assert.NoErr(t, os.MkdirAll(sub, 0o755))
	assert.Eq(t, realpath(t, FindRoot(sub)), repo)

	plain := realpath(t, t.TempDir())
	assert.Eq(t, FindRoot(plain), plain)
}

func TestResolve_RefusesDotDot(t *testing.T) {
	root := realpath(t, t.TempDir())
	_, err := ExecPolicy{Root: root}.Resolve("../outside")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "path outside project root")
}

func TestResolve_RefusesSymlinkOut(t *testing.T) {
	root := realpath(t, t.TempDir())
	target := filepath.Join(realpath(t, t.TempDir()), "x")
	assert.NoErr(t, os.WriteFile(target, []byte("x"), 0o644))
	assert.NoErr(t, os.Symlink(target, filepath.Join(root, "link")))
	_, err := ExecPolicy{Root: root}.Resolve("link")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "outside project root")
}

func TestResolve_AllowsNewFileInRoot(t *testing.T) {
	root := realpath(t, t.TempDir())
	got, err := ExecPolicy{Root: root}.Resolve("new/dir/f.go")
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(root, "new/dir/f.go"))
}

func TestResolve_AllowsRootItself(t *testing.T) {
	root := realpath(t, t.TempDir())
	got, err := ExecPolicy{Root: root}.Resolve(".")
	assert.NoErr(t, err)
	assert.Eq(t, got, root)
}
