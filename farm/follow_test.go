package farm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type recEvent struct {
	part, typ string
	m         map[string]any
}

type recWatch struct {
	mu  sync.Mutex
	evs []recEvent
}

func (w *recWatch) FarmEvent(typ string, m map[string]any) { w.add("", typ, m) }
func (w *recWatch) WorkerEvent(part, typ string, m map[string]any) {
	w.add(part, typ, m)
}
func (w *recWatch) SteerUndelivered(string, []string) {}
func (w *recWatch) add(part, typ string, m map[string]any) {
	w.mu.Lock()
	w.evs = append(w.evs, recEvent{part, typ, m})
	w.mu.Unlock()
}
func (w *recWatch) snapshot() []recEvent {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]recEvent(nil), w.evs...)
}

// waitFor polls until cond holds on the recorded events.
func (w *recWatch) waitFor(t *testing.T, cond func([]recEvent) bool) []recEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if evs := w.snapshot(); cond(evs) {
			return evs
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out; events %+v", w.snapshot())
	return nil
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	assertNoErr(t, os.MkdirAll(filepath.Dir(path), 0o700))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	assertNoErr(t, err)
	_, err = f.WriteString(s)
	assertNoErr(t, err)
	assertNoErr(t, f.Close())
}

func assertNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func line(typ string, kv ...any) string {
	m := map[string]any{"type": typ}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i].(string)] = kv[i+1]
	}
	b, _ := json.Marshal(m)
	return string(b) + "\n"
}

type followRig struct {
	dir     string
	w       *recWatch
	running atomic.Bool
	done    chan struct{}
	out     FollowOutcome
	err     error
	cancel  context.CancelFunc
}

func newFollowRig(t *testing.T) *followRig {
	r := &followRig{dir: t.TempDir(), w: &recWatch{}, done: make(chan struct{})}
	r.running.Store(true)
	appendTo(t, r.farmLog(), "")
	return r
}

func (r *followRig) farmLog() string { return filepath.Join(r.dir, farmLogName) }
func (r *followRig) partLog(part string) string {
	return filepath.Join(r.dir, partsDir, part, eventsFile)
}

func (r *followRig) start(t *testing.T, backlog int) {
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	t.Cleanup(cancel)
	f := &Follow{Dir: r.dir, Watch: r.w, Backlog: backlog, Poll: 10 * time.Millisecond, Running: r.running.Load}
	go func() {
		r.out, r.err = f.Run(ctx)
		close(r.done)
	}()
	// Run records where each existing file ends before its first poll;
	// lines appended before that would count as history.
	time.Sleep(30 * time.Millisecond)
}

func (r *followRig) wait(t *testing.T) (FollowOutcome, error) {
	t.Helper()
	select {
	case <-r.done:
		return r.out, r.err
	case <-time.After(2 * time.Second):
		t.Fatal("Follow.Run did not return")
	}
	return 0, nil
}

func hasEvent(part, typ string) func([]recEvent) bool {
	return func(evs []recEvent) bool {
		for _, e := range evs {
			if e.part == part && e.typ == typ {
				return true
			}
		}
		return false
	}
}

func TestFollow_FarmAndPartEvents(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	appendTo(t, r.farmLog(), line(EvPartStarted, "part", "p1", "role", "worker"))
	appendTo(t, r.partLog("p1"), line("tool_started", "tool", "ls"))
	evs := r.w.waitFor(t, hasEvent("p1", "tool_started"))
	if !hasEvent("", EvPartStarted)(evs) {
		t.Fatalf("farm event missing: %+v", evs)
	}
}

func TestFollow_ArgsAsRawMessage(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	appendTo(t, r.partLog("p1"), line("tool_started", "tool", "read", "args", map[string]any{"path": "a.go"}))
	evs := r.w.waitFor(t, hasEvent("p1", "tool_started"))
	raw, ok := evs[0].m["args"].(json.RawMessage)
	if !ok || string(raw) != `{"path":"a.go"}` {
		t.Fatalf("args %T %v", evs[0].m["args"], evs[0].m["args"])
	}
	if got := toolLine("p1", evs[0].m); got == "" || !strings.Contains(got, "a.go") {
		t.Fatalf("toolLine %q", got)
	}
}

