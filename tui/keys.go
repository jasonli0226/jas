package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
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
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// onEnter routes submitted input: the answer to an open approval prompt,
// steering while a run is active, a slash command, or a new run.
func (m *Model) onEnter() tea.Cmd {
	if m.approval != nil {
		m.answerApproval()
		return nil
	}
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.live == nil {
		return nil
	}
	m.input.Reset()
	if m.running() {
		m.live.Session.Steer(text)
		m.queued = append(m.queued, text)
		return nil
	}
	if strings.HasPrefix(text, "/") {
		return m.command(text)
	}
	return m.startRun(text)
}

// command runs a slash command.
func (m *Model) command(text string) tea.Cmd {
	name := strings.TrimPrefix(strings.Fields(text)[0], "/")
	return m.print(errStyle.Render("unknown command /" + core.Sanitize(name)))
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
