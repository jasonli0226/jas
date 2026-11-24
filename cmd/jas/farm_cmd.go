package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/tools"
)

const (
	farmsDir        = "farms"
	farmLogFile     = "farm.jsonl"
	farmPlanFile    = "plan.yaml"
	plannerDir      = "planner"
	defaultPlanPath = "./plan.yaml"
)

// trivialCheckHints are substrings that mark a check as running tests.
var trivialCheckHints = []string{"test", "pytest", "jest", "vitest", "cargo test", "go test", "npm test"}

// farmOnlyFlags are jas farm plan flags that are not stored settings.
var farmOnlyFlags = map[string]bool{"o": true, "allow-dirty": true, "trust-project": true, "review": true}

type planArgs struct {
	task       string
	out        string
	outSet     bool // -o was given
	allowDirty bool
	chat       bool // planned from the chat, which has no --allow-dirty
	trust      bool
	review     *bool // --review on jas farm "<task>"; nil: not given
	flags      map[string]string
}

const farmUsage = `usage:
  jas farm plan "<task>" [-o plan.yaml] [--allow-dirty] [--trust-project]
  jas farm run <plan.yaml> [--json] [--allow-dirty] [--trust-project] [--review]
  jas farm "<task>" [--review]  (needs a terminal; otherwise use plan + run)
  jas farm status [<farm-id>]
  jas farm clean [<farm-id>] [--branches]
  jas farm vars [--trust-project]`

// farmCmd dispatches jas farm subcommands, or runs plan then run for a task.
func farmCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return usageErr("%s", farmUsage)
	}
	switch args[0] {
	case "plan":
		_, _, err := farmPlanCmd(ctx, args[1:])
		return err
	case "run":
		return farmRunCmd(ctx, args[1:])
	case "status":
		return farmStatusCmd(args[1:])
	case "clean":
		return farmCleanCmd(ctx, args[1:])
	case "vars":
		return farmVarsCmd(ctx, args[1:])
	}
	if !isTerminal(int(os.Stdin.Fd())) || !isTerminal(int(os.Stderr.Fd())) {
		return usageErr(`jas farm "<task>" needs a terminal: use jas farm plan "<task>" then jas farm run <plan.yaml>`)
	}
	pa, err := parseDirectArgs(args)
	if err != nil {
		return err
	}
	e, path, approved, err := planFarm(ctx, pa, true)
	if err != nil || !approved {
		return err
	}
	return runPlan(ctx, e, path, false, pa.allowDirty, nil, pa.review)
}

// parsePlanArgs reads flags and the single task, accepting flags on either
// side of the task.
func parsePlanArgs(args []string) (planArgs, error) { return parsePlanFlags(args, false) }

// parseDirectArgs is parsePlanArgs for jas farm "<task>", which also takes
// --review; jas farm plan does not run anything to review.
func parseDirectArgs(args []string) (planArgs, error) { return parsePlanFlags(args, true) }

func parsePlanFlags(args []string, withReview bool) (planArgs, error) {
	fs := flag.NewFlagSet("farm plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var pa planArgs
	fs.StringVar(&pa.out, "o", defaultPlanPath, "where to write the plan")
	fs.BoolVar(&pa.allowDirty, "allow-dirty", false, "plan on a dirty tree; uncommitted changes are not part of the farm")
	fs.BoolVar(&pa.trust, "trust-project", false, "load the project context file without asking")
	var rv reviewFlag
	if withReview {
		fs.Var(&rv, "review", "run the read-only reviewer after integration (--review=false overrides farm.yaml)")
	}
	for _, k := range settings.Keys {
		if k.Flag {
			fs.String(strings.ReplaceAll(k.Name, "_", "-"), "", k.Name)
		}
	}
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return pa, usageErr("%v", err)
		}
		args = nil
		if fs.NArg() > 0 {
			rest = append(rest, fs.Arg(0))
			args = fs.Args()[1:]
		}
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return pa, usageErr(`expected exactly one task argument: jas farm plan [flags] "task"`)
	}
	pa.task = rest[0]
	pa.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "o" {
			pa.outSet = true
		}
		if f.Name == "review" {
			pa.review = rv.ptr()
		}
		if !farmOnlyFlags[f.Name] {
			pa.flags[strings.ReplaceAll(f.Name, "-", "_")] = f.Value.String()
		}
	})
	return pa, nil
}

// farmPlanCmd runs `jas farm plan`. It returns the plan path and whether the
// plan was approved.
func farmPlanCmd(ctx context.Context, args []string) (string, bool, error) {
	pa, err := parsePlanArgs(args)
	if err != nil {
		return "", false, err
	}
	_, path, approved, err := planFarm(ctx, pa, false)
	return path, approved, err
}

