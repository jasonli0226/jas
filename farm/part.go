package farm

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/sessions"
)

const (
	eventsFile       = "events.jsonl"
	workerRecordMode = "farm-worker"
	supervisorRole   = "supervisor"
)

// worker is one running attempt of a part.
type worker struct {
	part  Part
	role  Role
	path  string // worktree
	start string // worktree HEAD before the worker ran (base + merged deps)
	pol   core.ExecPolicy
	pool  *core.TokenPool
	log   core.EventLog
	sess  *core.Session
	rec   *sessions.Handle
}

// runAttempt prepares the part's worktree, runs its worker to the end and
// finishes it. It runs on its own goroutine.
func (s *Supervisor) runAttempt(ctx context.Context, part Part, attempt int, note string) attemptEnd {
	role, err := ResolveRole(s.Cfg, part.Role, part.Owns)
	if err != nil {
		return failed(part.ID, ReasonError, core.ExitFailure, err.Error())
	}
	w := &worker{part: part, role: role}
	if end, ok := s.prepare(ctx, w, attempt); !ok {
		return end
	}
	err = s.openWorker(w, attempt)
	defer s.closeWorker(w) // also releases what a failed open left behind
	if err != nil {
		return failed(part.ID, ReasonError, core.ExitFailure, err.Error())
	}

	pctx, cancel := withTimeout(ctx, s.Cfg.Budget.PerWorkerTimeout)
	defer cancel()
	res, sendErr := w.sess.Send(pctx, firstMessage(part, note))
	farmPools := map[string]bool{poolFarm: true, poolWork: true}
	end := classifyTurn(part.ID, res, farmPools, s.stopping(ctx))
	if sendErr != nil && end.kind == endDone {
		end = failed(part.ID, ReasonError, core.ExitFailure, sendErr.Error())
	}
	if end.kind != endDone {
		_ = w.sess.Close(res.Status, res.Code)
		return end
	}
	return s.finishDone(w, res.Final)
}

// prepare makes the worktree (serially across parts), copies files and runs
// setup. A retry starts from a fresh branch.
func (s *Supervisor) prepare(ctx context.Context, w *worker, attempt int) (attemptEnd, bool) {
	id := w.part.ID
	s.createMu.Lock()
	if attempt > 1 {
		_ = s.WT.Remove(ctx, id) // may already be gone
		_, _ = s.Git.Run(ctx, s.Main, "branch", "-D", s.WT.Branch(id))
	}
	path, _, err := s.WT.Create(ctx, id, s.baseSHA, w.part.After)
	if err == nil {
		err = s.WT.CopyFiles(path)
	}
	s.createMu.Unlock()
	if err != nil {
		return failed(id, prepareReason(err), core.ExitFailure, err.Error()), false
	}
	w.path = path
	head, err := s.Git.Run(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return failed(id, ReasonSetup, core.ExitFailure, err.Error()), false
	}
	w.start = strings.TrimSpace(head)
	s.setPart(id, func(ps *PartState) { ps.Worktree = path })
	res, err := s.WT.RunSetup(ctx, path)
	if strings.TrimSpace(s.WT.Setup) != "" {
		s.event(EvPartSetup, map[string]any{"part": id, "exit_code": res.ExitCode,
			"duration_ms": res.Duration.Milliseconds(), "timed_out": res.TimedOut})
	}
	if err != nil {
		return failed(id, ReasonSetup, core.ExitFailure, err.Error()), false
	}
	return attemptEnd{}, true
}

// openWorker opens the part's log, record and session.
func (s *Supervisor) openWorker(w *worker, attempt int) error {
	id := w.part.ID
	name := eventsFile
	if attempt > 1 {
		name = fmt.Sprintf("events-%d.jsonl", attempt)
	}
	logPath := filepath.Join(s.Dir, partsDir, id, name)
	log, err := core.OpenEventLog(logPath, nil, s.Redact)
	if err != nil {
		return err
	}
	w.log = log
	sid := core.NewSessionID(time.Now())
	gate, err := s.gate(id, w.role.Approve)
	if err != nil {
		return err
	}
	toolset, err := workerTools(w.role, PostTool{Board: s.Board, Part: id, Role: w.role.Name, SessionID: sid})
	if err != nil {
		return err
	}
	w.pol = WorkerPolicy(s.Policy, w.role, w.path)
	w.pool = s.Pools.Part(id, w.role.Budget)
	w.rec = s.register(w, sid, logPath)
	ws := WorkerSession{
		Part: w.part, Role: w.role, Root: w.path, System: WorkerPrompt(s.Plan, w.part, w.role, s.baseSHA),
		SessionID: sid, Log: log, Pool: w.pool, Inject: s.inject(w.part),
		Policy: w.pol, Meta: WorkerMeta(s.FarmID, id, s.baseSHA, s.Manifest.PlanSHA256),
		Tools: toolset, Gate: gate, Touched: touchHook(w.rec, w.path),
		MaxOutput: s.Cfg.Budget.MaxOutputTokens, Wrap: s.wrap,
	}
	sess, err := s.NewSession(ws)
	if err != nil {
		return fmt.Errorf("start worker session: %w", err)
	}
	w.sess = sess
	s.setPart(id, func(ps *PartState) { ps.SessionID, ps.Log = sid, logPath })
	s.event(EvPartStarted, map[string]any{"part": id, "role": w.role.Name, "attempt": attempt,
		"branch": s.WT.Branch(id), "worktree": w.path, "session_id": sid, "log": logPath})
	return nil
}

