package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

// A run shows the loader line above the input: a spinner frame and what
// the run is doing. Ticks advance the frame and re-arm; the run ending
// hides the line again.
func TestChat_LoaderSpinsWhileRunning(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("hi")

	view := c.view()
	assert.Contains(t, view, "⠋ working…")
	assert.Eq(t, loaderAboveInput(c.view()), true)

	c.update(tickMsg{}) // frame 1
	assert.Contains(t, c.view(), "⠙ working…")
	assert.NotContains(t, c.view(), "⠋ working…")
	assert.Eq(t, c.m.tickPend, true) // the tick re-armed the next frame

	close(release)
	c.pump()
	assert.NotContains(t, c.view(), "working…")
	assert.Eq(t, c.m.tickPend, true) // still armed: the next tick disarms it

	c.update(tickMsg{})
	assert.Eq(t, c.m.tickPend, false)
}

// Ticks from a run that already ended stop the spinner and do not re-arm.
func TestChat_TickAfterRunStaysIdle(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}))
	c.submit("hi")
	c.pump()
	assert.NotContains(t, c.view(), "working…")

	c.update(tickMsg{})
	assert.NotContains(t, c.view(), "working…")
	assert.Eq(t, c.m.tickPend, false)
}

// While the model reasons, the loader says so instead of working.
func TestChat_LoaderThinking(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Text: "done", Reasoning: "hm", OnEmit: func() { <-release }})
	c := newTestChat(t, b)
	c.submit("hi")
	assert.Contains(t, c.view(), "⠋ working…")
	c.pumpUntil(func(msg tea.Msg) bool { return c.m.tr.thinking })
	assert.Contains(t, c.view(), "⠋ thinking…")
	close(release)
	c.pump()
}

// The loader line sits directly above the input's top rule.
func loaderAboveInput(view string) bool {
	rule := strings.Repeat("─", defaultWidth)
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if l == "⠋ working…" && i+1 < len(lines) && lines[i+1] == rule {
			return true
		}
	}
	return false
}
