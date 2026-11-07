package tui

import (
	"fmt"
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
	if m.tickPend || !m.animating() {
		return nil
	}
	m.tickPend = true
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

// onTick advances the loader frame and re-arms the next one while the run
// is still active; the loader freezes (and hides) once it ends.
func (m *Model) onTick() tea.Cmd {
	m.tickPend = false
	if !m.animating() {
		return nil
	}
	m.frame = (m.frame + 1) % len(spinnerFrames)
	return m.armTick()
}

// animating reports whether the loader spins: a run, or the farm planning
// or starting.
func (m *Model) animating() bool { return m.running() || m.farmBusy != "" }

// setFarmBusy shows what the farm is doing in the loader until its result
// arrives, and starts the spinner.
func (m *Model) setFarmBusy(what string) tea.Cmd {
	m.farmBusy, m.farmSince = what, m.o.Now()
	return m.armTick()
}

// loader is the dim line above the input while a run is active: a braille
// spinner, what the run is doing, and how long it has been running. Empty
// when idle.
func (m *Model) loader() string {
	if !m.running() {
		if m.farmBusy == "" {
			return ""
		}
		elapsed := formatElapsed(m.o.Now().Sub(m.farmSince))
		return dimStyle.Render(fmt.Sprintf("%c farm: %s… %s", spinnerFrames[m.frame], m.farmBusy, elapsed))
	}
	what := "working…"
	if m.tr.thinking {
		what = "thinking…"
	}
	elapsed := formatElapsed(m.o.Now().Sub(m.run.started))
	return dimStyle.Render(fmt.Sprintf("%c %s %s", spinnerFrames[m.frame], what, elapsed))
}

// formatElapsed shortens a run duration for the loader: "3s", "1m 05s".
// Sub-second values round down to 0s; negative durations clamp to 0.
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	secs := int(d / time.Second)
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm %02ds", secs/60, secs%60)
}
