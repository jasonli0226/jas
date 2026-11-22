package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"strings"
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
	Redact func(string) string // applied to each shown diff line; nil is identity
	// Touched, when set, hears about writes for session records: the resolved
	// path after a successful write or edit, and tool "shell" with path ""
	// after any shell call that ran (its writes can't be tracked).
	Touched func(tool, path string)
	// WriteGuard, when set, checks a write or edit into the profile memory
	// before the approval gate: tool is "write" or "edit", resolved the
	// target, existed whether it was there, newContent the whole file after
	// the call. A non-nil error refuses the call with that text. nil means
	// no content checks; memory writes still always ask.
	WriteGuard func(tool, resolved string, existed bool, newContent string) error
	// WritePrecheck, when set, runs before WriteGuard and before the change
	// is previewed, on the same calls: the checks that need no content (a
	// read-only run, a wrong folder), so they are reported even when the
	// preview itself would fail. A non-nil error refuses the call.
	WritePrecheck func(tool, resolved string) error
	// GuardAllWrites, when true, runs WritePrecheck and WriteGuard on every
	// write and edit, not only those into the profile memory. The dream
	// executor sets it: its guard confines writes to the proposed store.
	GuardAllWrites bool
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
	history []providers.Message // the session's messages, for dedupRead
	slots   []*slot
	stopErr error // Unavailable
	logErr  error // first event-log failure
}

// Run executes calls and returns one tool_result block per call, in order.
// The error is non-nil only for ctx cancellation or an Unavailable gate; every
// call still gets a result.
func (e *Executor) Run(ctx context.Context, calls []providers.ContentBlock) ([]providers.ContentBlock, error) {
	return e.RunWithHistory(ctx, nil, calls)
}

// RunWithHistory is Run for a session whose current messages are history:
// a read whose output is still intact there gets a note instead (dedupRead).
// history is only read, possibly from several goroutines.
func (e *Executor) RunWithHistory(ctx context.Context, history []providers.Message, calls []providers.ContentBlock) ([]providers.ContentBlock, error) {
	r := &run{e: e, history: history}
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
	} else if k, dup := duplicateKey(c.Input); dup {
		// The prompt and the tool must read the same field; Go's struct decoding
		// is case-insensitive and last-wins, so refuse the ambiguity outright.
		s.settle(Result{Output: fmt.Sprintf("ambiguous JSON arguments for tool %q: duplicate key %q", c.Name, k), IsError: true}, statusError)
	}
	return s
}

