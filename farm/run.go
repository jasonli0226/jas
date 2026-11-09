package farm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonli0226/jas/core"
)

// farm_ended reasons beyond the budget stops and part failure reasons.
const (
	ReasonOK          = "ok"
	ReasonCheckFailed = "check_failed"
	ReasonPartFailed  = "part_failed"
	ReasonPartDropped = "part_dropped"
	ReasonInterrupted = "interrupted"
	ReasonUsage       = "usage"
)

// Final choices.
const (
	ChoiceMerge   = "merge"
	ChoiceKeep    = "keep"
	ChoiceDiscard = "discard"

	byOwner     = "owner"
	byNoTTY     = "policy:no-tty"
	mergePhrase = "merge anyway"
	// maxChoicePrompts bounds the final-choice loop on unrecognised answers.
	maxChoicePrompts = 5
)

// finalChoices are the final prompt's answers; "m", "k" and "d" abbreviate them.
var finalChoices = []string{ChoiceMerge, ChoiceKeep, ChoiceDiscard}

const (
	planCopyFile = "plan.yaml"
	farmLogName  = "farm.jsonl"
	boardFile    = "board.jsonl"
	farmsDirName = "farms"
)

// errDirtyMain refuses the owner's merge into a main checkout with
// uncommitted changes.
var errDirtyMain = errors.New("main checkout has uncommitted changes; keeping the branches")

// staleTargetError refuses the owner's merge when the target branch has
// commits the integrate branch does not contain (it moved during the farm,
// or the plan's base was older): the check never ran on that combination.
type staleTargetError struct{ target, head, branch string }

func (e *staleTargetError) Error() string {
	return fmt.Sprintf("%s moved to %s since the check, which never ran on that combination; keeping the branches (merge %s yourself and run the check)",
		e.target, shortSHA(e.head), e.branch)
}

// shortSHA is the first 12 characters of a commit id.
func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// RunOptions is everything `jas farm run` decided before the farm starts.
type RunOptions struct {
	PlanPath, Home, Main string // Home is the jas home (~/.jas or JAS_HOME)
	JSON                 bool   // tee farm.jsonl (only) to Stdout
	AllowDirty           bool
	Stdout, Stderr       io.Writer
	// Ask puts a question to the owner on the TTY; nil or ok=false means no
	// TTY: failed parts follow on_failure and the final choice is keep.
	Ask func(prompt string, choices []string) (string, bool)

	Cfg     Config // trusted farm config (LoadConfig)
	Trusted bool   // the project trust decision, recorded in farm_started
	// NewSession builds a worker's or the integrator's session; see
	// Supervisor.NewSession.
	NewSession func(w WorkerSession) (*core.Session, error)
	Approvals  *Approvals      // shared TTY approvals; nil: approval level never
	Hard       context.Context // second Ctrl-C; nil: never
	Width      func() int      // terminal columns for progress lines; nil or 0: no clipping
	// Watch receives farm and worker events; set, it replaces the default
	// progress lines. JSON still prints farm.jsonl only.
	Watch Watcher
	// WorkerUI gives each session (a part, or "integrate") a UI that gets its
	// streamed text and log events; nil: quiet.
	WorkerUI func(part string) core.UI
	// Gates supplies each session's approval gate; nil: Approvals.
	Gates  func(part, approve string) (core.Gate, error)
	Policy core.ExecPolicy // base policy for workers
	Redact func(string) string
}

// RunFarm runs an approved plan end to end: refusal checks, parts, the
// integrator, the supervisor's check (the verdict) and the final choice. It
// returns the process exit code and the farm_ended reason; err is set for
// refusals before the farm starts (exit 2) and internal failures (exit 1).
func RunFarm(ctx context.Context, opts RunOptions) (exitCode int, reason string, err error) {
	h, err := StartFarm(ctx, opts)
	if err != nil {
		var ee *core.ExitError
		if errors.As(err, &ee) {
			return ee.Code, ReasonUsage, err
		}
		return core.ExitFailure, ReasonError, err
	}
	res := h.Wait()
	return res.Code, res.Reason, res.Err
}

func withDefaults(o RunOptions) RunOptions {
	if o.Stdout == nil {
		o.Stdout = io.Discard
	}
	if o.Stderr == nil {
		o.Stderr = io.Discard
	}
	if o.Hard == nil {
		o.Hard = context.Background()
	}
	return o
}

// preflighted is what the checks before any worktree established.
type preflighted struct {
	raw     []byte
	plan    Plan
	hash    string
	baseSHA string
	wtDir   string
}

