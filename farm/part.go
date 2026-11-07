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
	"github.com/jasonli0226/jas/tools"
)

const (
	eventsFile       = "events.jsonl"
	workerRecordMode = "farm-worker"
	supervisorRole   = "supervisor"
	// partFixRounds is how many times a failed part_check goes back to the
	// worker before the part fails.
	partFixRounds = 1
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
	end := s.endOfTurn(ctx, part.ID, res, sendErr)
	afterCheck := false // the last message to the worker was a failed part_check
	for round := 0; end.kind == endDone; round++ {
		rev := s.reviewTurn(pctx, w, round, res.Final, afterCheck)
		if rev.fix == "" && rev.fail == nil {
			break
		}
		if rev.fix == "" || round == partFixRounds {
			end = *rev.fail
			res.Status, res.Code = "failed", end.exitCode
			break
		}
		afterCheck = rev.checkFailed
		res, sendErr = w.sess.Send(pctx, rev.fix)
		end = s.endOfTurn(ctx, part.ID, res, sendErr)
	}
	if end.kind != endDone {
		_ = w.sess.Close(res.Status, res.Code)
		return end
	}
	return s.finishDone(w, res.Final)
}

// endOfTurn classifies a worker's finished turn.
func (s *Supervisor) endOfTurn(ctx context.Context, id string, res core.TurnResult, sendErr error) attemptEnd {
	end := classifyTurn(id, res, map[string]bool{poolFarm: true, poolWork: true}, s.stopping(ctx))
	if sendErr != nil && end.kind == endDone {
		end = failed(id, ReasonError, core.ExitFailure, sendErr.Error())
	}
	return end
}

// turnReview is the verdict on a worker's final answer: fix is the message
// that sends it back ("" when none), fail is how the part fails when no fix
// round is left (or at once, when fix is "").
type turnReview struct {
	fix         string
	fail        *attemptEnd
	checkFailed bool
}

// reviewTurn checks a final answer before the part may finish: first writes
// outside the part's limit (shell commands included), then part_check. A
// worker answering a failed part_check with BLOCKED: defers the check to the
// merged tree.
func (s *Supervisor) reviewTurn(ctx context.Context, w *worker, round int, final string, afterCheck bool) turnReview {
	id := w.part.ID
	viol, err := DiffViolations(ctx, s.Git.Run, w.path, w.start, w.pol.Writes)
	if err != nil {
		end := failed(id, ReasonError, core.ExitFailure, err.Error())
		return turnReview{fail: &end}
	}
	if len(viol) > 0 {
		end := failed(id, ReasonWriteLimit, core.ExitFailure, "wrote outside its limit: "+strings.Join(viol, ", "))
		return turnReview{fix: writeFixMessage(viol, w.part.Owns), fail: &end}
	}
	if s.Cfg.PartCheck == "" {
		return turnReview{}
	}
	if reason, ok := blockedReason(final); ok && afterCheck {
		s.event(EvPartCheckDeferred, map[string]any{"part": id, "round": round, "reason": reason})
		return turnReview{}
	}
	chk := s.partCheck(ctx, w, round)
	if chk.ExitCode == 0 && !chk.TimedOut {
		return turnReview{}
	}
	end := failed(id, ReasonCheck, core.ExitUnmet, fmt.Sprintf("part_check failed (exit %d)", chk.ExitCode))
	return turnReview{fix: checkFixMessage(s.Cfg.PartCheck, chk), fail: &end, checkFailed: true}
}

// partCheck runs part_check in the worker's worktree before the part may be
// done, and logs part_check.
func (s *Supervisor) partCheck(ctx context.Context, w *worker, round int) core.CheckResult {
	res := core.RunCheck(ctx, s.checkSpec(s.Cfg.PartCheck, w.path), tools.RunShell)
	s.event(EvPartCheck, map[string]any{"part": w.part.ID, "round": round, "exit_code": res.ExitCode,
		"duration_ms": res.Duration.Milliseconds(), "timed_out": res.TimedOut})
	return res
}

// blockedPrefix starts a worker's answer that its failing part_check needs
// work outside its writable paths.
const blockedPrefix = "BLOCKED:"

// checkFixMessage hands a failed part_check back to the worker for its one
// fix round.
func checkFixMessage(cmd string, res core.CheckResult) string {
	what := fmt.Sprintf("exited %d", res.ExitCode)
	if res.TimedOut {
		what = "timed out"
	}
	return fmt.Sprintf("The supervisor ran this part's check `%s` in your worktree and it %s. "+
		"Your part is not done until it passes. Fix the cause within your writable paths, "+
		"run the check yourself, then give your final answer again.\n\n"+
		"If it fails only because of work outside your writable paths (another part's files reach your "+
		"worktree only when the farm merges), do not work around it or move files: reply with one line "+
		"starting `%s` and the reason, and stop. The check then runs on the merged tree.\n\nOutput (tail):\n```\n%s\n```",
		cmd, what, blockedPrefix, strings.TrimSpace(res.Output))
}

// writeFixMessage hands writes outside the part's limit back to the worker.
func writeFixMessage(viol, owns []string) string {
	return fmt.Sprintf("These paths changed outside your writable paths (%s): %s. Shell commands count too. "+
		"Move or delete them so only your owned paths differ from the base commit, then give your final answer again.",
		strings.Join(owns, ", "), strings.Join(viol, ", "))
}

