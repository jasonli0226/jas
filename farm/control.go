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

	once     sync.Once
	cmds     chan func(*sched) // run on RunParts' goroutine
	ended    chan struct{}     // closed when RunParts returns
	stopped  chan struct{}     // closed when the farm stops scheduling
	stopOnce sync.Once
}

func (c *controls) init() {
	c.once.Do(func() {
		c.cmds = make(chan func(*sched))
		c.ended = make(chan struct{})
		c.stopped = make(chan struct{})
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

// steer queues text on a registered session. It holds the lock across the
// lookup and the queueing, so it cannot race takeSteering: a steer either
// lands before the session is unregistered (and is read or handed back) or
// reports that the part is not running.
func (c *controls) steer(id, text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.workers[id]
	if w == nil || w.sess == nil {
		return false
	}
	w.sess.Steer(text)
	return true
}

// unregister removes a session and takes its unread steering, under the
// same lock as steer.
func (c *controls) unregister(w *worker) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.workers[w.part.ID] == w {
		delete(c.workers, w.part.ID)
	}
	if w.sess == nil {
		return nil
	}
	return w.sess.TakeSteering()
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

// stop marks the farm as no longer scheduling: requests parked in a pause
// gate give up, as the stop gate refuses new ones.
func (c *controls) stop() {
	c.init()
	c.stopOnce.Do(func() { close(c.stopped) })
}

// pauseGate holds a part's next request while the owner has it paused. A
// stop (soft or hard) releases it with an error, so a paused part never
// keeps a stopping farm alive.
type pauseGate struct {
	p  providers.Provider
	s  *Supervisor
	id string
}

func (g pauseGate) Stream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, <-chan error) {
	c := &g.s.ctl
	c.init()
	soft := context.Background()
	if g.s.soft != nil {
		soft = g.s.soft
	}
	for {
		ch := c.pauseGate(g.id)
		if ch == nil {
			return g.p.Stream(ctx, req)
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return failedStream(ctx.Err())
		case <-soft.Done():
			return failedStream(context.Canceled)
		case <-c.stopped:
			return failedStream(context.Canceled)
		}
	}
}

// wrapPart is wrap with the part's pause gate outermost, so a paused worker
// holds before it takes a rate-limiter slot.
func (s *Supervisor) wrapPart(id string) func(providers.Provider) providers.Provider {
	return func(p providers.Provider) providers.Provider {
		return pauseGate{p: s.wrap(p), s: s, id: id}
	}
}

// takeSteering unregisters a finishing worker (so no new steer can reach it)
// and hands owner text it never read to the watcher.
func (s *Supervisor) takeSteering(w *worker) []string {
	texts := s.ctl.unregister(w)
	if len(texts) > 0 && s.Watch != nil {
		s.Watch.SteerUndelivered(w.part.ID, texts)
	}
	return texts
}

// dropOwner drops a part and its dependents for the owner: a running part
// is cancelled and dropped when its attempt ends (see handle).
func (s *Supervisor) dropOwner(st *sched, id, source string) error {
	switch st.status[id] {
	case StatusRunning:
		if st.dropping == nil {
			st.dropping = map[string]string{}
		}
		st.dropping[id] = source
		s.ctl.cancel(id)
	case StatusQueued, StatusPaused, StatusFailed:
		s.dropped(st, id, source)
	default:
		return ErrPartNotRunning
	}
	return nil
}

// sourceOwner is the owner_decision source of the host's own commands (the
// chat or the farm's terminal).
const sourceOwner = "owner"

// dropped marks id and its dependents dropped and logs the owner's choice,
// made from source.
func (s *Supervisor) dropped(st *sched, id, source string) {
	s.setStatus(st, append([]string{id}, dependents(s.Plan, id)...), "", StatusDropped)
	s.event(EvOwnerDecision, map[string]any{"part": id, "choice": choiceDrop, "source": source})
}
