package farm

import (
	"context"
	"sync"
	"time"

	"github.com/jasonli0226/jas/providers"
)

// Clock is the time source of a Limiter; tests inject a fake.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Limiter spaces request starts evenly: each request takes the next free
// slot, one interval after the previous one. It is shared by every worker of
// a farm, so the provider sees one request rate, not one per worker.
type Limiter struct {
	clock    Clock
	interval time.Duration

	mu   sync.Mutex
	next time.Time // earliest start of the next slot
}

// NewLimiter returns a limiter allowing perMinute request starts a minute, or
// nil (no limit) when perMinute <= 0. A nil clock means real time.
func NewLimiter(perMinute int, clock Clock) *Limiter {
	if perMinute <= 0 {
		return nil
	}
	if clock == nil {
		clock = realClock{}
	}
	return &Limiter{clock: clock, interval: time.Minute / time.Duration(perMinute)}
}

// Wait blocks until the caller's slot starts or ctx ends. A nil Limiter
// never waits. A cancelled wait gives up its slot unused.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil {
		return ctx.Err()
	}
	l.mu.Lock()
	now := l.clock.Now()
	slot := now
	if l.next.After(slot) {
		slot = l.next
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()
	d := slot.Sub(now)
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-l.clock.After(d):
		return nil
	}
}

// RateLimited wraps a provider so every request first waits for a slot.
type RateLimited struct {
	Provider providers.Provider
	Limiter  *Limiter
}

var _ providers.Provider = RateLimited{}

// Stream implements providers.Provider.
func (r RateLimited) Stream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, <-chan error) {
	if err := r.Limiter.Wait(ctx); err != nil {
		return failedStream(err)
	}
	return r.Provider.Stream(ctx, req)
}

// failedStream is a stream that ends at once with err.
func failedStream(err error) (<-chan providers.StreamEvent, <-chan error) {
	out := make(chan providers.StreamEvent)
	errc := make(chan error, 1)
	close(out)
	errc <- err
	close(errc)
	return out, errc
}