// blockedReason reports whether a final answer starts with BLOCKED: (any
// case, markdown emphasis allowed) and returns the reason on its first line.
func blockedReason(final string) (string, bool) {
	const marks = "*_`# "
	t := strings.TrimLeft(strings.TrimSpace(final), marks)
	word := strings.TrimSuffix(blockedPrefix, ":")
	if len(t) < len(word) || !strings.EqualFold(t[:len(word)], word) {
		return "", false
	}
	rest := strings.TrimLeft(t[len(word):], "*_`")
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	reason, _, _ := strings.Cut(strings.TrimLeft(rest[1:], marks), "\n")
	reason = strings.TrimSpace(reason)
	return reason, reason != ""
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
	flog, err := core.OpenEventLog(logPath, nil, s.Redact)
	if err != nil {
		return err
	}
	log, ui := s.workerLog(id, flog)
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
	if attempt > 1 { // a fresh session has seen none of the board
		s.Board.ResetDelivery(id)
	}
	ws := WorkerSession{
		Part: w.part, Role: w.role, Root: w.path, System: WorkerPrompt(s.Plan, w.part, w.role, s.baseSHA),
		SessionID: sid, Log: log, Pool: w.pool, Inject: s.inject(w.part),
		Policy: w.pol, Meta: WorkerMeta(s.FarmID, id, s.baseSHA, s.Manifest.PlanSHA256),
		Tools: toolset, Gate: gate, Touched: touchHook(w.rec, w.path),
		MaxOutput: s.Cfg.Budget.MaxOutputTokens, Wrap: s.wrapPart(id), UI: ui,
	}
	sess, err := s.NewSession(ws)
	if err != nil {
		return fmt.Errorf("start worker session: %w", err)
	}
	w.sess = sess
	s.ctl.setWorker(id, w)
	s.setPart(id, func(ps *PartState) { ps.SessionID, ps.Log = sid, logPath })
	s.event(EvPartStarted, map[string]any{"part": id, "role": w.role.Name, "attempt": attempt,
		"branch": s.WT.Branch(id), "worktree": w.path, "session_id": sid, "log": logPath})
	return nil
}

// closeWorker records tokens and undelivered posts, then releases the
// worker's log and record.
func (s *Supervisor) closeWorker(w *worker) {
	s.takeSteering(w) // a part that failed or stopped hands back unread steers too
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
	if steer := s.takeSteering(w); len(steer) > 0 {
		s.event(EvOwnerDecision, map[string]any{"part": id, "choice": choiceSteerUndelivered, "count": len(steer)})
	}
	if err := w.sess.Close("ok", core.ExitOK); err != nil {
		return failed(id, ReasonError, core.ExitFailure, err.Error())
	}
	// The work is finished: commit it even if the farm is being stopped.
	ctx := s.hard()
	msg := fmt.Sprintf("farm %s part %s: %s", s.FarmID, id, firstLine(w.part.Goal))
	if _, err := s.WT.Commit(ctx, w.path, id, msg); err != nil {
		return s.finishFailed(id, err)
	}
	viol, err := DiffViolations(ctx, s.Git.Run, w.path, w.start, w.pol.Writes)
	if err != nil {
		return s.finishFailed(id, err)
	}
	if len(viol) > 0 {
		return failed(id, ReasonWriteLimit, core.ExitFailure, "wrote outside its limit: "+strings.Join(viol, ", "))
	}
	stat, err := s.Git.Run(ctx, w.path, "diff", "--shortstat", w.start, "HEAD")
	if err != nil {
		return s.finishFailed(id, err)
	}
	stat = strings.TrimSpace(stat)
	s.setPart(id, func(ps *PartState) { ps.Diffstat = stat })
	s.postDone(id, final, stat)
	return attemptEnd{id: id, kind: endDone}
}

// finishFailed is a git failure while finishing a done part: an interrupt
// when the hard context ended (the part pauses, worktree kept), else error.
func (s *Supervisor) finishFailed(id string, err error) attemptEnd {
	if s.hard().Err() != nil {
		return attemptEnd{id: id, kind: endStopped, exitCode: core.ExitInterrupted, detail: "interrupted"}
	}
	return failed(id, ReasonError, core.ExitFailure, err.Error())
}

// postDone auto-posts the part's done entry. A rejected post (a config with
// tiny post_chars) is logged and does not fail the part.
func (s *Supervisor) postDone(id, final, stat string) {
	room := s.Cfg.Board.PostChars - len([]rune(stat))
	summary := clipRunes(strings.TrimSpace(final), min(summaryRunes, room))
	if summary == "" {
		summary = "done"
	}
	_, err := s.Board.PostFrom(id, supervisorRole, TopicDone, map[string]any{"summary": summary, "diffstat": stat}, "", "")
	if err != nil { // a stored post is logged by the board hook (see init)
		s.event(EvBoardPosted, map[string]any{"part": id, "topic": TopicDone, "error": err.Error()})
	}
}

// boardPosted logs a stored post to farm.jsonl without repeating its text
// (board.jsonl has it): part, topic, seq, plus the name of an interface.
func (s *Supervisor) boardPosted(p Post) {
	ev := map[string]any{"part": p.Part, "topic": p.Topic, "seq": p.Seq}
	if name, ok := p.Fields["name"].(string); ok && p.Topic == TopicInterface {
		ev["name"] = name
	}
	s.event(EvBoardPosted, ev)
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

// workerLog wraps a session's log so the watcher sees its events and, when
// WorkerUI is set, that UI gets them too; ui is that UI or nil.
func (s *Supervisor) workerLog(part string, log core.EventLog) (core.EventLog, core.UI) {
	log = watchWorkerLog(s.Watch, part, log)
	if s.WorkerUI == nil {
		return log, nil
	}
	ui := s.WorkerUI(part)
	return core.NewUILog(log, ui), ui
}

// gate is the part's approval gate. Without shared approvals there is no
// TTY: gated calls end the part with approval_needed.
func (s *Supervisor) gate(part, approve string) (core.Gate, error) {
	if s.Gates != nil {
		return s.Gates(part, approve)
	}
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
