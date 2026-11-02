package tui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// spinnerFrames are the braille frames the loader cycles through while a
// run is active.
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// spinnerInterval is how long one loader frame stays on screen.
const spinnerInterval = 130 * time.Millisecond

// tickMsg advances the loader one frame.
type tickMsg struct{}

// armTick arms the next loader frame while a run is active, at most one
// timer at a time (tickMsg clears tickPending again).
func (m *Model) armTick() tea.Cmd {
	if m.tickPend || !m.running() {
		return nil
	}
	m.tickPend = true
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// onTick advances the loader frame and re-arms the next one while the run
// is still active; the loader freezes (and hides) once it ends.
func (m *Model) onTick() tea.Cmd {
	m.tickPend = false
	if !m.running() {
		return nil
	}
	m.frame = (m.frame + 1) % len(spinnerFrames)
	return m.armTick()
}

// loader is the dim line above the input while a run is active: a braille
// spinner and what the run is doing. Empty when idle.
func (m *Model) loader() string {
	if !m.running() {
		return ""
	}
	what := "working…"
	if m.tr.thinking {
		what = "thinking…"
	}
	return dimStyle.Render(string(spinnerFrames[m.frame]) + " " + what)
}
