package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/farm"
)

// Agent statuses as the rows and dashboard show them.
const (
	agentQueued  = "queued"
	agentRunning = "running"
	agentDone    = "done"
	agentFailed  = "failed"
	agentDropped = "dropped"
	agentPaused  = "paused"

	integrateAgent = "integrate" // the integrator's agent id
	maxFlipKeys    = 9           // Ctrl/Alt+1..9
	farmKeysHint   = "ctrl+1..9 flip · ctrl+0 back"
	rowsChrome     = 4 // rules, status line and blank line around the input
	// minTranscriptRows is the transcript left visible above the live region
	// before the farm rows collapse to the totals line.
	minTranscriptRows = 3
)

var agentMarks = map[string]string{
	agentQueued: "· queued", agentRunning: "● running", agentDone: "✓ done",
	agentFailed: "✗ failed", agentDropped: "– dropped", agentPaused: "‖ paused",
}

// farmState is the window's farm: one at a time, kept after it ends until
// the next one starts.
type farmState struct {
	run      FarmRun
	task     string
	agents   []*farmAgent // plan order; the integrator is appended when it starts
	started  time.Time
	budget   int    // farm_tokens; 0 unknown
	used     int    // farm total tokens
	check    string // verdict: pending | passed | failed
	ended    bool
	res      farm.Result
	gen      int  // the farm's generation (farmMsg)
	width    int  // terminal width for new agents' transcripts
	stopping bool // /farm stop (or a quit) asked for a graceful stop
}

// farmAgent is one part's (or the integrator's) session as the chat sees it.
type farmAgent struct {
	id, role, status string
	activity         string // last tool call or event
	check            string // part_check: "" | pass | fail
	diffstat         string
	worktree         string
	after            []string
	tokens           int
	num              int  // flip key 1..9; 0 = none
	needsApproval    bool // an approval prompt from this agent is open

	queued  []string   // owner steers sent and not yet delivered
	tr      Transcript // renders this agent's text and events
	printed []string   // every block rendered for this agent, for flips
}

// Messages from the farm port.
type (
	farmPlannedMsg struct {
		plan FarmPlan
		err  error
	}
	farmEditedMsg struct {
		plan FarmPlan
		raw  []byte
		err  error
	}
	farmStartedMsg struct {
		gen  int
		run  FarmRun
		plan farm.Plan
		task string
		err  error
	}
	farmEventMsg struct {
		typ string
		m   map[string]any
	}
	farmDoneMsg struct{ res farm.Result }
)

func newFarmState(run FarmRun, plan farm.Plan, task string, now time.Time, width int) *farmState {
	f := &farmState{run: run, task: task, started: now, check: "pending"}
	for i, p := range plan.Parts {
		a := &farmAgent{id: p.ID, role: p.Role, status: agentQueued, after: p.After, tr: Transcript{Width: width}}
		if i < maxFlipKeys {
			a.num = i + 1
		}
		f.agents = append(f.agents, a)
	}
	return f
}

func (f *farmState) agent(id string) *farmAgent {
	for _, a := range f.agents {
		if a.id == id {
			return a
		}
	}
	return nil
}

// nextNum is the lowest free flip key, or 0 when all nine are taken.
func (f *farmState) nextNum() int {
	used := map[int]bool{}
	for _, a := range f.agents {
		used[a.num] = true
	}
	for n := 1; n <= maxFlipKeys; n++ {
		if !used[n] {
			return n
		}
	}
	return 0
}

// running reports whether the farm has not ended yet.
func (f *farmState) running() bool { return f != nil && !f.ended }

