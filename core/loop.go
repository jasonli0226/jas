package core

import (
	"context"
	"errors"
	"strings"

	"github.com/jasonli0226/jas/providers"
)

// DefaultMaxTurns is used when MaxTurns is not positive.
const DefaultMaxTurns = 50

// session_ended statuses.
const (
	endOK             = "ok"
	endContextFull    = "context_full"
	endTurnCap        = "turn_cap"
	endApprovalNeeded = "approval_needed"
	endInterrupted    = "interrupted"
	endFailed         = "failed"
	endBudget         = "budget_exceeded"
	endOutputCap      = "output_cap"
)

// finishLength is the finish_reason of a reply cut off at max_tokens.
const finishLength = "length"

// Budget-stop reasons (session_ended / turn_ended reason for exit 7).
const (
	reasonTokens  = "tokens"
	reasonTimeout = "timeout"
)

// RunConfig is everything Run needs. Log should be the same log the Executor
// writes to (wrap it with NewUILog to show events in a UI).
type RunConfig struct {
	Provider        providers.Provider
	Model           string
	Prompt          string
	System          string
	Exec            *Executor
	Log             EventLog
	UI              UI
	MaxTurns        int
	Budget          *Budget        // nil = no context budget
	SessionMeta     map[string]any // merged into session_started
	MaxOutput       int            // max_tokens per model request; 0 omits it
	ReasoningEffort string         // caps a reasoning model's thinking; "" omits it
	Tokens          *TokenPool     // nil = no token cap
	Compact         *CompactConfig // nil = compaction off (the jas run default)
	WrapUp          bool           // ask for a final answer at the turn or token cap
}

// Summary describes a finished run.
type Summary struct {
	Turns int
	Usage providers.Usage
	Final string
	// Messages is the final conversation history, for inspection.
	Messages []providers.Message
}

// Run drives one prompt through a fresh Session: session_started, one Send,
// session_ended. It returns an *ExitError for any non-zero exit code.
func Run(ctx context.Context, c RunConfig) (Summary, error) {
	sess, err := NewSession(SessionConfig{
		Provider: c.Provider, Model: c.Model, System: c.System, Exec: c.Exec,
		Log: c.Log, UI: c.UI, MaxTurns: c.MaxTurns, Budget: c.Budget, Meta: c.SessionMeta, MaxOutput: c.MaxOutput, ReasoningEffort: c.ReasoningEffort, Tokens: c.Tokens, Compact: c.Compact, WrapUp: c.WrapUp,
	})
	if err != nil {
		return Summary{}, err
	}
	res, sendErr := sess.Send(ctx, c.Prompt)
	closeErr := sess.Close(res.Status, res.Code)
	return finish(Summary{Turns: res.Turns, Usage: res.Usage, Final: res.Final, Messages: sess.Messages()},
		res.Code, res.Reason, errors.Join(sendErr, closeErr))
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

// finish converts a non-zero code to an *ExitError. A log failure turns an
// otherwise clean run into exit 1; for other codes it is joined to the
// returned error so errors.Is finds it.
func finish(sum Summary, code int, reason string, logErr error) (Summary, error) {
	if code == ExitOK && logErr != nil {
		code, reason = ExitFailure, "event log failure"
	}
	if code == ExitOK {
		return sum, nil
	}
	ee := &ExitError{Code: code, Reason: reason}
	if logErr != nil {
		return sum, errors.Join(ee, logErr)
	}
	return sum, ee
}
