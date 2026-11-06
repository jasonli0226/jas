package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

const farmNeedsYou = "farm needs you · ctrl+0"

// farmAskMsg is a farm question for the owner (a failure's retry/drop/abort,
// the final merge/keep/discard, or a typed confirm when choices is nil).
// The answer goes to reply, or to then for a question the chat itself asks.
type farmAskMsg struct {
	prompt  string
	choices []string
	reply   chan<- string // buffered; exactly one answer
	then    func(ans string) tea.Cmd
	onOpen  func() tea.Cmd // runs as the question takes the input
}

// cancel ends the question with no answer.
func (q farmAskMsg) cancel() {
	if q.reply != nil {
		close(q.reply)
	}
}

// inputStash is the input box (and any picker) set aside while a prompt
// takes the input.
type inputStash struct {
	draft    string
	picker   *picker
	onPick   func(string) tea.Cmd
	onCancel func() tea.Cmd
}

// stashInput sets the input box and any open picker aside for a prompt.
// Prompts nest (a question, then an approval): only the first one stashes,
// so the owner's draft is never replaced by an empty box.
func (m *Model) stashInput() {
	if m.stash == nil {
		m.stash = &inputStash{draft: m.input.Value(), picker: m.picker, onPick: m.onPick, onCancel: m.onPickCancel}
	}
	m.input.Reset()
	m.picker, m.onPick, m.onPickCancel = nil, nil, nil
}

// restoreInput brings back what stashInput set aside, once no prompt holds
// the input any more.
func (m *Model) restoreInput() {
	s := m.stash
	if s == nil || m.approval != nil || m.ask != nil {
		return
	}
	m.stash = nil
	m.input.SetValue(s.draft)
	m.picker, m.onPick, m.onPickCancel = s.picker, s.onPick, s.onCancel
}

// draft is the owner's own text: the input box, or what a prompt set aside.
func (m *Model) draft() string {
	if m.stash != nil {
		return m.stash.draft
	}
	return m.input.Value()
}

// setDraft replaces the owner's own text wherever it currently lives, so a
// prompt holding the input never swallows it.
func (m *Model) setDraft(s string) {
	if m.stash != nil {
		m.stash.draft = s
		return
	}
	m.input.SetValue(s)
}

// ring sounds the terminal bell once when the farm needs the owner.
func (m *Model) ring() tea.Cmd {
	if !m.o.FarmBell {
		return nil
	}
	m.bells++
	return tea.Raw("\a")
}

// farmAsker is the Ask hook: it posts the question and waits for the answer.
func farmAsker(send func(tea.Msg)) func(string, []string) (string, bool) {
	return func(prompt string, choices []string) (string, bool) {
		reply := make(chan string, 1)
		send(farmAskMsg{prompt: prompt, choices: choices, reply: reply})
		ans, ok := <-reply
		return ans, ok
	}
}

// onFarmAsk opens a farm question now if the owner is in the main chat with
// no turn running and no prompt open, else keeps it pending.
func (m *Model) onFarmAsk(msg farmAskMsg) tea.Cmd {
	m.askPending = append(m.askPending, msg)
	return m.openFarmAsk()
}

// canAsk reports whether a farm question may take the input now.
func (m *Model) canAsk() bool {
	return m.ask == nil && m.approval == nil && m.viewing == "" && !m.running() && len(m.askPending) > 0
}

// openFarmAsk opens the oldest pending farm question when it can.
func (m *Model) openFarmAsk() tea.Cmd {
	if !m.canAsk() {
		return nil
	}
	q := m.askPending[0]
	m.askPending = m.askPending[1:]
	m.ask = &q
	m.stashInput()
	if q.onOpen != nil {
		return tea.Batch(q.onOpen(), m.ring())
	}
	return m.ring()
}

