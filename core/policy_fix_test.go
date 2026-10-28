package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestResolve_RefusesDanglingSymlink(t *testing.T) {
	root := realpath(t, t.TempDir())
	outside := realpath(t, t.TempDir())
	assert.NoErr(t, os.Symlink(filepath.Join(outside, "newfile"), filepath.Join(root, "out")))
	assert.NoErr(t, os.Symlink(filepath.Join(root, "missing"), filepath.Join(root, "in")))
	for _, name := range []string{"out", "in", "out/sub"} {
		_, err := ExecPolicy{Root: root}.Resolve(name)
		if err == nil {
			t.Fatalf("%s: expected error", name)
		}
		assert.Contains(t, err.Error(), "dangling symlink")
	}
}

func TestResolve_SymlinkedParentOutside(t *testing.T) {
	root := realpath(t, t.TempDir())
	outside := realpath(t, t.TempDir())
	assert.NoErr(t, os.Symlink(outside, filepath.Join(root, "d")))
	_, err := ExecPolicy{Root: root}.Resolve("d/new.txt")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "outside project root")
}

func TestResolve_PrefixSiblingAndAbsolute(t *testing.T) {
	base := realpath(t, t.TempDir())
	root := filepath.Join(base, "root")
	evil := filepath.Join(base, "root-evil")
	assert.NoErr(t, os.MkdirAll(root, 0o755))
	assert.NoErr(t, os.MkdirAll(evil, 0o755))
	p := ExecPolicy{Root: root}
	for _, path := range []string{evil, filepath.Join(evil, "f"), "../root-evil/f", "/etc/passwd"} {
		_, err := p.Resolve(path)
		if err == nil {
			t.Fatalf("%s: expected error", path)
		}
		assert.Contains(t, err.Error(), "outside project root")
	}
	got, err := p.Resolve(filepath.Join(root, "ok"))
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(root, "ok"))
}

func TestResolve_DirSymlink(t *testing.T) {
	root := realpath(t, t.TempDir())
	outside := realpath(t, t.TempDir())
	assert.NoErr(t, os.Mkdir(filepath.Join(root, "real"), 0o755))
	assert.NoErr(t, os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "in")))
	assert.NoErr(t, os.Symlink(outside, filepath.Join(root, "out")))
	p := ExecPolicy{Root: root}
	got, err := p.Resolve("in/f")
	assert.NoErr(t, err)
	assert.Eq(t, got, filepath.Join(root, "real", "f"))
	_, err = p.Resolve("out")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "outside project root")
}

func TestResolve_EmptyRoot(t *testing.T) {
	_, err := ExecPolicy{}.Resolve("x")
	if err == nil {
		t.Fatal("expected error")
	}
	assert.Contains(t, err.Error(), "project root not set")
}