// closeWorker records tokens and undelivered posts, then releases the
// worker's log and record.
func (s *Supervisor) closeWorker(w *worker) {
	if w.sess != nil {
		_ = w.sess.Close("failed", core.ExitFailure) // no-op when already closed
	}
	undelivered := s.Board.Undelivered(w.part.ID)
	used := 0
	if w.pool != nil {
		used = w.pool.Used()
	}
	s.setPart(w.part.ID, func(ps *PartState) {
		ps.Tokens += used
		ps.Undelivered = undelivered
	})
	_ = w.rec.Close()
	if w.log != nil {
		if err := w.log.Close(); err != nil {
			s.noteErr(fmt.Errorf("part %s log: %w", w.part.ID, err))
		}
	}
}

// finishDone closes a worker that gave its final answer, commits its work,
// checks the write limit and posts done.
func (s *Supervisor) finishDone(w *worker, final string) attemptEnd {
	id := w.part.ID
	if steer := w.sess.TakeSteering(); len(steer) > 0 {
		s.event(EvOwnerDecision, map[string]any{"part": id, "choice": choiceSteerUndelivered, "count": len(steer)})
	}
	if err := w.sess.Close("ok", core.ExitOK); err != nil {
		return failed(id, ReasonError, core.ExitFailure, err.Error())
	}
	// The work is finished: commit it even if the farm is being stopped.
	ctx := s.hard()
	msg := fmt.Sprintf("farm %s part %s: %s", s.FarmID, id, firstLine(w.part.Goal))
	if _, err := s.WT.Commit(ctx, w.path, id, msg); err != nil {
		return failed(id, ReasonError, core.ExitFailure, err.Error())
	}
	viol, err := DiffViolations(ctx, s.Git.Run, w.path, w.start, w.pol.Writes)
	if err != nil {
		return failed(id, ReasonError, core.ExitFailure, err.Error())
	}
	if len(viol) > 0 {
		return failed(id, ReasonWriteLimit, core.ExitFailure, "wrote outside its limit: "+strings.Join(viol, ", "))
	}
	stat, err := s.Git.Run(ctx, w.path, "diff", "--shortstat", w.start, "HEAD")
	if err != nil {
		return failed(id, ReasonError, core.ExitFailure, err.Error())
	}
	stat = strings.TrimSpace(stat)
	s.setPart(id, func(ps *PartState) { ps.Diffstat = stat })
	s.postDone(id, final, stat)
	return attemptEnd{id: id, kind: endDone}
}

// postDone auto-posts the part's done entry. A rejected post (a config with
// tiny post_chars) is logged and does not fail the part.
func (s *Supervisor) postDone(id, final, stat string) {
	room := s.Cfg.Board.PostChars - len([]rune(stat))
	summary := clipRunes(strings.TrimSpace(final), min(summaryRunes, room))
	if summary == "" {
		summary = "done"
	}
	p, err := s.Board.PostFrom(id, supervisorRole, TopicDone, map[string]any{"summary": summary, "diffstat": stat}, "", "")
	if err != nil {
		s.event(EvBoardPosted, map[string]any{"part": id, "topic": TopicDone, "error": err.Error()})
		return
	}
	s.event(EvBoardPosted, map[string]any{"part": id, "topic": TopicDone, "seq": p.Seq})
}

// inject delivers relevant board posts at each tool-results boundary.
func (s *Supervisor) inject(part Part) func() (string, map[string]any) {
	rel := relevance(s.Plan, part)
	return func() (string, map[string]any) {
		text, seqs, truncated := s.Board.Deliver(part.ID, rel)
		if len(seqs) == 0 {
			return "", nil
		}
		s.event(EvBoardDelivered, map[string]any{"part": part.ID, "seqs": seqs, "truncated": truncated})
		return fenceBoard(text), map[string]any{"board_seqs": seqs}
	}
}

// gate is the part's approval gate. Without shared approvals there is no
// TTY: gated calls end the part with approval_needed.
func (s *Supervisor) gate(part, approve string) (core.Gate, error) {
	if s.Approvals != nil {
		return s.Approvals.Gate(part, approve)
	}
	return NewApprovals(strings.NewReader(""), io.Discard, false, core.Never).Gate(part, approve)
}

// register writes the worker's session record. Records are advisory, so a
// failure leaves the worker unrecorded (a nil handle is a no-op).
func (s *Supervisor) register(w *worker, sid, logPath string) *sessions.Handle {
	if s.recDir == "" {
		return nil
	}
	h, err := sessions.Register(s.recDir, sessions.Record{
		SessionID: sid, Mode: workerRecordMode, Title: clipRunes(firstLine(w.part.Goal), 80),
		Branch: s.WT.Branch(w.part.ID), Worktree: w.path, Log: logPath,
		Farm: &sessions.Farm{ID: s.FarmID, Part: w.part.ID, Owns: w.part.Owns, TargetBranch: s.target},
	})
	if err != nil {
		return nil
	}
	h.Start(s.hard())
	return h
}

// touchHook feeds Executor.Touched into the record as worktree-relative paths.
func touchHook(h *sessions.Handle, root string) func(tool, path string) {
	if h == nil {
		return nil
	}
	return func(tool, path string) {
		if tool == "shell" {
			h.ShellUsed()
			return
		}
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			h.Touch(filepath.ToSlash(rel))
		}
	}
}