func refuseRun(format string, a ...any) error {
	return &core.ExitError{Code: core.ExitUsage, Reason: fmt.Sprintf(format, a...)}
}

// preflight refuses (exit 2) before any worktree: unparsable, unapproved or
// edited plan, proposed roles, roles not in trusted config, an invalid plan,
// base not an ancestor of HEAD, a dirty tree, no check, a bad worktree dir.
func preflight(ctx context.Context, git GitRunner, o RunOptions) (preflighted, error) {
	var pre preflighted
	raw, err := os.ReadFile(o.PlanPath)
	if err != nil {
		return pre, refuseRun("read plan: %v", err)
	}
	plan, err := ParsePlan(raw)
	if err != nil {
		return pre, refuseRun("%v", err)
	}
	if plan.Approved == nil {
		return pre, refuseRun("plan is not approved: review it with jas farm plan on a terminal")
	}
	hash := CanonicalHash(raw)
	if hash != plan.Approved.SHA256 {
		return pre, refuseRun("plan hash mismatch: the plan changed after approval; approve it again")
	}
	if err := planRunnable(plan, o.Cfg); err != nil {
		return pre, err
	}
	base, err := resolveBase(ctx, git, o.Main, plan.Base)
	if err != nil {
		return pre, err
	}
	files, err := git.Run(ctx, o.Main, "ls-tree", "-r", "--name-only", base)
	if err != nil {
		return pre, err
	}
	if probs := runProblems(plan, o.Cfg, strings.Fields(files)); len(probs) > 0 {
		return pre, refuseRun("invalid plan:\n%s", FormatProblems(probs))
	}
	dirty, err := Dirty(ctx, git, o.Main)
	if err != nil {
		return pre, err
	}
	if dirty && !o.AllowDirty {
		return pre, refuseRun("working tree has uncommitted changes; commit them or pass --allow-dirty")
	}
	if dirty {
		fmt.Fprintln(o.Stderr, "jas: warning: uncommitted changes are not part of the farm")
	}
	wt, err := ResolveWorktreeDir(o.Cfg, o.Main, o.Home)
	if err != nil {
		return pre, refuseRun("%v", err)
	}
	return preflighted{raw: raw, plan: plan, hash: hash, baseSHA: base, wtDir: wt}, nil
}

// planRunnable checks what approval cannot vouch for: the plan's shape and
// the trusted config it runs under.
func planRunnable(plan Plan, cfg Config) error {
	if plan.Refuse != nil {
		return refuseRun("plan is a refusal (%s), not a plan", plan.Refuse.Reason)
	}
	if len(plan.ProposedRoles) > 0 {
		return refuseRun("proposed roles must be added to .jas/farm.yaml")
	}
	if len(plan.Parts) == 0 {
		return refuseRun("plan has no parts")
	}
	if strings.TrimSpace(cfg.Check) == "" {
		return refuseRun("no check command: set check: in .jas/farm.yaml (needs a trusted project) or in the jas home farm.yaml")
	}
	for _, p := range plan.Parts {
		if _, err := ResolveRole(cfg, p.Role, p.Owns); err != nil {
			return refuseRun("part %s: %v (roles come from trusted config only)", p.ID, err)
		}
	}
	return nil
}

// runProblems validates the plan at run time; ownership overlap counts only
// while split.disjoint_files is on.
func runProblems(plan Plan, cfg Config, files []string) []Problem {
	if cfg.Split.DisjointFiles {
		return Validate(plan, cfg, files)
	}
	probs, _ := validateStructure(plan, cfg)
	return probs
}

// resolveBase resolves base (default HEAD) and requires it to be HEAD or an
// ancestor of HEAD.
func resolveBase(ctx context.Context, git GitRunner, main, base string) (string, error) {
	if base == "" {
		base = "HEAD"
	}
	out, err := git.Run(ctx, main, "rev-parse", "--verify", "--quiet", base+"^{commit}")
	if err != nil {
		return "", refuseRun("plan base %s is not a commit in this repository", base)
	}
	sha := strings.TrimSpace(out)
	if _, err := git.Run(ctx, main, "merge-base", "--is-ancestor", sha, "HEAD"); err != nil {
		return "", refuseRun("plan base %s is not an ancestor of HEAD", base)
	}
	return sha, nil
}

// farmRun is one started farm.
type farmRun struct {
	o     RunOptions
	git   GitRunner
	pre   preflighted
	s     *Supervisor
	log   core.EventLog
	board *Board
	start time.Time

	branch      string      // the integrate branch, once integration ran
	hardStopped atomic.Bool // Handle.Stop(true): farm_ended carries hard
	notMerged   bool        // the owner asked to merge an unmet farm and did not confirm
}

