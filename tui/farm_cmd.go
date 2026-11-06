package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
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

// unquote strips the quotes around a /farm task; a double-quoted task is
// unescaped as Go would, so a suggestion's %q round-trips.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if u, err := strconv.Unquote(s); err == nil && strings.HasPrefix(s, `"`) {
		return strings.TrimSpace(u)
	}
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// farmer returns the farm port when a new farm may start here, else a notice.
func (m *Model) farmer() (Farmer, tea.Cmd) {
	f := m.b.Farmer()
	why := ""
	switch {
	case f == nil:
		why = "farms are not available in this chat"
	case m.farm.running():
		why = "a farm is already running in this window (one at a time); /farm shows it"
	case m.farmReview != nil || m.farmReplan != nil:
		why = "a farm plan is already under review"
	}
	if f != nil && why == "" {
		if ok, reason := f.Available(); !ok {
			why = "farm: " + reason
		}
	}
	if why != "" {
		m.tellAgent("farm not started: " + why)
		return nil, m.notice(why)
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
	return tea.Batch(m.notice("farm: planning "+taskHead(task)+" …"), func() tea.Msg {
		p, err := f.Plan(context.Background(), task, model)
		return farmPlannedMsg{plan: p, err: err}
	})
}

// onFarmPlanned shows a drafted plan and opens the review picker.
func (m *Model) onFarmPlanned(msg farmPlannedMsg) tea.Cmd {
	m.farmReview = nil
	if msg.err != nil {
		m.tellAgent("farm planning failed: " + msg.err.Error())
		return m.answer(errStyle.Render("farm: " + m.clean(msg.err.Error())))
	}
	p := msg.plan
	if p.Refusal != "" {
		m.tellAgent("farm planner refused: " + p.Refusal + "; it suggests: " + p.Suggest)
		if m.draft() == "" {
			m.setDraft(p.Task)
			return m.notice("farm: planner refused: "+p.Refusal, "try: "+p.Suggest, "the task is back in the input to run here")
		}
		return m.notice("farm: planner refused: "+p.Refusal, "try: "+p.Suggest)
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
	m.openPickerC("farm plan", []pickItem{
		{label: reviewApprove, value: reviewApprove, desc: "start the farm"},
		{label: reviewEdit, value: reviewEdit, desc: "open the plan in $EDITOR"},
		{label: reviewReplan, value: reviewReplan, desc: "send feedback to the planner"},
		{label: reviewQuit, value: reviewQuit, desc: "start nothing"},
	}, m.onFarmReview, func() tea.Cmd { return m.onFarmReview(reviewQuit) })
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
	m.tellAgent("the owner quit the farm plan review; nothing started")
	return tea.Batch(m.notice("farm: nothing started"), func() tea.Msg { f.Discard(plan); return nil })
}

// farmApprove approves p and starts it on the chat's model and level.
func (m *Model) farmApprove(f Farmer, p FarmPlan) tea.Cmd {
	m.farmGen++
	gen := m.farmGen
	model, level, hooks := m.model, m.gate.Level(), m.farmHooks(gen)
	return func() tea.Msg {
		path, err := f.Approve(p)
		if err != nil {
			return farmStartedMsg{err: err}
		}
		plan, _ := farm.ParsePlan(p.Raw)
		run, err := f.Start(context.Background(), path, model, level, hooks)
		return farmStartedMsg{gen: gen, run: run, plan: plan, task: p.Task, err: err}
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
	m.farmGen++
	gen := m.farmGen
	model, level, hooks := m.model, m.gate.Level(), m.farmHooks(gen)
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
		return farmStartedMsg{gen: gen, run: run, plan: plan, task: plan.Task, err: err}
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
		m.tellAgent("farm failed to start: " + msg.err.Error())
		return m.answer(errStyle.Render("farm: " + m.clean(msg.err.Error())))
	}
	if msg.gen != m.farmGen {
		return nil // a newer farm was started meanwhile
	}
	m.farm = newFarmState(msg.run, msg.plan, msg.task, m.o.Now(), m.width)
	m.farm.width, m.farm.gen = m.width, msg.gen
	run, send, gen := msg.run, m.send, msg.gen
	go func() { send(farmMsg{gen: gen, msg: farmDoneMsg{res: run.Wait()}}) }() // a farm outlives any one command
	m.tellAgent(fmt.Sprintf("farm %s started; its summary comes when it ends", run.FarmID()))
	cmds := []tea.Cmd{m.notice(fmt.Sprintf("farm %s started · %s · /farm for the dashboard", run.FarmID(), farmKeysHint))}
	early := m.farmEarly
	m.farmEarly = nil
	for _, e := range early { // events the farm logged before Start returned
		_, cmd := m.Update(e)
		cmds = append(cmds, cmd)
	}
	return tea.Batch(cmds...)
}

// farmMsg is a farm hook's message tagged with its farm's generation, so a
// message from an older farm is dropped and one that beats farmStartedMsg is
// kept until the farm's state exists.
type farmMsg struct {
	gen int
	msg tea.Msg
}

func (m *Model) onFarmMsg(fm farmMsg) tea.Cmd {
	if fm.gen != m.farmGen {
		return nil
	}
	if m.farm == nil || m.farm.gen != fm.gen {
		m.farmEarly = append(m.farmEarly, fm)
		return nil
	}
	_, cmd := m.Update(fm.msg)
	return cmd
}

// farmHooks connects a farm's reports to this chat. Hooks run on farm
// goroutines and only post messages.
func (m *Model) farmHooks(gen int) FarmHooks {
	raw := m.send
	send := func(msg tea.Msg) { raw(farmMsg{gen: gen, msg: msg}) }
	grants := core.NewGrants(m.o.Windows) // "always for this farm" ends with it
	return FarmHooks{
		Event:  func(typ string, data map[string]any) { send(farmEventMsg{typ: typ, m: data}) },
		Worker: func(part string) core.UI { return farmBridge{part: part, send: send} },
		Undelivered: func(part string, texts []string) {
			send(farmUndeliveredMsg{part: part, texts: texts})
		},
		Gate: func(part, approve string) (core.Gate, error) {
			level := m.gate.Level()
			if approve != "" {
				l, err := core.ParseLevel(approve)
				if err != nil {
					return nil, err
				}
				level = l
			}
			return m.gate.ForPart(part, level, grants), nil
		},
		Ask: farmAsker(raw),
	}
}

// Suggestion answers.
const (
	suggestYes  = "yes"
	suggestNo   = "no"
	suggestEdit = "edit"
)

// suggestFarm shows the agent's drafted task as a readable block and asks the
// owner whether to plan it; nothing starts before they answer.
func (m *Model) suggestFarm(data map[string]any) tea.Cmd {
	task, _ := data["task"].(string)
	if task = strings.TrimSpace(task); task == "" {
		return nil
	}
	ask := farmAskMsg{
		prompt:  "plan this farm?",
		choices: []string{suggestYes, suggestNo, suggestEdit},
		then:    func(ans string) tea.Cmd { return m.answerSuggestion(task, ans) },
		// Shown as the question opens, after the turn that made the call.
		onOpen: func() tea.Cmd { return m.print(m.tr.Notice(m.suggestionBlock(task))...) },
	}
	return m.onFarmAsk(ask)
}

// suggestionBlock renders the suggested task, wrapped and marked off.
func (m *Model) suggestionBlock(task string) string {
	width := max(m.width-2, 20)
	lines := []string{boldStyle.Render("the agent suggests a farm")}
	for _, para := range strings.Split(task, "\n") {
		for _, l := range strings.Split(ansi.Wrap(m.clean(para), width, ""), "\n") {
			lines = append(lines, "│ "+l)
		}
	}
	return strings.Join(lines, "\n")
}

// answerSuggestion acts on the owner's answer: plan the task as drafted, put
// it in the input to edit (never over a draft), or drop it ("" is no).
func (m *Model) answerSuggestion(task, ans string) tea.Cmd {
	switch ans {
	case suggestYes:
		return m.farmPlan(task)
	case suggestEdit:
		line := fmt.Sprintf("/farm %q", task)
		if m.draft() == "" {
			m.setDraft(line)
			return nil
		}
		return m.notice("suggested: " + line)
	}
	m.tellAgent("the owner dismissed the farm suggestion; nothing started")
	return m.notice("farm suggestion dismissed")
}

// farmTaskHeadMax caps the task shown in the planning notice, in runes.
const farmTaskHeadMax = 60

// taskHead is a task's first line, cut to farmTaskHeadMax runes.
func taskHead(task string) string {
	head, _, _ := strings.Cut(strings.TrimSpace(task), "\n")
	if r := []rune(strings.TrimSpace(head)); len(r) > farmTaskHeadMax {
		return string(r[:farmTaskHeadMax-1]) + "…"
	}
	return strings.TrimSpace(head)
}

// tellAgent queues a farm outcome for the agent's next message, so it never
// has to guess what became of a farm the owner started or dropped.
func (m *Model) tellAgent(line string) {
	m.farmUpdates = append(m.farmUpdates, m.o.Redact(line))
}

// takeFarmNotes returns what the next message carries about farms (updates,
// then any end summary) and its label, and clears both.
func (m *Model) takeFarmNotes() (note, label string) {
	parts := append([]string{}, m.farmUpdates...)
	label = "farm update"
	if m.farmNote != "" {
		parts, label = append(parts, m.farmNote), "farm summary"
	}
	m.farmUpdates, m.farmNote = nil, ""
	return strings.Join(parts, "\n"), label
}