// duplicateKey reports a top-level key of a JSON object that equals an earlier
// one under strings.EqualFold (exact repeats included). Tool args are flat.
func duplicateKey(args json.RawMessage) (key string, dup bool) {
	dec := json.NewDecoder(bytes.NewReader(args))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return "", false
	}
	var seen []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return "", false
		}
		k, _ := tok.(string)
		for _, p := range seen {
			if strings.EqualFold(p, k) {
				return k, true
			}
		}
		seen = append(seen, k)
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return "", false
		}
	}
	return "", false
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
	if r.refuseMemoryWrite(ctx, s) {
		r.emitResult(s)
		return
	}
	t := s.tool
	if r.profileWrite(s) {
		t = AskAlways(t)
	}
	d, err := gate.Allow(ctx, t, s.call.Input, r.preview(ctx, s))
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
	if d != AutoGranted { // only a decision someone made is an approval event
		r.noteLogErr(r.e.Log.Append(EvApproval, map[string]any{
			"call_id": s.call.ToolUseID, "tool": s.call.Name, "decision": string(d),
		}))
	}
	switch d {
	case Granted, AutoGranted:
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

// writeTarget returns the resolved path of a write or edit call, or false
// for any other call or a path that does not resolve.
func (r *run) writeTarget(s *slot) (string, bool) {
	if s.call.Name != "write" && s.call.Name != "edit" {
		return "", false
	}
	var a struct{ Path string }
	if json.Unmarshal(s.call.Input, &a) != nil || a.Path == "" {
		return "", false
	}
	full, err := r.e.Policy.Resolve(a.Path)
	return full, err == nil
}

// profileWrite reports whether s is a write or edit that lands in the active
// profile's skills or memory directory.
func (r *run) profileWrite(s *slot) bool {
	full, ok := r.writeTarget(s)
	return ok && (r.e.Policy.InProfileSkills(full) || r.e.Policy.InProfileMemory(full))
}

// refuseMemoryWrite runs Executor.WritePrecheck, then Executor.WriteGuard
// on a write or edit into the profile memory, against the whole file the
// call would leave. It settles s and reports true when the call is refused:
// a check said no, or the change could not be previewed. A cancelled
// preview settles s cancelled.
func (r *run) refuseMemoryWrite(ctx context.Context, s *slot) bool {
	guard, precheck := r.e.WriteGuard, r.e.WritePrecheck
	if guard == nil && precheck == nil {
		return false
	}
	full, ok := r.writeTarget(s)
	if !ok || !(r.e.GuardAllWrites || r.e.Policy.InProfileMemory(full)) {
		return false
	}
	refuse := func(msg string) bool {
		s.settle(Result{Output: msg, IsError: true}, statusError)
		return true
	}
	if precheck != nil {
		if err := precheck(s.call.Name, full); err != nil {
			return refuse(err.Error())
		}
	}
	if guard == nil {
		return false
	}
	p, ok := s.tool.(Previewer)
	if !ok {
		return refuse("memory write could not be checked: " + s.call.Name + " cannot preview")
	}
	change, err := p.Preview(ctx, r.e.Policy, s.call.Input)
	switch {
	case ctx.Err() != nil:
		s.settle(Result{Output: cancelledOutput, IsError: true}, statusCancelled)
		return true
	case err != nil:
		return refuse("memory write could not be checked: " + err.Error())
	case change == nil: // binary content, or an old file too large to diff
		return refuse("memory write could not be checked: no preview of the change")
	}
	_, statErr := os.Stat(full)
	if err := guard(s.call.Name, full, statErr == nil, change.New); err != nil {
		return refuse(err.Error())
	}
	return false
}

// preview returns the gate's preview of s: the diff its tool would make, or
// nil when the tool cannot preview, the preview fails, or nothing changes.
func (r *run) preview(ctx context.Context, s *slot) func() *Diff {
	return func() *Diff {
		p, ok := s.tool.(Previewer)
		if !ok {
			return nil
		}
		change, err := p.Preview(ctx, r.e.Policy, s.call.Input)
		if err != nil || change == nil {
			return nil
		}
		return ComputeDiff(*change, r.e.Redact)
	}
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
	case res.IsError && res.Denied:
		s.settle(res, statusDenied)
	case res.IsError:
		s.settle(res, statusError)
	default:
		s.settle(res, statusOK)
	}
	r.touched(s, err == nil && !res.IsError)
}

// touched reports a finished call to Executor.Touched.
func (r *run) touched(s *slot, ok bool) {
	hook := r.e.Touched
	if hook == nil {
		return
	}
	switch s.call.Name {
	case "shell":
		hook("shell", "")
	case "write", "edit":
		if !ok {
			return
		}
		var a struct{ Path string }
		if json.Unmarshal(s.call.Input, &a) != nil || a.Path == "" {
			return
		}
		if full, err := r.e.Policy.Resolve(a.Path); err == nil {
			hook(s.call.Name, full)
		}
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
	unchanged := false
	if s.status == statusOK {
		s.res, unchanged = r.e.Policy.dedupRead(r.history, s.call, s.res)
	}
	logged := map[string]any{
		"call_id": s.call.ToolUseID, "tool": s.call.Name,
		"status": s.status, "output": s.res.Output,
	}
	if unchanged {
		logged["unchanged"] = true
	}
	shown := r.withDiff(s, logged)
	sa, split := r.e.Log.(screenAppender)
	if shown == nil || !split {
		r.noteLogErr(r.e.Log.Append(EvToolResult, logged))
		return
	}
	r.noteLogErr(sa.AppendScreen(EvToolResult, logged, shown))
}

// withDiff adds the diff of a successful change to logged and returns the
// screen payload, or nil when there is no diff. The log keeps only added
// lines of a change marked HideRemoved (a write over an existing file): the
// old content was never read by the model and may hold secrets that pattern
// redaction misses, so it goes to the screen only. A ScreenOnly change (an
// edit) logs no diff at all.
func (r *run) withDiff(s *slot, logged map[string]any) map[string]any {
	if s.status != statusOK || s.res.Change == nil {
		return nil
	}
	d := ComputeDiff(*s.res.Change, r.e.Redact)
	if d == nil {
		return nil
	}
	shown := maps.Clone(logged)
	shown["diff"] = d
	switch {
	case s.res.Change.ScreenOnly: // the log gets no diff
	case s.res.Change.HideRemoved:
		logged["diff"] = d.AddedOnly()
	default:
		logged["diff"] = d
	}
	return shown
}

func rawOrEmpty(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage("{}")
	}
	return raw
}
