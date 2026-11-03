package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

// runDoneMsg reports that a Send returned.
type runDoneMsg struct {
	gen int
	res core.TurnResult
	err error
}

// compactDoneMsg reports that a /compact returned.
type compactDoneMsg struct {
	gen int
	res core.CompactResult
	err error
}

// activeRun is a Send or a /compact in progress on its own goroutine.
type activeRun struct {
	cancel  context.CancelFunc
	done    chan struct{} // closed when the work returns
	compact bool          // a /compact, which prints its own result line
	started time.Time     // when spawn began; drives the loader elapsed
}

func (m *Model) running() bool { return m.run != nil }

// spawn makes work the active run: it runs on a goroutine with a cancellable
// ctx and posts the message it returns.
func (m *Model) spawn(compact bool, work func(context.Context) tea.Msg) {
	ctx, cancel := context.WithCancel(context.Background())
	r := &activeRun{cancel: cancel, done: make(chan struct{}), compact: compact, started: m.o.Now()}
	m.run = r
	m.frame = 0 // the loader starts from the first frame
	send := m.send
	go func() {
		msg := work(ctx)
		close(r.done)
		send(msg)
	}()
}

// startRun prints the shown blocks and sends text on a goroutine, which
// posts runDoneMsg when Send returns.
func (m *Model) startRun(text string, shown []string) tea.Cmd {
	gen, s := m.gen, m.live.Session
	m.spawn(false, func(ctx context.Context) tea.Msg {
		res, err := s.Send(ctx, text)
		return runDoneMsg{gen: gen, res: res, err: err}
	})
	return tea.Batch(m.print(shown...), m.armTick())
}

// startCompact compacts the session on a goroutine, which posts
// compactDoneMsg when Compact returns.
func (m *Model) startCompact(focus string) tea.Cmd {
	gen, s := m.gen, m.live.Session
	m.spawn(true, func(ctx context.Context) tea.Msg {
		res, err := s.Compact(ctx, focus)
		return compactDoneMsg{gen: gen, res: res, err: err}
	})
	return tea.Batch(m.notice("compacting…"), m.armTick())
}

// onEvent tracks usage, steering delivery and compaction, and prints tool
// and compaction rows.
func (m *Model) onEvent(msg eventMsg) tea.Cmd {
	switch msg.typ {
	case EvSuggestFarm:
		return m.suggestFarm(msg.data)
	case core.EvUsage:
		m.usage.prompt = intField(msg.data, "prompt_tokens")
		m.usage.in += m.usage.prompt
		m.usage.out += intField(msg.data, "completion_tokens")
	case core.EvUserMessage:
		if steer, _ := msg.data["steer"].(bool); steer {
			m.queued = nil // the loop delivered every queued text at once
			// Print the delivered steering as a user block so the reply that
			// follows opens a new agent turn instead of continuing the last.
			blocks := m.tr.Flush(m.o.Now())
			return m.print(append(blocks, m.tr.User(contentText(msg.data))...)...)
		}
	case core.EvCompaction:
		// The next request sends only what is left, so ctx% drops now.
		m.usage.prompt = intField(msg.data, "tokens_after")
		if u, ok := msg.data["usage"].(map[string]any); ok {
			m.usage.in += intField(u, "prompt_tokens")
			m.usage.out += intField(u, "completion_tokens")
		}
		if m.run != nil && m.run.compact {
			return nil // /compact prints its own line when it returns
		}
	}
	blocks := m.tr.Event(msg.typ, msg.data, m.o.Now())
	if msg.typ == core.EvToolResult {
		m.noteLastTool()
	}
	return m.print(blocks...)
}

