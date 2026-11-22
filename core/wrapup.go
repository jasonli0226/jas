package core

import (
	"errors"
	"fmt"
	"slices"

	"github.com/jasonli0226/jas/providers"
)

// Wrap-up (#195): with SessionConfig.WrapUp, the request that would end a
// run at its turn cap or token cap becomes a last request with tools off and
// a note asking for the final answer, so a capped run still answers. The
// note goes into that request only, not into the history.

// Wrap-up reasons (the wrap_up event's reason).
const (
	wrapTurnCap = "turn_cap"
	wrapTokens  = "tokens"
)

// minWrapOutput is the least output room a token-cap wrap-up needs; with
// less the run stops at the cap as it would without WrapUp.
const minWrapOutput = 1000

const (
	wrapNoteTurns = "You have reached the turn limit for this run. Do not call tools. " +
		"Give your final answer now, from what you have gathered so far, in the format you were asked for."
	wrapNoteTokens = "You have nearly used the token budget for this run. Do not call tools. " +
		"Give your final answer now, from what you have gathered so far, in the format you were asked for."
)

// wrapUp is a pending wrap-up request.
type wrapUp struct {
	reason    string
	maxTokens int          // output cap for the request; 0 keeps outputCap
	budget    *BudgetError // the cap a token wrap-up stands in for
}

func (w *wrapUp) note() string {
	if w.reason == wrapTokens {
		return wrapNoteTokens
	}
	return wrapNoteTurns
}

// turnWrapUp is the wrap-up for the last allowed turn, or nil.
func (s *Session) turnWrapUp(m *msgState) *wrapUp {
	if !s.c.WrapUp || m.turns != s.c.MaxTurns {
		return nil
	}
	return &wrapUp{reason: wrapTurnCap}
}

// tokenWrapUp is the wrap-up that stands in for a request refused by the
// token cap: its output cap is the room left after the prompt and the note.
// nil when WrapUp is off, err is no budget error, or the room is too small.
func (s *Session) tokenWrapUp(err error) *wrapUp {
	var be *BudgetError
	if !s.c.WrapUp || !errors.As(err, &be) {
		return nil
	}
	return tokenWrap(be, be.Want-s.outputCap())
}

// earlyWrapUp makes the next request the token wrap-up when the room it
// would leave under the token cap is less than wrapReserve, so a model that
// reasons before it answers still has a full output cap for the answer
// (#219). nil when WrapUp is off, there is no cap, or there is room.
func (s *Session) earlyWrapUp() *wrapUp {
	if !s.c.WrapUp || s.c.Tokens == nil {
		return nil
	}
	pool, left := s.c.Tokens.tightest()
	if pool == nil {
		return nil
	}
	est := s.estimate(s.newRequest())
	if left-est >= s.wrapReserve() {
		return nil
	}
	be := &BudgetError{Pool: pool.name, Limit: pool.limit, Used: pool.limit - left, Want: est}
	return tokenWrap(be, est-s.outputCap())
}

// tokenWrap is the token wrap-up for cap be and a request whose prompt is
// about prompt tokens, or nil when the room left is under minWrapOutput.
func tokenWrap(be *BudgetError, prompt int) *wrapUp {
	room := be.Limit - be.Used - prompt - len(wrapNoteTokens)/bytesPerToken - wrapNoteSlack
	if room < minWrapOutput {
		return nil
	}
	return &wrapUp{reason: wrapTokens, maxTokens: room, budget: be}
}

// wrapReserve is the output room an early token wrap-up holds back: the
// output cap, or defaultWrapReserve when none is sent.
func (s *Session) wrapReserve() int {
	if n := s.outputCap(); n > 0 {
		return n
	}
	return defaultWrapReserve
}

// defaultWrapReserve is wrapReserve without an output cap; reasoning models
// can spend tens of thousands of tokens before the answer.
const defaultWrapReserve = 32000

// wrapNoteSlack covers the estimate's rounding of the note's message.
const wrapNoteSlack = 16

// wrapRequest turns req into the wrap-up request w.
func wrapRequest(req providers.Request, w *wrapUp) providers.Request {
	req.ToolChoice = "none"
	if w.maxTokens > 0 && (req.MaxTokens <= 0 || w.maxTokens < req.MaxTokens) {
		req.MaxTokens = w.maxTokens
	}
	msgs := slices.Clone(req.Messages)
	note := providers.ContentBlock{Type: providers.BlockText, Text: w.note()}
	if n := len(msgs); n > 0 && msgs[n-1].Role == providers.RoleUser {
		last := msgs[n-1]
		last.Content = append(slices.Clone(last.Content), note)
		msgs[n-1] = last
	} else {
		msgs = append(msgs, providers.Message{Role: providers.RoleUser, Content: []providers.ContentBlock{note}})
	}
	req.Messages = msgs
	return req
}

// wrapCutOff ends the turn after a token wrap-up reply cut off at its output
// cap: the budget ran out, so the run ends at the token cap it stood in for
// (exit 7), not at max_output_tokens (#219).
func (s *Session) wrapCutOff(m *msgState, w *wrapUp, msg providers.Message) (bool, string, int, string) {
	s.push(msg, s.logAssistant(msg, false, m.finish))
	m.final = textOf(msg)
	_, st, code, reason := s.budgetStopped(w.budget)
	return true, st, code, reason
}

// wrapEnd ends the turn after the wrap-up reply. An answer ends it ok; tool
// calls are not run (their results say so, keeping the history whole) and
// the turn ends at the cap the wrap-up stood in for.
func (s *Session) wrapEnd(m *msgState, w *wrapUp, msg providers.Message, uses []providers.ContentBlock) (bool, string, int, string) {
	s.push(msg, s.logAssistant(msg, len(uses) == 0, m.finish))
	if len(uses) == 0 {
		m.final = textOf(msg)
		if w.reason == wrapTokens {
			return true, endOK, ExitOK, "wrapped up at the token cap"
		}
		return true, endOK, ExitOK, "wrapped up at the turn cap"
	}
	s.push(providers.Message{Role: providers.RoleUser, Content: s.notRunUses(uses)}, "")
	if w.reason == wrapTokens {
		_, st, code, reason := s.budgetStopped(w.budget)
		return true, st, code, reason
	}
	return true, endTurnCap, ExitTurnCap, fmt.Sprintf("turn cap of %d reached", s.c.MaxTurns)
}

// notRunUses logs and returns not_run results for tool calls jas will not run.
func (s *Session) notRunUses(uses []providers.ContentBlock) []providers.ContentBlock {
	out := make([]providers.ContentBlock, len(uses))
	for i, u := range uses {
		out[i] = providers.ContentBlock{Type: providers.BlockToolResult, ToolUseID: u.ToolUseID, Text: wrapNotRunOutput, IsError: true}
		s.note(s.c.Log.Append(EvToolResult, map[string]any{
			"call_id": u.ToolUseID, "tool": u.Name, "status": statusNotRun, "output": wrapNotRunOutput,
		}))
	}
	s.note(s.c.Log.Sync())
	return out
}

const wrapNotRunOutput = "not run: the run reached its cap; this call had no effect."
