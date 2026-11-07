package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/tui"
)

// chatFarmer is the chat's farm port (tui.Farmer) over the same planner,
// review checks and supervisor as jas farm.
type chatFarmer struct {
	b *chatBackend

	mu    sync.Mutex
	plans map[string]*chatPlan // by farm id, until approved or discarded
}

// chatPlan is a draft under review in the chat.
type chatPlan struct {
	e    *env
	pl   *planner
	in   farm.PlannerInput
	flog core.EventLog
	dir  string
	res  farm.PlannerResult
	task string
}

var _ tui.Farmer = (*chatFarmer)(nil)

// Farmer returns the chat's farm port.
func (b *chatBackend) Farmer() tui.Farmer {
	b.farmerOnce.Do(func() { b.farmer = &chatFarmer{b: b, plans: map[string]*chatPlan{}} })
	return b.farmer
}

// Available reports whether this project can run a farm: trusted, with a
// check that resolves.
func (f *chatFarmer) Available() (bool, string) {
	e := f.b.e
	if !e.pc.trusted {
		return false, "project not trusted: farm config and roles need a trusted project (restart with jas --trust-project)"
	}
	cfg, _, err := farm.LoadConfig(e.root, e.home, true)
	if err != nil {
		return false, "farm config: " + err.Error()
	}
	if strings.TrimSpace(cfg.Check) == "" {
		return false, noCheckMsg(e)
	}
	return true, ""
}

// TreeReady reports whether the working tree can be planned: a farm branches
// from HEAD, so the repository needs a commit and no uncommitted changes. It
// runs git, so it is called off the chat's event loop (the suggest_farm tool).
func (f *chatFarmer) TreeReady(ctx context.Context) (bool, string) {
	root, git := f.b.e.root, farm.NewGit()
	if _, err := git.Run(ctx, root, "rev-parse", "--verify", "HEAD"); err != nil {
		return false, "the repository has no commit yet: a farm branches from HEAD; " +
			"offer to commit the project (with the owner's ok), then call suggest_farm again"
	}
	dirty, err := farm.Dirty(ctx, git, root)
	if err != nil {
		return false, "git status: " + err.Error()
	}
	if dirty {
		return false, "the working tree has uncommitted changes (a farm plans from HEAD, so they would be left out); " +
			"offer to commit them (with the owner's ok), then call suggest_farm again"
	}
	return true, ""
}

// farmEnv is the env for a farm on model: the provider it resolves to and
// its bare id, as the chat's own sessions use.
func (f *chatFarmer) farmEnv(model string) (*env, error) {
	name, baseURL, bare, err := f.b.providerFor(model)
	if err != nil {
		return nil, err
	}
	prov, err := f.b.provider(name, baseURL)
	if err != nil {
		return nil, err
	}
	e := *f.b.e
	e.provider, e.cfg.Model, e.interactive = prov, bare, false
	return &e, nil
}

// Plan drafts a plan for task, as jas farm plan does up to the review.
func (f *chatFarmer) Plan(ctx context.Context, task, model string) (tui.FarmPlan, error) {
	e, err := f.farmEnv(model)
	if err != nil {
		return tui.FarmPlan{}, err
	}
	pf, err := farmPreflight(ctx, e, planArgs{task: task})
	if err != nil {
		return tui.FarmPlan{}, err
	}
	id := farm.NewFarmID(time.Now())
	dir := filepath.Join(e.home, farmsDir, id)
	flog, err := core.OpenEventLog(filepath.Join(dir, farmLogFile), nil, e.red.Redact)
	if err != nil {
		return tui.FarmPlan{}, err
	}
	roles, _ := farm.Available(pf.cfg)
	in := farm.PlannerInput{Task: task, Base: pf.base, Files: pf.files, Cfg: pf.cfg, Roles: roles}
	pl, err := startPlanner(ctx, e, pf.cfg, in, id, filepath.Join(dir, plannerDir, eventsFile))
	if err != nil {
		flog.Close()
		return tui.FarmPlan{}, err
	}
	cp := &chatPlan{e: e, pl: pl, in: in, flog: flog, dir: dir, task: task}
	res, err := pl.Draft(ctx)
	if err != nil {
		cp.close(core.ExitFailure)
		return tui.FarmPlan{}, err
	}
	p := cp.set(id, res)
	p.Summary = append(pf.warnings, p.Summary...)
	if res.Refusal != nil {
		cp.refused(res.Refusal)
		cp.close(core.ExitRefused)
		return p, nil
	}
	f.mu.Lock()
	f.plans[id] = cp
	f.mu.Unlock()
	return p, nil
}

