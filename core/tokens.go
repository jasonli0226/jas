package core

import (
	"context"
	"errors"
	"fmt"
	"math"
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

	mu     sync.Mutex
	used   int     // settled tokens plus outstanding reservations
	weight float64 // share of a cached prompt token charged; 0 = unset (full). Read on the root only
}

// NewTokenPool returns a pool named name with the given limit (<= 0 means
// unlimited) whose reservations also count against parent (nil for none).
func NewTokenPool(name string, limit int, parent *TokenPool) *TokenPool {
	return &TokenPool{name: name, limit: limit, parent: parent}
}

// SetCachedWeight sets the share of a cached prompt token that settling
// charges (cached_token_weight). Every pool in a chain uses its root's
// weight; unset charges in full.
func (p *TokenPool) SetCachedWeight(w float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.weight = w
}

// cachedWeight is the weight of p's root, 1 when unset.
func (p *TokenPool) cachedWeight() float64 {
	root := p
	for root.parent != nil {
		root = root.parent
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.weight <= 0 {
		return 1
	}
	return root.weight
}

// chargeTokens is what a request with usage u costs a pool: uncached prompt
// and completion tokens in full, cached prompt tokens at weight. cached is
// clamped to [0, prompt] so odd provider data never charges outside the
// "all cached" to "none cached" range.
func chargeTokens(u providers.Usage, weight float64) int {
	cached := min(max(u.CachedTokens, 0), u.PromptTokens)
	return u.PromptTokens - cached + int(math.Round(float64(cached)*weight)) + u.CompletionTokens
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

// SettleUsage settles at what usage u costs, with cached prompt tokens at
// the pool chain's weight.
func (r *Reservation) SettleUsage(u providers.Usage) {
	if r == nil {
		return
	}
	r.Settle(chargeTokens(u, r.pools[0].cachedWeight()))
}

// tightest returns the capped pool in p's chain with the least room left,
// and that room; nil when no pool in the chain has a limit.
func (p *TokenPool) tightest() (*TokenPool, int) {
	var (
		best *TokenPool
		left int
	)
	for _, q := range p.chain() {
		if q.limit <= 0 {
			continue
		}
		if n := q.limit - q.Used(); best == nil || n < left {
			best, left = q, n
		}
	}
	return best, left
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
	r.SettleUsage(u)
}

// budgetStopped ends the turn with exit 7 when err is a token budget error,
// after logging budget_stop.
func (s *Session) budgetStopped(err error) (done bool, status string, code int, reason string) {
	var be *BudgetError
	if !errors.As(err, &be) {
		return false, "", 0, ""
	}
	s.pool = be.Pool
	s.note(s.c.Log.Append(EvBudgetStop, map[string]any{
		"reason": reasonTokens, "pool": be.Pool, "limit": be.Limit, "used": be.Used, "want": be.Want,
	}))
	return true, endBudget, ExitBudget, reasonTokens
}

// ctxEnd is how a turn ends once ctx is done: a deadline is the wall-clock
// cap (exit 7, after budget_stop), anything else is an interrupt.
func (s *Session) ctxEnd(ctx context.Context) (status string, code int, reason string) {
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return endInterrupted, ExitInterrupted, "interrupted"
	}
	s.note(s.c.Log.Append(EvBudgetStop, map[string]any{"reason": reasonTimeout}))
	return endBudget, ExitBudget, reasonTimeout
}

// outputCap is the request's max_tokens: MaxOutput, lowered to the room the
// known context window leaves after the prompt, since servers such as vLLM
// reject prompt + max_tokens over the window. 0 (omit) stays 0.
func (s *Session) outputCap() int {
	n := s.c.MaxOutput
	b := s.c.Budget
	if n <= 0 || b == nil || b.Window <= 0 {
		return n
	}
	if room := b.Window - b.Count(s.messages); room > 0 && room < n {
		return room
	}
	return n
}