// startFarm makes the farm dir, copies the plan, opens farm.jsonl (teed to
// stdout with --json) and the board, and logs farm_started.
func startFarm(git GitRunner, o RunOptions, pre preflighted) (*farmRun, error) {
	gen, err := ResolveGenerated(context.Background(), o.Cfg, git, o.Main, pre.baseSHA)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	id := NewFarmID(now)
	dir := filepath.Join(o.Home, farmsDirName, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, planCopyFile), pre.raw, 0o600); err != nil {
		return nil, fmt.Errorf("copy plan: %w", err)
	}
	var tee io.Writer
	if o.JSON {
		tee = o.Stdout
	}
	flog, err := core.OpenEventLog(filepath.Join(dir, farmLogName), tee, o.Redact)
	if err != nil {
		return nil, err
	}
	watch := o.Watch
	if watch == nil && !o.JSON {
		prog := NewProgress(o.Stdout, o.Redact)
		prog.Width = o.Width
		watch = prog
	}
	log := watchFarmLog(watch, idLog{EventLog: flog, id: id})
	board, err := OpenBoard(filepath.Join(dir, boardFile), o.Redact, o.Cfg.Board)
	if err != nil {
		flog.Close()
		return nil, err
	}
	var deny func(string) bool
	if o.Policy.Root != "" {
		deny = o.Policy.Denied
	}
	wt := &Worktrees{Git: git, Main: o.Main, Dir: pre.wtDir, FarmID: id, Copy: o.Cfg.Worktree.Copy, Setup: o.Cfg.Worktree.Setup, Deny: deny}
	s := &Supervisor{
		Plan: pre.plan, Cfg: o.Cfg, FarmID: id, Dir: dir, Main: o.Main, Git: git, WT: wt, Board: board, Log: log,
		Manifest:   &Manifest{FarmID: id, PlanSHA256: pre.hash, Base: pre.baseSHA},
		NewSession: o.NewSession, Ask: o.Ask, Pools: NewPools(o.Cfg), Hard: o.Hard,
		Policy: o.Policy, Approvals: o.Approvals, Redact: o.Redact, Watch: watch,
		WorkerUI: o.WorkerUI, Gates: o.Gates, Gen: gen,
	}
	r := &farmRun{o: o, git: git, pre: pre, s: s, log: log, board: board, start: now}
	s.event(EvFarmStarted, map[string]any{
		"plan_sha256": pre.hash, "base": pre.baseSHA, "roles_digest": RolesDigest(o.Cfg),
		"config_digest": ConfigDigest(o.Cfg), "trust": o.Trusted, "plan": filepath.Join(dir, planCopyFile),
		"farm_tokens": o.Cfg.Budget.FarmTokens,
	})
	return r, nil
}

func (r *farmRun) close() {
	_ = r.board.Close()
	_ = r.log.Close()
}

// run drives parts, integration, verdict and the final choice.
func (r *farmRun) run(ctx context.Context) (int, string, error) {
	s := r.s
	out, err := s.RunParts(ctx)
	if err != nil {
		return r.end(core.ExitFailure, ReasonError, err)
	}
	switch {
	case out.Interrupted || ctx.Err() != nil:
		return r.end(core.ExitInterrupted, ReasonInterrupted, nil)
	case out.BudgetStop != "":
		return r.end(core.ExitBudget, out.BudgetStop, nil)
	case s.stopFlag.Load(): // aborted by the owner or on_failure: abort
		return r.end(core.ExitUnmet, ReasonPartFailed, nil)
	case len(out.Done) == 0:
		return r.end(core.ExitUnmet, unmetParts(out), nil)
	}
	branch, ierr := Integrate(ctx, s, out.Done)
	r.branch = branch
	if ctx.Err() != nil || s.hard().Err() != nil {
		return r.end(core.ExitInterrupted, ReasonInterrupted, nil)
	}
	var ie *IntegrationError
	if ierr != nil && !errors.As(ierr, &ie) {
		return r.end(core.ExitFailure, ReasonError, ierr)
	}
	if ie != nil && ie.BudgetStop {
		return r.end(core.ExitBudget, stopTokens, nil)
	}
	reason := unmetParts(out)
	if ie != nil {
		reason = ReasonPartFailed
	} else {
		res := Verdict(ctx, s, s.WT.Path(integratePart))
		if ctx.Err() != nil || s.hard().Err() != nil {
			return r.end(core.ExitInterrupted, ReasonInterrupted, nil)
		}
		if reason == ReasonOK && res.ExitCode != 0 {
			reason = ReasonCheckFailed
		}
	}
	code := core.ExitOK
	if reason != ReasonOK {
		code = core.ExitUnmet
	}
	r.finalChoice(branch, reason)
	return r.end(code, reason, nil)
}

