package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/jasonli0226/jas/providers"
)

// tool_result statuses in the event log.
const (
	statusOK          = "ok"
	statusError       = "error"
	statusDenied      = "denied"
	statusCancelled   = "cancelled"
	statusInterrupted = "interrupted" // written by ResumeSession, never by Run
	statusNotRun      = "not_run"     // written by ResumeSession, never by Run
)

const cancelledOutput = "cancelled"

// Executor runs a turn's tool calls: consecutive read-only calls in parallel,
// each mutating call alone as a barrier, results in call order.
type Executor struct {
	Reg    *Registry
	Policy ExecPolicy
	Gate   Gate // nil means AllowAll
	Log    EventLog
	Filter func(Result) Result // redaction hook; nil means identity
}

type slot struct {
	call    providers.ContentBlock
	tool    Tool
	res     Result
	status  string
	settled bool // result already decided; nothing to execute
}

type run struct {
	e       *Executor
	slots   []*slot
	stopErr error // Unavailable
	logErr  error // first event-log failure
}

// Run executes calls and returns one tool_result block per call, in order.
// The error is non-nil only for ctx cancellation or an Unavailable gate; every
// call still gets a result.
func (e *Executor) Run(ctx context.Context, calls []providers.ContentBlock) ([]providers.ContentBlock, error) {
	r := &run{e: e}
	var batch []*slot
	flush := func() {
		r.runBatch(ctx, batch)
		batch = nil
	}
	for i, c := range calls {
		if r.stopErr != nil || ctx.Err() != nil {
			flush()
			r.cancelRest(calls[i:])
			break
		}
		s := r.prepare(c)
		if s.settled || s.tool.Kind() == ReadOnly {
			batch = append(batch, s)
			continue
		}
		flush()
		r.runMutating(ctx, s)
	}
	flush()
	if err := e.Log.Sync(); err != nil {
		r.noteLogErr(err)
	}

	out := make([]providers.ContentBlock, len(r.slots))
	for i, s := range r.slots {
		out[i] = providers.ContentBlock{
			Type:      providers.BlockToolResult,
			ToolUseID: s.call.ToolUseID,
			Text:      s.res.Output,
			IsError:   s.res.IsError,
		}
	}
	switch {
	case r.stopErr != nil:
		return out, r.withLogErr(r.stopErr)
	case ctx.Err() != nil:
		return out, r.withLogErr(ctx.Err())
	}
	return out, r.logErr
}

// withLogErr keeps the primary error unwrapped unless a log failure also occurred.
func (r *run) withLogErr(primary error) error {
	if r.logErr == nil {
		return primary
	}
	return errors.Join(primary, r.logErr)
}

func (r *run) noteLogErr(err error) {
	if err != nil && r.logErr == nil {
		r.logErr = fmt.Errorf("event log: %w", err)
	}
}

// prepare resolves the tool and validates arguments; unknown tools and bad
// arguments yield settled error slots.
func (r *run) prepare(c providers.ContentBlock) *slot {
	s := &slot{call: c}
	r.slots = append(r.slots, s)
	tool, ok := r.e.Reg.Get(c.Name)
	if !ok {
		s.settle(Result{Output: fmt.Sprintf("unknown tool %q", c.Name), IsError: true}, statusError)
		return s
	}
	s.tool = tool
	if len(c.Input) > 0 && !json.Valid(c.Input) {
		s.settle(Result{Output: fmt.Sprintf("invalid JSON arguments for tool %q", c.Name), IsError: true}, statusError)
	}
	return s
}

func (s *slot) settle(res Result, status string) {
	s.res, s.status, s.settled = res, status, true
}

func (r *run) cancelRest(rest []providers.ContentBlock) {
	for _, c := range rest {
		s := &slot{call: c}
		s.settle(Result{Output: cancelledOutput, IsError: true}, statusCancelled)
		r.slots = append(r.slots, s)
		r.emitResult(s)
	}
}

// runBatch runs the unsettled slots in parallel, then logs results in call order.
func (r *run) runBatch(ctx context.Context, batch []*slot) {
	var wg sync.WaitGroup
	for _, s := range batch {
		if s.settled {
			continue
		}
		r.emitStarted(s, false, false)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.execute(ctx, s)
		}()
	}
	wg.Wait()
	for _, s := range batch {
		r.emitResult(s)
	}
}

func (r *run) runMutating(ctx context.Context, s *slot) {
	if s.settled {
		r.emitResult(s)
		return
	}
	gate := r.e.Gate
	if gate == nil {
		gate = AllowAll{}
	}
	d, err := gate.Allow(ctx, s.tool, s.call.Input)
	if err != nil {
		if ctx.Err() != nil {
			s.settle(Result{Output: cancelledOutput, IsError: true}, statusCancelled)
			r.emitResult(s)
			return
		}
		s.settle(Result{Output: "approval failed: " + err.Error(), IsError: true}, statusError)
		r.emitResult(s)
		return
	}
	r.noteLogErr(r.e.Log.Append(EvApproval, map[string]any{
		"call_id": s.call.ToolUseID, "tool": s.call.Name, "decision": string(d),
	}))
	switch d {
	case Granted:
	case Denied:
		s.settle(Result{Output: "denied: the user did not approve this call", IsError: true}, statusDenied)
		r.emitResult(s)
		return
	default: // Unavailable or anything unrecognised: stop the turn
		r.stopErr = &ExitError{Code: ExitApprovalNeeded, Reason: "approval needed but no terminal is available"}
		s.settle(Result{Output: cancelledOutput, IsError: true}, statusCancelled)
		r.emitResult(s)
		return
	}
	// Durable intent record before any side effect (design §3).
	if err := r.emitStarted(s, true, true); err != nil {
		s.settle(Result{Output: "event log write failed, call not run: " + err.Error(), IsError: true}, statusError)
		r.emitResult(s)
		return
	}
	r.execute(ctx, s)
	r.emitResult(s)
}

// execute calls the tool, converting errors and panics to error results.
func (r *run) execute(ctx context.Context, s *slot) {
	var (
		res Result
		err error
	)
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("tool panicked: %v", p)
			}
		}()
		res, err = s.tool.Call(ctx, r.e.Policy, s.call.Input)
	}()
	switch {
	case err != nil && ctx.Err() != nil:
		s.settle(Result{Output: cancelledOutput, IsError: true}, statusCancelled)
	case err != nil:
		s.settle(Result{Output: err.Error(), IsError: true}, statusError)
	case res.IsError:
		s.settle(res, statusError)
	default:
		s.settle(res, statusOK)
	}
}

func (r *run) emitStarted(s *slot, mutating, durable bool) error {
	payload := map[string]any{
		"tool": s.call.Name, "call_id": s.call.ToolUseID,
		"args": rawOrEmpty(s.call.Input), "mutating": mutating,
	}
	var err error
	if durable {
		err = r.e.Log.AppendSync(EvToolStarted, payload)
	} else {
		err = r.e.Log.Append(EvToolStarted, payload)
	}
	r.noteLogErr(err)
	return err
}

func (r *run) emitResult(s *slot) {
	if s.status != statusCancelled && s.status != statusDenied && r.e.Filter != nil {
		s.res = r.e.Filter(s.res)
	}
	r.noteLogErr(r.e.Log.Append(EvToolResult, map[string]any{
		"call_id": s.call.ToolUseID, "tool": s.call.Name,
		"status": s.status, "output": s.res.Output,
	}))
}

func rawOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage("{}")
	}
	return raw
}
