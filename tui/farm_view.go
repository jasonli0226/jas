package tui

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

const (
	// redrawLines is how much history a flip redraws when the terminal
	// height is unknown.
	redrawLines = 60
	// liveRegionRows is kept free for the live region when a flip redraws.
	liveRegionRows = 8
)

// Messages from a farm worker's UI (one bridge per agent).
type (
	farmTextMsg struct {
		part, s string
	}
	farmWorkerEventMsg struct {
		part, typ string
		m         map[string]any
	}
)

// farmBridge is a worker's core.UI: it posts the worker's text and events.
type farmBridge struct {
	part string
	send func(tea.Msg)
}

func (b farmBridge) Text(d string)    { b.send(farmTextMsg{b.part, d}) }
func (b farmBridge) Reasoning(string) {}
func (b farmBridge) Event(typ string, data any) {
	if m, ok := data.(map[string]any); ok {
		b.send(farmWorkerEventMsg{b.part, typ, m})
	}
}

// flipTarget maps Ctrl/Alt+1..9 to the agent with that number and
// Ctrl/Alt+0 to the main chat ("").
func flipTarget(key string, agents []*farmAgent) (string, bool) {
	rest, ok := strings.CutPrefix(key, "ctrl+")
	if !ok {
		if rest, ok = strings.CutPrefix(key, "alt+"); !ok {
			return "", false
		}
	}
	n, err := strconv.Atoi(rest)
	if err != nil || len(rest) != 1 {
		return "", false
	}
	if n == 0 {
		return "", true
	}
	for _, a := range agents {
		if a.num == n {
			return a.id, true
		}
	}
	return "", false
}

// isFlipKey reports a Ctrl/Alt+digit key.
func isFlipKey(k string) bool {
	for _, p := range []string{"ctrl+", "alt+"} {
		if rest, ok := strings.CutPrefix(k, p); ok && len(rest) == 1 && rest[0] >= '0' && rest[0] <= '9' {
			return true
		}
	}
	return false
}

// onFlipKey flips on a Ctrl/Alt+digit; false when k is not one.
func (m *Model) onFlipKey(k string) (tea.Cmd, bool) {
	if !isFlipKey(k) {
		return nil, false
	}
	if m.farm == nil {
		m.hint = "no farm running · /farm \"<task>\" starts one"
		return nil, true
	}
	to, ok := flipTarget(k, m.farm.agents)
	if !ok {
		m.hint = "no agent on " + k
		return nil, true
	}
	return m.flip(to), true
}

// flip switches the window to an agent's view (or the main chat with ""):
// clear the screen and redraw that view's recent history.
func (m *Model) flip(to string) tea.Cmd {
	var blocks []string
	if to == "" {
		blocks = m.fit(m.printed)
		m.held = 0
	} else {
		a := m.farm.agent(to)
		head := dimStyle.Render(fmt.Sprintf("── %s · %s · %s ──", m.clean(a.id), m.clean(a.role), m.clean(a.worktree)))
		blocks = append([]string{head}, m.fit(a.printed)...)
		if a.status == agentQueued && len(a.after) > 0 {
			blocks = append(blocks, dimStyle.Render("waiting for "+m.clean(strings.Join(a.after, ", "))))
		}
	}
	m.viewing = to
	return tea.Sequence(tea.ClearScreen, m.emit(blocks...))
}

// fit returns the newest blocks that fit the screen, each line hard-wrapped
// to the current width (blocks rendered before a resize may be wider).
func (m *Model) fit(blocks []string) []string {
	room := redrawLines
	if m.height > 0 {
		room = max(m.height-liveRegionRows, 1)
	}
	var out []string
	for i := len(blocks) - 1; i >= 0 && room > 0; i-- {
		b := ansi.Hardwrap(blocks[i], m.width, true)
		room -= strings.Count(b, "\n") + 1
		out = append([]string{b}, out...)
	}
	return out
}

// farmPrint records blocks in an agent's history and shows them only while
// that agent is viewed.
func (m *Model) farmPrint(a *farmAgent, blocks ...string) tea.Cmd {
	a.printed = append(a.printed, blocks...)
	if m.viewing == a.id {
		return m.emit(blocks...)
	}
	return nil
}

// onFarmText renders a worker's streamed text.
func (m *Model) onFarmText(msg farmTextMsg) tea.Cmd {
	a := m.farmAgent(msg.part)
	if a == nil {
		return nil
	}
	return m.farmPrint(a, a.tr.Text(msg.s, m.o.Now())...)
}

// onFarmWorkerEvent renders a worker's log event and updates its activity.
func (m *Model) onFarmWorkerEvent(msg farmWorkerEventMsg) tea.Cmd {
	a := m.farmAgent(msg.part)
	if a == nil {
		return nil
	}
	now := m.o.Now()
	var blocks []string
	switch msg.typ {
	case core.EvToolStarted:
		tool, _ := msg.m["tool"].(string)
		args, _ := msg.m["args"].(map[string]any)
		a.activity = strings.TrimSpace("→ " + tool + " " + subject(tool, args))
	case core.EvUserMessage:
		blocks = append(a.tr.Flush(now), a.tr.User(contentText(msg.m))...)
	case core.EvTurnEnded, core.EvSessionEnded:
		blocks = a.tr.Flush(now)
	}
	if msg.typ != core.EvUserMessage {
		blocks = append(blocks, a.tr.Event(msg.typ, msg.m, now)...)
	}
	return m.farmPrint(a, blocks...)
}

func (m *Model) farmAgent(id string) *farmAgent {
	if m.farm == nil {
		return nil
	}
	return m.farm.agent(id)
}

// farmStrip is the bottom line of an agent view: every agent with the
// viewed one bold, and the count of main-chat blocks held back.
func (m *Model) farmStrip() string {
	var parts []string
	for _, a := range m.farm.agents {
		label := a.id
		if a.num > 0 {
			label = fmt.Sprintf("%d %s", a.num, a.id)
		}
		label = m.clean(label) + " " + strings.Fields(agentMarks[a.status])[0]
		if a.id == m.viewing {
			label = boldStyle.Render(label)
		}
		parts = append(parts, label)
	}
	line := strings.Join(parts, "  ")
	if m.held > 0 {
		line += dimStyle.Render(fmt.Sprintf(" · main chat: %d new", m.held))
	}
	line += dimStyle.Render(" · ctrl+0 main chat")
	return ansi.Truncate(line, m.width, "…")
}

// farmAttach is /farm attach <part|N>.
func (m *Model) farmAttach(arg string) tea.Cmd {
	if m.farm == nil {
		return m.notice("no farm in this window")
	}
	for _, a := range m.farm.agents {
		if a.id == arg || (a.num > 0 && strconv.Itoa(a.num) == arg) {
			return m.flip(a.id)
		}
	}
	return m.notice("no agent " + arg + " in this farm")
}

func init() {
	farmSubcommands["attach"] = (*Model).farmAttach
	farmSubcommands["back"] = func(m *Model, _ string) tea.Cmd {
		if m.viewing == "" {
			return nil
		}
		return m.flip("")
	}
}
