package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

// statusInfo is what the status line under the input box shows.
type statusInfo struct {
	Profile string // active profile, "" hides the field
	Model   string
	Level   core.Level
	Prompt  int // prompt tokens of the last request
	Window  int // the model's context window
	In, Out int // session totals of prompt and completion tokens
	Root    string
	Branch  string // git branch of Root, "" outside a repo
	Title   string // session title, "" when unset
	Queued  int    // steering texts waiting for the run

	ShowThinking bool // /thinking is on
}

// Render returns the dim status line with ctx% coloured as the prompt nears
// the compaction threshold, cut to width with an ellipsis (width <= 0 means
// no limit).
func (s statusInfo) Render(width int) string {
	pct := 0
	if s.Window > 0 {
		pct = s.Prompt * 100 / s.Window
	}
	// The model name, folder and branch come from config and disk: escape
	// any control runes before they reach the terminal.
	folder := core.Sanitize(filepath.Base(s.Root))
	if s.Branch != "" {
		folder += " (" + core.Sanitize(s.Branch) + ")"
	}
	head := fmt.Sprintf("%s · %s · ", core.Sanitize(s.Model), s.Level)
	if s.Profile != "" {
		head = core.Sanitize(s.Profile) + " · " + head
	}
	tail := fmt.Sprintf(" · ↑%s ↓%s · %s", formatTokens(s.In), formatTokens(s.Out), folder)
	if s.Title != "" {
		tail += " · " + core.Sanitize(s.Title)
	}
	if s.ShowThinking {
		tail += " · thinking shown"
	}
	if s.Queued > 0 {
		tail += fmt.Sprintf(" · %d queued", s.Queued)
	}
	line := dimStyle.Render(head) + s.ctxStyle().Render(fmt.Sprintf("%d%%", pct)) + dimStyle.Render(tail)
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

// ctxStyle is dim until the prompt reaches 75% of the size at which the
// session compacts (warn), then 90% (hot). The TUI does not see a
// configured compact_reserve_tokens, so it assumes the default.
func (s statusInfo) ctxStyle() lipgloss.Style {
	if s.Window <= 0 {
		return dimStyle
	}
	threshold := max(s.Window-core.DefaultCompactReserve, s.Window/2)
	switch {
	case s.Prompt*10 >= threshold*9:
		return ctxHotStyle
	case s.Prompt*4 >= threshold*3:
		return ctxWarnStyle
	}
	return dimStyle
}

// gitBranch reads the branch checked out at root straight from .git (no
// subprocess): the branch name, a short hash when detached, "" outside a
// repo. A linked worktree's .git is a file naming its git dir. Reftable
// repos keep a placeholder HEAD (refs/heads/.invalid) and show no branch.
func gitBranch(root string) string {
	dir := filepath.Join(root, ".git")
	fi, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	if !fi.IsDir() {
		b, ok := readGitFile(dir)
		rest, found := strings.CutPrefix(strings.TrimSpace(b), "gitdir: ")
		if !ok || !found {
			return ""
		}
		if !filepath.IsAbs(rest) {
			rest = filepath.Join(root, rest)
		}
		dir = rest
	}
	b, ok := readGitFile(filepath.Join(dir, "HEAD"))
	if !ok {
		return ""
	}
	head := strings.TrimSpace(b)
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		if ref == ".invalid" {
			return ""
		}
		return ref
	}
	if len(head) >= 7 && strings.Trim(head, "0123456789abcdef") == "" {
		return head[:7]
	}
	return ""
}

// maxGitFile bounds what gitBranch reads; HEAD and .git files are tiny.
const maxGitFile = 4096

// readGitFile reads a small regular file. The status line re-reads .git
// after every run, so a FIFO or device the agent left there must not block
// or flood the UI.
func readGitFile(path string) (string, bool) {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxGitFile {
		return "", false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(b), true
}

// formatTokens shortens a token count: 999, 12.3k, 2.5M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 999_950: // above this %.1fk would print 1000.0k
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
