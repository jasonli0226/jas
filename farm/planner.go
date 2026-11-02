package farm

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/jasonli0226/jas/core"
)

// PlannerInput is what the planner is given: the task text only, never chat
// history.
type PlannerInput struct {
	Task, Base string
	Files      []string
	Cfg        Config
	Roles      map[string]RoleDef
}

// PlannerResult is one parsed planner answer. Raw is the YAML block with
// base set to the real HEAD. Problems are validation failures that survived
// the retry; Refusal is set when the planner or the code refused to split.
type PlannerResult struct {
	Raw      []byte
	Plan     Plan
	Problems []Problem
	Refusal  *Refusal
}

var yamlFence = regexp.MustCompile("(?s)```ya?ml[ \t]*\r?\n(.*?)```")

// Planner is a read-only planning session that can be asked to revise.
type Planner struct {
	sess *core.Session
	in   PlannerInput
}

// NewPlanner starts the planner session. sc.Exec must hold read-only tools.
func NewPlanner(sc core.SessionConfig, in PlannerInput) (*Planner, error) {
	sc.System = core.BuildSystem(sc.System, PlannerPrompt(in))
	s, err := core.NewSession(sc)
	if err != nil {
		return nil, err
	}
	return &Planner{sess: s, in: in}, nil
}

// Close ends the planner session.
func (p *Planner) Close(status string, code int) error { return p.sess.Close(status, code) }

// Draft asks for the first plan.
func (p *Planner) Draft(ctx context.Context) (PlannerResult, error) {
	return p.ask(ctx, "Task:\n"+p.in.Task+"\n\nExplore the repository, then answer with exactly one ```yaml block.")
}

// Revise sends owner feedback and parses the new answer.
func (p *Planner) Revise(ctx context.Context, feedback string) (PlannerResult, error) {
	return p.ask(ctx, "The owner asks for changes to the plan:\n"+feedback+"\n\nAnswer with the full revised plan in exactly one ```yaml block.")
}

// RunPlanner runs one planning round (with one validation retry) and closes
// the session.
func RunPlanner(ctx context.Context, sc core.SessionConfig, in PlannerInput) (PlannerResult, error) {
	p, err := NewPlanner(sc, in)
	if err != nil {
		return PlannerResult{}, err
	}
	res, err := p.Draft(ctx)
	if err != nil {
		_ = p.Close("failed", core.ExitFailure)
		return res, err
	}
	return res, p.Close("ok", core.ExitOK)
}

// ask sends msg, parses the answer and, on problems, retries once.
func (p *Planner) ask(ctx context.Context, msg string) (PlannerResult, error) {
	res, err := p.round(ctx, msg)
	if err != nil || (len(res.Problems) == 0) {
		return res, err
	}
	return p.round(ctx, "The plan has problems:\n"+problemList(res.Problems)+"\nFix them and answer with the full plan in exactly one ```yaml block.")
}

func (p *Planner) round(ctx context.Context, msg string) (PlannerResult, error) {
	tr, err := p.sess.Send(ctx, msg)
	if err != nil {
		return PlannerResult{}, err
	}
	if tr.Status != "ok" {
		return PlannerResult{}, &core.ExitError{Code: tr.Code, Reason: "planner stopped: " + tr.Reason}
	}
	return p.parse(tr.Final), nil
}

func (p *Planner) parse(answer string) PlannerResult {
	blocks := yamlFence.FindAllStringSubmatch(answer, -1)
	if len(blocks) != 1 {
		return PlannerResult{Problems: []Problem{{Message: fmt.Sprintf("expected exactly one ```yaml block, found %d", len(blocks))}}}
	}
	return CheckPlan(withBase([]byte(blocks[0][1]), p.in.Base), p.in)
}

