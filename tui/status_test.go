package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestStatus_Format(t *testing.T) {
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Prompt: 5000, Window: 10000, Tokens: 12345, Root: "/x/jas"}
	assert.Eq(t, s.Render(0), "glm · shell · 50% · 12.3k tok · jas")

	s.Running, s.Queued = true, 2
	assert.Eq(t, s.Render(0), "glm · shell · 50% · 12.3k tok · jas · working… · 2 queued")
}

func TestStatus_TokenFormats(t *testing.T) {
	assert.Eq(t, formatTokens(999), "999")
	assert.Eq(t, formatTokens(1000), "1.0k")
	assert.Eq(t, formatTokens(2_500_000), "2.5M")
}

func TestStatus_TruncatesToWidth(t *testing.T) {
	s := statusInfo{Model: "glm", Level: core.ShellOnly, Prompt: 5000, Window: 10000, Tokens: 12345, Root: "/x/jas"}
	got := ansi.Strip(s.Render(20))
	assert.Eq(t, len([]rune(got)) <= 20, true)
	assert.Eq(t, strings.HasSuffix(got, "…"), true)
}

func TestStatus_SanitizesModelAndRoot(t *testing.T) {
	s := statusInfo{Model: "glm\x1b[2J\x07", Level: core.ShellOnly, Root: "/x/j\x1bas"}
	got := s.Render(0)
	assert.NotContains(t, got, "\x1b")
	assert.NotContains(t, got, "\x07")
	assert.Eq(t, got, `glm\x1b[2J\x07 · shell · 0% · 0 tok · j\x1bas`)
}
