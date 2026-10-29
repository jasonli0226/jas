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
	Meta     map[string]any // merged into session_started / session_resumed
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
// and Close must be called from one goroutine; Steer is safe from any.
type Session struct {
	c        SessionConfig
	model    string
	messages []providers.Message
	turns    int             // session total, for session_ended
	usage    providers.Usage // session total, for session_ended
	logErr   error           // first event-log failure in the current Send
	closed   bool

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
// prompt from current trust.
func ResumeSession(c SessionConfig, st ReplayState) (*Session, error) {
	s := newSession(c)
	s.messages = slices.Clone(st.Messages)
	if err := s.open(EvSessionResumed); err != nil {
		return nil, err
	}
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
		if status == endOK && s.logErr == nil {
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
	s.messages = append(s.messages, providers.Message{Role: providers.RoleUser, Content: content})
	data := map[string]any{"content": toWire(content)}
	if steer {
		data["steer"] = true
	}
	s.note(s.c.Log.Append(EvUserMessage, data))
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
	return TurnResult{Status: status, Code: code, Reason: reason, Turns: m.turns, Usage: m.usage, Final: m.final}
}

// Steer queues text for delivery at the next point the loop can take it.
func (s *Session) Steer(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = append(s.queue, text)
}

// drain empties the steering queue, returning its texts joined.
func (s *Session) drain() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	text := strings.Join(s.queue, steerSep)
	s.queue = nil
	return text
}

// Messages returns a copy of the conversation history.
func (s *Session) Messages() []providers.Message {
	return slices.Clone(s.messages)
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

// Close writes session_ended with session totals. Later calls do nothing.
func (s *Session) Close(status string, code int) error {
	if s.closed {
		return nil
	}
	s.closed = true
	if err := s.c.Log.AppendSync(EvSessionEnded, map[string]any{
		"status": status, "exit_code": code, "turns": s.turns,
		"prompt_tokens": s.usage.PromptTokens, "completion_tokens": s.usage.CompletionTokens,
	}); err != nil {
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
			return endInterrupted, ExitInterrupted, "interrupted"
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
	if c.Budget != nil && c.Budget.WouldExceed(s.messages) {
		return true, endContextFull, ExitContextFull, "context window full"
	}
	req := providers.Request{
		Model: s.model, System: c.System, Tools: c.Exec.Reg.Specs(),
		Messages: slices.Clone(s.messages),
	}
	stream, errc := c.Provider.Stream(ctx, req)
	var (
		acc   providers.Accumulator
		usage *providers.Usage
	)
	for ev := range stream {
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
		s.recordUsage(m, *usage)
		if c.Budget != nil {
			c.Budget.Observe(usage.PromptTokens, len(req.Messages))
		}
	}
	msg, uses, doneErr := acc.Done()
	if streamErr == nil && doneErr != nil {
		streamErr = doneErr
	}
	if streamErr != nil {
		return s.streamFailed(ctx, streamErr, msg, uses)
	}

	s.logAssistant(msg, len(uses) == 0)
	s.messages = append(s.messages, msg)
	if len(uses) == 0 {
		m.final = textOf(msg)
		return true, endOK, ExitOK, ""
	}
	results, err := c.Exec.Run(ctx, uses)
	s.messages = append(s.messages, providers.Message{Role: providers.RoleUser, Content: results})
	if err != nil { // queued steering stays queued for the caller
		st, code := classify(err)
		return true, st, code, err.Error()
	}
	if steer := s.drain(); steer != "" {
		s.addUser(steer, true)
	}
	s.elide()
	return false, "", 0, ""
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
			s.logAssistant(msg, false)
			s.messages = append(s.messages, msg)
			s.messages = append(s.messages, providers.Message{Role: providers.RoleUser, Content: s.cancelUses(uses)})
		}
		return true, endInterrupted, ExitInterrupted, "interrupted"
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

func (s *Session) logAssistant(msg providers.Message, final bool) {
	s.note(s.c.Log.Append(EvAssistantMessage, map[string]any{
		"text": textOf(msg), "final": final, "content": toWire(msg.Content),
	}))
}
