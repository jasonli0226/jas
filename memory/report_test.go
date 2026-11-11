package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func writeNotes(t *testing.T, dir string, names ...string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o700)) // subdirs are not counted
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "sub", "deep.md"), []byte("x"), 0o600))
	for _, n := range names {
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, n), []byte("x\n"), 0o600))
	}
}

func TestReport_Counts(t *testing.T) {
	root := t.TempDir()
	d := Dirs{Profile: filepath.Join(root, "p"), Project: filepath.Join(root, "p", "projects", "k")}
	writeNotes(t, d.Profile, "MEMORY.md", "a.md", "b.md", "c.txt")
	writeNotes(t, d.Project, "MEMORY.md")
	prof, proj, err := LoadIndexes(d, false)
	assert.NoErr(t, err)
	out := Report(d, prof, proj, ModeOn)
	assert.Contains(t, out, "memory: on")
	assert.Contains(t, out, d.Profile)
	assert.Contains(t, out, d.Project)
	assert.Contains(t, out, "1/200 lines, 0.0/25 KB")
	assert.Contains(t, out, "2 notes")
	assert.Contains(t, out, "0 notes")
	assert.Eq(t, strings.Contains(out, "truncated"), false)

	big := strings.Repeat("- line\n", MaxIndexLines+10)
	assert.NoErr(t, os.WriteFile(filepath.Join(d.Profile, "MEMORY.md"), []byte(big), 0o600))
	prof, proj, err = LoadIndexes(d, false)
	assert.NoErr(t, err)
	assert.Contains(t, Report(d, prof, proj, ModeOn), "truncated")
}

func TestReport_HiddenAndOff(t *testing.T) {
	d := Dirs{Profile: "/m/p", Project: "/m/p/projects/k"}
	out := Report(d, Index{Path: "/m/p/MEMORY.md"}, Index{}, ModeRead)
	assert.Contains(t, out, "memory: read")
	assert.Contains(t, out, "hidden")
	off := Report(d, Index{}, Index{}, ModeOff)
	assert.Contains(t, off, "memory is off")
	assert.Contains(t, off, d.Profile)
	assert.Eq(t, strings.Count(off, "\n"), 0)
}
