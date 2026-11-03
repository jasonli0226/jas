package farm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// gated returns turns for part a that write a file, then hold until release
// is closed (signalling on started), then finish.
func gated(started chan<- struct{}, release <-chan struct{}) []fake.Turn {
	hold := writeCall("a/x.txt", "a\n")
	hold.OnEmit = func() {
		started <- struct{}{}
		<-release
	}
	return []fake.Turn{hold, final("a done")}
}

// provRig records each session's fake provider by part.
type provRig struct {
	mu    sync.Mutex
	provs map[string]*fake.Provider
}

func (p *provRig) wrap(r *runRig, sc sessionScript) {
	p.provs = map[string]*fake.Provider{}
	r.opts.NewSession = func(ws WorkerSession) (*core.Session, error) {
		prov := &fake.Provider{Turns: sc(ws)}
		p.mu.Lock()
		p.provs[ws.Part.ID] = prov
		p.mu.Unlock()
		c := ws.Config(prov, "m")
		c.MaxTurns = 10
		return core.NewSession(c)
	}
}

func (p *provRig) requests(part string) []providers.Request {
	p.mu.Lock()
	prov := p.provs[part]
	p.mu.Unlock()
	if prov == nil {
		return nil
	}
	return prov.Requests
}

func start(t *testing.T, r *runRig) *Handle {
	t.Helper()
	h, err := StartFarm(context.Background(), r.opts)
	if err != nil {
		t.Fatalf("StartFarm: %v", err)
	}
	return h
}

func TestStartFarm_WaitMatchesRunFarm(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	h := start(t, r)
	res := h.Wait()
	if res.Code != 0 || res.Reason != "ok" || res.Err != nil {
		t.Fatalf("result %+v", res)
	}
	if want := branchPrefix + h.FarmID() + "/integrate"; res.Branch != want {
		t.Fatalf("branch %q, want %q", res.Branch, want)
	}
	select {
	case <-h.Done():
	default:
		t.Fatal("Done not closed after Wait")
	}
}

