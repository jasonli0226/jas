package farm

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/sessions"
)

const (
	manifestFile = "manifest.json"
	partsDir     = "parts"
)

// Outcome is how RunParts ended. Paused lists parts that never finished:
// waiting on a failed dependency, never started, or stopped by an
// interrupt, an abort or a farm budget.
type Outcome struct {
	Done, Failed, Dropped, Paused []string
	BudgetStop                    string // "", "tokens", "timeout"
	Interrupted                   bool
}

// Supervisor runs a plan's parts in parallel worktrees.
type Supervisor struct {
	Plan     Plan
	Cfg      Config
	FarmID   string
	Dir      string // ~/.jas/farms/<farm-id>
	Main     string // main checkout
	Git      GitRunner
	WT       *Worktrees
	Board    *Board
	Log      core.EventLog // farm.jsonl
	Manifest *Manifest     // saved to <Dir>/manifest.json on every change

	// NewSession builds one worker's session from w (see WorkerSession):
	// typically w.Config(provider, model), then the CLI's Budget, UI,
	// MaxTurns, redaction, and the trusted project context prepended to
	// w.System. It must not start the conversation; the supervisor sends the
	// first message, closes the session and closes w.Log.
	NewSession func(w WorkerSession) (*core.Session, error)
	// Ask puts a question to the owner on the TTY; ok=false means no TTY,
	// and on_failure decides.
	Ask   func(prompt string, choices []string) (string, bool)
	Pools Pools

	// Hard cancels everything (second Ctrl-C); nil means never. The ctx
	// given to RunParts is the soft stop (first Ctrl-C).
	Hard   context.Context
	Policy core.ExecPolicy // base worker policy; see WorkerPolicy
	Broker *Broker         // every part's approval gate; nil: unattended at level never
	Redact func(string) string
	Watch  Watcher // nil: no progress lines (--json, tests)
	// WorkerUI: see RunOptions.
	WorkerUI func(part string) core.UI

	// mergeCheck is a passing integrate_check on a clean merge that nothing
	// changed afterwards; Verdict reuses it instead of running check again.
	mergeCheck     *core.CheckResult
	mergeCheckHead string // the commit mergeCheck checked
	Clock          Clock  // rate limiter clock; nil is real time
	// Gen is the run's generated files (ResolveGenerated): undone in part
	// worktrees outside a part's owns, regenerated on the merged tree.
	Gen []Generated

	ctl      controls // the owner's live controls (Handle)
	soft     context.Context
	stopFlag atomic.Bool // set: workers' next requests are refused
	limiter  *Limiter
	createMu sync.Mutex
	baseSHA  string
	target   string // main checkout's branch, for session records
	recDir   string // session records dir; "" when unavailable

	errMu   sync.Mutex
	saveErr error
}

// sched is the scheduler's own state; only RunParts' goroutine touches it.
type sched struct {
	status   map[string]string
	attempts map[string]int
	retry    map[string]string // part -> why its last attempt failed
	running  int
	stopping bool
	dropping map[string]string // running parts the owner dropped -> the decision's source
	asking   int               // failure questions waiting for the owner
	out      Outcome
}

// RunParts runs every part in DAG order, at most split.max_workers at once,
// and returns when no part is running and none can start. Cancelling ctx is
// the first Ctrl-C: nothing new starts and running workers stop after their
// current request. Cancelling s.Hard cancels the workers at once.
func (s *Supervisor) RunParts(ctx context.Context) (Outcome, error) {
	if err := s.init(ctx); err != nil {
		return Outcome{}, err
	}
	s.ctl.init()
	defer close(s.ctl.ended)
	s.soft = ctx
	farmCtx, cancelFarm := withTimeout(s.hard(), s.Cfg.Budget.FarmTimeout)
	defer cancelFarm()
	runCtx, abort := context.WithCancel(farmCtx)
	defer abort()

	st := &sched{status: map[string]string{}, attempts: map[string]int{}, retry: map[string]string{}}
	for _, p := range s.Plan.Parts {
		st.status[p.ID] = StatusQueued
	}
	results := make(chan attemptEnd)
	softDone, farmDone := ctx.Done(), farmCtx.Done()
	for {
		if !st.stopping {
			s.startReady(runCtx, st, results)
		}
		// A pending failure question keeps the farm alive, unless it is
		// stopping: then nobody waits for the answer.
		if st.running == 0 && (st.asking == 0 || st.stopping) {
			break
		}
		select {
		case e := <-results:
			st.running--
			s.handle(st, e, abort)
		case fn := <-s.ctl.cmds:
			fn(st)
		case <-softDone:
			softDone = nil
			st.out.Interrupted = true
			s.stop(st)
		case <-farmDone:
			farmDone = nil
			if errors.Is(farmCtx.Err(), context.DeadlineExceeded) {
				s.budgetStop(st, stopTimeout, "")
			} else {
				st.out.Interrupted = true
			}
			s.stop(st)
		}
	}
	// The last worker may end before the select sees the deadline or cancel.
	if errors.Is(farmCtx.Err(), context.DeadlineExceeded) {
		s.budgetStop(st, stopTimeout, "")
	}
	if ctx.Err() != nil || s.hard().Err() != nil {
		st.out.Interrupted = true
	}
	s.save()
	return s.outcome(st), s.err()
}

