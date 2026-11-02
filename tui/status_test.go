package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestStatus_Format(t *testing.T) {
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Prompt: 5000, Window: 10000, In: 10000, Out: 2345, Root: "/x/jas"}
	assert.Eq(t, ansi.Strip(s.Render(0)), "glm · shell · 50% · ↑10.0k ↓2.3k · jas")

	s.Branch, s.Queued = "main", 2
	assert.Eq(t, ansi.Strip(s.Render(0)), "glm · shell · 50% · ↑10.0k ↓2.3k · jas (main) · 2 queued")
}

func TestStatus_ThinkingShown(t *testing.T) {
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Root: "/x/jas"}
	assert.Eq(t, ansi.Strip(s.Render(0)), "glm · shell · 0% · ↑0 ↓0 · jas")

	s.ShowThinking = true
	assert.Eq(t, ansi.Strip(s.Render(0)), "glm · shell · 0% · ↑0 ↓0 · jas · thinking shown")
}

func TestStatus_CtxThresholdStyle(t *testing.T) {
	// Window 100k with the default reserve compacts above 83,616 tokens:
	// warn from 75% of that, hot from 90%.
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Window: 100_000, Root: "/x/jas"}
	s.Prompt = 10_000
	low := s.Render(0)
	assert.NotContains(t, low, ctxWarnStyle.Render("10%"))
	assert.NotContains(t, low, ctxHotStyle.Render("10%"))
	s.Prompt = 70_000
	assert.Contains(t, s.Render(0), ctxWarnStyle.Render("70%"))
	s.Prompt = 80_000
	assert.Contains(t, s.Render(0), ctxHotStyle.Render("80%"))
}

func TestStatus_TokenFormats(t *testing.T) {
	assert.Eq(t, formatTokens(999), "999")
	assert.Eq(t, formatTokens(1000), "1.0k")
	assert.Eq(t, formatTokens(2_500_000), "2.5M")
}

func TestStatus_TruncatesToWidth(t *testing.T) {
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Prompt: 5000, Window: 10000, In: 12345, Root: "/x/jas", Branch: "main"}
	got := ansi.Strip(s.Render(20))
	assert.Eq(t, len([]rune(got)) <= 20, true)
	assert.Eq(t, strings.HasSuffix(got, "…"), true)
}

func TestStatus_SanitizesModelRootAndBranch(t *testing.T) {
	s := statusInfo{Model: "glm\x1b[2J\x07", Level: core.ShellOnly, Root: "/x/j\x1bas", Branch: "b\x1b"}
	got := s.Render(0)
	assert.NotContains(t, got, "\x1b[2J")
	assert.NotContains(t, got, "\x07")
	assert.Eq(t, ansi.Strip(got), `glm\x1b[2J\x07 · shell · 0% · ↑0 ↓0 · j\x1bas (b\x1b)`)
}

func TestGitBranch(t *testing.T) {
	write := func(path, body string) {
		t.Helper()
		assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755))
		assert.NoErr(t, os.WriteFile(path, []byte(body), 0o644))
	}
	repo := t.TempDir()
	write(filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/feat/x\n")
	assert.Eq(t, gitBranch(repo), "feat/x")

	detached := t.TempDir()
	write(filepath.Join(detached, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")
	assert.Eq(t, gitBranch(detached), "0123456")

	// A linked worktree's .git is a file pointing at its git dir.
	wt, gitdir := t.TempDir(), t.TempDir()
	write(filepath.Join(gitdir, "HEAD"), "ref: refs/heads/wt\n")
	write(filepath.Join(wt, ".git"), "gitdir: "+gitdir+"\n")
	assert.Eq(t, gitBranch(wt), "wt")

	assert.Eq(t, gitBranch(t.TempDir()), "")

	// Relative gitdir (submodules), HEAD outside refs/heads, reftable's
	// placeholder HEAD, and a HEAD that is not a regular file.
	sub := t.TempDir()
	write(filepath.Join(sub, "mods", "a", "HEAD"), "ref: refs/heads/sub\n")
	write(filepath.Join(sub, ".git"), "gitdir: mods/a\r\n")
	assert.Eq(t, gitBranch(sub), "sub")
	odd := t.TempDir()
	write(filepath.Join(odd, ".git", "HEAD"), "ref: refs/remotes/origin/x\n")
	assert.Eq(t, gitBranch(odd), "")
	reftable := t.TempDir()
	write(filepath.Join(reftable, ".git", "HEAD"), "ref: refs/heads/.invalid\n")
	assert.Eq(t, gitBranch(reftable), "")
	notFile := t.TempDir()
	assert.NoErr(t, os.MkdirAll(filepath.Join(notFile, ".git", "HEAD"), 0o755))
	assert.Eq(t, gitBranch(notFile), "")
}
