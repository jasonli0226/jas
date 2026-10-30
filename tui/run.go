package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

// runDoneMsg reports that a Send returned.
type runDoneMsg struct {
	gen int
	res core.TurnResult
	err error
}

// activeRun is a Send in progress on its own goroutine.
type activeRun struct {
	cancel context.CancelFunc
	done   chan struct{} // closed when Send returns
}

func (m *Model) running() bool { return m.run != nil }

// startRun prints the user block and sends text on a goroutine, which posts
// runDoneMsg when Send returns.
func (m *Model) startRun(text string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	r := &activeRun{cancel: cancel, done: make(chan struct{})}
	m.run = r
	gen, s, send := m.gen, m.live.Session, m.send
	go func() {
		res, err := s.Send(ctx, text)
		close(r.done)
		send(runDoneMsg{gen: gen, res: res, err: err})
	}()
	return m.print(m.tr.User(text)...)
}

// onEvent tracks usage and steering delivery, and prints tool rows.
func (m *Model) onEvent(msg eventMsg) tea.Cmd {
	switch msg.typ {
	case core.EvUsage:
		prompt := intField(msg.data, "prompt_tokens")
		m.usage.prompt = prompt
		m.usage.total += prompt + intField(msg.data, "completion_tokens")
	case core.EvUserMessage:
		if steer, _ := msg.data["steer"].(bool); steer {
			m.queued = nil // the loop delivered every queued text at once
		}
	}
	return m.print(m.tr.Event(msg.typ, msg.data, m.o.Now())...)
}

// intField reads a number from an event payload, which holds ints when it
// comes straight from core and float64s after a JSON round trip.
func intField(data map[string]any, key string) int {
	switch v := data[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	}
	return 0
}

// onRunDone ends a run: steering that never reached the model goes back in
// front of the input, the partial block and any notice are printed, and a
// pending quit proceeds.
func (m *Model) onRunDone(msg runDoneMsg) tea.Cmd {
	m.run.cancel()
	m.run = nil
	m.queued = nil
	if left := m.live.Session.TakeSteering(); len(left) > 0 {
		text := strings.Join(left, "\n")
		if cur := m.input.Value(); cur != "" {
			text += "\n" + cur
		}
		m.input.SetValue(text)
	}
	blocks := m.tr.Flush(m.o.Now())
	if n := runNotice(msg.res); n != "" {
		blocks = append(blocks, dimStyle.Render(n))
	}
	if msg.err != nil {
		blocks = append(blocks, errStyle.Render("event log error: "+core.Sanitize(msg.err.Error())))
	}
	var closeCmd tea.Cmd
	if m.quitting {
		closeCmd = m.closeSession() // before print, so flush waits for the close
	}
	return tea.Batch(closeCmd, m.print(blocks...))
}

// runNotice is the line printed after a run that did not end ok.
func runNotice(res core.TurnResult) string {
	reason := core.Sanitize(res.Reason)
	switch res.Status {
	case "ok":
		return ""
	case "interrupted":
		return "interrupted"
	case "turn_cap":
		return "turn cap reached: " + reason
	case "context_full":
		return "context window full: start a /new chat"
	case "failed":
		return "failed: " + reason
	}
	return core.Sanitize(res.Status) + ": " + reason
}

// quit cancels an active run and waits for it (onRunDone resumes the quit),
// then closes the session; the program ends after the last print.
func (m *Model) quit() tea.Cmd {
	m.quitting = true
	if m.run != nil {
		m.run.cancel()
		return nil
	}
	if cmd := m.closeSession(); cmd != nil {
		return cmd
	}
	return m.flush()
}

// closeSession closes the live session from a command, off the event loop.
func (m *Model) closeSession() tea.Cmd {
	if m.live == nil || m.closing {
		return nil
	}
	m.closing = true
	live := m.live
	return func() tea.Msg { return closedMsg{err: live.Close("ok", core.ExitOK)} }
}

func (m *Model) onClosed(msg closedMsg) tea.Cmd {
	m.closing, m.closed = false, true
	if msg.err != nil {
		return m.print(errStyle.Render("event log error: " + core.Sanitize(msg.err.Error())))
	}
	return m.print("session " + m.live.ID + " saved · jas --continue to resume")
}

// shutdown runs after the program has exited. If it ended without a clean
// quit (ctx cancelled), it stops the run and closes the session.
func (m *Model) shutdown() error {
	if m.run != nil {
		m.run.cancel()
		<-m.run.done
	}
	if m.live == nil || m.closing || m.closed {
		return nil
	}
	return m.live.Close("interrupted", core.ExitInterrupted)
}