// CheckPlan parses raw plan bytes, validates them and applies the
// code-enforced refusals. The plan's base is forced to in.Base.
func CheckPlan(raw []byte, in PlannerInput) PlannerResult {
	plan, err := ParsePlan(raw)
	if err != nil {
		return PlannerResult{Raw: raw, Problems: []Problem{{Message: err.Error()}}}
	}
	if plan.Refuse == nil {
		plan.Base = in.Base
	}
	res := PlannerResult{Raw: raw, Plan: plan}
	if plan.Refuse != nil {
		res.Refusal = plan.Refuse
		return res
	}
	if res.Problems = Validate(plan, in.Cfg, in.Files); len(res.Problems) > 0 {
		return res
	}
	res.Refusal = CodeRefusal(plan, in.Cfg, in.Files)
	return res
}

// withBase drops any top-level base: the planner wrote and puts the real one first.
func withBase(raw []byte, base string) []byte {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(l, "base:") {
			continue
		}
		out = append(out, l)
	}
	body := strings.TrimLeft(strings.Join(out, "\n"), "\n")
	if strings.Contains(body, "\nrefuse:") || strings.HasPrefix(body, "refuse:") {
		return []byte(body)
	}
	return []byte("base: " + base + "\n" + body)
}

func problemList(ps []Problem) string {
	var b strings.Builder
	for _, p := range ps {
		if p.Part != "" {
			fmt.Fprintf(&b, "- part %s: %s\n", p.Part, p.Message)
		} else {
			fmt.Fprintf(&b, "- %s\n", p.Message)
		}
	}
	return b.String()
}

// FormatProblems renders problems one per line for the owner.
func FormatProblems(ps []Problem) string { return problemList(ps) }

const plannerRules = `You are the jas farm planner. You split one task into parts that separate
worker agents can do in parallel, each in its own git worktree. You have
read-only tools (read, ls, grep) on the repository at the base commit.

Refuse by default. Split only when the work divides into at least %d writing
parts whose owned paths do not overlap%s, and the parts can run at the same
time. Otherwise answer with a refusal:

` + "```yaml" + `
refuse:
  reason: small_change   # small_change | single_package | overlapping_ownership | serial_dependencies | no_check | unclear_task | no_clear_contract
  detail: "one sentence"
suggest: 'jas run "<task>"'
` + "```" + `

A split plan has exactly this shape (do not write base: or approved:; they are
added for you):

` + "```yaml" + `
task: "<the task>"
contracts: |
  <rules every part follows>
interfaces:            # structured contracts the integrator checks
  - name: pkg.Func
    owner: <part id>
    shape: "func Func(x int) error"
parts:
  - id: core
    role: coder
    goal: "<what this part does>"
    owns: ["core/**"]
  - id: tests-core
    role: tester
    goal: "<what to test>"
    owns: ["core/**"]
    after: [core]
shared: ["go.mod"]     # files only the integrator may write
why_split: "<why splitting pays off>"
` + "```" + `

Every writing part needs a non-empty owns list. Use after: to order parts that
must not run at once. Answer with exactly one yaml block.

Roles available:
`

// PlannerPrompt is the planner's system prompt: the refuse-by-default rule,
// the split limits, the available roles and the plan's YAML shape.
func PlannerPrompt(in PlannerInput) string {
	disjoint := ""
	if in.Cfg.Split.DisjointFiles {
		disjoint = " (disjoint_files is on: no two concurrently runnable parts may own the same file)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, plannerRules, in.Cfg.Split.MinParts, disjoint)
	names := make([]string, 0, len(in.Roles))
	for n := range in.Roles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		d := in.Roles[n]
		writes := "owned paths"
		if len(d.Writes) > 0 {
			writes = "owned paths limited to " + strings.Join(d.Writes, ", ")
		}
		fmt.Fprintf(&b, "- %s: tools %s; writes %s\n", n, strings.Join(d.Tools, ", "), writes)
	}
	fmt.Fprintf(&b, "\nAt most %d workers run at once. The verdict check is: %s\n", in.Cfg.Split.MaxWorkers, in.Cfg.Check)
	return b.String()
}
