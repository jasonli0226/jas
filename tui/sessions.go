package tui

import (
	"fmt"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

// sessionMsg reports a finished open: the first session, /new or /resume.
type sessionMsg struct {
	gen      int
	live     *Live
	notices  []string
	resumed  bool  // from /resume or --continue: grants and level reset, summary printed
	closeErr error // closing the session this one replaced
	err      error
}

// pendingOpen is a session being opened by a command. Whoever claims it
// first owns it: the command, or shutdown when the command never ran.
type pendingOpen struct {
	gen     int
	claimed atomic.Bool
	prev    *sessionCloser // the session being replaced; closed once the new one opens
	done    chan struct{}  // closed once the command has finished
	live    *Live          // the opened session; set before done closes
}

// openFirst opens the first session, or with Continue resumes the newest one.
func (m *Model) openFirst() tea.Cmd {
	b, gate, o := m.b, m.gate, m.o
	return m.beginOpen(func(ui core.UI) sessionMsg {
		var notices []string
		if o.Continue {
			infos, err := b.Sessions("")
			if err != nil {
				return sessionMsg{err: err}
			}
			if len(infos) > 0 {
				live, notices, err := b.Resume(infos[0].Path, ui, gate)
				return sessionMsg{live: live, notices: notices, resumed: true, err: err}
			}
			notices = append(notices, "no saved session for this project; starting a new one")
		}
		live, err := b.NewSession(o.Model, ui, gate)
		return sessionMsg{live: live, notices: notices, err: err}
	})
}

// newSession replaces the current session with a fresh one on m.model.
func (m *Model) newSession() tea.Cmd {
	b, gate, model := m.b, m.gate, m.model
	return m.beginOpen(func(ui core.UI) sessionMsg {
		live, err := b.NewSession(model, ui, gate)
		return sessionMsg{live: live, err: err}
	})
}

// resumeSession replaces the current session with the saved one at path.
func (m *Model) resumeSession(path string) tea.Cmd {
	b, gate := m.b, m.gate
	return m.beginOpen(func(ui core.UI) sessionMsg {
		live, notices, err := b.Resume(path, ui, gate)
		return sessionMsg{live: live, notices: notices, resumed: true, err: err}
	})
}

// beginOpen runs open on a command under a new generation. Once it succeeds
// the current session, if any, is closed; on failure that session stays.
// Callers make sure no run is active, so no output of the old session is
// still on its way when the generation changes.
func (m *Model) beginOpen(open func(ui core.UI) sessionMsg) tea.Cmd {
	m.lastGen++
	gen := m.lastGen
	ui := &bridge{gen: gen, send: m.send}
	p := &pendingOpen{gen: gen, prev: m.closer, done: make(chan struct{})}
	m.opening = p
	return func() tea.Msg {
		if !p.claimed.CompareAndSwap(false, true) {
			return nil // shutdown got here first
		}
		defer close(p.done)
		msg := open(ui)
		msg.gen = gen
		if msg.err == nil {
			p.live = msg.live
			if p.prev != nil {
				msg.closeErr = p.prev.close("ok", core.ExitOK)
			}
		}
		return msg
	}
}

func (m *Model) onSession(msg sessionMsg) tea.Cmd {
	if p := m.opening; p == nil || msg.gen != p.gen {
		return m.closeStale(msg.live)
	}
	m.opening = nil
	if msg.err != nil {
		return m.onOpenFailed(msg.err)
	}
	var blocks []string
	if msg.closeErr != nil {
		blocks = append(blocks, errStyle.Render("event log error: "+m.clean(msg.closeErr.Error())))
	}
	m.live, m.closer, m.gen, m.model = msg.live, &sessionCloser{live: msg.live}, msg.gen, msg.live.Model
	m.window = m.b.Window(msg.live.Model)
	if msg.live.Budget != nil && msg.live.Budget.Window > 0 {
		m.window = msg.live.Budget.Window
	}
	m.usage = usage{}
	for _, n := range msg.notices {
		blocks = append(blocks, dimStyle.Render(m.clean(n)))
	}
	if msg.resumed {
		m.gate.Reset(m.o.Level) // grants and level changes are never stored in a session
		blocks = append(blocks, dimStyle.Render(fmt.Sprintf("resumed %s · %d messages",
			m.clean(msg.live.ID), len(msg.live.Session.Messages()))))
	}
	var closeCmd tea.Cmd
	if m.quitting { // quit began while the session was opening
		closeCmd = m.closeSession() // before print, so flush waits for the close
	}
	return tea.Batch(closeCmd, m.print(blocks...))
}

// onOpenFailed reports a failed open. Without a session to fall back to the
// chat ends; otherwise the current session stays.
func (m *Model) onOpenFailed(err error) tea.Cmd {
	block := errStyle.Render("open session: " + m.clean(err.Error()))
	if m.live == nil {
		m.err = err
		m.quitting = true
		return m.print(block)
	}
	var closeCmd tea.Cmd
	if m.quitting {
		closeCmd = m.closeSession()
	}
	return tea.Batch(closeCmd, m.print(block))
}

// closeStale closes a session whose open was superseded by a later one.
func (m *Model) closeStale(live *Live) tea.Cmd {
	if live == nil {
		return nil
	}
	return func() tea.Msg {
		if err := live.Close("ok", core.ExitOK); err != nil {
			return noticeMsg{"event log error: " + m.clean(err.Error())}
		}
		return nil
	}
}
