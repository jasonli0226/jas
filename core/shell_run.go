package core

import "fmt"

// ShellRun is the owner's ! or !! command as the session log records it once
// it finishes. Local (!!) output is kept for the owner and never reaches the
// model, on resume included.
type ShellRun struct {
	Command   string `json:"command"`
	Output    string `json:"output"`
	ExitCode  int    `json:"exit_code"`
	Error     string `json:"error,omitempty"` // the command could not run
	Cancelled bool   `json:"cancelled,omitempty"`
	Truncated bool   `json:"truncated,omitempty"` // Output hit the capture cap
	Local     bool   `json:"local,omitempty"`
}

// RecordShell logs a finished owner command. It only appends to the log, so
// it is safe to call from the command's own goroutine during a Send.
func (s *Session) RecordShell(r ShellRun) error {
	if err := s.c.Log.Append(EvShellRan, r); err != nil {
		return fmt.Errorf("event log: %w", err)
	}
	return nil
}

// PendingShell returns the ! commands a resumed session's next message
// should carry (ReplayState.PendingShell); nil for a new session.
func (s *Session) PendingShell() []ShellRun { return s.pendingShell }