// apply updates the agents from one farm event.
func (f *farmState) apply(typ string, m map[string]any) {
	part, _ := m["part"].(string)
	a := f.agent(part)
	switch typ {
	case farm.EvFarmStarted:
		f.budget = int(num(m["farm_tokens"]))
	case farm.EvPartStarted:
		if a != nil {
			a.status, a.activity = agentRunning, ""
			if w, ok := m["worktree"].(string); ok {
				a.worktree = w
			}
		}
	case farm.EvPartCheck:
		if a != nil {
			a.check, a.activity = "fail", "part_check failed"
			if num(m["exit_code"]) == 0 && m["timed_out"] != true {
				a.check, a.activity = "pass", "part_check passed"
			}
		}
	case farm.EvPartEnded:
		if a != nil {
			a.status = agentStatus(fmt.Sprint(m["status"]))
			if r, ok := m["reason"].(string); ok {
				a.activity = r
			}
			if d, ok := m["diffstat"].(string); ok {
				a.diffstat = d
			}
		}
	case farm.EvIntegrateStarted:
		if f.agent(integrateAgent) == nil {
			f.agents = append(f.agents, &farmAgent{id: integrateAgent, role: "integrator", status: agentRunning,
				num: f.nextNum(), tr: Transcript{Width: f.width}})
		}
	case farm.EvIntegrateEnded:
		if a := f.agent(integrateAgent); a != nil {
			a.status = agentStatus(fmt.Sprint(m["status"]))
		}
	case farm.EvCheckRan:
		f.check = "failed"
		if num(m["exit_code"]) == 0 && m["timed_out"] != true {
			f.check = "passed"
		}
	}
}

// refresh takes statuses and tokens from the manifest, which also covers
// dependents dropped without an event of their own.
func (f *farmState) refresh(man farm.Manifest) {
	for id, ps := range man.Parts {
		if a := f.agent(id); a != nil && ps != nil {
			if ps.Status != "" {
				a.status = agentStatus(ps.Status)
			}
			a.tokens = ps.Tokens
		}
	}
	if a := f.agent(integrateAgent); a != nil && man.Integrator != nil {
		a.tokens = man.Integrator.Tokens
	}
	if man.TotalTokens > 0 {
		f.used = man.TotalTokens
	}
}

func agentStatus(s string) string {
	if _, ok := agentMarks[s]; ok {
		return s
	}
	return agentQueued
}

// rows are the live rows under the input: one per agent and a totals line,
// or only the totals line when the terminal is too short.
func (m *Model) farmRows() []string {
	f := m.farm
	if !f.running() {
		return nil
	}
	totals := m.farmTotals()
	if m.height > 0 && len(f.agents)+1+m.input.Height()+rowsChrome+minTranscriptRows > m.height {
		return []string{totals}
	}
	width := 0
	for _, a := range f.agents {
		width = max(width, len([]rune(a.id)))
	}
	rows := make([]string, 0, len(f.agents)+1)
	for _, a := range f.agents {
		key := " "
		if a.num > 0 {
			key = fmt.Sprint(a.num)
		}
		activity := m.clean(a.activity)
		if a.needsApproval {
			activity = "⚠ needs approval"
		}
		line := fmt.Sprintf("%s %-*s %-10s %-28s %s", key, width, m.clean(a.id), agentMarks[a.status],
			activity, formatTokens(a.tokens))
		rows = append(rows, ansi.Truncate(strings.TrimRight(line, " "), m.width, "…"))
	}
	return append(rows, totals)
}

func (m *Model) farmTotals() string {
	f := m.farm
	tok := formatTokens(f.used)
	if f.budget > 0 {
		tok += " / " + formatTokens(f.budget)
	}
	line := fmt.Sprintf("farm %s tok · %s · check %s     %s", tok, m.o.Now().Sub(f.started).Round(time.Second), f.check, farmKeysHint)
	if s := m.askStrip(); s != "" {
		line = s + " · " + line
	}
	return dimStyle.Render(ansi.Truncate(line, m.width, "…"))
}