// planFarm plans pa.task and returns the env, the plan path and whether the
// plan was approved. For jas farm "<task>" (direct) without -o the plan is
// written to the farm dir rather than into the repository, where it would
// make the tree dirty for the run that follows.
func planFarm(ctx context.Context, pa planArgs, direct bool) (*env, string, bool, error) {
	e, err := prepare(ctx, pa.flags, pa.trust)
	if err != nil {
		return nil, "", false, err
	}
	path, approved, err := planWith(ctx, e, pa, direct)
	return e, path, approved, err
}

func planWith(ctx context.Context, e *env, pa planArgs, direct bool) (string, bool, error) {
	pf, err := farmPreflight(ctx, e, pa)
	for _, w := range pf.warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	if err != nil {
		return "", false, err
	}
	cfg, base, files := pf.cfg, pf.base, pf.files
	now := time.Now()
	farmID := farm.NewFarmID(now)
	dir := filepath.Join(e.home, farmsDir, farmID)
	if direct && !pa.outSet {
		pa.out = filepath.Join(dir, farmPlanFile)
	}
	flog, err := core.OpenEventLog(filepath.Join(dir, farmLogFile), nil, e.red.Redact)
	if err != nil {
		return "", false, err
	}
	defer flog.Close()

	roles, _ := farm.Available(cfg)
	in := farm.PlannerInput{Task: pa.task, Base: base, Files: files, Cfg: cfg, Roles: roles}
	pl, err := startPlanner(ctx, e, cfg, in, farmID, filepath.Join(dir, plannerDir, eventsFile))
	if err != nil {
		return "", false, err
	}
	res, err := pl.Draft(ctx)
	code := core.ExitOK
	defer func() { _ = pl.Close(closeStatus(code), code) }()
	if err != nil {
		code = exitCodeOf(err)
		return "", false, err
	}
	if res.Refusal != nil {
		code = core.ExitRefused
		return "", false, refuse(e, flog, pa.task, res.Refusal)
	}
	final, approved, err := finishPlan(ctx, e, pa, pl.Revise, in, flog, res)
	if err != nil {
		code = exitCodeOf(err)
		return "", false, err
	}
	if final != nil {
		if err := os.WriteFile(filepath.Join(dir, farmPlanFile), final, 0o644); err != nil {
			return "", false, fmt.Errorf("write plan copy: %w", err)
		}
	}
	return pa.out, approved, nil
}

func closeStatus(code int) string {
	if code == core.ExitOK {
		return "ok"
	}
	return "failed"
}

func exitCodeOf(err error) int {
	var ee *core.ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return core.ExitFailure
}

// preflighted is what farmPreflight established before any model call.
type preflighted struct {
	cfg      farm.Config
	base     string
	files    []string
	warnings []string // "jas: …" lines, redacted and sanitized, for the owner
}

// farmPreflight runs every check that must pass before a model call. Warnings
// are returned, not printed, so the chat can show them in its transcript.
func farmPreflight(ctx context.Context, e *env, pa planArgs) (preflighted, error) {
	var pf preflighted
	cfg, notices, err := e.loadFarmConfig(e.pc.trusted)
	pf.cfg = cfg
	if err != nil {
		return pf, usageErr("farm config: %v", err)
	}
	warn := func(s string) { pf.warnings = append(pf.warnings, core.Sanitize(e.red.Redact("jas: "+s))) }
	for _, n := range notices {
		warn(n)
	}
	if strings.TrimSpace(cfg.Check) == "" {
		return pf, usageErr("%s", noCheckMsg(e))
	}
	git := farm.NewGit()
	dirty, err := farm.Dirty(ctx, git, e.root)
	if err != nil {
		return pf, err
	}
	if dirty {
		if pa.chat {
			return pf, usageErr("working tree has uncommitted changes; commit them first (a farm plans from HEAD)")
		}
		if !pa.allowDirty {
			return pf, usageErr("working tree has uncommitted changes; commit them or pass --allow-dirty")
		}
		warn("warning: uncommitted changes are not part of the farm")
	}
	if checkLooksTrivial(cfg.Check) {
		warn("warning: check looks trivial (no test command): " + cfg.Check)
	}
	head, err := git.Run(ctx, e.root, "rev-parse", "HEAD")
	if err != nil {
		return pf, usageErr("cannot read HEAD (does the repository have a commit?): %v", err)
	}
	listed, err := git.Run(ctx, e.root, "ls-files")
	if err != nil {
		return pf, err
	}
	pf.base, pf.files = strings.TrimSpace(head), strings.Split(strings.TrimSpace(listed), "\n")
	return pf, nil
}

// noCheckMsg says how to set the farm's check.
func noCheckMsg(e *env) string {
	return "no check command: set check: in .jas/farm.yaml (needs a trusted project) or in " + filepath.Join(e.home, "farm.yaml")
}

