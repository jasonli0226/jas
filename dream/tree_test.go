package dream

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestHashTree_MissingDir(t *testing.T) {
	h, err := HashTree(filepath.Join(t.TempDir(), "none"))
	assert.NoErr(t, err)
	assert.Eq(t, len(h), 0)
	if h == nil {
		t.Fatal("want non-nil map")
	}
}

func TestHashTree_Files(t *testing.T) {
	d := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(d, "projects", "k"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(d, "MEMORY.md"), []byte("x"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(d, "projects", "k", "n.md"), []byte("y"), 0o644))
	h, err := HashTree(d)
	assert.NoErr(t, err)
	assert.Eq(t, len(h), 2)
	assert.Eq(t, h["MEMORY.md"], "2d711642b726b04401627ca9fbac32f5c8530fb1903cc4db02258717921a4881")
	assert.Eq(t, len(h["projects/k/n.md"]), 64)
}

func TestCopyTree_SkipsInnerLinks(t *testing.T) {
	src, dst := t.TempDir(), filepath.Join(t.TempDir(), "out")
	assert.NoErr(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(src, "sub", "a.md"), []byte("a"), 0o644))
	assert.NoErr(t, os.Symlink(filepath.Join(src, "sub", "a.md"), filepath.Join(src, "link.md")))
	assert.NoErr(t, os.Symlink(filepath.Join(src, "sub"), filepath.Join(src, "linkdir")))
	assert.NoErr(t, CopyTree(src, dst))
	b, err := os.ReadFile(filepath.Join(dst, "sub", "a.md"))
	assert.NoErr(t, err)
	assert.Eq(t, string(b), "a")
	for _, n := range []string{"link.md", "linkdir"} {
		if _, err := os.Lstat(filepath.Join(dst, n)); !os.IsNotExist(err) {
			t.Fatalf("%s copied (err=%v)", n, err)
		}
	}
}

func TestChangedPaths(t *testing.T) {
	a := map[string]string{"keep": "1", "mod": "1", "gone": "1"}
	b := map[string]string{"keep": "1", "mod": "2", "new": "1"}
	got := ChangedPaths(a, b)
	assert.Eq(t, len(got), 3)
	assert.Eq(t, got[0], "gone")
	assert.Eq(t, got[1], "mod")
	assert.Eq(t, got[2], "new")
}