func TestFollow_PartialLine(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	full := line("tool_started", "tool", "ls")
	appendTo(t, r.partLog("p1"), full[:10])
	time.Sleep(50 * time.Millisecond)
	if n := len(r.w.snapshot()); n != 0 {
		t.Fatalf("half a line delivered: %d events", n)
	}
	appendTo(t, r.partLog("p1"), full[10:])
	r.w.waitFor(t, hasEvent("p1", "tool_started"))
	time.Sleep(50 * time.Millisecond)
	if n := len(r.w.snapshot()); n != 1 {
		t.Fatalf("%d events, want 1", n)
	}
}

func TestFollow_NewPart(t *testing.T) {
	r := newFollowRig(t)
	appendTo(t, r.partLog("p1"), "")
	r.start(t, 0)
	time.Sleep(30 * time.Millisecond)
	appendTo(t, r.partLog("p2"), line("tool_started", "tool", "ls"))
	appendTo(t, filepath.Join(r.dir, partsDir, "p1", "events-2.jsonl"), line("tool_started", "tool", "grep"))
	r.w.waitFor(t, func(evs []recEvent) bool {
		return hasEvent("p2", "tool_started")(evs) && hasEvent("p1", "tool_started")(evs)
	})
}

func TestFollow_SkipsBadLines(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	appendTo(t, r.partLog("p1"), "not json\n"+`{"type":42}`+"\n"+line("tool_started", "tool", "ls"))
	evs := r.w.waitFor(t, hasEvent("p1", "tool_started"))
	if len(evs) != 1 {
		t.Fatalf("bad lines delivered: %+v", evs)
	}
}

func TestFollow_Backlog(t *testing.T) {
	r := newFollowRig(t)
	for i := 0; i < 10; i++ {
		appendTo(t, r.partLog("p1"), line("tool_started", "tool", fmt.Sprintf("t%d", i)))
	}
	appendTo(t, r.farmLog(), line(EvPartStarted, "part", "p1"))
	r.start(t, 3)
	appendTo(t, r.partLog("p1"), line("tool_started", "tool", "live"))
	evs := r.w.waitFor(t, func(evs []recEvent) bool { return len(evs) >= 4 })
	time.Sleep(30 * time.Millisecond)
	evs = r.w.snapshot()
	var tools []string
	for _, e := range evs {
		if e.part == "" {
			t.Fatalf("farm.jsonl history replayed: %+v", e)
		}
		tools = append(tools, fmt.Sprint(e.m["tool"]))
	}
	if fmt.Sprint(tools) != "[t7 t8 t9 live]" {
		t.Fatalf("tools %v", tools)
	}
}

func TestFollow_Ended(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	appendTo(t, r.farmLog(), line(EvFarmEnded, "reason", "ok"))
	out, err := r.wait(t)
	if err != nil || out != FollowEnded {
		t.Fatalf("outcome %v, %v", out, err)
	}
	if !hasEvent("", EvFarmEnded)(r.w.snapshot()) {
		t.Fatal("farm_ended not delivered")
	}
}

func TestFollow_ProcessDied(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	time.Sleep(30 * time.Millisecond)
	appendTo(t, r.partLog("p1"), line("tool_started", "tool", "ls"))
	r.running.Store(false)
	out, err := r.wait(t)
	if err != nil || out != FollowDied {
		t.Fatalf("outcome %v, %v", out, err)
	}
	if !hasEvent("p1", "tool_started")(r.w.snapshot()) {
		t.Fatal("lines on disk not drained before returning")
	}
}

func TestFollow_Detach(t *testing.T) {
	r := newFollowRig(t)
	r.start(t, 0)
	r.cancel()
	out, err := r.wait(t)
	if err != nil || out != FollowDetached {
		t.Fatalf("outcome %v, %v", out, err)
	}
}
