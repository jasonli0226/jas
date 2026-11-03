package tui

import (
	"context"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
)

// twoPartPlan is a parsable plan with parts a and b (b after nothing).
const twoPartPlan = "task: fix x\nparts:\n  - id: a\n    role: coder\n    goal: g\n    owns: [\"a/**\"]\n" +
	"  - id: b\n    role: coder\n    goal: g\n    owns: [\"b/**\"]\nwhy_split: two\n"

// fakeFarmer is a scripted Farmer: Plan returns plan (or err); Start hands
// back run and keeps the hooks so tests can play farm events.
type fakeFarmer struct {
	mu        sync.Mutex
	available bool
	why       string
	plan      FarmPlan
	planErr   error
	run       *fakeRun
	hooks     FarmHooks
	started   string // plan path given to Start
	discarded bool
	feedback  []string
}

func newFakeFarmer() *fakeFarmer {
	return &fakeFarmer{available: true, run: newFakeRun(),
		plan: FarmPlan{ID: "f1", Task: "fix x", Summary: []string{"Plan: fix x"}, Raw: []byte(twoPartPlan)}}
}

func (f *fakeFarmer) Available() (bool, string) { return f.available, f.why }
func (f *fakeFarmer) Plan(context.Context, string, string) (FarmPlan, error) {
	return f.plan, f.planErr
}
func (f *fakeFarmer) Replan(_ context.Context, p FarmPlan, fb string) (FarmPlan, error) {
	f.mu.Lock()
	f.feedback = append(f.feedback, fb)
	f.mu.Unlock()
	return p, nil
}
func (f *fakeFarmer) Recheck(p FarmPlan, raw []byte) FarmPlan { p.Raw = raw; return p }
func (f *fakeFarmer) Approve(p FarmPlan) (string, error)      { return "/plans/" + p.ID + ".yaml", nil }
func (f *fakeFarmer) Discard(FarmPlan)                        { f.discarded = true }
func (f *fakeFarmer) Start(_ context.Context, path, _ string, _ core.Level, h FarmHooks) (FarmRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started, f.hooks = path, h
	return f.run, nil
}

// event plays a farm event through the chat's hook.
func (f *fakeFarmer) event(typ string, m map[string]any) {
	f.mu.Lock()
	h := f.hooks
	f.mu.Unlock()
	h.Event(typ, m)
}

// fakeRun is a scripted FarmRun; calls are recorded in order.
type fakeRun struct {
	mu    sync.Mutex
	calls []string
	man   farm.Manifest
	done  chan struct{}
	res   farm.Result
}

func newFakeRun() *fakeRun {
	return &fakeRun{done: make(chan struct{}), man: farm.Manifest{FarmID: "f1", Parts: map[string]*farm.PartState{}}}
}

func (r *fakeRun) call(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, s)
}

func (r *fakeRun) FarmID() string { return "f1" }
func (r *fakeRun) Snapshot() farm.Manifest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.man
}
func (r *fakeRun) Steer(part, text string) error { r.call("steer " + part + " " + text); return nil }
func (r *fakeRun) Pause(part string) error       { r.call("pause " + part); return nil }
func (r *fakeRun) Resume(part string) error      { r.call("resume " + part); return nil }
func (r *fakeRun) Drop(part string) error        { r.call("drop " + part); return nil }
func (r *fakeRun) Stop(hard bool) {
	if hard {
		r.call("stop hard")
	} else {
		r.call("stop")
	}
}
func (r *fakeRun) Done() <-chan struct{} { return r.done }
func (r *fakeRun) Wait() farm.Result     { <-r.done; return r.res }

// finish ends the run with res.
func (r *fakeRun) finish(res farm.Result) {
	r.res = res
	close(r.done)
}

// drain feeds every message posted so far into the model.
func (c *testChat) drain() {
	for {
		select {
		case msg := <-c.msgs:
			c.update(msg)
		case <-time.After(20 * time.Millisecond):
			return
		}
	}
}

// startFarm starts the fake farm through /farm and the approve pick.
func startFarm(t *testing.T, c *testChat) *fakeFarmer {
	t.Helper()
	f := c.b.farmer.(*fakeFarmer)
	c.submit(`/farm "fix x"`)
	if c.m.picker == nil {
		t.Fatalf("no plan review picker; printed %q", c.printedNoBanner())
	}
	c.key("enter") // approve is the first choice
	c.drain()
	if c.m.farm == nil || c.m.farm.run == nil {
		t.Fatalf("farm not started; printed %q", c.printedNoBanner())
	}
	return f
}

var _ tea.Msg = farmEventMsg{}

func farmResult(reason string, code int) farm.Result { return farm.Result{Reason: reason, Code: code} }