// init fills in derived state and the manifest's part entries.
func (s *Supervisor) init(ctx context.Context) error {
	if s.NewSession == nil || s.WT == nil || s.Board == nil || s.Log == nil || s.Manifest == nil || s.Git == nil {
		return errors.New("farm supervisor: NewSession, WT, Board, Log, Manifest and Git are required")
	}
	base := s.Manifest.Base
	if base == "" {
		base = s.Plan.Base
	}
	if base == "" {
		base = "HEAD"
	}
	sha, err := s.Git.Run(ctx, s.Main, "rev-parse", "--verify", base+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve base %s: %w", base, err)
	}
	s.baseSHA = strings.TrimSpace(sha)
	if br, err := s.Git.Run(ctx, s.Main, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		s.target = strings.TrimSpace(br)
	}
	if err := Prune(ctx, s.Git, s.Main); err != nil {
		return err
	}
	if s.Pools.Farm == nil {
		s.Pools = NewPools(s.Cfg)
	}
	s.limiter = NewLimiter(s.Cfg.Budget.RequestsPerMinute, s.Clock)
	// Session records are advisory: without the directory workers run
	// unrecorded.
	if dir, err := sessions.Dir(s.Main); err == nil {
		s.recDir = dir
	}
	s.Board.SetOnPost(s.boardPosted)
	s.Manifest.Update(func(m *Manifest) {
		if m.Parts == nil {
			m.Parts = map[string]*PartState{}
		}
		for _, p := range s.Plan.Parts {
			if m.Parts[p.ID] == nil {
				m.Parts[p.ID] = &PartState{Role: p.Role, Branch: s.WT.Branch(p.ID), Status: StatusQueued}
			}
		}
	})
	s.save()
	return nil
}

// startReady starts queued parts whose deps are done, in plan order, while
// there is a free worker slot.
func (s *Supervisor) startReady(ctx context.Context, st *sched, results chan<- attemptEnd) {
	for _, p := range s.Plan.Parts {
		if st.running >= max(s.Cfg.Split.MaxWorkers, 1) {
			return
		}
		if st.status[p.ID] != StatusQueued || !s.depsDone(st, p) {
			continue
		}
		st.status[p.ID] = StatusRunning
		st.attempts[p.ID]++
		st.running++
		s.setPart(p.ID, func(ps *PartState) { ps.Status, ps.FailureReason, ps.ExitCode = StatusRunning, "", 0 })
		pctx, cancel := context.WithCancel(ctx)
		s.ctl.setCancel(p.ID, cancel)
		go func(p Part, n int, note string) {
			defer cancel()
			results <- s.runAttempt(pctx, p, n, note)
		}(p, st.attempts[p.ID], st.retry[p.ID])
	}
}

func (s *Supervisor) depsDone(st *sched, p Part) bool {
	for _, d := range p.After {
		if st.status[d] != StatusDone {
			return false
		}
	}
	return true
}

// handle records one attempt's end and applies the failure policy.
func (s *Supervisor) handle(st *sched, e attemptEnd, abort context.CancelFunc) {
	s.ctl.setCancel(e.id, nil)
	if source, ok := st.dropping[e.id]; ok {
		delete(st.dropping, e.id)
		st.status[e.id] = StatusPaused // no longer running; dropped below
		s.event(EvPartEnded, map[string]any{"part": e.id, "exit_code": e.exitCode, "status": StatusDropped})
		s.dropped(st, e.id, source)
		return
	}
	ended := map[string]any{"part": e.id, "exit_code": e.exitCode}
	switch e.kind {
	case endDone:
		st.status[e.id] = StatusDone
	case endStopped, endFarmStop:
		st.status[e.id] = StatusPaused
		if e.kind == endFarmStop {
			s.budgetStop(st, stopTokens, e.detail)
			s.stop(st)
		}
	case endFailed:
		st.status[e.id] = StatusFailed
		ended["reason"] = e.reason
		if e.detail != "" {
			ended["detail"] = e.detail
		}
	}
	ended["status"] = st.status[e.id]
	s.setPart(e.id, func(ps *PartState) {
		ps.Status, ps.ExitCode = st.status[e.id], e.exitCode
		if e.kind == endFailed {
			ps.FailureReason = e.reason
		}
		ended["tokens"], ended["diffstat"] = ps.Tokens, ps.Diffstat
	})
	s.event(EvPartEnded, ended)
	if e.kind == endFailed {
		s.failed(st, e, abort)
	}
}