func TestHandle_SteerReachesRunningWorker(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var pr provRig
	r := newRunRig(t, "true", twoParts(), twoWriters)
	pr.wrap(r, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	if err := h.Steer("a", "use slog"); err != nil {
		t.Fatal(err)
	}
	if err := h.Steer("zz", "x"); !errors.Is(err, ErrPartNotRunning) {
		t.Fatalf("steer unknown part: %v", err)
	}
	close(release)
	if res := h.Wait(); res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
	reqs := pr.requests("a")
	if len(reqs) < 2 || !strings.Contains(requestText(reqs[len(reqs)-1]), "use slog") {
		t.Fatal("the steer did not reach part a's next request")
	}
}

func requestText(req providers.Request) string {
	var b strings.Builder
	for _, m := range req.Messages {
		for _, c := range m.Content {
			b.WriteString(c.Text)
		}
	}
	return b.String()
}

func TestHandle_PauseHoldsNextRequest(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	var pr provRig
	r := newRunRig(t, "true", twoParts(), twoWriters)
	pr.wrap(r, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	if err := h.Pause("a"); err != nil {
		t.Fatal(err)
	}
	close(release)
	time.Sleep(200 * time.Millisecond)
	if n := len(pr.requests("a")); n != 1 {
		t.Fatalf("paused part made %d requests, want 1", n)
	}
	if err := h.Resume("a"); err != nil {
		t.Fatal(err)
	}
	if res := h.Wait(); res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
	var choices []string
	for _, ev := range r.events(EvOwnerDecision) {
		if ev["part"] == "a" {
			choices = append(choices, ev["choice"].(string))
		}
	}
	if strings.Join(choices, ",") != "pause,resume" {
		t.Fatalf("owner decisions %v", choices)
	}
}

func TestHandle_DropDropsDependents(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	parts := []Part{coder("a", "a/**"), coder("b", "b/**"), coder("c", "c/**", "a")}
	r := newRunRig(t, "true", parts, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	if err := h.Drop("a"); err != nil {
		t.Fatal(err)
	}
	close(release)
	res := h.Wait()
	if res.Reason != ReasonPartDropped {
		t.Fatalf("result %+v", res)
	}
	m := h.Snapshot()
	if m.Parts["a"].Status != StatusDropped || m.Parts["c"].Status != StatusDropped || m.Parts["b"].Status != StatusDone {
		t.Fatalf("statuses a=%s b=%s c=%s", m.Parts["a"].Status, m.Parts["b"].Status, m.Parts["c"].Status)
	}
}

func TestHandle_StopHardMarksFarmEnded(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	h.Stop(false)
	h.Stop(true)
	close(release)
	res := h.Wait()
	if res.Code != core.ExitInterrupted {
		t.Fatalf("result %+v", res)
	}
	if end := r.last(EvFarmEnded); end["status"] != "interrupted" || end["hard"] != true {
		t.Fatalf("farm_ended %v", end)
	}
}

func TestHandle_UndeliveredSteeringHandedBack(t *testing.T) {
	// A steer that arrives after the worker's final answer (here: while the
	// supervisor runs part_check) is never read, so it is handed back.
	r := newRunRig(t, "true", twoParts(), twoWriters)
	r.opts.Cfg.PartCheck = "true"
	w := &undeliveredWatcher{}
	var h atomic.Pointer[Handle]
	steered := make(chan struct{})
	w.onFarm = func(typ string, m map[string]any) {
		if typ == EvPartCheck && m["part"] == "a" {
			for h.Load() == nil {
				time.Sleep(time.Millisecond)
			}
			if err := h.Load().Steer("a", "late"); err != nil {
				t.Errorf("steer: %v", err)
			}
			close(steered)
		}
	}
	r.opts.Watch = w
	h.Store(start(t, r))
	h.Load().Wait()
	<-steered
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.part != "a" || strings.Join(w.texts, "|") != "late" {
		t.Fatalf("handed back %q %v", w.part, w.texts)
	}
}

type undeliveredWatcher struct {
	recWatcher
	onFarm func(typ string, m map[string]any)
	mu     sync.Mutex
	part   string
	texts  []string
}

func (w *undeliveredWatcher) FarmEvent(typ string, m map[string]any) {
	if w.onFarm != nil {
		w.onFarm(typ, m)
	}
}

func (w *undeliveredWatcher) SteerUndelivered(part string, texts []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.part, w.texts = part, texts
}

func TestFinalChoice_MergeRefusedOnDirtyMain(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	var errOut bytes.Buffer
	r.opts.Stderr = &errOut
	r.opts.Ask = func(prompt string, choices []string) (string, bool) {
		// Dirty the main checkout just before the owner answers merge.
		if err := os.WriteFile(filepath.Join(r.main, "a.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return "m", true
	}
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "main checkout has uncommitted changes; keeping the branches") {
		t.Fatalf("stderr %q", errOut.String())
	}
	ev := r.last(EvOwnerDecision)
	if ev["choice"] != ChoiceKeep || !strings.Contains(fmt.Sprint(ev["note"]), "uncommitted changes") {
		t.Fatalf("final decision %v", ev)
	}
	if line := ProgressLine(EvOwnerDecision, ev); !strings.Contains(line, "uncommitted changes") {
		t.Fatalf("progress line %q", line)
	}
}

func TestHandle_PauseThenSoftStopEnds(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	h := start(t, r)
	<-started
	if err := h.Pause("a"); err != nil {
		t.Fatal(err)
	}
	close(release) // a's next request now parks in the pause gate
	time.Sleep(100 * time.Millisecond)
	h.Stop(false)
	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		h.Stop(true)
		t.Fatal("a soft stop never ended a farm with a paused part")
	}
	if res := h.Wait(); res.Code != core.ExitInterrupted {
		t.Fatalf("result %+v", res)
	}
}

func TestFailureQuestion_DoesNotStallIndependentParts(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return twoWriters(ws)
	})
	r.opts.Cfg.Split.MaxWorkers = 1 // b can only start once a's slot is free
	answer, asked := make(chan string), make(chan struct{}, 1)
	r.opts.Ask = func(prompt string, choices []string) (string, bool) {
		if strings.Contains(prompt, "retry, drop or abort") {
			asked <- struct{}{}
			return <-answer, true
		}
		return "k", true
	}
	h := start(t, r)
	<-asked
	deadline := time.After(5 * time.Second)
	for {
		if m := h.Snapshot(); m.Parts["b"].Status == StatusDone {
			break
		}
		select {
		case <-deadline:
			answer <- "drop"
			t.Fatal("part b never ran while a's failure question was open")
		case <-time.After(20 * time.Millisecond):
		}
	}
	answer <- "drop"
	if res := h.Wait(); res.Reason != ReasonPartDropped {
		t.Fatalf("result %+v", res)
	}
}

func TestFailureQuestion_HardStopDoesNotWait(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return twoWriters(ws)
	})
	asked := make(chan struct{}, 1)
	r.opts.Ask = func(prompt string, choices []string) (string, bool) {
		if strings.Contains(prompt, "retry, drop or abort") {
			asked <- struct{}{}
			select {} // the owner never answers
		}
		return "k", true
	}
	h := start(t, r)
	<-asked
	h.Stop(false)
	h.Stop(true)
	select {
	case <-h.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("a hard stop waited on an unanswered failure question")
	}
}

func TestHandle_SteerIntegrator(t *testing.T) {
	started, release := make(chan struct{}, 1), make(chan struct{})
	r := newRunRig(t, brokenCheck, twoParts(), func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case "a":
			return writes(map[string]string{"a/x.txt": "BROKEN\n"}, "a done")
		case "b":
			return twoWriters(ws)
		}
		fix := writeCall("a/x.txt", "ok\n")
		fix.OnEmit = func() {
			started <- struct{}{}
			<-release
		}
		commit, _ := json.Marshal(map[string]string{"command": "git add -A && git commit --no-verify -q -m fix"})
		return []fake.Turn{fix, call("shell", string(commit)), final("fixed")}
	})
	h := start(t, r)
	<-started
	if err := h.Steer(integratePart, "keep a's tests"); err != nil {
		t.Fatalf("steer the integrator: %v", err)
	}
	close(release)
	if res := h.Wait(); res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
}
