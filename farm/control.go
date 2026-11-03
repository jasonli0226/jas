package farm

import (
	"context"
	"errors"
	"sync"

	"github.com/jasonli0226/jas/providers"
)

// ErrPartNotRunning means the part has no live session (queued, finished,
// unknown, or the parts phase is over).
var ErrPartNotRunning = errors.New("part is not running")

// controls is the owner's live handle on running parts: their sessions (for
// steering), cancel funcs (for drop), pause gates, and a command channel to
// the scheduler. The zero value is ready to use.
type controls struct {
	mu      sync.Mutex
	workers map[string]*worker
	cancels map[string]context.CancelFunc
	paused  map[string]chan struct{} // closed on resume

	once  sync.Once
	cmds  chan func(*sched) // run on RunParts' goroutine
	ended chan struct{}     // closed when RunParts returns
}

func (c *controls) init() {
	c.once.Do(func() {
		c.cmds = make(chan func(*sched))
		c.ended = make(chan struct{})
	})
}

// do runs fn on the scheduler's goroutine and waits for it; false once
// RunParts has ended.
func (c *controls) do(fn func(*sched)) bool {
	c.init()
	done := make(chan struct{})
	select {
	case c.cmds <- func(st *sched) { fn(st); close(done) }:
		<-done
		return true
	case <-c.ended:
		return false
	}
}

func (c *controls) setWorker(id string, w *worker) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workers == nil {
		c.workers = map[string]*worker{}
	}
	if w == nil {
		delete(c.workers, id)
		return
	}
	c.workers[id] = w
}

func (c *controls) worker(id string) *worker {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workers[id]
}

func (c *controls) setCancel(id string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancels == nil {
		c.cancels = map[string]context.CancelFunc{}
	}
	if cancel == nil {
		delete(c.cancels, id)
		return
	}
	c.cancels[id] = cancel
}

func (c *controls) cancel(id string) {
	c.mu.Lock()
	fn := c.cancels[id]
	c.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// pause makes the part's next provider request wait; false if already paused.
func (c *controls) pause(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.paused == nil {
		c.paused = map[string]chan struct{}{}
	}
	if c.paused[id] != nil {
		return false
	}
	c.paused[id] = make(chan struct{})
	return true
}

// resume releases a paused part; false if it was not paused.
func (c *controls) resume(id string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := c.paused[id]
	if ch == nil {
		return false
	}
	delete(c.paused, id)
	close(ch)
	return true
}

func (c *controls) pauseGate(id string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused[id]
}

// pauseGate holds a part's next request while the owner has it paused.
type pauseGate struct {
	p  providers.Provider
	c  *controls
	id string
}

func (g pauseGate) Stream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, <-chan error) {
	for {
		ch := g.c.pauseGate(g.id)
		if ch == nil {
			return g.p.Stream(ctx, req)
		}
		select {
		case <-ch:
		case <-ctx.Done():
			errc := make(chan error, 1)
			errc <- ctx.Err()
			out := make(chan providers.StreamEvent)
			close(out)
			return out, errc
		}
	}
}

// wrapPart is wrap with the part's pause gate innermost, so a paused worker
// holds before taking a rate-limiter slot.
func (s *Supervisor) wrapPart(id string) func(providers.Provider) providers.Provider {
	return func(p providers.Provider) providers.Provider {
		return s.wrap(pauseGate{p: p, c: &s.ctl, id: id})
	}
}

// takeSteering unregisters a finishing worker (so no new steer can reach it)
// and hands owner text it never read to the watcher.
func (s *Supervisor) takeSteering(w *worker) []string {
	s.ctl.setWorker(w.part.ID, nil)
	if w.sess == nil {
		return nil
	}
	texts := w.sess.TakeSteering()
	if len(texts) > 0 && s.Watch != nil {
		s.Watch.SteerUndelivered(w.part.ID, texts)
	}
	return texts
}

// dropOwner drops a part and its dependents for the owner: a running part
// is cancelled and dropped when its attempt ends (see handle).
func (s *Supervisor) dropOwner(st *sched, id string) error {
	switch st.status[id] {
	case StatusRunning:
		if st.dropping == nil {
			st.dropping = map[string]bool{}
		}
		st.dropping[id] = true
		s.ctl.cancel(id)
	case StatusQueued, StatusPaused, StatusFailed:
		s.dropped(st, id)
	default:
		return ErrPartNotRunning
	}
	return nil
}

// dropped marks id and its dependents dropped and logs the owner's choice.
func (s *Supervisor) dropped(st *sched, id string) {
	s.setStatus(st, append([]string{id}, dependents(s.Plan, id)...), "", StatusDropped)
	s.event(EvOwnerDecision, map[string]any{"part": id, "choice": choiceDrop, "source": "owner"})
}
