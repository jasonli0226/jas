package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

// A run shows the loader line above the input: a spinner frame, what the
// run is doing, and how long it has been running. Ticks advance the frame
// and re-arm; the run ending hides the line again.
func TestChat_LoaderSpinsWhileRunning(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("hi")

	view := c.view()
	assert.Contains(t, view, "⠋ working… 0s")
	assert.Eq(t, loaderAboveInput(c.view()), true)

	c.update(tickMsg{}) // frame 1
	assert.Contains(t, c.view(), "⠙ working… 0s")
	assert.NotContains(t, c.view(), "⠋ working…")
	assert.Eq(t, c.m.tickPend, true) // the tick re-armed the next frame

	close(release)
	c.pump()
	assert.NotContains(t, c.view(), "working…")
	assert.Eq(t, c.m.tickPend, true) // still armed: the next tick disarms it

	c.update(tickMsg{})
	assert.Eq(t, c.m.tickPend, false)
}

// Advancing Options.Now updates the loader's elapsed suffix.
func TestChat_LoaderElapsedAdvances(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Text: "done", OnEmit: func() { <-release }})
	c := newTestChat(t, b)
	c.submit("hi")
	assert.Contains(t, c.view(), "⠋ working… 0s")

	c.now = c.now.Add(3 * time.Second)
	assert.Contains(t, c.view(), "⠋ working… 3s")

	c.now = c.now.Add(62 * time.Second) // 65s total
	assert.Contains(t, c.view(), "⠋ working… 1m 05s")

	close(release)
	c.pump()
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
	assert.Contains(t, c.view(), "⠋ working… 0s")
	c.pumpUntil(func(msg tea.Msg) bool { return c.m.tr.thinking })
	assert.Contains(t, c.view(), "⠋ thinking… 0s")
	close(release)
	c.pump()
}

func TestFormatElapsed(t *testing.T) {
	assert.Eq(t, formatElapsed(0), "0s")
	assert.Eq(t, formatElapsed(500*time.Millisecond), "0s")
	assert.Eq(t, formatElapsed(3*time.Second), "3s")
	assert.Eq(t, formatElapsed(59*time.Second), "59s")
	assert.Eq(t, formatElapsed(60*time.Second), "1m 00s")
	assert.Eq(t, formatElapsed(65*time.Second), "1m 05s")
	assert.Eq(t, formatElapsed(125*time.Second), "2m 05s")
	assert.Eq(t, formatElapsed(-time.Second), "0s")
}

// The loader line sits directly above the input's top rule.
func loaderAboveInput(view string) bool {
	rule := strings.Repeat("─", defaultWidth)
	lines := strings.Split(view, "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "⠋ working…") && i+1 < len(lines) && lines[i+1] == rule {
			return true
		}
	}
	return false
}
