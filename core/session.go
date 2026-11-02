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
}

// TurnResult describes how one user message (one Send, or the last steering
// message it picked up) ended.
type TurnResult struct {
	Status string // "ok" | "context_full" | "turn_cap" | "approval_needed" | "interrupted" | "failed"
	Code   int    // the Exit* code jas run would exit with
	Reason string
	Turns  int // model requests for this user message
	Usage  providers.Usage
	Final  string
}

// Session holds a conversation across user messages. Send, SetModel, Messages
// and Close must not overlap; Steer is safe from any goroutine. The chat (tui)
// runs Send on its own goroutine and refuses /new, /model and /resume until it
// returns, so the other calls never run during a Send and need no mutex.
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

	mu    sync.Mutex
	queue []string // steering texts not yet delivered
}

// msgState is the per-user-message part of a Send.
type msgState struct {
	turns int
	usage providers.Usage
	final string
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
	s.push(providers.Message{Role: providers.RoleUser, Content: content}, id)
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
	return TurnResult{Status: status, Code: code, Reason: reason, Turns: m.turns, Usage: m.usage, Final: m.final}
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
	if err := s.c.Log.Append(EvModelChanged, map[string]any{"model": model}); err != nil {
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
	msg, uses, err := s.request(ctx, m)
	if done, st, code, reason := s.budgetStopped(err); done {
		return true, st, code, reason
	}
	if err != nil && s.retryAfterOverflow(ctx, err) {
		_, cerr := s.Compact(ctx, "")
		if done, st, code, reason := s.compactFailed(ctx, cerr); done {
			return true, st, code, reason
		}
		msg, uses, err = s.request(ctx, m)
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

	s.push(msg, s.logAssistant(msg, len(uses) == 0))
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

// request streams one model request over the current history, showing its
// output and recording its usage.
func (s *Session) request(ctx context.Context, m *msgState) (providers.Message, []providers.ContentBlock, error) {
	c := s.c
	req := providers.Request{
		Model: s.model, System: c.System, Tools: c.Exec.Reg.Specs(),
		Messages: slices.Clone(s.messages), MaxTokens: s.outputCap(),
	}
	est := s.estimate(req)
	res, err := s.reserve(est)
	if err != nil {
		return providers.Message{}, nil, err
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
			s.push(msg, s.logAssistant(msg, false))
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
func (s *Session) logAssistant(msg providers.Message, final bool) string {
	id, err := s.c.Log.AppendID(EvAssistantMessage, map[string]any{
		"text": textOf(msg), "final": final, "content": toWire(msg.Content),
	})
	s.note(err)
	return id
}