// set records res as the current draft and renders it for the chat.
func (cp *chatPlan) set(id string, res farm.PlannerResult) tui.FarmPlan {
	cp.res = res
	clean := func(s string) string { return core.Sanitize(cp.e.red.Redact(s)) }
	var buf bytes.Buffer
	showPlan(&buf, res, clean)
	p := tui.FarmPlan{ID: id, Task: cp.task, Raw: res.Raw,
		Summary: strings.Split(strings.Trim(buf.String(), "\n"), "\n")}
	for _, pr := range res.Problems {
		p.Problems = append(p.Problems, clean(pr.Message))
	}
	if r := res.Refusal; r != nil {
		p.Refusal, p.Suggest = clean(r.Reason), clean(r.Suggest)
		if p.Suggest == "" {
			p.Suggest = fmt.Sprintf("run it in the chat: %s", clean(cp.task))
		}
	}
	return p
}

func (cp *chatPlan) refused(r *farm.Refusal) {
	_ = cp.flog.Append(farm.EvFarmPlanRefused, map[string]any{"reason": r.Reason, "detail": r.Detail, "suggest": r.Suggest})
}

func (cp *chatPlan) close(code int) {
	_ = cp.pl.Close(closeStatus(code), code)
	_ = cp.flog.Close()
}

func (f *chatFarmer) plan(id string) (*chatPlan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := f.plans[id]
	if cp == nil {
		return nil, errors.New("no plan under review")
	}
	return cp, nil
}

func (f *chatFarmer) forget(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.plans, id)
}

// Replan sends feedback to the planner; a refusal ends the review.
func (f *chatFarmer) Replan(ctx context.Context, p tui.FarmPlan, feedback string) (tui.FarmPlan, error) {
	cp, err := f.plan(p.ID)
	if err != nil {
		return p, err
	}
	res, err := cp.pl.Revise(ctx, feedback)
	if err != nil {
		return p, err
	}
	next := cp.set(p.ID, res)
	if res.Refusal != nil {
		cp.refused(res.Refusal)
		cp.close(core.ExitRefused)
		f.forget(p.ID)
	}
	return next, nil
}

// Recheck re-validates plan bytes the owner edited.
func (f *chatFarmer) Recheck(p tui.FarmPlan, raw []byte) tui.FarmPlan {
	cp, err := f.plan(p.ID)
	if err != nil {
		p.Problems = []string{err.Error()}
		return p
	}
	return cp.set(p.ID, farm.CheckPlan(raw, cp.in))
}

// Approve writes the approved plan into the farm dir (never the repo).
func (f *chatFarmer) Approve(p tui.FarmPlan) (string, error) {
	if p.Refusal != "" || len(p.Problems) > 0 {
		return "", errors.New("cannot approve: the plan has problems; edit or replan")
	}
	cp, err := f.plan(p.ID)
	if err != nil {
		return "", err
	}
	final := farm.Approve(cp.res.Raw, approverOwner, time.Now())
	path := filepath.Join(cp.dir, farmPlanFile)
	if err := os.WriteFile(path, final, 0o644); err != nil {
		return "", fmt.Errorf("write plan: %w", err)
	}
	cp.close(core.ExitOK)
	f.forget(p.ID)
	return path, nil
}

// Discard closes a plan's planner without approving it.
func (f *chatFarmer) Discard(p tui.FarmPlan) {
	if cp, err := f.plan(p.ID); err == nil {
		cp.close(core.ExitOK)
		f.forget(p.ID)
	}
}

// Start runs an approved plan as jas farm run does, reporting through h.
func (f *chatFarmer) Start(ctx context.Context, planPath, model string, level core.Level, h tui.FarmHooks) (tui.FarmRun, error) {
	e, err := f.farmEnv(model)
	if err != nil {
		return nil, err
	}
	cfg, _, err := farm.LoadConfig(e.root, e.home, e.pc.trusted)
	if err != nil {
		return nil, usageErr("farm config: %v", err)
	}
	opts := farm.RunOptions{
		PlanPath: planPath, Home: e.home, Main: e.root, Stdout: io.Discard, Stderr: io.Discard,
		Ask: h.Ask, Cfg: cfg, Trusted: e.pc.trusted, NewSession: farmSession(e),
		Policy: e.policy, Redact: e.red.Redact, Watch: hookWatcher{h},
		Approvals: farm.NewApprovals(strings.NewReader(""), io.Discard, false, level),
	}
	if h.Worker != nil {
		opts.WorkerUI = func(part string) core.UI { return core.NewRedactingUI(h.Worker(part), e.red.Redact) }
	}
	if h.Gate != nil {
		opts.Gates = h.Gate
	}
	run, err := farm.StartFarm(ctx, opts)
	if err != nil {
		return nil, err // never a nil *Handle inside a non-nil FarmRun
	}
	return run, nil
}

// hookWatcher forwards farm events to the chat's hooks; workers report
// through their own UIs.
type hookWatcher struct{ h tui.FarmHooks }

func (w hookWatcher) FarmEvent(typ string, m map[string]any) {
	if w.h.Event != nil {
		w.h.Event(typ, m)
	}
}

func (w hookWatcher) WorkerEvent(string, string, map[string]any) {}

func (w hookWatcher) SteerUndelivered(part string, texts []string) {
	if w.h.Undelivered != nil {
		w.h.Undelivered(part, texts)
	}
}
