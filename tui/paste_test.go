package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

func numberedLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line" + strings.Repeat("x", i%3)
	}
	return strings.Join(lines, "\n")
}

func TestChat_SmallPasteStaysInline(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	body := numberedLines(pasteMaxLines)
	c.update(tea.PasteMsg{Content: body})
	assert.Eq(t, c.m.input.Value(), body)
}

func TestChat_LargePasteCollapsesAndExpandsOnSubmit(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	c := newTestChat(t, b)
	body := numberedLines(pasteMaxLines + 1)
	c.typeText("see ")
	c.update(tea.PasteMsg{Content: strings.ReplaceAll(body, "\n", "\r\n")})
	assert.Eq(t, c.m.input.Value(), "see [paste #1 +11 lines]")
	c.typeText(" thanks")
	c.key("enter")
	c.pump()
	sent := requestText(b.prov.Requests[0])
	assert.Contains(t, sent, "see "+body+" thanks")
	assert.NotContains(t, sent, "[paste #1")
}

func TestChat_LongSingleLinePasteCollapses(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(tea.PasteMsg{Content: strings.Repeat("a", pasteMaxChars+1)})
	assert.Eq(t, c.m.input.Value(), "[paste #1 1001 chars]")
	c.update(tea.PasteMsg{Content: strings.Repeat("b", pasteMaxChars)})
	assert.Eq(t, c.m.input.Value(), "[paste #1 1001 chars]"+strings.Repeat("b", pasteMaxChars))
}

func TestChat_PasteMarkerDeletesAsOne(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("x")
	c.update(tea.PasteMsg{Content: numberedLines(20)})
	c.key("backspace")
	assert.Eq(t, c.m.input.Value(), "x")
	c.key("backspace")
	assert.Eq(t, c.m.input.Value(), "")

	c.update(tea.PasteMsg{Content: numberedLines(20)})
	c.typeText("y")
	c.key("home")
	c.key("delete")
	assert.Eq(t, c.m.input.Value(), "y")
}

func TestChat_HistoryKeepsPasteMarker(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"}, fake.Turn{Text: "ok"})
	c := newTestChat(t, b)
	body := numberedLines(30)
	c.update(tea.PasteMsg{Content: body})
	c.key("enter")
	c.pump()
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "[paste #1 +30 lines]")
	c.key("enter")
	c.pump()
	assert.Contains(t, requestText(b.prov.Requests[1]), body)
}
