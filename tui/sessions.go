package tui

import (
	"fmt"
	"io"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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

// openFirst opens the first session, or with Continue resumes the newest
// one, and titles it with Name when set.
func (m *Model) openFirst() tea.Cmd {
	b, gate, o := m.b, m.gate, m.o
	return m.beginOpen(func(ui core.UI) sessionMsg {
		msg := openOrContinue(b, gate, o, ui)
		if msg.err == nil && o.Name != "" {
			if err := msg.live.Session.SetTitle(o.Name); err != nil {
				msg.notices = append(msg.notices, err.Error())
			}
		}
		return msg
	})
}

func openOrContinue(b Backend, gate core.Gate, o Options, ui core.UI) sessionMsg {
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
	return sessionMsg{live: live, notices: append(notices, liveNotices(live)...), err: err}
}

// liveNotices is a new session's opening warnings; none when it failed.
func liveNotices(live *Live) []string {
	if live == nil {
		return nil
	}
	return live.Notices
}

// banner renders the start banner for this chat (see the banner function).
func (m *Model) banner() []string {
	return banner(m.o.Version, core.Sanitize(m.profile), m.b.ContextFiles(), m.b.SkillNames(), m.width)
}

// clearView clears the terminal scrollback view and reprints the start
// banner. The session (context, grants, model) is unchanged. Sequence so
// the clear runs before the banner print.
func (m *Model) clearView() tea.Cmd {
	if m.viewing != "" {
		return m.flip(m.viewing) // Ctrl+L in an agent view redraws that view
	}
	return tea.Sequence(tea.ClearScreen, m.print(m.banner()...))
}

// clearTerminalSeq homes the cursor and erases the visible screen and the
// scrollback, like clear(1). tea.ClearScreen alone erases only the inline
// frame's rows, so the old transcript stayed on screen above it.
const clearTerminalSeq = ansi.CursorHomePosition + ansi.EraseEntireScreen + ansi.EraseEntireDisplay

// clearTerminal writes clearTerminalSeq straight to the terminal. tea.Raw
// would queue it until the next frame, but Println writes at once, so the
// banner printed after it would land first and the clear would erase it.
// Sequence it before the print. A failed write leaves the old screen, which
// the ClearScreen that follows still repaints around; nothing to report.
func (m *Model) clearTerminal() tea.Cmd {
	out := m.out
	if out == nil {
		return nil
	}
	return func() tea.Msg {
		_, _ = io.WriteString(out, clearTerminalSeq)
		return nil
	}
}

// newSession replaces the current session with a fresh one on m.model.
// The screen and the terminal's scrollback clear and the start banner
// prints again: everything it showed may have changed, and the old
// transcript should not stay above it. Ctrl+L (clearView) keeps the
// scrollback. Clear, banner and repaint are sequenced; the open runs
// alongside that Batch item.
//
// The order differs from clearView: tea.ClearScreen buffers its cursor
// move until the next frame while Println writes at once, so a ClearScreen
// before the banner would move the frame up over the banner's last rows.
// After the direct clear the cursor is home, the banner prints from there,
// and ClearScreen then repaints the frame below it.
func (m *Model) newSession() tea.Cmd {
	m.backToMain()
	b, gate, model := m.b, m.gate, m.model
	// print queues into the outbox, so the banner precedes the new
	// session's blocks even though the open runs concurrently.
	return tea.Batch(tea.Sequence(m.clearTerminal(), m.print(m.banner()...), tea.ClearScreen), m.beginOpen(func(ui core.UI) sessionMsg {
		live, err := b.NewSession(model, ui, gate)
		return sessionMsg{live: live, notices: liveNotices(live), err: err}
	}))
}

// resumeSession replaces the current session with the saved one at path.
func (m *Model) resumeSession(path string) tea.Cmd {
	m.backToMain()
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
	m.bangNotes = pendingBangNotes(msg.live.Session.PendingShell()) // the old session's stay in its log
	var known bool
	m.window, known = m.b.Window(msg.live.Model)
	m.branch = gitBranch(m.o.Root)
	if msg.live.Budget != nil && msg.live.Budget.Window > 0 {
		m.window = msg.live.Budget.Window
	}
	m.tipWindow(msg.live.Model, known)
	m.usage = usage{}
	for _, n := range msg.notices {
		blocks = append(blocks, dimStyle.Render(m.clean(n)))
	}
	if msg.resumed {
		m.gate.Reset(m.o.Level) // grants and level changes are never stored in a session
		// Reprint the conversation, then the notice, so scrollback ends at the input.
		msgs := msg.live.Session.Messages()
		m.hist.seed(msgs)
		blocks = append(blocks, m.tr.History(msgs, m.o.Redact, m.o.Now())...)
		m.noteLastTool()
		blocks = append(blocks, m.tr.Notice(dimStyle.Render(fmt.Sprintf("resumed %s · %d messages",
			m.clean(msg.live.ID), len(msgs))))...)
	}
	var closeCmd, hintCmd tea.Cmd
	if m.quitting { // quit began while the session was opening
		closeCmd = m.closeSession() // before print, so flush waits for the close
	} else if !msg.resumed {
		hintCmd = m.tipResume(msg.live.Path)
	}
	return tea.Batch(closeCmd, m.print(blocks...), hintCmd)
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
