package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/farm"
)

// Plan review choices.
const (
	reviewApprove = "approve"
	reviewEdit    = "edit"
	reviewReplan  = "replan"
	reviewQuit    = "quit"
)

// cmdFarm is /farm: no argument shows the dashboard, `run <plan.yaml>` starts
// an approved plan, a known subcommand acts on the running farm, anything
// else is a task to plan.
func (m *Model) cmdFarm(arg string) tea.Cmd {
	sub, rest, _ := strings.Cut(arg, " ")
	rest = strings.TrimSpace(rest)
	switch sub {
	case "":
		return m.farmDashboard()
	case "run":
		return m.farmRunPlan(rest)
	}
	if fn, ok := farmSubcommands[sub]; ok {
		return fn(m, rest)
	}
	return m.farmPlan(unquote(arg))
}

// farmSubcommands act on the running farm.
var farmSubcommands = map[string]func(*Model, string) tea.Cmd{}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// farmer returns the farm port when a new farm may start here, else a notice.
func (m *Model) farmer() (Farmer, tea.Cmd) {
	f := m.b.Farmer()
	if f == nil {
		return nil, m.notice("farms are not available in this chat")
	}
	if ok, why := f.Available(); !ok {
		return nil, m.notice("farm: " + why)
	}
	if m.farm.running() {
		return nil, m.notice("a farm is already running in this window (one at a time); /farm shows it")
	}
	if m.farmReview != nil || m.farmReplan != nil {
		return nil, m.notice("a farm plan is already under review")
	}
	return f, nil
}

// farmPlan asks the planner for a plan for task.
func (m *Model) farmPlan(task string) tea.Cmd {
	if task == "" {
		return m.notice(`usage: /farm "<task>" · /farm run <plan.yaml> · /farm`)
	}
	f, refuse := m.farmer()
	if f == nil {
		return refuse
	}
	model := m.model
	m.farmReview = &FarmPlan{Task: task} // holds the slot while the planner works
	return tea.Batch(m.notice("farm: planning "+task+" …"), func() tea.Msg {
		p, err := f.Plan(context.Background(), task, model)
		return farmPlannedMsg{plan: p, err: err}
	})
}

// onFarmPlanned shows a drafted plan and opens the review picker.
func (m *Model) onFarmPlanned(msg farmPlannedMsg) tea.Cmd {
	m.farmReview = nil
	if msg.err != nil {
		return m.print(errStyle.Render("farm: " + m.clean(msg.err.Error())))
	}
	p := msg.plan
	if p.Refusal != "" {
		m.input.SetValue(p.Task)
		return m.notice("farm: planner refused: "+p.Refusal, "try: "+p.Suggest, "the task is back in the input to run here")
	}
	lines := append([]string{}, p.Summary...)
	for _, pr := range p.Problems {
		lines = append(lines, "problem: "+pr)
	}
	m.farmReview = &p
	m.openFarmReview()
	return m.print(m.tr.Notice(strings.Join(lines, "\n"))...)
}

func (m *Model) openFarmReview() {
	m.openPicker("farm plan", []pickItem{
		{label: reviewApprove, value: reviewApprove, desc: "start the farm"},
		{label: reviewEdit, value: reviewEdit, desc: "open the plan in $EDITOR"},
		{label: reviewReplan, value: reviewReplan, desc: "send feedback to the planner"},
		{label: reviewQuit, value: reviewQuit, desc: "start nothing"},
	}, m.onFarmReview)
	m.onPickCancel = func() tea.Cmd { return m.onFarmReview(reviewQuit) }
}

// onFarmReview acts on the owner's review choice.
func (m *Model) onFarmReview(choice string) tea.Cmd {
	m.onPickCancel = nil
	p := m.farmReview
	if p == nil {
		return nil
	}
	f := m.b.Farmer()
	switch choice {
	case reviewApprove:
		if len(p.Problems) > 0 {
			m.openFarmReview()
			return m.notice("cannot approve: the plan has problems; edit or replan")
		}
		m.farmReview = nil
		return m.farmApprove(f, *p)
	case reviewEdit:
		return m.farmEdit(*p)
	case reviewReplan:
		m.farmReview, m.farmReplan = nil, p
		return m.notice("replan: type feedback for the planner and press Enter (empty reopens the plan)")
	}
	m.farmReview = nil
	plan := *p
	return tea.Batch(m.notice("farm: nothing started"), func() tea.Msg { f.Discard(plan); return nil })
}

