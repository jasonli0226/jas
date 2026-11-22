package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/jasonli0226/jas/providers"
)

// steerSep joins queued steering texts into one user message.
const steerSep = "\n\n"

// SessionConfig is everything a Session needs. Log should be the same log the
// Executor writes to (wrap it with NewUILog to show events in a UI).
type SessionConfig struct {
	Provider providers.Provider
	Model    string
	System   string
	Exec     *Executor
	Log      EventLog
	UI       UI
	MaxTurns int            // per user message; <=0 → DefaultMaxTurns
	Budget   *Budget        // nil = no context budget
	Compact  *CompactConfig // nil: compaction off (jas run)
	Meta     map[string]any // merged into session_started / session_resumed
	// MaxOutput is sent as each model request's max_tokens; 0 omits it.
	MaxOutput int
	// Tokens caps total tokens: each request reserves its estimate first and
	// the session stops with exit 7 when that would cross the cap. nil = none.
	Tokens *TokenPool
	// Inject, if set, is called after each tool-results message is pushed and
	// before the next request. Non-empty text is appended to that message as a
	// text block and logged as an injected event with meta merged in. Never
	// called after a final answer; independent of the steering queue.
	Inject func() (text string, meta map[string]any)
	// WrapUp turns the request that would hit MaxTurns or Tokens into a last
	// one that asks for the final answer with tools off (see wrapup.go).
	WrapUp bool
}

// TurnResult describes how one user message (one Send, or the last steering
// message it picked up) ended.
type TurnResult struct {
	Status string // "ok" | "context_full" | "turn_cap" | "approval_needed" | "interrupted" | "failed" | "budget_exceeded" | "output_cap"
	Code   int    // the Exit* code jas run would exit with
	Reason string
	// Pool names the token pool that refused a request when Reason is
	// "tokens", so a farm can tell a part's cap from the farm's.
	Pool  string
	Turns int // model requests for this user message
	Usage providers.Usage
	Final string
}

// Session holds a conversation across user messages. Send, SetModel, SetSystem,
// Messages and Close must not overlap; Steer is safe from any goroutine. The
// chat (tui) runs Send on its own goroutine and refuses /new, /model, /resume
// and /reload until it returns, so the other calls never run during a Send and
// need no mutex.
type Session struct {
	c        SessionConfig
	model    string
	title    string
	messages []providers.Message
	ids      []string        // event id per message; "" for tool results
	turns    int             // session total, for session_ended
	usage    providers.Usage // session total, for session_ended
	logErr   error           // first event-log failure in the current Send
	closed   bool
	reason   string // reason of the last non-ok turn, for session_ended
	pool     string // pool that refused the current user message, if any

	pendingShell []ShellRun // from the replay; read once by the chat at resume

	mu    sync.Mutex
	queue []string // steering texts not yet delivered
}

// msgState is the per-user-message part of a Send.
type msgState struct {
	turns  int
	usage  providers.Usage
	final  string
	finish string // finish_reason of the latest reply
}

// NewSession starts a session and writes session_started.
func NewSession(c SessionConfig) (*Session, error) {
	s := newSession(c)
	if err := s.open(EvSessionStarted); err != nil {
		return nil, err
	}
	return s, nil
}

// ResumeSession continues a replayed conversation and writes session_resumed
// to c.Log, which should be the reopened log of the same file. System and
// Model come from c, never from the file: the caller rebuilds the system
// prompt from current trust and picks the model with ResumeModel.
func ResumeSession(c SessionConfig, st ReplayState) (*Session, error) {
	s := newSession(c)
	s.title = st.Title
	s.pendingShell = slices.Clone(st.PendingShell)
	s.messages = slices.Clone(st.Messages)
	s.ids = make([]string, len(s.messages))
	copy(s.ids, st.MessageIDs)
	if err := s.open(EvSessionResumed); err != nil {
		return nil, err
	}
	// Give each orphaned call a real result so the file stays consistent and
	// a later Replay doesn't synthesize it again.
	for _, c := range st.Interrupted {
		s.note(s.c.Log.Append(EvToolResult, map[string]any{
			"call_id": c.ID, "tool": c.Tool, "status": c.Status, "output": c.Output,
		}))
	}
	s.note(s.c.Log.Sync())
	// Replay restores full tool outputs; re-stub them before the first
	// budget check, or a long session could never be resumed.
	s.elide()
	if s.logErr != nil {
		return nil, s.logErr
	}
	return s, nil
}