// unmetParts is part_failed, part_dropped or ok from the parts alone.
func unmetParts(out Outcome) string {
	switch {
	case len(out.Failed) > 0 || len(out.Paused) > 0:
		return ReasonPartFailed
	case len(out.Dropped) > 0:
		return ReasonPartDropped
	}
	return ReasonOK
}

// end writes the manifest's outcome and farm_ended.
func (r *farmRun) end(code int, reason string, err error) (int, string, error) {
	s := r.s
	status := map[int]string{core.ExitOK: "ok", core.ExitUnmet: "unmet", core.ExitBudget: "budget",
		core.ExitInterrupted: "interrupted"}[code]
	if status == "" {
		status = "failed"
	}
	totals := map[string]any{"status": status, "tokens": s.Pools.Farm.Used(),
		"duration_ms": time.Since(r.start).Milliseconds()}
	s.Manifest.Update(func(m *Manifest) {
		m.ExitCode, m.Reason, m.TotalTokens = code, reason, s.Pools.Farm.Used()
		counts := map[string]int{}
		for _, ps := range m.Parts {
			counts[ps.Status]++
		}
		for _, st := range []string{StatusDone, StatusFailed, StatusDropped, StatusPaused} {
			totals["parts_"+st] = counts[st]
		}
		if m.FinalChoice != "" {
			totals["final_choice"] = m.FinalChoice
		}
	})
	s.save()
	if err != nil {
		totals["error"] = err.Error()
	}
	if r.hardStopped.Load() {
		totals["hard"] = true
	}
	s.event(EvFarmEnded, FarmEnded(code, reason, totals))
	if serr := s.err(); serr != nil && err == nil {
		err = serr
		code, reason = core.ExitFailure, ReasonError
	}
	if !r.o.JSON {
		fmt.Fprintf(r.o.Stdout, "farm %s: %s (exit %d); log %s\n", s.FarmID, reason, code, filepath.Join(s.Dir, farmLogName))
	}
	return code, reason, err
}

// finalChoice asks the owner to merge, keep or discard, applies it, then
// removes the worktrees. Without a TTY the choice is keep.
func (r *farmRun) finalChoice(branch, reason string) {
	s := r.s
	choice, by := r.askChoice(branch, reason)
	var note, targetHead string
	if choice == ChoiceMerge {
		var stale *staleTargetError
		if err := r.ownerMerge(branch, reason); errors.Is(err, errDirtyMain) {
			note = err.Error()
			choice = ChoiceKeep
		} else if errors.As(err, &stale) {
			note, targetHead = err.Error(), stale.head
			choice = ChoiceKeep
		} else if err != nil {
			note = "merge failed, keeping the branches: " + err.Error()
			choice = ChoiceKeep
		}
	}
	if note != "" {
		fmt.Fprintf(r.o.Stderr, "jas: %s\n", note)
	} else if r.notMerged {
		note = "not merged" // askChoice already told the terminal
	}
	ev := map[string]any{"choice": choice, "by": by, "branch": branch}
	if note != "" {
		ev["note"] = note // the chat shows decisions only from events
	}
	if targetHead != "" {
		ev["target_head"] = targetHead
	}
	s.event(EvOwnerDecision, ev)
	s.Manifest.Update(func(m *Manifest) { m.FinalChoice = choice })
	s.save()
	r.removeWorktrees()
	if choice == ChoiceDiscard {
		r.deleteBranches()
	}
}