func checkLooksTrivial(check string) bool {
	for _, h := range trivialCheckHints {
		if strings.Contains(check, h) {
			return false
		}
	}
	return true
}

// planner is a planner session that owns its event log.
type planner struct {
	*farm.Planner
	log core.EventLog
}

// Close ends the session and closes its event log.
func (p *planner) Close(status string, code int) error {
	err := p.Planner.Close(status, code)
	if cerr := p.log.Close(); err == nil {
		err = cerr
	}
	return err
}

// startPlanner opens the planner session with read-only tools. Its memory
// is read-only, as in every session no one can approve a write in.
func startPlanner(ctx context.Context, e *env, cfg farm.Config, in farm.PlannerInput, farmID, logPath string) (*planner, error) {
	log, err := core.OpenEventLog(logPath, nil, e.red.Redact)
	if err != nil {
		return nil, err
	}
	model := cfg.Models.Planner
	if model == "" {
		model = e.cfg.Model
	}
	e.warnUnknownWindow(model)
	meta := e.sessionMeta(model)
	meta["farm_id"], meta["role"] = farmID, "planner"
	exec := &core.Executor{
		Reg: core.NewRegistry(tools.ReadOnly()...), Policy: e.policy, Log: log,
		Filter: e.red.Filter, Redact: e.red.Redact,
	}
	guardMemory(exec, e.mem, true, e.red.Redact)
	pl, err := farm.NewPlanner(core.SessionConfig{
		Provider: e.provider, Model: model, System: e.pc.readOnlyMemory(e.pc.system), Exec: exec, Log: log,
		UI: core.NewQuietUI(), MaxTurns: e.cfg.MaxTurns, Budget: e.budget(model), Meta: meta,
		MaxOutput: e.cfg.MaxOutputTokens, ReasoningEffort: e.cfg.ReasoningEffort, Tokens: rootPool("farm", cfg.Budget.FarmTokens, e.cfg.CachedTokenWeight),
	}, in)
	if err != nil {
		log.Close()
		return nil, err
	}
	return &planner{Planner: pl, log: log}, nil
}

// refuse reports a refusal on stdout, logs it and returns the exit 8 error.
func refuse(e *env, flog core.EventLog, task string, r *farm.Refusal) error {
	suggest := r.Suggest
	if suggest == "" {
		suggest = fmt.Sprintf("jas run %q", task)
	}
	fmt.Printf("planner refused: %s — %s\n", core.Sanitize(e.red.Redact(r.Reason)), core.Sanitize(e.red.Redact(r.Detail)))
	fmt.Printf("try: %s\n", core.Sanitize(e.red.Redact(suggest)))
	_ = flog.Append(farm.EvFarmPlanRefused, map[string]any{"reason": r.Reason, "detail": r.Detail, "suggest": suggest})
	return &core.ExitError{Code: core.ExitRefused, Reason: "planner refused: " + r.Reason}
}

func planInvalid(res farm.PlannerResult, redact func(string) string) error {
	lines := strings.Split(strings.TrimRight(redact(farm.FormatProblems(res.Problems)), "\n"), "\n")
	for i, l := range lines {
		lines[i] = core.Sanitize(l)
	}
	return usageErr("planner produced an invalid plan:\n%s", strings.Join(lines, "\n"))
}

// finishPlan runs the owner review on a TTY, or writes the unapproved plan
// without one. Problems that survived the retry go to the owner on a TTY (the
// review blocks approval) and exit 2 without one. It returns the bytes written
// to -o (nil if none).
func finishPlan(ctx context.Context, e *env, pa planArgs, replan func(context.Context, string) (farm.PlannerResult, error),
	in farm.PlannerInput, flog core.EventLog, res farm.PlannerResult) ([]byte, bool, error) {
	if !e.interactive {
		if len(res.Problems) > 0 {
			return nil, false, planInvalid(res, e.red.Redact)
		}
		if err := writePlan(pa.out, res.Raw); err != nil {
			return nil, false, err
		}
		fmt.Printf("plan written to %s (not approved)\n", pa.out)
		return res.Raw, false, nil
	}
	r := reviewIO{
		In: e.stdin, Out: os.Stderr, Edit: runEditor, Now: time.Now, Redact: e.red.Redact,
		Recheck:  func(raw []byte) farm.PlannerResult { return farm.CheckPlan(raw, in) },
		Replan:   replan,
		OnRefuse: func(rf *farm.Refusal) error { return refuse(e, flog, pa.task, rf) },
	}
	final, approved, err := reviewPlan(ctx, r, res, pa.out)
	if err == nil && approved {
		fmt.Printf("plan approved and written to %s\n", pa.out)
	} else if err == nil {
		fmt.Println("nothing approved")
	}
	return final, approved, err
}

func writePlan(path string, b []byte) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	return nil
}
