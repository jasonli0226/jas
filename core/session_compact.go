package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonli0226/jas/providers"
)

// ErrNothingToCompact means history has no range worth summarizing.
var ErrNothingToCompact = errors.New("nothing to compact")

// reasonFullAfterCompact ends a turn whose retried request overflowed again.
const reasonFullAfterCompact = "context window full after compaction"

// CompactResult describes one compaction.
type CompactResult struct {
	Messages     int // messages replaced by the summary
	TokensBefore int
	TokensAfter  int
	Usage        providers.Usage
}

// Compact replaces older history with a model-written summary, keeping about
// Compact.Keep tokens of recent messages verbatim. On any error history is
// unchanged. focus, if set, steers what the summary emphasizes.
func (s *Session) Compact(ctx context.Context, focus string) (CompactResult, error) {
	c := s.c
	if c.Compact == nil || c.Budget == nil {
		return CompactResult{}, errors.New("compaction is off")
	}
	prev, hasPrev := "", false
	if len(s.messages) > 0 {
		prev, hasPrev = isSummary(s.messages[0])
	}
	cut := cutIndex(s.messages, c.Compact.Keep)
	if cut == 0 {
		return CompactResult{}, ErrNothingToCompact
	}
	firstKept := s.ids[cut]
	if firstKept == "" { // resumed from a file older than event ids
		return CompactResult{}, errors.New("compaction failed: kept message has no event id")
	}
	start := 0
	if hasPrev {
		start = 1
	}
	span := s.messages[start:cut]
	model := c.Compact.Model
	if model == "" {
		model = s.model
	}
	text, usage, err := s.summarize(ctx, summaryRequest(model, span, prev, focus))
	if err != nil {
		return CompactResult{}, err
	}
	read, modified := fileLists(prev, span)
	summary := withFileLists(text, read, modified)

	msgs := append([]providers.Message{summaryMessage(summary)}, s.messages[cut:]...)
	before := c.Budget.Count(s.messages)
	after := (&Budget{}).Count(msgs) // what Count gives after Rebase
	// Log before changing anything, so a failed write leaves history intact.
	id, err := c.Log.AppendID(EvCompaction, map[string]any{
		"summary": summary, "first_kept_id": firstKept,
		"tokens_before": before, "tokens_after": after,
		"usage": map[string]any{"prompt_tokens": usage.PromptTokens, "completion_tokens": usage.CompletionTokens},
	})
	if err != nil {
		s.note(err)
		return CompactResult{}, fmt.Errorf("compaction failed: event log: %w", err)
	}
	s.messages = msgs
	s.ids = append([]string{id}, s.ids[cut:]...)
	c.Budget.Rebase()
	s.usage = addUsage(s.usage, usage)
	return CompactResult{Messages: cut, TokensBefore: before, TokensAfter: c.Budget.Count(s.messages), Usage: usage}, nil
}

// summarize streams a summary request and returns its text. Nothing reaches
// the UI.
func (s *Session) summarize(ctx context.Context, req providers.Request) (string, providers.Usage, error) {
	stream, errc := s.c.Provider.Stream(ctx, req)
	var (
		acc   providers.Accumulator
		usage providers.Usage
	)
	for ev := range stream {
		if ev.Usage != nil {
			usage = *ev.Usage
		}
		acc.Add(ev)
	}
	streamErr := <-errc
	msg, uses, doneErr := acc.Done()
	if streamErr == nil {
		streamErr = doneErr
	}
	switch {
	case ctx.Err() != nil:
		return "", usage, ctx.Err()
	case streamErr != nil:
		return "", usage, fmt.Errorf("compaction failed: %w", streamErr)
	case len(uses) > 0:
		return "", usage, errors.New("compaction failed: summary tried to call a tool")
	}
	text := textOf(msg)
	if strings.TrimSpace(text) == "" {
		return "", usage, errors.New("compaction failed: empty summary")
	}
	return text, usage, nil
}

// autoCompact compacts when the prompt is over the threshold. done reports
// that the turn ends here; nothing to compact falls through to the budget
// check.
func (s *Session) autoCompact(ctx context.Context) (done bool, status string, code int, reason string) {
	c := s.c
	if c.Compact == nil || c.Budget == nil || c.Budget.Count(s.messages) <= compactThreshold(c.Budget.Window, c.Compact.Reserve) {
		return false, "", 0, ""
	}
	_, err := s.Compact(ctx, "")
	if errors.Is(err, ErrNothingToCompact) {
		return false, "", 0, ""
	}
	return compactFailed(ctx, err)
}

// retryAfterOverflow reports whether a request that failed with err should be
// redone after compacting.
func (s *Session) retryAfterOverflow(ctx context.Context, err error) bool {
	return s.c.Compact != nil && ctx.Err() == nil && errors.Is(err, providers.ErrContextLength)
}

// compactFailed maps a Compact error to a turn ending (none when err is nil).
func compactFailed(ctx context.Context, err error) (bool, string, int, string) {
	switch {
	case err == nil:
		return false, "", 0, ""
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return true, endInterrupted, ExitInterrupted, "interrupted"
	default:
		return true, endContextFull, ExitContextFull, err.Error()
	}
}
