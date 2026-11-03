package tui

import (
	"context"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
)

// Farmer is the chat's farm port: plan a task, review it, and start the
// approved plan. cmd/jas implements it over the same planner and supervisor
// as jas farm. Every method may block and log, so the chat calls them from
// a tea.Cmd, never from Update.
type Farmer interface {
	// Available reports whether a farm can run here (a trusted project whose
	// check resolves) and, if not, why.
	Available() (ok bool, why string)
	// Plan drafts a plan for task on model (the chat's current model).
	Plan(ctx context.Context, task, model string) (FarmPlan, error)
	// Replan sends the owner's feedback to p's planner.
	Replan(ctx context.Context, p FarmPlan, feedback string) (FarmPlan, error)
	// Recheck re-validates raw plan bytes the owner edited.
	Recheck(p FarmPlan, raw []byte) FarmPlan
	// Approve writes p with its approval into the farm dir and returns the
	// path; it refuses a plan with a refusal or problems.
	Approve(p FarmPlan) (planPath string, err error)
	// Discard closes p's planner without approving it.
	Discard(p FarmPlan)
	// Start runs an approved plan on model at level, reporting through h.
	Start(ctx context.Context, planPath, model string, level core.Level, h FarmHooks) (FarmRun, error)
}

// FarmPlan is a drafted plan as the chat shows it.
type FarmPlan struct {
	ID       string   // farm dir id; opaque to the chat
	Task     string   // the task the owner gave
	Summary  []string // plan summary lines, redacted and sanitized
	Raw      []byte   // plan YAML, for $EDITOR
	Refusal  string   // planner's refusal reason; "" when it split
	Suggest  string   // with a refusal: what to run instead
	Problems []string // validation problems that block approval
}

// FarmHooks is how a running farm reports to the chat. Every hook is called
// from farm goroutines.
type FarmHooks struct {
	Event       func(typ string, m map[string]any)            // farm.jsonl events
	Worker      func(part string) core.UI                     // a UI per session (parts, "integrate")
	Gate        func(part, approve string) (core.Gate, error) // approval gate per session
	Ask         func(prompt string, choices []string) (string, bool)
	Undelivered func(part string, texts []string) // steering a part never read
}

// FarmRun is a running farm (*farm.Handle).
type FarmRun interface {
	FarmID() string
	Snapshot() farm.Manifest
	Steer(part, text string) error
	Pause(part string) error
	Resume(part string) error
	Drop(part string) error
	Stop(hard bool)
	Done() <-chan struct{}
	Wait() farm.Result
}

var _ FarmRun = (*farm.Handle)(nil)
