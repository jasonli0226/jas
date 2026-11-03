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
type farmAskMsg struct {
	prompt  string
	choices []string
	reply   chan<- string // buffered; exactly one answer
}

// inputStash is the input box (and any picker) set aside while a prompt
// takes the input.
type inputStash struct {
	draft    string
	picker   *picker
	onPick   func(string) tea.Cmd
	onCancel func() tea.Cmd
}

// stashInput sets the input box and any open picker aside.
func (m *Model) stashInput() {
	m.stash = &inputStash{draft: m.input.Value(), picker: m.picker, onPick: m.onPick, onCancel: m.onPickCancel}
	m.input.Reset()
	m.picker, m.onPick, m.onPickCancel = nil, nil, nil
}

// restoreInput brings back what stashInput set aside.
func (m *Model) restoreInput() {
	s := m.stash
	if s == nil {
		return
	}
	m.stash = nil
	m.input.SetValue(s.draft)
	m.picker, m.onPick, m.onPickCancel = s.picker, s.onPick, s.onCancel
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
		close(m.ask.reply)
		m.ask = nil
	}
	for _, q := range m.askPending {
		close(q.reply)
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
