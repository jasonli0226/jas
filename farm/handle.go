package farm

import "context"

// Result is how a farm ended: the process exit code, the farm_ended reason,
// the integrate branch ("" if integration never ran) and any internal error.
type Result struct {
	Code   int
	Reason string
	Branch string
	Err    error
}

// Handle is a running farm. Its methods are safe from any goroutine; they may
// log events, so a caller that must not block (the chat's Update) calls them
// from its own goroutine.
type Handle struct {
	r          *farmRun
	softCancel context.CancelFunc
	hardCancel context.CancelFunc
	done       chan struct{}
	res        Result
}

// StartFarm runs the preflight checks and, if they pass, starts the farm on
// its own goroutine. Preflight refusals return an error as RunFarm's would
// (an *core.ExitError with exit 2 for usage errors).
func StartFarm(ctx context.Context, opts RunOptions) (*Handle, error) {
	opts = withDefaults(opts)
	soft, softCancel := context.WithCancel(ctx)
	hard, hardCancel := context.WithCancel(opts.Hard)
	opts.Hard = hard
	git := NewGit()
	pre, err := preflight(soft, git, opts)
	if err == nil {
		var r *farmRun
		if r, err = startFarm(git, opts, pre); err == nil {
			h := &Handle{r: r, softCancel: softCancel, hardCancel: hardCancel, done: make(chan struct{})}
			go h.run(soft)
			return h, nil
		}
	}
	softCancel()
	hardCancel()
	return nil, err
}

func (h *Handle) run(ctx context.Context) {
	defer close(h.done)
	defer h.r.close()
	code, reason, err := h.r.run(ctx)
	h.res = Result{Code: code, Reason: reason, Branch: h.r.branch, Err: err}
	h.softCancel()
	h.hardCancel()
}

// Wait blocks until the farm has ended and returns how.
func (h *Handle) Wait() Result {
	<-h.done
	return h.res
}

// Done is closed when the farm has ended.
func (h *Handle) Done() <-chan struct{} { return h.done }

// FarmID names the farm (its directory under <home>/farms).
func (h *Handle) FarmID() string { return h.r.s.FarmID }

// Broker is the run's approval broker; nil when the run has none.
func (h *Handle) Broker() *Broker { return h.r.s.Broker }

// Snapshot is a copy of the current manifest.
func (h *Handle) Snapshot() Manifest {
	var cp Manifest
	h.r.s.Manifest.Update(func(m *Manifest) {
		cp = *m
		cp.Parts = make(map[string]*PartState, len(m.Parts))
		for id, ps := range m.Parts {
			c := *ps
			cp.Parts[id] = &c
		}
		if m.Integrator != nil {
			c := *m.Integrator
			cp.Integrator = &c
		}
	})
	return cp
}

// Steer queues owner text for a running part; it reaches the model at the
// next turn boundary, or comes back through Watcher.SteerUndelivered.
func (h *Handle) Steer(part, text string) error {
	if !h.r.s.ctl.steer(part, text) {
		return ErrPartNotRunning
	}
	return nil
}

// Pause makes a running part wait before its next model request.
func (h *Handle) Pause(part string) error { return h.pauseResume(part, true) }

// Resume releases a paused part.
func (h *Handle) Resume(part string) error { return h.pauseResume(part, false) }

func (h *Handle) pauseResume(part string, pause bool) error {
	s := h.r.s
	if s.ctl.worker(part) == nil {
		return ErrPartNotRunning
	}
	choice := "resume"
	if pause {
		choice = "pause"
		if !s.ctl.pause(part) {
			return nil
		}
	} else if !s.ctl.resume(part) {
		return nil
	}
	s.event(EvOwnerDecision, map[string]any{"part": part, "choice": choice, "source": "owner"})
	return nil
}

// Drop drops a part and its dependents; a running part is cancelled.
func (h *Handle) Drop(part string) error {
	var err error
	if !h.r.s.ctl.do(func(st *sched) { err = h.r.s.dropOwner(st, part) }) {
		return ErrPartNotRunning
	}
	return err
}

// Stop stops the farm: soft, as the first Ctrl-C of jas farm run (no new
// parts; running requests finish), or hard, as the second (cancel now).
func (h *Handle) Stop(hard bool) {
	if hard {
		h.r.hardStopped.Store(true)
		h.hardCancel()
		return
	}
	h.softCancel()
}
