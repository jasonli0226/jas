package core_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func newStore(t *testing.T) core.TrustStore {
	t.Helper()
	return core.TrustStore{Path: filepath.Join(t.TempDir(), "trust.json")}
}

func TestTrust_ContentChangeAsksAgain(t *testing.T) {
	root, st := t.TempDir(), newStore(t)
	ok, err := st.Check(root, []byte("v1"))
	assert.NoErr(t, err)
	assert.Eq(t, ok, false)
	assert.NoErr(t, st.Trust(root, []byte("v1")))
	ok, err = st.Check(root, []byte("v1"))
	assert.NoErr(t, err)
	assert.Eq(t, ok, true)
	ok, _ = st.Check(root, []byte("v2"))
	assert.Eq(t, ok, false)
	info, err := os.Stat(st.Path)
	assert.NoErr(t, err)
	assert.Eq(t, info.Mode().Perm(), os.FileMode(0o600))
}

func TestTrust_KeyIsSymlinkResolvedRoot(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	assert.NoErr(t, os.Symlink(root, link))
	st := newStore(t)
	assert.NoErr(t, st.Trust(link, []byte("x")))
	ok, _ := st.Check(root, []byte("x"))
	assert.Eq(t, ok, true)
}

func TestDecide_Table(t *testing.T) {
	cases := []struct {
		name                       string
		trusted, flag, interactive bool
		answer                     string
		wantLoad, wantAsked        bool
		wantStoredAfter            bool
	}{
		{"trusted", true, false, false, "", true, false, true},
		{"flag", false, true, false, "", true, false, false},
		{"yes once", false, false, true, "y", true, true, false},
		{"always", false, false, true, "always", true, true, true},
		{"no", false, false, true, "n", false, true, false},
		{"empty", false, false, true, "", false, true, false},
		{"no tty", false, false, false, "", false, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root, st := t.TempDir(), newStore(t)
			content := []byte("rules")
			if c.trusted {
				assert.NoErr(t, st.Trust(root, content))
			}
			asked := false
			var prompt string
			load, err := core.DecideContext(root, content, st, c.interactive, c.flag, func(p string) string {
				asked, prompt = true, p
				return c.answer
			})
			assert.NoErr(t, err)
			assert.Eq(t, load, c.wantLoad)
			assert.Eq(t, asked, c.wantAsked)
			if asked {
				assert.Eq(t, prompt, "trust "+root+" and load AGENTS.md? [y/N/always] ")
			}
			stored, _ := st.Check(root, content)
			assert.Eq(t, stored, c.wantStoredAfter)
		})
	}
}

func TestDecide_NoTTYSkipsWithWarning(t *testing.T) {
	r, w, err := os.Pipe()
	assert.NoErr(t, err)
	old := os.Stderr
	os.Stderr = w
	load, derr := core.DecideContext(t.TempDir(), []byte("x"), newStore(t), false, false, nil)
	os.Stderr = old
	w.Close()
	assert.NoErr(t, derr)
	assert.Eq(t, load, false)
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	assert.Contains(t, string(buf[:n]), "skipping untrusted AGENTS.md (pass --trust-project to load)")
}

func TestFindContextFile(t *testing.T) {
	root := t.TempDir()
	_, _, ok := core.FindContextFile(root)
	assert.Eq(t, ok, false)
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("claude"), 0o644))
	p, c, ok := core.FindContextFile(root)
	assert.Eq(t, ok, true)
	assert.Eq(t, filepath.Base(p), "CLAUDE.md")
	assert.Eq(t, string(c), "claude")
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("agents"), 0o644))
	p, _, _ = core.FindContextFile(root)
	assert.Eq(t, filepath.Base(p), "AGENTS.md")
}

func TestFindContextFile_DeniedSymlinkNotLoaded(t *testing.T) {
	root := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=abc"), 0o600))
	assert.NoErr(t, os.Symlink(".env", filepath.Join(root, "CLAUDE.md")))
	_, c, ok := core.FindContextFile(root)
	assert.Eq(t, ok, false)
	assert.Eq(t, len(c), 0)
}

func TestCapContext(t *testing.T) {
	assert.Eq(t, core.CapContext("hi"), "hi")
	big := core.CapContext(strings.Repeat("a", core.MaxContextBytes+10))
	assert.Contains(t, big, "[truncated")
	if len(big) > core.MaxContextBytes+200 {
		t.Fatalf("not capped: %d", len(big))
	}
}
