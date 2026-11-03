package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Copy the spec fixes.
const (
	quitFarmPrompt = "a farm is running — stop it and quit? y/n"
	mainPrompt     = "› "
)

type (
	// farmUndeliveredMsg hands back owner text a part never read.
	farmUndeliveredMsg struct {
		part  string
		texts []string
	}
	// farmCalledMsg reports a farm control call's error (nil: nothing to say).
	farmCalledMsg struct{ err error }
	// farmQuitMsg resumes a quit once the stopped farm has ended.
	farmQuitMsg struct{}
)

// steerAgent sends text to the viewed agent.
func (m *Model) steerAgent(text string) tea.Cmd {
	a := m.farmAgent(m.viewing)
	if a == nil {
		return nil
	}
	if a.status != agentRunning {
		return m.notice(fmt.Sprintf("%s is %s; it can't be steered (done agents are never re-woken; retry a failed one)", a.id, a.status))
	}
	m.input.Reset()
	a.queued = append(a.queued, text)
	run, id := m.farm.run, a.id
	return func() tea.Msg { return farmCalledMsg{err: run.Steer(id, text)} }
}

// onFarmUndelivered prints the hand-back line and returns the texts to the
// input, after any draft.
func (m *Model) onFarmUndelivered(msg farmUndeliveredMsg) tea.Cmd {
	if a := m.farmAgent(msg.part); a != nil {
		a.queued = nil
	}
	parts := []string{}
	if d := m.input.Value(); d != "" {
		parts = append(parts, d)
	}
	m.input.SetValue(strings.Join(append(parts, msg.texts...), "\n"))
	line := fmt.Sprintf("[%s] not delivered (part finished): %s", m.clean(msg.part), m.clean(strings.Join(msg.texts, " · ")))
	return m.emitNotice(line)
}

// emitNotice prints a notice where the owner is looking: on the screen in
// any view, and in the main chat's history.
func (m *Model) emitNotice(line string) tea.Cmd {
	blocks := m.tr.Notice(dimStyle.Render(line))
	m.printed = append(m.printed, blocks...)
	return m.emit(blocks...)
}

// confirmQuitFarm asks before quitting with a running farm.
func (m *Model) confirmQuitFarm() tea.Cmd {
	m.openPicker(quitFarmPrompt, []pickItem{
		{label: "y", value: "y", desc: "stop the farm (worktrees and branches are kept) and quit"},
		{label: "n", value: "n", desc: "keep going"},
	}, func(v string) tea.Cmd {
		if v != "y" {
			return nil
		}
		m.farmQuitting = true
		m.farm.stopping = true
		run, send := m.farm.run, m.send
		go func() { <-run.Done(); send(farmQuitMsg{}) }()
		return func() tea.Msg { run.Stop(false); return nil }
	})
	return nil
}

// farmStop is /farm stop: graceful first, hard on the second.
func (m *Model) farmStop(string) tea.Cmd {
	f := m.farm
	if !f.running() {
		return m.notice("no farm running")
	}
	hard := f.stopping
	f.stopping = true
	run := f.run
	msg := "farm: stopping (no new parts; running requests finish) · /farm stop again cancels now"
	if hard {
		msg = "farm: cancelling now"
	}
	return tea.Batch(m.notice(msg), func() tea.Msg { run.Stop(hard); return nil })
}

// farmAgentArg resolves a part name or flip number.
func (m *Model) farmAgentArg(arg string) (*farmAgent, error) {
	if m.farm == nil {
		return nil, errors.New("no farm in this window")
	}
	for _, a := range m.farm.agents {
		if a.id == arg || (a.num > 0 && strconv.Itoa(a.num) == arg) {
			return a, nil
		}
	}
	return nil, fmt.Errorf("no agent %s in this farm", arg)
}

// farmControl runs a per-part control (pause, resume, drop) off the loop.
func farmControl(verb string, call func(FarmRun, string) error) func(*Model, string) tea.Cmd {
	return func(m *Model, arg string) tea.Cmd {
		a, err := m.farmAgentArg(arg)
		if err != nil {
			return m.notice(err.Error())
		}
		run, id := m.farm.run, a.id
		return tea.Batch(m.notice(fmt.Sprintf("farm: %s %s", verb, id)),
			func() tea.Msg { return farmCalledMsg{err: call(run, id)} })
	}
}

func (m *Model) onFarmCalled(msg farmCalledMsg) tea.Cmd {
	if msg.err == nil {
		return nil
	}
	return m.notice("farm: " + msg.err.Error())
}

func init() {
	farmSubcommands["stop"] = (*Model).farmStop
	farmSubcommands["pause"] = farmControl("pausing", FarmRun.Pause)
	farmSubcommands["resume"] = farmControl("resuming", FarmRun.Resume)
	farmSubcommands["drop"] = farmControl("dropping", FarmRun.Drop)
}
