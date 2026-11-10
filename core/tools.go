package core

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/jasonli0226/jas/providers"
)

// Kind says whether a tool may change state; it decides batching.
type Kind int

const (
	ReadOnly Kind = iota
	Mutating
)

// Result is a tool's output. Errors are results, never Go errors out of the loop.
type Result struct {
	Output  string
	IsError bool
	// Denied marks an error result as a policy refusal; the executor records
	// it with status "denied" rather than "error".
	Denied bool
	Change *Change // display only: never sent to the model or logged as output
}

// Previewer is implemented by mutating tools that can say what a call would
// change without changing anything. A nil Change with a nil error means
// nothing to show.
type Previewer interface {
	Preview(ctx context.Context, p ExecPolicy, args json.RawMessage) (*Change, error)
}

// ExecPolicy is the per-run tool environment. It travels explicitly because
// farm tasks share one process (no os.Chdir).
type ExecPolicy struct {
	Root      string
	MaxOutput int
	// Env lists extra environment variable names shell children inherit,
	// or KEY=VALUE pairs that set a variable. Pairs must come only from
	// trusted code (the farm supervisor), never from project config: a value
	// could set PATH or LD_PRELOAD.
	Env          []string
	Deny         []string
	RedactSkip   []string // config redact_skip_paths (Task 11)
	ShellTimeout time.Duration
	// ReadRoots are extra symlink-resolved directories (the discovered skill
	// directories) that read-only tools may open; see ResolveRead.
	ReadRoots []string
	// ProfileSkills is the symlink-resolved skills directory of the active
	// profile; "" disables the read exception. Read-only scopes rooted in it
	// skip JasHomeFloor and HomeDeny for paths inside it (see readRoot).
	ProfileSkills string
	// HomeDeny is the absolute deny pattern for a moved jas home (also in
	// Deny); the profile read exception lifts it with JasHomeFloor.
	HomeDeny string
	// liftHome is set only by readRoot/ProfileScope on a read scope rooted
	// inside ProfileSkills.
	liftHome bool
	// projectRoot is the resolved project root on a lifted read scope whose
	// read root lies inside the project; its relative patterns still apply.
	projectRoot string
	// Writes limits where write and edit may change files; nil is
	// unrestricted. See WriteAllowed.
	Writes []WriteRule
}

// WriteRule restricts writable paths. A path matches a rule when it matches
// every glob in All; a path is writable when any rule matches.
type WriteRule struct{ All []string }

// Tool is one capability offered to the model.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Kind() Kind
	// Call runs the tool. A returned error is turned into an error result.
	Call(ctx context.Context, p ExecPolicy, args json.RawMessage) (Result, error)
}

// Registry is an immutable set of tools keyed by name.
type Registry struct {
	tools map[string]Tool
}

// NewRegistry builds a registry; a later tool replaces an earlier one of the same name.
func NewRegistry(tools ...Tool) *Registry {
	m := make(map[string]Tool, len(tools))
	for _, t := range tools {
		m[t.Name()] = t
	}
	return &Registry{tools: m}
}

// Get looks a tool up by name.
func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Specs returns tool specs sorted by name so the prompt prefix is stable.
func (r *Registry) Specs() []providers.ToolSpec {
	specs := make([]providers.ToolSpec, 0, len(r.tools))
	for _, t := range r.tools {
		specs = append(specs, providers.ToolSpec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}
	sort.Slice(specs, func(i, j int) bool { return specs[i].Name < specs[j].Name })
	return specs
}

// Decision is a Gate's verdict on a mutating call.
type Decision string

const (
	Granted Decision = "granted"
	// AutoGranted runs a call nobody was asked about: the approve level does
	// not gate it, or a standing grant covers it. No approval event is
	// logged for it, so approval events mean someone actually decided.
	AutoGranted Decision = "auto"
	Denied      Decision = "denied"
	Unavailable Decision = "unavailable" // approval needed but no way to ask
)

// Gate decides whether a mutating call may run.
type Gate interface {
	// preview, when non-nil, returns the diff the call would make; a gate
	// calls it only when it is about to ask.
	Allow(ctx context.Context, t Tool, args json.RawMessage, preview func() *Diff) (Decision, error)
}

// AllowAll grants everything without asking (slice 1).
type AllowAll struct{}

func (AllowAll) Allow(context.Context, Tool, json.RawMessage, func() *Diff) (Decision, error) {
	return AutoGranted, nil
}
