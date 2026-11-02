package farm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func realGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@localhost",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@localhost")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, errb.String())
	}
	return out.String(), nil
}

// repo makes a git repo with core/a.go committed and returns its dir and base commit.
func repo(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	ctx := context.Background()
	mustGit := func(args ...string) string {
		out, err := realGit(ctx, dir, args...)
		assert.NoErr(t, err)
		return out
	}
	mustGit("init", "-q")
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "core"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "core", "a.go"), []byte("package core\n// one\n// two\n// three\n"), 0o644))
	mustGit("add", ".")
	mustGit("-c", "commit.gpgsign=false", "commit", "-q", "-m", "base")
	return dir, trimNL(mustGit("rev-parse", "HEAD"))
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

var coreOnly = []core.WriteRule{{All: []string{"core/**"}}}

func TestDiffViolations_ShellWrite(t *testing.T) {
	dir, base := repo(t)
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "cmd"), 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "cmd", "x.go"), []byte("x"), 0o644))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "core", "a.go"), []byte("changed"), 0o644))
	got, err := DiffViolations(context.Background(), realGit, dir, base, coreOnly)
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[cmd/x.go]")
}

func TestDiffViolations_RenameBothPaths(t *testing.T) {
	dir, base := repo(t)
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "cmd"), 0o755))
	_, err := realGit(context.Background(), dir, "mv", "core/a.go", "cmd/a.go")
	assert.NoErr(t, err)
	got, err := DiffViolations(context.Background(), realGit, dir, base, []core.WriteRule{{All: []string{"nothing/**"}}})
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[cmd/a.go core/a.go]")
	// With core/** allowed only the new path is reported.
	got, err = DiffViolations(context.Background(), realGit, dir, base, coreOnly)
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[cmd/a.go]")
}

func TestDiffViolations_NoRulesNoViolations(t *testing.T) {
	dir, base := repo(t)
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "z.txt"), []byte("z"), 0o644))
	got, err := DiffViolations(context.Background(), realGit, dir, base, nil)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 0)
}

func TestDiffViolations_GitError(t *testing.T) {
	_, err := DiffViolations(context.Background(), realGit, t.TempDir(), "HEAD", coreOnly)
	assert.Eq(t, err != nil, true)
}