func (r *farmRun) askChoice(branch, reason string) (choice, by string) {
	if r.o.Ask == nil {
		return ChoiceKeep, byNoTTY
	}
	target := r.s.target
	prompt := fmt.Sprintf("farm %s: %s. [m]erge %s into %s, [k]eep unmerged (branches only, %s untouched), [d]iscard? ", r.s.FarmID, reason, branch, target, target)
	if _, stale, err := r.s.staleTarget(context.Background(), branch); err == nil && stale {
		prompt = fmt.Sprintf("farm %s: %s. %s moved since the check, so merging is refused. [k]eep unmerged (branches only), [d]iscard? ", r.s.FarmID, reason, target)
	}
	for range maxChoicePrompts {
		ans, ok := r.o.Ask(prompt, finalChoices)
		if !ok {
			return ChoiceKeep, byNoTTY
		}
		if strings.TrimSpace(ans) == "" {
			return ChoiceKeep, byOwner
		}
		c, _ := MatchChoice(ans, finalChoices) // any front end: never guess discard
		switch c {
		case ChoiceMerge:
			if reason == ReasonOK {
				return ChoiceMerge, byOwner
			}
			ans, ok := r.o.Ask(fmt.Sprintf("the farm is unmet (%s); type %q to merge it: ", reason, mergePhrase), nil)
			if ok && strings.TrimSpace(ans) == mergePhrase {
				return ChoiceMerge, byOwner
			}
			fmt.Fprintln(r.o.Stderr, "not merged")
			r.notMerged = true
		case ChoiceKeep:
			return ChoiceKeep, byOwner
		case ChoiceDiscard:
			return ChoiceDiscard, byOwner
		}
	}
	return ChoiceKeep, byOwner
}

// ownerMerge merges the integrate branch into the owner's branch in the main
// checkout with the owner's own git identity (not the farm's): a --no-ff
// merge, with a status trailer when the farm is unmet.
func (r *farmRun) ownerMerge(branch, reason string) error {
	s := r.s
	cur, err := s.Git.Run(context.Background(), s.Main, "symbolic-ref", "--quiet", "--short", "HEAD")
	cur = strings.TrimSpace(cur)
	if err != nil || cur != s.target {
		return fmt.Errorf("the main checkout is no longer on branch %s", s.target)
	}
	// The owner may have kept working in the main checkout during the farm.
	if dirty, err := Dirty(context.Background(), s.Git, s.Main); err != nil {
		return err
	} else if dirty {
		return errDirtyMain
	}
	if head, stale, err := s.staleTarget(context.Background(), branch); err != nil {
		return err
	} else if stale {
		return &staleTargetError{target: s.target, head: head, branch: branch}
	}
	msg := fmt.Sprintf("Merge jas farm %s\n\nJas-Farm: %s\n", s.FarmID, s.FarmID)
	if reason != ReasonOK {
		msg += fmt.Sprintf("Jas-Farm-Status: unmet (%s)\n", reason)
	}
	cmd := exec.Command("git", "merge", "--no-ff", "--no-verify", "--no-edit", "-m", msg, branch)
	cmd.Dir = s.Main
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		abort := exec.Command("git", "merge", "--abort")
		abort.Dir = s.Main
		_ = abort.Run()
		return fmt.Errorf("git merge: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

// removeWorktrees removes every part's and the integrator's worktree; the
// branches stay.
func (r *farmRun) removeWorktrees() {
	ctx := context.Background()
	for _, p := range append(r.s.Plan.Parts, Part{ID: integratePart}) {
		if _, err := os.Stat(r.s.WT.Path(p.ID)); err != nil {
			continue
		}
		if err := r.s.WT.Remove(ctx, p.ID); err != nil {
			fmt.Fprintf(r.o.Stderr, "jas: remove worktree %s: %v\n", p.ID, err)
		}
	}
}

// deleteBranches deletes every jas/farm/<id>/* branch.
func (r *farmRun) deleteBranches() {
	ctx := context.Background()
	out, err := r.git.Run(ctx, r.o.Main, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+branchPrefix+r.s.FarmID+"/")
	if err != nil {
		fmt.Fprintf(r.o.Stderr, "jas: list farm branches: %v\n", err)
		return
	}
	for _, br := range strings.Fields(out) {
		if _, err := r.git.Run(ctx, r.o.Main, "branch", "-D", br); err != nil {
			fmt.Fprintf(r.o.Stderr, "jas: delete branch %s: %v\n", br, err)
		}
	}
}

// ConfigDigest hashes the resolved farm config.
func ConfigDigest(cfg Config) string {
	b, _ := json.Marshal(cfg) // plain data: cannot fail
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// idLog adds farm_id to every farm.jsonl payload.
type idLog struct {
	core.EventLog
	id string
}

func (l idLog) with(data any) any {
	m, ok := data.(map[string]any)
	if !ok {
		return data
	}
	out := make(map[string]any, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	out["farm_id"] = l.id
	return out
}

func (l idLog) Append(typ string, data any) error { return l.EventLog.Append(typ, l.with(data)) }
func (l idLog) AppendSync(typ string, data any) error {
	return l.EventLog.AppendSync(typ, l.with(data))
}
func (l idLog) AppendID(typ string, data any) (string, error) {
	return l.EventLog.AppendID(typ, l.with(data))
}
