package core

import "fmt"

// Process exit codes (design §3).
const (
	ExitOK             = 0
	ExitFailure        = 1
	ExitUsage          = 2
	ExitContextFull    = 3
	ExitTurnCap        = 4
	ExitApprovalNeeded = 5
	ExitUnmet          = 6 // a check run by jas failed, or required work is unmet
	ExitBudget         = 7 // a token or wall-clock cap was reached
	ExitRefused        = 8 // farm planner refused to split
	ExitInterrupted    = 130
)

// ExitError carries an exit code and a human reason.
type ExitError struct {
	Code   int
	Reason string
}

func (e *ExitError) Error() string { return fmt.Sprintf("%s (exit %d)", e.Reason, e.Code) }
