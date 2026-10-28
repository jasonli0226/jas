//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestRead_RefusesNonRegular(t *testing.T) {
	p := policy(t)
	assert.NoErr(t, syscall.Mkfifo(filepath.Join(p.Root, "fifo"), 0o644))
	assert.NoErr(t, os.Mkdir(filepath.Join(p.Root, "dir"), 0o755))
	for _, name := range []string{"fifo", "dir"} {
		r := call(t, Read{}, p, `{"path":"`+name+`"}`)
		assert.Eq(t, r.IsError, true)
		assert.Contains(t, r.Output, "not a regular file")
	}
}

func TestLsGrep_SymlinkOutsideRoot(t *testing.T) {
	p := policy(t)
	outside := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("needle\n"), 0o644))
	assert.NoErr(t, os.Symlink(outside, filepath.Join(p.Root, "outdir")))
	assert.NoErr(t, os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(p.Root, "outfile")))
	write(t, p, "ok.txt", "needle\n")

	r := call(t, Grep{}, p, `{"pattern":"needle"}`)
	assert.Contains(t, r.Output, "ok.txt:1:needle")
	assert.NotContains(t, r.Output, "outfile")
	assert.NotContains(t, r.Output, "outdir")
	assert.NotContains(t, r.Output, "secret")

	r = call(t, Grep{}, p, `{"pattern":"needle","path":"outdir"}`)
	assert.Eq(t, r.IsError, true)
	r = call(t, Ls{}, p, `{"path":"outdir"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "outside project root")
}

func TestGrep_NonexistentPath(t *testing.T) {
	p := policy(t)
	r := call(t, Grep{}, p, `{"pattern":"x","path":"nope"}`)
	assert.Eq(t, r.IsError, true)
}
