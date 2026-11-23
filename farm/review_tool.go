package farm

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/jasonli0226/jas/core"
)

// Finding kinds a reviewer may post.
const (
	FindingMissed = "missed"
	FindingDrift  = "drift"
	FindingUnsure = "unsure"
)

var findingKinds = []string{FindingMissed, FindingDrift, FindingUnsure}

// Finding is one reviewer finding with its supervisor-assigned id.
type Finding struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	PlanRef string `json:"plan_ref"`
	Where   string `json:"where"`
	Text    string `json:"text"`
}

// reviewRound is the state of one reviewer session: the plan it checks
// against, the round (1 or 2), the round-1 ids a round-2 session may refer
// to, and what it has posted so far. Safe for concurrent use.
type reviewRound struct {
	plan     Plan
	round    int
	priorIDs []string

	// postMu serializes whole posts so ids follow post order. It is never
	// taken by findings()/finished(), so a board hook may call them.
	postMu sync.Mutex
	mu     sync.Mutex // guards found and isDone
	found  []Finding
	isDone bool
}

// newReviewRound starts a round; prior is the round-1 findings (nil in round 1).
func newReviewRound(plan Plan, round int, prior []Finding) *reviewRound {
	ids := make([]string, len(prior))
	for i, f := range prior {
		ids[i] = f.ID
	}
	return &reviewRound{plan: plan, round: round, priorIDs: ids}
}

// findings returns a copy of the findings posted this round, in post order.
func (r *reviewRound) findings() []Finding {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.found)
}

// finished reports whether review_done was posted this round.
func (r *reviewRound) finished() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.isDone
}

// planRefOK reports whether ref names the task, a part, an interface, or
// (when the plan has any) the contracts.
func (r *reviewRound) planRefOK(ref string) bool {
	switch {
	case ref == "task":
		return true
	case ref == "contracts":
		return strings.TrimSpace(r.plan.Contracts) != ""
	case strings.HasPrefix(ref, "interface:"):
		name := strings.TrimPrefix(ref, "interface:")
		return slices.ContainsFunc(r.plan.Interfaces, func(i Interface) bool { return i.Name == name })
	}
	return slices.ContainsFunc(r.plan.Parts, func(p Part) bool { return p.ID == ref })
}

// assignID validates a supplied id (round 2) or picks the next free one.
// The caller holds r.postMu (found only changes under it).
func (r *reviewRound) assignID(given string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.round == 1 {
		if given != "" {
			return "", fmt.Errorf("id is assigned by the supervisor; omit it")
		}
		return "R" + strconv.Itoa(len(r.found)+1), nil
	}
	if given != "" {
		if !slices.Contains(r.priorIDs, given) {
			return "", fmt.Errorf("id %q is not a round-1 finding (%s); omit id for a new finding", given, strings.Join(r.priorIDs, ", "))
		}
		return given, nil
	}
	for n := 1; ; n++ {
		id := "R" + strconv.Itoa(n)
		if !slices.Contains(r.priorIDs, id) && !slices.ContainsFunc(r.found, func(f Finding) bool { return f.ID == id }) {
			return id, nil
		}
	}
}

// ReviewPostTool is the board_post tool a reviewer uses: findings and
// review_done only, checked against the plan. It writes only the board, so
// it needs no approval (ReadOnly).
type ReviewPostTool struct {
	Board *Board
	Part  string
	// SessionID is recorded on each post; optional.
	SessionID string
	Round     *reviewRound
}

func (ReviewPostTool) Name() string { return "board_post" }
func (ReviewPostTool) Description() string {
	return "Report review results. finding {kind: missed|drift|unsure, plan_ref: task|<part id>|interface:<name>|contracts, " +
		"where: file:line (required unless kind is missed), text} for each plan deviation; " +
		"review_done {summary, round} exactly once when finished. Round 2: set id to a round-1 finding id to " +
		"re-report it; omit id for a new finding. Brief and factual."
}
func (ReviewPostTool) Kind() core.Kind { return core.ReadOnly }
func (ReviewPostTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"topic":{"type":"string","enum":["finding","review_done"]},` +
		`"fields":{"type":"object","description":"finding: {kind, plan_ref, where, text, id?}; review_done: {summary, round}."}},` +
		`"required":["topic","fields"]}`)
}

func (t ReviewPostTool) Call(_ context.Context, _ core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Topic  string         `json:"topic"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return core.Result{Output: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}
	if a.Topic != TopicFinding && a.Topic != TopicReviewDone {
		return rejected(fmt.Errorf("topic must be finding or review_done")), nil
	}
	r := t.Round
	// postMu, not mu, spans PostFrom: its onPost hook may read the round.
	r.postMu.Lock()
	defer r.postMu.Unlock()
	fields := maps2(a.Fields)
	var f Finding
	switch a.Topic {
	case TopicFinding:
		var err error
		if f, err = r.checkFinding(fields); err != nil {
			return rejected(err), nil
		}
		fields["id"] = f.ID
	case TopicReviewDone:
		// Board fields are strings: take a whole JSON number as its string.
		if n, ok := fields["round"].(float64); ok && n == math.Trunc(n) {
			fields["round"] = strconv.Itoa(int(n))
		}
		if got, _ := fields["round"].(string); got != strconv.Itoa(r.round) {
			return rejected(fmt.Errorf("round must be %q", strconv.Itoa(r.round))), nil
		}
	}
	p, err := t.Board.PostFrom(t.Part, reviewerRole, a.Topic, fields, t.SessionID, "")
	if err != nil {
		return rejected(err), nil
	}
	// Keep the board's redacted (and rune-capped) text: findings go on to
	// the integrator, round 2 and the manifest.
	f.Where, _ = p.Fields["where"].(string)
	f.Text, _ = p.Fields["text"].(string)
	r.mu.Lock()
	defer r.mu.Unlock()
	if a.Topic == TopicReviewDone {
		r.isDone = true
		return core.Result{Output: "posted review_done"}, nil
	}
	r.found = append(r.found, f)
	return core.Result{Output: "posted finding " + f.ID}, nil
}

// checkFinding runs the plan-aware checks and assigns the id. The caller
// holds r.postMu.
func (r *reviewRound) checkFinding(fields map[string]any) (Finding, error) {
	s := func(k string) string { v, _ := fields[k].(string); return v }
	kind, ref, where := s("kind"), s("plan_ref"), s("where")
	if !slices.Contains(findingKinds, kind) {
		return Finding{}, fmt.Errorf("kind %q must be one of %s", kind, strings.Join(findingKinds, ", "))
	}
	if !r.planRefOK(ref) {
		return Finding{}, fmt.Errorf("plan_ref %q is not task, a part id, interface:<name> or contracts", ref)
	}
	if kind != FindingMissed && strings.TrimSpace(where) == "" {
		return Finding{}, fmt.Errorf("where is required for kind %s", kind)
	}
	id, err := r.assignID(s("id"))
	if err != nil {
		return Finding{}, err
	}
	return Finding{ID: id, Kind: kind, PlanRef: ref, Where: where, Text: s("text")}, nil
}

func rejected(err error) core.Result {
	return core.Result{Output: "post rejected: " + err.Error(), IsError: true}
}

// maps2 copies fields so the tool never mutates the model's arguments.
func maps2(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}
