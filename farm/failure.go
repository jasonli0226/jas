package farm

import (
	"errors"
	"fmt"

	"github.com/jasonli0226/jas/core"
)

// Part failure reasons (manifest failure_reason).
const (
	ReasonError          = "error"
	ReasonBudget         = "budget"
	ReasonTimeout        = "timeout"
	ReasonTurnCap        = "turn_cap"
	ReasonWriteLimit     = "write_limit"
	ReasonApprovalNeeded = "approval_needed"
	ReasonSetup          = "setup"
	ReasonMergeConflict  = "merge_conflict"
)

// Owner choices for a failed part (TTY) and BudgetStop values.
const (
	choiceRetry = "retry"
	choiceDrop  = "drop"
	choiceAbort = "abort"

	stopTokens  = "tokens"
	stopTimeout = "timeout"

	choiceSteerUndelivered = "steer_undelivered"
)

var failureChoices = []string{choiceRetry, choiceDrop, choiceAbort}

// endKind is how one attempt of a part ended.
type endKind int

const (
	endDone     endKind = iota
	endFailed           // the part failed; reason says why
	endStopped          // the farm stopped it (interrupt, abort, farm budget)
	endFarmStop         // it hit the farm's token pool: stop the farm
)

// attemptEnd is the outcome of one attempt, sent to the scheduler.
type attemptEnd struct {
	id       string
	kind     endKind
	reason   string // failure reason for endFailed
	detail   string // human detail, used in the retry note
	exitCode int
}

func failed(id, reason string, code int, detail string) attemptEnd {
	return attemptEnd{id: id, kind: endFailed, reason: reason, exitCode: code, detail: detail}
}

// classifyTurn maps a worker's turn result to an attempt end. farmPools are
// the pool names whose exhaustion stops the whole farm; stopping reports
// that the farm itself cancelled or gated the session.
func classifyTurn(id string, res core.TurnResult, farmPools map[string]bool, stopping bool) attemptEnd {
	switch res.Code {
	case core.ExitOK:
		return attemptEnd{id: id, kind: endDone}
	case core.ExitTurnCap:
		return failed(id, ReasonTurnCap, res.Code, res.Reason)
	case core.ExitApprovalNeeded:
		return failed(id, ReasonApprovalNeeded, res.Code, res.Reason)
	case core.ExitInterrupted:
		if stopping {
			return attemptEnd{id: id, kind: endStopped, exitCode: res.Code}
		}
		return failed(id, ReasonError, res.Code, res.Reason)
	case core.ExitBudget:
		switch {
		case res.Reason == stopTokens && farmPools[res.Pool]:
			return attemptEnd{id: id, kind: endFarmStop, exitCode: res.Code, detail: "farm token budget"}
		case res.Reason == stopTokens:
			return failed(id, ReasonBudget, res.Code, "token budget "+res.Pool)
		case stopping:
			return attemptEnd{id: id, kind: endStopped, exitCode: res.Code}
		default:
			return failed(id, ReasonTimeout, res.Code, "per_worker_timeout")
		}
	}
	return failed(id, ReasonError, res.Code, fmt.Sprintf("%s (exit %d)", res.Reason, res.Code))
}

// prepareReason maps a worktree preparation error to a failure reason.
func prepareReason(err error) string {
	var mc *MergeConflictError
	if errors.As(err, &mc) {
		return ReasonMergeConflict
	}
	return ReasonSetup
}

// decide picks retry, drop or abort for a failed part: the owner's answer on
// a TTY, else on_failure.
func (s *Supervisor) decide(e attemptEnd) (choice, source string) {
	if s.Ask != nil {
		prompt := fmt.Sprintf("part %s failed (%s: %s); retry, drop or abort? ", e.id, e.reason, e.detail)
		ask := func() (string, bool) { return s.Ask(prompt, failureChoices) }
		var c string
		var ok bool
		if s.Approvals != nil {
			c, ok = s.Approvals.Ask(s.hard(), ask)
		} else {
			c, ok = ask()
		}
		if ok && (c == choiceRetry || c == choiceDrop || c == choiceAbort) {
			return c, "owner"
		}
		if ok { // anything else is the safe default
			return choiceDrop, "owner"
		}
	}
	if s.Cfg.OnFailure == onFailureAbort {
		return choiceAbort, "on_failure"
	}
	return choiceDrop, "on_failure"
}
