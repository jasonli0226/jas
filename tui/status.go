package tui

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

// statusInfo is what the status line under the input box shows.
type statusInfo struct {
	Model   string
	Level   core.Level
	Prompt  int // prompt tokens of the last request
	Window  int // the model's context window
	Tokens  int // session total of prompt and completion tokens
	Root    string
	Running bool
	Queued  int // steering texts waiting for the run
}

// Render returns the plain status line, cut to width with an ellipsis
// (width <= 0 means no limit).
func (s statusInfo) Render(width int) string {
	pct := 0
	if s.Window > 0 {
		pct = s.Prompt * 100 / s.Window
	}
	// The model name and folder come from config and disk: escape any
	// control runes before they reach the terminal.
	line := fmt.Sprintf("%s · %s · %d%% · %s tok · %s",
		core.Sanitize(s.Model), s.Level, pct, formatTokens(s.Tokens), core.Sanitize(filepath.Base(s.Root)))
	if s.Running {
		line += " · working…"
	}
	if s.Queued > 0 {
		line += fmt.Sprintf(" · %d queued", s.Queued)
	}
	if width > 0 {
		line = ansi.Truncate(line, width, "…")
	}
	return line
}

// formatTokens shortens a token count: 999, 12.3k, 2.5M.
func formatTokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}