// failed pauses the part's dependents, then retries, drops or aborts.
func (s *Supervisor) failed(st *sched, e attemptEnd, abort context.CancelFunc) {
	deps := dependents(s.Plan, e.id)
	s.setStatus(st, deps, StatusQueued, StatusPaused)
	if st.stopping {
		return
	}
	if s.Ask == nil { // policy decides at once
		choice, source := s.decide(e)
		s.applyChoice(st, e, choice, source, abort)
		return
	}
	// The owner may take a while: ask off the scheduler, so parts that do not
	// depend on the answer keep running, and apply the answer back here.
	st.asking++
	go func() {
		choice, source := s.decide(e)
		select {
		case s.ctl.cmds <- func(st *sched) {
			st.asking--
			if !st.stopping {
				s.applyChoice(st, e, choice, source, abort)
			}
		}:
		case <-s.ctl.ended:
		}
	}()
}

// applyChoice records and applies the owner's (or policy's) choice for a
// failed part.
func (s *Supervisor) applyChoice(st *sched, e attemptEnd, choice, source string, abort context.CancelFunc) {
	deps := dependents(s.Plan, e.id)
	s.event(EvOwnerDecision, map[string]any{"part": e.id, "choice": choice, "source": source})
	switch choice {
	case choiceRetry:
		st.retry[e.id] = e.reason + ": " + e.detail
		s.setStatus(st, []string{e.id}, StatusFailed, StatusQueued)
		s.setStatus(st, deps, StatusPaused, StatusQueued)
	case choiceDrop:
		s.setStatus(st, append([]string{e.id}, deps...), "", StatusDropped)
	case choiceAbort:
		s.stop(st)
		abort()
	}
}

// setStatus moves each listed part from status from ("" = any not yet
// finished, plus failed) to to.
func (s *Supervisor) setStatus(st *sched, ids []string, from, to string) {
	for _, id := range ids {
		cur := st.status[id]
		ok := cur == from
		if from == "" {
			ok = cur != StatusDone && cur != StatusRunning && cur != StatusDropped
		}
		if !ok {
			continue
		}
		st.status[id] = to
		s.setPart(id, func(ps *PartState) { ps.Status = to })
	}
}

// stop ends scheduling and makes running workers stop after their current
// request; approvals held for nobody at the terminal are refused.
func (s *Supervisor) stop(st *sched) {
	st.stopping = true
	s.stopFlag.Store(true)
	s.ctl.stop()
	if s.Broker != nil {
		s.Broker.Stop()
	}
}

func (s *Supervisor) budgetStop(st *sched, reason, detail string) {
	if st.out.BudgetStop != "" {
		return
	}
	st.out.BudgetStop = reason
	ev := map[string]any{"reason": reason}
	if detail != "" {
		ev["detail"] = detail
	}
	s.event(EvBudgetStop, ev)
}

// stopping reports whether the farm (not the part) is ending sessions.
func (s *Supervisor) stopping(runCtx context.Context) bool {
	return s.stopFlag.Load() || runCtx.Err() != nil || (s.soft != nil && s.soft.Err() != nil)
}

func (s *Supervisor) outcome(st *sched) Outcome {
	out := st.out
	for _, p := range s.Plan.Parts {
		switch st.status[p.ID] {
		case StatusDone:
			out.Done = append(out.Done, p.ID)
		case StatusFailed:
			out.Failed = append(out.Failed, p.ID)
		case StatusDropped:
			out.Dropped = append(out.Dropped, p.ID)
		default:
			out.Paused = append(out.Paused, p.ID)
		}
	}
	return out
}

// wrap is WorkerSession.Wrap: the shared rate limiter around the stop gate,
// so a request that waited for its slot is still refused after a stop.
func (s *Supervisor) wrap(p providers.Provider) providers.Provider {
	var out providers.Provider = stopGate{p: p, s: s}
	if s.limiter != nil {
		out = RateLimited{Provider: out, Limiter: s.limiter}
	}
	return out
}

// stopGate refuses new requests once the farm stops, so a running worker
// ends after its in-flight request instead of mid-stream.
type stopGate struct {
	p providers.Provider
	s *Supervisor
}

func (g stopGate) Stream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, <-chan error) {
	if g.s.stopFlag.Load() || (g.s.soft != nil && g.s.soft.Err() != nil) {
		return failedStream(context.Canceled)
	}
	return g.p.Stream(ctx, req)
}

func (s *Supervisor) hard() context.Context {
	if s.Hard == nil {
		return context.Background()
	}
	return s.Hard
}

func withTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// setPart changes one part's manifest entry and saves the manifest.
func (s *Supervisor) setPart(id string, fn func(*PartState)) {
	s.Manifest.Update(func(m *Manifest) {
		if ps := m.Parts[id]; ps != nil {
			fn(ps)
		}
		m.TotalTokens = s.Pools.Farm.Used()
	})
	s.save()
}

func (s *Supervisor) save() {
	if err := s.Manifest.Save(filepath.Join(s.Dir, manifestFile)); err != nil {
		s.noteErr(err)
	}
}

// event appends to farm.jsonl; a failure is returned from RunParts.
func (s *Supervisor) event(typ string, data map[string]any) {
	if err := s.Log.Append(typ, data); err != nil {
		s.noteErr(fmt.Errorf("farm log: %w", err))
	}
}

func (s *Supervisor) noteErr(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.saveErr == nil {
		s.saveErr = err
	}
}

func (s *Supervisor) err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.saveErr
}
