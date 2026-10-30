package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const (
	quitWindow = time.Second // two Ctrl-C presses closer than this quit
	quitHint   = "press Ctrl-C again to quit"
)

// onKey handles a key press: Enter, Esc and Ctrl-C are the chat's own, the
// rest edit the input.
func (m *Model) onKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.quitting {
		return nil
	}
	if m.picker != nil && m.approval == nil { // an approval prompt comes first
		return m.onPickerKey(msg)
	}
	k := msg.String()
	if k != "ctrl+c" {
		m.hint = ""
	}
	switch k {
	case "enter":
		return m.onEnter()
	case "esc":
		m.approval = nil // the cancelled ctx unblocks the gate
		if m.run != nil {
			m.run.cancel()
		}
		return nil
	case "ctrl+c":
		return m.onCtrlC()
	case "tab":
		if list := m.completions(); len(list) > 0 {
			m.input.SetValue("/" + list[0] + " ")
			return nil
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// onEnter routes submitted input: the answer to an open approval prompt, a
// slash command, steering while a run is active, or a new run. Nothing is
// submitted while a session is opening.
func (m *Model) onEnter() tea.Cmd {
	if m.approval != nil {
		m.answerApproval()
		return nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.live == nil || m.opening != nil {
		return nil
	}
	if strings.HasPrefix(text, "/") {
		return m.command(text)
	}
	m.input.Reset()
	if m.running() {
		m.live.Session.Steer(text)
		m.queued = append(m.queued, text)
		return nil
	}
	return m.startRun(text, text)
}

// onCtrlC clears non-empty input without arming the quit; on an empty box a
// second press within quitWindow quits and a slower one shows a hint.
func (m *Model) onCtrlC() tea.Cmd {
	if m.input.Value() != "" {
		m.input.Reset()
		m.lastCtrlC = time.Time{} // clearing is not the first quit press
		return nil
	}
	now, prev := m.o.Now(), m.lastCtrlC
	m.lastCtrlC = now
	if !prev.IsZero() && now.Sub(prev) < quitWindow {
		m.hint = ""
		return m.quit()
	}
	m.hint = quitHint
	return nil
}