// contentText joins the text blocks of a user_message payload as the
// redacting UI delivers it (decoded JSON).
func contentText(data map[string]any) string {
	blocks, _ := data["content"].([]any)
	var parts []string
	for _, b := range blocks {
		if blk, ok := b.(map[string]any); ok && blk["type"] == "text" {
			if s, _ := blk["text"].(string); s != "" {
				parts = append(parts, s)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// intField reads a number from an event payload: ints straight from core,
// json.Number after the redacting UI (it decodes with UseNumber), float64
// after a plain JSON round trip.
func intField(data map[string]any, key string) int {
	switch v := data[key].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return int(n)
		}
		f, _ := v.Float64()
		return int(f)
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
	if m.approval != nil && m.approval.part == "" { // a farm part's prompt stays open
		m.approval = nil
		m.restoreInput()
	}
	if msg.res.Final != "" {
		m.reply = msg.res.Final
	}
	m.restoreSteering()
	m.branch = gitBranch(m.o.Root) // the run may have switched branches
	blocks := m.tr.Flush(m.o.Now())
	if n := runNotice(msg.res); n != "" {
		blocks = append(blocks, m.tr.Notice(dimStyle.Render(m.o.Redact(n)))...)
	}
	if msg.err != nil {
		blocks = append(blocks, m.tr.Notice(errStyle.Render("event log error: "+m.clean(msg.err.Error())))...)
	}
	var closeCmd tea.Cmd
	if m.quitting {
		closeCmd = m.closeSession() // before print, so flush waits for the close
	}
	return tea.Batch(closeCmd, m.print(blocks...), m.openFarmAsk())
}

// restoreSteering puts steering that never reached the model back in front
// of the input.
func (m *Model) restoreSteering() {
	if left := m.live.Session.TakeSteering(); len(left) > 0 {
		text := strings.Join(left, "\n")
		if cur := m.input.Value(); cur != "" {
			text += "\n" + cur
		}
		m.input.SetValue(text)
	}
}

// onCompactDone ends a /compact: it prints the outcome, puts back steering
// typed meanwhile and lets a pending quit proceed.
func (m *Model) onCompactDone(msg compactDoneMsg) tea.Cmd {
	m.run.cancel()
	m.run = nil
	m.queued = nil
	m.restoreSteering()
	var line string
	switch {
	case msg.err == nil:
		m.usage.prompt = msg.res.TokensAfter
		line = dimStyle.Render(fmt.Sprintf("compacted %d messages · %s → %s tok",
			msg.res.Messages, formatTokens(msg.res.TokensBefore), formatTokens(msg.res.TokensAfter)))
	case errors.Is(msg.err, core.ErrNothingToCompact):
		line = dimStyle.Render("nothing to compact yet")
	case errors.Is(msg.err, context.Canceled): // Esc or quit
		line = dimStyle.Render("interrupted")
	default:
		reason := strings.TrimPrefix(msg.err.Error(), compactFailed)
		line = errStyle.Render(compactFailed + m.clean(reason))
	}
	var closeCmd tea.Cmd
	if m.quitting {
		closeCmd = m.closeSession() // before print, so flush waits for the close
	}
	return tea.Batch(closeCmd, m.print(m.tr.Notice(line)...))
}

// onApproval opens the approval prompt, closing any open picker so Enter
// answers the prompt. A diff prints first, after the model's pending text
// and under a "<tool> <path>" header, in the tool group. One that arrives
// with no run to answer for is refused at once.
func (m *Model) onApproval(msg approvalMsg) tea.Cmd {
	if msg.part != "" {
		return m.farmApproval(msg)
	}
	if m.run == nil {
		msg.reply <- ""
		return nil
	}
	var cmd tea.Cmd
	if msg.diff != nil {
		out := append(m.tr.Settle(m.o.Now()), m.tr.ToolGroup()...)
		out = append(out, cutRow(msg.header, m.width))
		cmd = m.print(append(out, renderDiff(msg.diff, m.width, 0)...)...)
	}
	m.approval = &msg
	m.stashInput()
	return cmd
}

// answerApproval sends the input to the open prompt and closes it. Free text
// is a denial that is also queued as steering, before the reply, so the loop
// has it when the denied result goes back to the model.
func (m *Model) answerApproval() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	m.input.Reset()
	part := m.approval.part
	if part == "" && replySteers(text) { // free text to a farm part is just a denial
		m.live.Session.Steer(text)
		m.queued = append(m.queued, text)
	}
	m.approval.reply <- text // buffered: the gate waits for exactly one answer
	m.approval = nil
	return m.approvalDone(part)
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

// quit cancels an active run and waits for it (onRunDone or onCompactDone
// resumes the quit), then closes the session; the program ends after the
// last print.
func (m *Model) quit() tea.Cmd {
	if m.farm.running() && !m.farmQuitting {
		return m.confirmQuitFarm()
	}
	m.viewing = "" // the last blocks print wherever the owner was
	m.cancelFarmAsks()
	m.quitting = true
	if m.run != nil {
		m.run.cancel()
		return nil
	}
	if m.opening != nil {
		return nil // onSession closes the session once it arrives
	}
	if cmd := m.closeSession(); cmd != nil {
		return cmd
	}
	return m.flush()
}

// sessionCloser closes one session exactly once; a concurrent caller waits
// for the first close to finish and gets its error.
type sessionCloser struct {
	live *Live
	once sync.Once
	err  error
}

func (c *sessionCloser) close(status string, code int) error {
	c.once.Do(func() { c.err = c.live.Close(status, code) })
	return c.err
}

// closeSession closes the live session from a command, off the event loop.
func (m *Model) closeSession() tea.Cmd {
	if m.closer == nil || m.closing || m.closed {
		return nil
	}
	m.closing = true
	c := m.closer
	return func() tea.Msg { return closedMsg{err: c.close("ok", core.ExitOK)} }
}

func (m *Model) onClosed(msg closedMsg) tea.Cmd {
	m.closing, m.closed = false, true
	if msg.err != nil {
		return m.print(errStyle.Render("event log error: " + m.clean(msg.err.Error())))
	}
	return m.print("session " + m.clean(m.live.ID) + " saved · jas --continue to resume")
}

// shutdown runs after the program has exited. It stops a run, waits for a
// session still opening or closing, and closes any session left open (the
// program ended without a clean quit, e.g. ctx cancelled).
func (m *Model) shutdown() error {
	if m.run != nil {
		m.run.cancel()
		<-m.run.done
	}
	// A pending open that never ran leaves the current session to close
	// below. One that ran lost its sessionMsg; its command closed the session
	// it replaced, so the new one is left to close.
	if p := m.opening; p != nil && !p.claimed.CompareAndSwap(false, true) {
		<-p.done
		if p.live != nil {
			m.closer, m.closed = &sessionCloser{live: p.live}, false
		}
	}
	if m.closer == nil || m.closed {
		return nil
	}
	return m.closer.close("interrupted", core.ExitInterrupted)
}