// open writes the session's opening event with Meta merged and the model.
func (s *Session) open(typ string) error {
	data := map[string]any{}
	maps.Copy(data, s.c.Meta)
	data["model"] = s.model
	if err := s.c.Log.AppendSync(typ, data); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// newSession applies defaults without logging, so a resume path can seed
// messages and write its own opening event.
func newSession(c SessionConfig) *Session {
	if c.MaxTurns <= 0 {
		c.MaxTurns = DefaultMaxTurns
	}
	if c.UI == nil {
		c.UI = NewQuietUI()
	}
	if c.Budget != nil && c.Exec != nil {
		c.Budget.Overhead = PromptOverhead(c.System, c.Exec.Reg.Specs())
	}
	return &Session{c: c, model: c.Model}
}

// Send adds a user message and runs the agent loop until a final answer, the
// per-message turn cap, an error, or cancellation. Steering text queued while
// it runs is delivered after tool results, or as a new user message if the
// model answers first. The returned error is an event-log failure, already
// reflected in the result.
func (s *Session) Send(ctx context.Context, text string) (TurnResult, error) {
	if s.closed {
		return TurnResult{}, errors.New("session closed")
	}
	s.logErr = nil
	m := s.begin(text, false)
	for {
		status, code, reason := s.loop(ctx, m)
		if status == endOK && s.logErr == nil && ctx.Err() == nil { // a cancelled run leaves steering queued
			if steer := s.drain(); steer != "" {
				s.endTurn(m, status, code, reason)
				m = s.begin(steer, true)
				continue
			}
		}
		res := s.endTurn(m, status, code, reason)
		return res, s.logErr
	}
}

// begin appends a user message and logs user_message and turn_started.
func (s *Session) begin(text string, steer bool) *msgState {
	s.pool = ""
	s.addUser(text, steer)
	s.note(s.c.Log.Append(EvTurnStarted, map[string]any{}))
	return &msgState{}
}

func (s *Session) addUser(text string, steer bool) {
	content := []providers.ContentBlock{{Type: providers.BlockText, Text: text}}
	data := map[string]any{"content": toWire(content)}
	if steer {
		data["steer"] = true
	}
	id, err := s.c.Log.AppendID(EvUserMessage, data)
	s.note(err)
	if n := len(s.messages); n > 0 && plainUser(s.messages[n-1]) {
		// A resumed session whose last turn never reached the model: one
		// user message, not two in a row (some APIs refuse that).
		s.messages[n-1].Content = append(slices.Clone(s.messages[n-1].Content), content...)
		return
	}
	s.push(providers.Message{Role: providers.RoleUser, Content: content}, id)
}

// plainUser reports whether m is a user message without tool results, which
// a following user message can be merged into.
func plainUser(m providers.Message) bool {
	if m.Role != providers.RoleUser {
		return false
	}
	for _, b := range m.Content {
		if b.Type == providers.BlockToolResult {
			return false
		}
	}
	return true
}

// push appends a message with the id of its event ("" if it has none).
func (s *Session) push(msg providers.Message, id string) {
	s.messages = append(s.messages, msg)
	s.ids = append(s.ids, id)
}

// endTurn writes turn_ended. A log failure turns an otherwise clean turn into
// a failure.
func (s *Session) endTurn(m *msgState, status string, code int, reason string) TurnResult {
	if code == ExitOK && s.logErr != nil {
		status, code, reason = endFailed, ExitFailure, "event log failure"
	}
	s.note(s.c.Log.AppendSync(EvTurnEnded, map[string]any{
		"status": status, "exit_code": code, "reason": reason, "turns": m.turns,
		"prompt_tokens": m.usage.PromptTokens, "completion_tokens": m.usage.CompletionTokens,
	}))
	if code == ExitOK && s.logErr != nil { // only turn_ended itself failed
		status, code, reason = endFailed, ExitFailure, "event log failure"
	}
	s.reason = ""
	if code != ExitOK {
		s.reason = reason
	}
	return TurnResult{Status: status, Code: code, Reason: reason, Pool: s.pool, Turns: m.turns, Usage: m.usage, Final: m.final}
}

// Steer queues text for delivery at the next point the loop can take it.
func (s *Session) Steer(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, text)
}

// TakeSteering empties the steering queue and returns its texts in order, so
// a caller that stops the run can put them back in the input box.
func (s *Session) TakeSteering() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	q := s.queue
	s.queue = nil
	return q
}

// drain empties the steering queue, returning its texts joined.
func (s *Session) drain() string {
	return strings.Join(s.TakeSteering(), steerSep)
}

