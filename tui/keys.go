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

// onKey handles a key press: Enter, Esc, Ctrl-L and Ctrl-C are the chat's
// own; the rest edit the input.
func (m *Model) onKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.quitting {
		return nil
	}
	k := msg.String()
	// Ctrl+L clears the view and reprints the banner; keep the session.
	// Handled before the picker so it works while a picker is open (pickers
	// do not use Ctrl+L).
	if k == "ctrl+l" {
		m.hint = ""
		return m.clearView()
	}
	if m.picker != nil && m.approval == nil { // an approval prompt comes first
		return m.onPickerKey(msg)
	}
	if k != "ctrl+c" {
		m.hint = ""
	}
	if menu := m.slashMenu(); menu != nil && m.onSlashKey(menu, msg) {
		return nil
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
	case "up", "down":
		if m.onHistoryKey(k) {
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
	m.hist.add(text)
	if strings.HasPrefix(text, "/") {
		return m.command(text)
	}
	m.input.Reset()
	if m.running() {
		m.live.Session.Steer(text)
		m.queued = append(m.queued, text)
		return nil
	}
	return m.startRun(text, m.tr.User(text))
}

// onHistoryKey recalls submitted input: Up from the first line of the
// input, Down from the last, so the arrows still move within a multi-line
// draft. A recalled / command keeps the / menu shut.
func (m *Model) onHistoryKey(k string) bool {
	if m.approval != nil {
		return false
	}
	var v string
	var ok bool
	switch {
	case k == "up" && m.input.Line() == 0:
		v, ok = m.hist.prev(m.input.Value())
	case k == "down" && m.input.Line() == m.input.LineCount()-1:
		v, ok = m.hist.next()
	}
	if ok {
		m.input.SetValue(v)
		m.slashHidden = v
	}
	return ok
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
