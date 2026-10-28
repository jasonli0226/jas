package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/providers"
)

// DefaultMaxTurns is used when RunConfig.MaxTurns is not positive.
const DefaultMaxTurns = 50

// session_ended statuses.
const (
	endOK             = "ok"
	endContextFull    = "context_full"
	endTurnCap        = "turn_cap"
	endApprovalNeeded = "approval_needed"
	endInterrupted    = "interrupted"
	endFailed         = "failed"
)

// RunConfig is everything Run needs. Log should be the same log the Executor
// writes to (wrap it with NewUILog to show events in a UI).
type RunConfig struct {
	Provider    providers.Provider
	Model       string
	Prompt      string
	System      string
	Exec        *Executor
	Log         EventLog
	UI          UI
	MaxTurns    int
	SessionMeta map[string]any // merged into session_started
}

// Summary describes a finished run.
type Summary struct {
	Turns int
	Usage providers.Usage
	Final string
}

type runState struct {
	c        RunConfig
	messages []providers.Message
	sum      Summary
}

// Run drives the agent loop: stream a response, run its tool calls, repeat
// until a final answer, the turn cap, an error, or cancellation. It always
// writes session_started first and session_ended last, and returns an
// *ExitError for any non-zero exit code.
func Run(ctx context.Context, c RunConfig) (Summary, error) {
	if c.MaxTurns <= 0 {
		c.MaxTurns = DefaultMaxTurns
	}
	if c.UI == nil {
		c.UI = NewQuietUI()
	}
	s := &runState{c: c, messages: []providers.Message{{
		Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: c.Prompt}},
	}}}
	started := map[string]any{}
	maps.Copy(started, c.SessionMeta)
	started["model"] = c.Model
	if err := c.Log.AppendSync(EvSessionStarted, started); err != nil {
		return s.sum, fmt.Errorf("event log: %w", err)
	}
	status, code, reason := s.loop(ctx)
	return s.finish(status, code, reason)
}

func (s *runState) loop(ctx context.Context) (status string, code int, reason string) {
	for s.sum.Turns < s.c.MaxTurns {
		if ctx.Err() != nil {
			return endInterrupted, ExitInterrupted, "interrupted"
		}
		s.sum.Turns++
		done, st, code, reason := s.turn(ctx)
		if done {
			return st, code, reason
		}
	}
	return endTurnCap, ExitTurnCap, fmt.Sprintf("turn cap of %d reached", s.c.MaxTurns)
}

// turn runs one provider round-trip plus its tool calls. done reports that the
// run is over (successfully or not).
func (s *runState) turn(ctx context.Context) (done bool, status string, code int, reason string) {
	c := s.c
	req := providers.Request{
		Model: c.Model, System: c.System, Tools: c.Exec.Reg.Specs(),
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
		s.recordUsage(*usage)
	}
	msg, uses, doneErr := acc.Done()
	if streamErr == nil && doneErr != nil {
		streamErr = doneErr
	}
	if streamErr != nil {
		return s.streamFailed(ctx, streamErr, msg, uses)
	}

	s.logAssistant(msg, len(uses) == 0)
	if len(uses) == 0 {
		s.sum.Final = textOf(msg)
		return true, endOK, ExitOK, ""
	}
	s.messages = append(s.messages, msg)
	results, err := c.Exec.Run(ctx, uses)
	s.messages = append(s.messages, providers.Message{Role: providers.RoleUser, Content: results})
	if err != nil {
		st, code := classify(err)
		return true, st, code, err.Error()
	}
	return false, "", 0, ""
}

// streamFailed maps a provider error to an exit. On cancellation, tool calls
// accumulated so far get synthesized cancelled results (design §3 rule 4).
func (s *runState) streamFailed(ctx context.Context, err error, msg providers.Message, uses []providers.ContentBlock) (bool, string, int, string) {
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

func (s *runState) cancelUses(uses []providers.ContentBlock) []providers.ContentBlock {
	out := make([]providers.ContentBlock, len(uses))
	for i, u := range uses {
		out[i] = providers.ContentBlock{Type: providers.BlockToolResult, ToolUseID: u.ToolUseID, Text: cancelledOutput, IsError: true}
		_ = s.c.Log.Append(EvToolResult, map[string]any{
			"call_id": u.ToolUseID, "tool": u.Name, "status": statusCancelled, "output": cancelledOutput,
		})
	}
	_ = s.c.Log.Sync()
	return out
}

func (s *runState) recordUsage(u providers.Usage) {
	s.sum.Usage.PromptTokens += u.PromptTokens
	s.sum.Usage.CompletionTokens += u.CompletionTokens
	s.sum.Usage.CachedTokens += u.CachedTokens
	_ = s.c.Log.Append(EvUsage, map[string]any{
		"prompt_tokens": u.PromptTokens, "completion_tokens": u.CompletionTokens, "cached_tokens": u.CachedTokens,
	})
}

func (s *runState) logAssistant(msg providers.Message, final bool) {
	_ = s.c.Log.Append(EvAssistantMessage, map[string]any{"text": textOf(msg), "final": final})
}

func textOf(m providers.Message) string {
	var b strings.Builder
	for _, blk := range m.Content {
		if blk.Type == providers.BlockText {
			b.WriteString(blk.Text)
		}
	}
	return b.String()
}

// classify maps an Executor error to a session status and exit code.
func classify(err error) (string, int) {
	var ee *ExitError
	switch {
	case errors.Is(err, context.Canceled):
		return endInterrupted, ExitInterrupted
	case errors.As(err, &ee):
		if ee.Code == ExitApprovalNeeded {
			return endApprovalNeeded, ee.Code
		}
		return endFailed, ee.Code
	default:
		return endFailed, ExitFailure
	}
}

// finish writes session_ended and converts a non-zero code to an *ExitError.
func (s *runState) finish(status string, code int, reason string) (Summary, error) {
	err := s.c.Log.AppendSync(EvSessionEnded, map[string]any{
		"status": status, "exit_code": code, "turns": s.sum.Turns,
		"prompt_tokens": s.sum.Usage.PromptTokens, "completion_tokens": s.sum.Usage.CompletionTokens,
	})
	if code != ExitOK {
		return s.sum, &ExitError{Code: code, Reason: reason}
	}
	if err != nil {
		return s.sum, fmt.Errorf("event log: %w", err)
	}
	return s.sum, nil
}