// Messages returns a copy of the conversation history.
func (s *Session) Messages() []providers.Message {
	return slices.Clone(s.messages)
}

// MessageIDs returns the event id of each message, aligned with Messages; ""
// for tool-results messages.
func (s *Session) MessageIDs() []string {
	return slices.Clone(s.ids)
}

// SetModel switches the model for later requests and logs model_changed. The
// switch happens even if logging fails; the log error is returned.
func (s *Session) SetModel(model string) error {
	s.model = model
	data := map[string]any{"model": model}
	for _, k := range []string{"provider", "base_url"} { // a switch stays on the session's endpoint
		if v, ok := s.c.Meta[k]; ok {
			data[k] = v
		}
	}
	if err := s.c.Log.Append(EvModelChanged, data); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// maxTitleRunes caps a session title, like the picker's first-message line.
const maxTitleRunes = 80

// SetTitle names the session and logs session_titled; "" clears the name.
// The title is folded to one line and capped. Like SetModel it takes effect
// even if logging fails; the log error is returned.
func (s *Session) SetTitle(title string) error {
	s.title = NormalizeTitle(title)
	if err := s.c.Log.Append(EvSessionTitled, map[string]any{"title": s.title}); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// Title is the session's current title, "" when none is set.
func (s *Session) Title() string { return s.title }

// System is the current system prompt sent with each request.
func (s *Session) System() string { return s.c.System }

// SetSystem replaces the system prompt for later requests, refreshes the
// budget overhead from the new text, and logs context_reloaded. meta is
// merged into the event (for example context_files after /reload). Like
// SetModel it takes effect even if logging fails; the log error is returned.
func (s *Session) SetSystem(system string, meta map[string]any) error {
	s.c.System = system
	if s.c.Budget != nil && s.c.Exec != nil {
		s.c.Budget.Overhead = PromptOverhead(system, s.c.Exec.Reg.Specs())
	}
	data := map[string]any{"bytes": len(system)}
	maps.Copy(data, meta)
	if err := s.c.Log.Append(EvContextReloaded, data); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// SetReadRoots replaces the executor's extra read roots (skill directories
// after /reload). A nil executor is a no-op.
func (s *Session) SetReadRoots(roots []string) {
	if s.c.Exec == nil {
		return
	}
	s.c.Exec.Policy.ReadRoots = slices.Clone(roots)
}

// SetProfileSkills replaces the executor's profile read exception (the
// active profile's skills directory) after a profile switch. A nil executor
// is a no-op.
func (s *Session) SetProfileSkills(dir string) {
	if s.c.Exec == nil {
		return
	}
	s.c.Exec.Policy.ProfileSkills = dir
}

// SetProfileMemory replaces the executor's profile memory directory after a
// profile switch. A nil executor is a no-op.
func (s *Session) SetProfileMemory(dir string) {
	if s.c.Exec == nil {
		return
	}
	s.c.Exec.Policy.ProfileMemory = dir
}

// LogProfileChanged logs profile_changed with data (from, to, the reloaded
// context files and skills, settings deferred to /new).
func (s *Session) LogProfileChanged(data map[string]any) error {
	if err := s.c.Log.Append(EvProfileChanged, data); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// NormalizeTitle collapses whitespace to single spaces and cuts the title to
// maxTitleRunes.
func NormalizeTitle(title string) string {
	rs := []rune(strings.Join(strings.Fields(title), " "))
	if len(rs) > maxTitleRunes {
		rs = rs[:maxTitleRunes]
	}
	return string(rs)
}

// Close writes session_ended with session totals. Later calls do nothing.
func (s *Session) Close(status string, code int) error {
	if s.closed {
		return nil
	}
	s.closed = true
	ev := map[string]any{
		"status": status, "exit_code": code, "turns": s.turns,
		"prompt_tokens": s.usage.PromptTokens, "completion_tokens": s.usage.CompletionTokens,
	}
	if code != ExitOK && s.reason != "" {
		ev["reason"] = s.reason
	}
	if err := s.c.Log.AppendSync(EvSessionEnded, ev); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// note records the first event-log failure.
func (s *Session) note(err error) {
	if err != nil && s.logErr == nil {
		s.logErr = fmt.Errorf("event log: %w", err)
	}
}

func (s *Session) loop(ctx context.Context, m *msgState) (status string, code int, reason string) {
	for m.turns < s.c.MaxTurns {
		if ctx.Err() != nil {
			return s.ctxEnd(ctx)
		}
		m.turns++
		s.turns++
		done, st, code, reason := s.turn(ctx, m)
		if done {
			return st, code, reason
		}
	}
	return endTurnCap, ExitTurnCap, fmt.Sprintf("turn cap of %d reached", s.c.MaxTurns)
}

// turn runs one provider round-trip plus its tool calls. done reports that the
// user message is finished (successfully or not).
func (s *Session) turn(ctx context.Context, m *msgState) (done bool, status string, code int, reason string) {
	c := s.c
	if done, st, code, reason := s.autoCompact(ctx); done {
		return true, st, code, reason
	}
	if c.Budget != nil && c.Budget.WouldExceed(s.messages) {
		return true, endContextFull, ExitContextFull, "context window full"
	}
	w := s.turnWrapUp(m)
	if w == nil {
		w = s.earlyWrapUp()
	}
	msg, uses, err := s.request(ctx, m, w)
	if tw := s.tokenWrapUp(err); w == nil && tw != nil {
		w = tw
		msg, uses, err = s.request(ctx, m, w)
	}
	if done, st, code, reason := s.budgetStopped(err); done {
		return true, st, code, reason
	}
	if err != nil && s.retryAfterOverflow(ctx, err) {
		_, cerr := s.Compact(ctx, "")
		if done, st, code, reason := s.compactFailed(ctx, cerr); done {
			return true, st, code, reason
		}
		msg, uses, err = s.request(ctx, m, w)
		if done, st, code, reason := s.budgetStopped(err); done {
			return true, st, code, reason
		}
		if errors.Is(err, providers.ErrContextLength) {
			return true, endContextFull, ExitContextFull, reasonFullAfterCompact
		}
	}
	if err != nil {
		return s.streamFailed(ctx, err, msg, uses)
	}

	// A reply cut off at the output cap is not an answer, though it has no
	// tool calls (#194).
	cut := len(uses) == 0 && m.finish == finishLength
	if w != nil && cut && w.reason == wrapTokens {
		return s.wrapCutOff(m, w, msg)
	}
	if w != nil && !cut {
		return s.wrapEnd(m, w, msg, uses)
	}
	s.push(msg, s.logAssistant(msg, len(uses) == 0 && !cut, m.finish))
	if cut {
		m.final = textOf(msg)
		return true, endOutputCap, ExitOutputCap, s.outputCapReason()
	}
	if len(uses) == 0 {
		m.final = textOf(msg)
		return true, endOK, ExitOK, ""
	}
	results, err := c.Exec.Run(ctx, uses)
	s.push(providers.Message{Role: providers.RoleUser, Content: results}, "")
	if err != nil { // queued steering stays queued for the caller
		if ctx.Err() != nil {
			st, code, reason := s.ctxEnd(ctx)
			return true, st, code, reason
		}
		st, code := classify(err)
		return true, st, code, err.Error()
	}
	if ctx.Err() == nil {
		s.inject()
	}
	// Steering stays queued after a cancel or on the last allowed turn, where
	// no further request would answer it.
	if ctx.Err() == nil && m.turns < c.MaxTurns {
		if steer := s.drain(); steer != "" {
			s.addUser(steer, true)
		}
	}
	s.elide()
	return false, "", 0, ""
}

// outputCapReason says which limit cut the reply off: max_output_tokens, or
// the provider's own when none is sent.
func (s *Session) outputCapReason() string {
	if n := s.outputCap(); n > 0 {
		return fmt.Sprintf("reply cut off at max_output_tokens (%d)", n)
	}
	return "reply cut off at the provider's output limit"
}

// inject appends the Inject hook's text to the tool-results message just
// pushed and logs it, so Replay can rebuild the same message.
func (s *Session) inject() {
	if s.c.Inject == nil || len(s.messages) == 0 {
		return
	}
	text, meta := s.c.Inject()
	if text == "" {
		return
	}
	last := len(s.messages) - 1
	msg := s.messages[last]
	msg.Content = append(slices.Clone(msg.Content), providers.ContentBlock{Type: providers.BlockText, Text: text})
	s.messages[last] = msg
	data := map[string]any{}
	maps.Copy(data, meta)
	data["text"] = text
	s.note(s.c.Log.Append(EvInjected, data))
}

// newRequest is the next ordinary request over the current history.
func (s *Session) newRequest() providers.Request {
	return providers.Request{
		Model: s.model, System: s.c.System, Tools: s.c.Exec.Reg.Specs(),
		Messages: slices.Clone(s.messages), MaxTokens: s.outputCap(),
	}
}

// request streams one model request over the current history, showing its
// output and recording its usage. A non-nil w makes it the wrap-up request.
func (s *Session) request(ctx context.Context, m *msgState, w *wrapUp) (providers.Message, []providers.ContentBlock, error) {
	c := s.c
	req := s.newRequest()
	if w != nil {
		req = wrapRequest(req, w)
	}
	est := s.estimate(req)
	res, err := s.reserve(est)
	if err != nil {
		return providers.Message{}, nil, err
	}
	if w != nil {
		s.note(c.Log.Append(EvWrapUp, map[string]any{"reason": w.reason, "max_tokens": req.MaxTokens}))
	}
	stream, errc := c.Provider.Stream(ctx, req)
	var (
		acc   providers.Accumulator
		usage *providers.Usage
	)
	for ev := range stream {
		if ev.Reasoning != "" {
			c.UI.Reasoning(ev.Reasoning)
		}
		if ev.Text != "" {
			c.UI.Text(ev.Text)
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
		acc.Add(ev)
	}
	streamErr := <-errc
	if usage != nil {
		res.Settle(usage.PromptTokens + usage.CompletionTokens)
	} else {
		res.Settle(est)
	}
	if usage != nil {
		s.recordUsage(m, *usage)
		if c.Budget != nil {
			c.Budget.Observe(usage.PromptTokens, len(req.Messages))
		}
	}
	m.finish = acc.FinishReason()
	msg, uses, doneErr := acc.Done()
	if streamErr == nil && doneErr != nil {
		streamErr = doneErr
	}
	return msg, uses, streamErr
}

// elide replaces old tool results with stubs when the budget says so.
func (s *Session) elide() {
	if s.c.Budget == nil {
		return
	}
	msgs, st := s.c.Budget.MaybeElide(s.messages)
	if st == nil {
		return
	}
	s.messages = msgs
	s.note(s.c.Log.Append(EvElided, map[string]any{
		"count": st.Count, "tokens_before": st.TokensBefore, "tokens_after": st.TokensAfter,
	}))
}

// streamFailed maps a provider error to an exit. On cancellation, tool calls
// accumulated so far get synthesized cancelled results (design §3 rule 4).
func (s *Session) streamFailed(ctx context.Context, err error, msg providers.Message, uses []providers.ContentBlock) (bool, string, int, string) {
	switch {
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		if len(uses) > 0 {
			s.push(msg, s.logAssistant(msg, false, ""))
			s.push(providers.Message{Role: providers.RoleUser, Content: s.cancelUses(uses)}, "")
		}
		st, code, reason := s.ctxEnd(ctx)
		return true, st, code, reason
	case errors.Is(err, providers.ErrContextLength):
		return true, endContextFull, ExitContextFull, "context window full"
	default:
		return true, endFailed, ExitFailure, "provider: " + err.Error()
	}
}

func (s *Session) cancelUses(uses []providers.ContentBlock) []providers.ContentBlock {
	out := make([]providers.ContentBlock, len(uses))
	for i, u := range uses {
		out[i] = providers.ContentBlock{Type: providers.BlockToolResult, ToolUseID: u.ToolUseID, Text: cancelledOutput, IsError: true}
		s.note(s.c.Log.Append(EvToolResult, map[string]any{
			"call_id": u.ToolUseID, "tool": u.Name, "status": statusCancelled, "output": cancelledOutput,
		}))
	}
	s.note(s.c.Log.Sync())
	return out
}

func (s *Session) recordUsage(m *msgState, u providers.Usage) {
	m.usage = addUsage(m.usage, u)
	s.usage = addUsage(s.usage, u)
	s.note(s.c.Log.Append(EvUsage, map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens, "cached_tokens": u.CachedTokens,
	}))
}

func addUsage(a, b providers.Usage) providers.Usage {
	a.PromptTokens += b.PromptTokens
	a.CompletionTokens += b.CompletionTokens
	a.CachedTokens += b.CachedTokens
	return a
}

// logAssistant logs msg and returns its event id ("" if the append failed).
// finish is the reply's finish_reason, logged when known.
func (s *Session) logAssistant(msg providers.Message, final bool, finish string) string {
	data := map[string]any{"text": textOf(msg), "final": final, "content": toWire(msg.Content)}
	if finish != "" {
		data["finish_reason"] = finish
	}
	id, err := s.c.Log.AppendID(EvAssistantMessage, data)
	s.note(err)
	return id
}
