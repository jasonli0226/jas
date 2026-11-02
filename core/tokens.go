package core

import (
	"errors"
	"fmt"
	"sync"

	"github.com/jasonli0226/jas/providers"
)

// TokenPool caps the tokens a run may spend. Requests reserve their estimate
// before they are sent and settle to the reported usage afterwards, so
// concurrent sessions sharing a pool (a farm) can never overshoot it by an
// in-flight request. A pool with a parent also holds every reservation in
// the parent chain.
type TokenPool struct {
	name   string
	limit  int // <= 0: unlimited
	parent *TokenPool

	mu   sync.Mutex
	used int // settled tokens plus outstanding reservations
}

// NewTokenPool returns a pool named name with the given limit (<= 0 means
// unlimited) whose reservations also count against parent (nil for none).
func NewTokenPool(name string, limit int, parent *TokenPool) *TokenPool {
	return &TokenPool{name: name, limit: limit, parent: parent}
}

// BudgetError reports that a reservation would cross a pool's limit.
type BudgetError struct {
	Pool              string
	Limit, Used, Want int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("token budget %q exceeded: %d used of %d, request needs about %d", e.Pool, e.Used, e.Limit, e.Want)
}

// Reservation is tokens held in a pool chain until Settle.
type Reservation struct {
	pools   []*TokenPool
	n       int
	settled bool
	mu      sync.Mutex
}

// chain returns p and its ancestors, root last.
func (p *TokenPool) chain() []*TokenPool {
	var out []*TokenPool
	for q := p; q != nil; q = q.parent {
		out = append(out, q)
	}
	return out
}

// Reserve holds n tokens in p and every ancestor, or reserves nothing and
// returns *BudgetError naming the first pool that would overflow.
func (p *TokenPool) Reserve(n int) (*Reservation, error) {
	pools := p.chain()
	// Lock root first so concurrent reservations on sibling pools can't
	// deadlock, then check every pool before taking anything.
	for i := len(pools) - 1; i >= 0; i-- {
		pools[i].mu.Lock()
	}
	defer func() {
		for _, q := range pools {
			q.mu.Unlock()
		}
	}()
	for _, q := range pools {
		if q.limit > 0 && q.used+n > q.limit {
			return nil, &BudgetError{Pool: q.name, Limit: q.limit, Used: q.used, Want: n}
		}
	}
	for _, q := range pools {
		q.used += n
	}
	return &Reservation{pools: pools, n: n}, nil
}

// Settle replaces the held estimate with the tokens actually used. Only the
// first call has an effect.
func (r *Reservation) Settle(used int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.settled {
		return
	}
	r.settled = true
	for _, q := range r.pools {
		q.mu.Lock()
		q.used += used - r.n
		q.mu.Unlock()
	}
}

// Used returns settled tokens plus outstanding reservations.
func (p *TokenPool) Used() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.used
}

// estimate is the tokens req may use: its prompt (from the context budget
// when there is one) plus its output cap.
func (s *Session) estimate(req providers.Request) int {
	if s.c.Budget != nil {
		return s.c.Budget.Count(req.Messages) + req.MaxTokens
	}
	n := PromptOverhead(req.System, req.Tools)
	for _, m := range req.Messages {
		n += msgBytes(m) / bytesPerToken
	}
	return n + req.MaxTokens
}

// reserve holds est tokens in the session's pool; nil pool means no cap.
func (s *Session) reserve(est int) (*Reservation, error) {
	if s.c.Tokens == nil {
		return nil, nil
	}
	return s.c.Tokens.Reserve(est)
}

// charge records tokens spent outside the gated request path (compaction
// summaries) so the pool still sees them.
func (s *Session) charge(u providers.Usage) {
	if s.c.Tokens == nil {
		return
	}
	r, _ := s.c.Tokens.Reserve(0)
	r.Settle(u.PromptTokens + u.CompletionTokens)
}

// budgetStopped ends the turn with exit 7 when err is a token budget error,
// after logging budget_stop.
func (s *Session) budgetStopped(err error) (done bool, status string, code int, reason string) {
	var be *BudgetError
	if !errors.As(err, &be) {
		return false, "", 0, ""
	}
	s.note(s.c.Log.Append(EvBudgetStop, map[string]any{
		"reason": reasonTokens, "pool": be.Pool, "limit": be.Limit, "used": be.Used, "want": be.Want,
	}))
	return true, endBudget, ExitBudget, reasonTokens
}