// farmDashboard prints a snapshot of every agent into the chat.
func (m *Model) farmDashboard() tea.Cmd {
	f := m.farm
	if f == nil {
		return m.notice(`no farm in this window — /farm "<task>" starts one`)
	}
	state := "running"
	if f.ended {
		state = "ended: " + f.res.Reason
	}
	lines := []string{fmt.Sprintf("farm %s · %s · %s · %s · %s tok", m.clean(f.run.FarmID()), m.clean(f.task), state,
		m.o.Now().Sub(f.started).Round(time.Second), formatTokens(f.used))}
	lines = append(lines, fmt.Sprintf(" %-2s %-18s %-10s %-10s %-28s %-7s %s", "#", "part", "role", "status", "last activity", "tokens", "check"))
	for _, a := range f.agents {
		key, check := "", a.check
		if a.num > 0 {
			key = fmt.Sprint(a.num)
		}
		if check == "" {
			check = "—"
		}
		lines = append(lines, fmt.Sprintf(" %-2s %-18s %-10s %-10s %-28s %-7s %s", key, m.clean(a.id), m.clean(a.role),
			agentMarks[a.status], m.clean(a.activity), formatTokens(a.tokens), check))
	}
	lines = append(lines, " "+farmKeysHint+" · /farm pause|resume|drop <part> · /farm stop")
	return m.answer(m.tr.Notice(strings.Join(lines, "\n"))...)
}

// onFarmEvent updates the rows and prints finished events once.
func (m *Model) onFarmEvent(msg farmEventMsg) tea.Cmd {
	f := m.farm
	if f == nil {
		return nil
	}
	f.apply(msg.typ, msg.m)
	f.refresh(f.run.Snapshot())
	var cmds []tea.Cmd
	switch msg.typ {
	case farm.EvPartEnded:
		part, _ := msg.m["part"].(string)
		cmds = append(cmds, m.dropFarmPrompts(part)) // its gate is gone
	case farm.EvIntegrateEnded:
		cmds = append(cmds, m.dropFarmPrompts(integrateAgent))
	}
	show := msg.typ == farm.EvOwnerDecision && msg.m["note"] != nil
	switch msg.typ {
	case farm.EvPartCheck, farm.EvPartEnded, farm.EvBoardPosted, farm.EvIntegrateCheck,
		farm.EvIntegrateEnded, farm.EvCheckRan, farm.EvBudgetStop:
		show = true
	}
	if line := farm.ProgressLine(msg.typ, msg.m); show && line != "" {
		cmds = append(cmds, m.heldNotice(line))
	}
	return tea.Batch(cmds...)
}

// onFarmDone marks the farm ended, prints its summary and keeps it for the
// owner's next message to the main session (never a turn of its own).
func (m *Model) onFarmDone(msg farmDoneMsg) tea.Cmd {
	f := m.farm
	if f == nil {
		return nil
	}
	f.ended, f.res = true, msg.res
	man := f.run.Snapshot()
	f.refresh(man)
	order := make([]string, 0, len(f.agents))
	for _, a := range f.agents {
		if a.id != integrateAgent {
			order = append(order, a.id)
		}
	}
	m.farmNote = m.o.Redact(farmSummary(man, msg.res, order))
	return tea.Batch(m.dropFarmPrompts(""), m.heldNotice(strings.Split(m.farmNote, "\n")...))
}

// farmSummary is the end-of-farm summary: outcome, branch, final choice and
// each part's status, in order.
func farmSummary(man farm.Manifest, res farm.Result, order []string) string {
	lines := []string{fmt.Sprintf("farm %s ended: %s (exit %d)", man.FarmID, res.Reason, res.Code)}
	if res.Branch != "" {
		lines = append(lines, "branch "+res.Branch)
	}
	if man.FinalChoice != "" {
		lines = append(lines, "final choice "+man.FinalChoice)
	}
	if man.Check != nil {
		verdict := "passed"
		if man.Check.ExitCode != 0 || man.Check.TimedOut {
			verdict = "failed"
		}
		lines = append(lines, "check "+verdict)
	}
	for _, id := range order {
		ps := man.Parts[id]
		if ps == nil {
			continue
		}
		detail := ps.Diffstat
		if ps.Status == farm.StatusFailed && ps.FailureReason != "" {
			detail = ps.FailureReason
		}
		line := id + " " + ps.Status
		if detail != "" {
			line += " (" + detail + ")"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func num(v any) float64 {
	switch n := v.(type) {
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case float64:
		return n
	}
	return 0
}