// answerFarmAsk answers the open farm question from the input: a choice by
// whole word or prefix, or anything for a typed confirm; an unknown answer
// is asked again.
func (m *Model) answerFarmAsk() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	q := m.ask
	ans := text
	if len(q.choices) > 0 && text != "" {
		c, ok := matchChoice(strings.ToLower(text), q.choices)
		if !ok {
			m.input.Reset()
			m.hint = "answer one of: " + strings.Join(q.choices, ", ")
			return nil
		}
		ans = c
	}
	m.input.Reset()
	m.ask = nil
	if q.then != nil {
		m.restoreInput()
		return tea.Batch(q.then(ans), m.openFarmAsk())
	}
	q.reply <- ans
	m.restoreInput()
	return m.openFarmAsk()
}

// matchChoice returns the one choice ans names (whole word or prefix, either
// way round), if exactly one does.
func matchChoice(ans string, choices []string) (string, bool) {
	var found []string
	for _, c := range choices {
		if ans == c {
			return c, true
		}
		if strings.HasPrefix(c, ans) || strings.HasPrefix(ans, c) {
			found = append(found, c)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}

// cancelFarmAsks answers every farm question with no answer (on quit).
func (m *Model) cancelFarmAsks() {
	if m.ask != nil {
		m.ask.cancel()
		m.ask = nil
	}
	for _, q := range m.askPending {
		q.cancel()
	}
	m.askPending = nil
}

// farmApproval opens a farm part's approval prompt in any view.
func (m *Model) farmApproval(msg approvalMsg) tea.Cmd {
	var cmds []tea.Cmd
	if msg.diff != nil {
		out := []string{cutRow(msg.header, m.width)}
		out = append(out, renderDiff(msg.diff, m.width, 0)...)
		cmds = append(cmds, m.emit(out...)) // shown where the owner is, not recorded
	}
	if a := m.farmAgent(msg.part); a != nil {
		a.needsApproval = true
	}
	m.stashInput()
	m.approval = &msg
	return tea.Batch(append(cmds, m.ring())...)
}

// approvalDone clears an answered approval's marks and gives the input back.
func (m *Model) approvalDone(part string) tea.Cmd {
	if a := m.farmAgent(part); a != nil {
		a.needsApproval = false
	}
	m.restoreInput()
	return m.openFarmAsk()
}

// askStrip is the notice that a farm question waits, for the strip and the
// totals line.
func (m *Model) askStrip() string {
	if len(m.askPending) == 0 {
		return ""
	}
	return farmNeedsYou
}

// askPrompt renders the open farm question.
func (m *Model) askPrompt() string {
	if m.ask == nil {
		return ""
	}
	p := m.ask.prompt
	if len(m.ask.choices) > 0 {
		p = strings.TrimRight(p, " ") + " [" + strings.Join(m.ask.choices, "/") + "]"
	}
	return boldStyle.Render(ansi.Wrap(core.Sanitize(m.o.Redact(p)), m.width, ""))
}

// closeFarmApproval answers the open farm approval with text ("" denies) and
// gives the input back. Used for Esc and when the asking part has ended (its
// gate already returned; the buffered reply is never read).
func (m *Model) closeFarmApproval(text string) tea.Cmd {
	part := m.approval.part
	m.approval.reply <- text
	m.approval = nil
	return m.approvalDone(part)
}

// dropFarmPrompts closes prompts that belong to a part that ended (part !=
// "") or to a farm that ended (part == ""): approvals, and with a farm end
// every farm question too.
func (m *Model) dropFarmPrompts(part string) tea.Cmd {
	var cmd tea.Cmd
	if a := m.approval; a != nil && a.part != "" && (part == "" || a.part == part) {
		cmd = m.closeFarmApproval("")
	}
	if part == "" {
		if m.ask != nil { // the input holds an answer in progress: drop it
			m.ask.cancel()
			m.ask = nil
			m.input.Reset()
			m.restoreInput()
		}
		for _, q := range m.askPending {
			q.cancel()
		}
		m.askPending = nil
	}
	return cmd
}