// farmApprove approves p and starts it on the chat's model and level.
func (m *Model) farmApprove(f Farmer, p FarmPlan) tea.Cmd {
	model, level, hooks := m.model, m.gate.Level(), m.farmHooks()
	return func() tea.Msg {
		path, err := f.Approve(p)
		if err != nil {
			return farmStartedMsg{err: err}
		}
		plan, _ := farm.ParsePlan(p.Raw)
		run, err := f.Start(context.Background(), path, model, level, hooks)
		return farmStartedMsg{run: run, plan: plan, task: p.Task, err: err}
	}
}

// farmRunPlan starts an approved plan file.
func (m *Model) farmRunPlan(path string) tea.Cmd {
	if path == "" {
		return m.notice("usage: /farm run <plan.yaml>")
	}
	f, refuse := m.farmer()
	if f == nil {
		return refuse
	}
	model, level, hooks := m.model, m.gate.Level(), m.farmHooks()
	return func() tea.Msg {
		raw, err := os.ReadFile(path)
		if err != nil {
			return farmStartedMsg{err: err}
		}
		plan, err := farm.ParsePlan(raw)
		if err != nil {
			return farmStartedMsg{err: err}
		}
		run, err := f.Start(context.Background(), path, model, level, hooks)
		return farmStartedMsg{run: run, plan: plan, task: plan.Task, err: err}
	}
}

// farmEdit opens the plan in $EDITOR, then re-checks it.
func (m *Model) farmEdit(p FarmPlan) tea.Cmd {
	tmp, err := os.CreateTemp("", "jas-plan-*.yaml")
	if err == nil {
		_, err = tmp.Write(p.Raw)
		if cerr := tmp.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		m.openFarmReview()
		return m.notice("edit: " + err.Error())
	}
	ed := strings.Fields(os.Getenv("EDITOR"))
	if len(ed) == 0 {
		ed = []string{"vi"}
	}
	cmd := exec.Command(ed[0], append(ed[1:], tmp.Name())...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		raw, rerr := os.ReadFile(tmp.Name())
		_ = os.Remove(tmp.Name())
		if err == nil {
			err = rerr
		}
		return farmEditedMsg{plan: p, raw: raw, err: err}
	})
}

func (m *Model) onFarmEdited(msg farmEditedMsg) tea.Cmd {
	if msg.err != nil {
		m.farmReview = &msg.plan
		m.openFarmReview()
		return m.notice("editor: " + msg.err.Error())
	}
	f, p, raw := m.b.Farmer(), msg.plan, msg.raw
	return func() tea.Msg { return farmPlannedMsg{plan: f.Recheck(p, raw)} }
}

// farmFeedback sends replan feedback typed into the input.
func (m *Model) farmFeedback(text string) tea.Cmd {
	p := *m.farmReplan
	m.farmReplan = nil
	if text == "" {
		m.farmReview = &p
		m.openFarmReview()
		return nil
	}
	m.farmReview = &FarmPlan{Task: p.Task} // holds the slot while the planner works
	f := m.b.Farmer()
	return tea.Batch(m.notice("farm: replanning …"), func() tea.Msg {
		next, err := f.Replan(context.Background(), p, text)
		return farmPlannedMsg{plan: next, err: err}
	})
}

// onFarmStarted shows the rows and waits for the farm's end.
func (m *Model) onFarmStarted(msg farmStartedMsg) tea.Cmd {
	if msg.err != nil {
		return m.print(errStyle.Render("farm: " + m.clean(msg.err.Error())))
	}
	m.farm = newFarmState(msg.run, msg.plan, msg.task, m.o.Now())
	run, send := msg.run, m.send
	go func() { send(farmDoneMsg{res: run.Wait()}) }() // a farm outlives any one command
	return m.notice(fmt.Sprintf("farm %s started · %s · /farm for the dashboard", run.FarmID(), farmKeysHint))
}

// farmHooks connects a farm's reports to this chat. Hooks run on farm
// goroutines and only post messages.
func (m *Model) farmHooks() FarmHooks {
	send := m.send
	return FarmHooks{
		Event: func(typ string, data map[string]any) { send(farmEventMsg{typ: typ, m: data}) },
	}
}
