package core

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

const syncMarker = "<sync>"

type logRec struct {
	Typ  string
	Data map[string]any
	Sync bool // AppendSync, or a Sync() call (Typ == syncMarker)
}

type fakeLog struct {
	mu   sync.Mutex
	recs []logRec
}

func (l *fakeLog) add(typ string, data any, sync bool) {
	raw, _ := json.Marshal(data)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	l.mu.Lock()
	l.recs = append(l.recs, logRec{typ, m, sync})
	l.mu.Unlock()
}
func (l *fakeLog) Append(typ string, data any) error { l.add(typ, data, false); return nil }
func (l *fakeLog) AppendID(typ string, data any) (string, error) {
	l.add(typ, data, false)
	return "", nil
}
func (l *fakeLog) AppendSync(typ string, data any) error { l.add(typ, data, true); return nil }
func (l *fakeLog) Sync() error                           { l.add(syncMarker, nil, true); return nil }
func (l *fakeLog) Close() error                          { return nil }
func (l *fakeLog) snapshot() []logRec {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logRec(nil), l.recs...)
}

type fakeTool struct {
	name       string
	kind       Kind
	delay      time.Duration
	onCall     func()
	finished   chan struct{} // closed when Call returns, if non-nil
	mu         sync.Mutex
	start, end time.Time
	calls      int
}

func (f *fakeTool) Name() string            { return f.name }
func (f *fakeTool) Description() string     { return f.name + " tool" }
func (f *fakeTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (f *fakeTool) Kind() Kind              { return f.kind }
func (f *fakeTool) Call(ctx context.Context, _ ExecPolicy, _ json.RawMessage) (Result, error) {
	f.mu.Lock()
	f.start = time.Now()
	f.calls++
	f.mu.Unlock()
	if f.onCall != nil {
		f.onCall()
	}
	select {
	case <-time.After(f.delay):
	case <-ctx.Done():
	}
	f.mu.Lock()
	f.end = time.Now()
	f.mu.Unlock()
	if f.finished != nil {
		defer close(f.finished)
	}
	if ctx.Err() != nil {
		return Result{}, ctx.Err()
	}
	return Result{Output: "out:" + f.name}, nil
}

func call(id, name string) providers.ContentBlock {
	return providers.ContentBlock{Type: providers.BlockToolUse, ToolUseID: id, Name: name, Input: json.RawMessage(`{}`)}
}

func newExec(log EventLog, gate Gate, tools ...Tool) *Executor {
	return &Executor{Reg: NewRegistry(tools...), Gate: gate, Log: log}
}

type gateFunc func(Tool) (Decision, error)

func (g gateFunc) Allow(_ context.Context, t Tool, _ json.RawMessage, _ func() *Diff) (Decision, error) {
	return g(t)
}

func statuses(l *fakeLog) []string {
	var out []string
	for _, r := range l.snapshot() {
		if r.Typ == EvToolResult {
			out = append(out, r.Data["status"].(string))
		}
	}
	return out
}

func TestExec_ConsecutiveReadsRunInParallel(t *testing.T) {
	a := &fakeTool{name: "a", delay: 300 * time.Millisecond}
	b := &fakeTool{name: "b", delay: 300 * time.Millisecond}
	c := &fakeTool{name: "c", delay: 300 * time.Millisecond}
	e := newExec(&fakeLog{}, AllowAll{}, a, b, c)
	t0 := time.Now()
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a"), call("2", "b"), call("3", "c")})
	assert.NoErr(t, err)
	assert.Eq(t, len(res), 3)
	if d := time.Since(t0); d >= 600*time.Millisecond {
		t.Fatalf("not parallel: %v", d)
	}
}

func TestExec_MutatingIsBarrier(t *testing.T) {
	ra := &fakeTool{name: "ra", delay: 50 * time.Millisecond}
	ed := &fakeTool{name: "edit", kind: Mutating, delay: 50 * time.Millisecond}
	rb := &fakeTool{name: "rb", delay: 10 * time.Millisecond}
	e := newExec(&fakeLog{}, AllowAll{}, ra, ed, rb)
	_, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "ra"), call("2", "edit"), call("3", "rb")})
	assert.NoErr(t, err)
	if ed.start.Before(ra.end) {
		t.Fatal("edit started before readA ended")
	}
	if rb.start.Before(ed.end) {
		t.Fatal("readB started before edit ended")
	}
}

func TestExec_ResultsInCallOrder(t *testing.T) {
	a := &fakeTool{name: "a", delay: 90 * time.Millisecond}
	b := &fakeTool{name: "b", delay: 50 * time.Millisecond}
	c := &fakeTool{name: "c", delay: 5 * time.Millisecond}
	e := newExec(&fakeLog{}, AllowAll{}, a, b, c)
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a"), call("2", "b"), call("3", "c")})
	assert.NoErr(t, err)
	for i, id := range []string{"1", "2", "3"} {
		assert.Eq(t, res[i].ToolUseID, id)
		assert.Eq(t, res[i].Type, providers.BlockToolResult)
	}
	assert.Eq(t, res[0].Text, "out:a")
	assert.Eq(t, res[2].Text, "out:c")
}

func TestExec_UnknownTool(t *testing.T) {
	ok := &fakeTool{name: "ok"}
	log := &fakeLog{}
	e := newExec(log, AllowAll{}, ok)
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "nope"), call("2", "ok")})
	assert.NoErr(t, err)
	assert.Eq(t, res[0].IsError, true)
	assert.Eq(t, res[0].Text, `unknown tool "nope"`)
	assert.Eq(t, res[1].IsError, false)
	assert.Eq(t, ok.calls, 1)
	assert.Eq(t, statuses(log)[0], "error")
}

func TestExec_MalformedArgs(t *testing.T) {
	ok := &fakeTool{name: "ok"}
	e := newExec(&fakeLog{}, AllowAll{}, ok)
	bad := call("1", "ok")
	bad.Input = json.RawMessage(`{"path":`)
	res, err := e.Run(context.Background(), []providers.ContentBlock{bad})
	assert.NoErr(t, err)
	assert.Eq(t, res[0].IsError, true)
	assert.Contains(t, res[0].Text, "invalid JSON arguments")
	assert.Eq(t, ok.calls, 0)
}

func TestExec_DuplicateKeyArgsRejectedBeforeGate(t *testing.T) {
	for _, in := range []string{
		`{"command":"go test","COMMAND":"rm -rf x"}`,
		`{"command":"a","command":"b"}`,
	} {
		sh := &fakeTool{name: "shell", kind: Mutating}
		gateCalls := 0
		e := newExec(&fakeLog{}, gateFunc(func(Tool) (Decision, error) { gateCalls++; return Granted, nil }), sh)
		c := call("1", "shell")
		c.Input = json.RawMessage(in)
		res, err := e.Run(context.Background(), []providers.ContentBlock{c})
		assert.NoErr(t, err)
		assert.Eq(t, res[0].IsError, true)
		assert.Contains(t, res[0].Text, `ambiguous JSON arguments for tool "shell": duplicate key`)
		assert.Eq(t, gateCalls, 0)
		assert.Eq(t, sh.calls, 0)
	}
}

func TestExec_MutatingFsyncsStartedBeforeCall(t *testing.T) {
	log := &fakeLog{}
	var startedSynced bool
	w := &fakeTool{name: "write", kind: Mutating}
	w.onCall = func() {
		for _, r := range log.snapshot() {
			if r.Typ == EvToolStarted && r.Sync {
				startedSynced = true
			}
		}
	}
	e := newExec(log, AllowAll{}, w)
	_, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "write")})
	assert.NoErr(t, err)
	assert.Eq(t, startedSynced, true)
	for _, r := range log.snapshot() {
		if r.Typ == EvToolStarted {
			assert.Eq(t, r.Data["mutating"], true)
			assert.Eq(t, r.Data["call_id"], "1")
			assert.Eq(t, r.Data["tool"], "write")
		}
	}
}

func TestExec_SyncsAfterTurnResults(t *testing.T) {
	log := &fakeLog{}
	e := newExec(log, AllowAll{}, &fakeTool{name: "a"}, &fakeTool{name: "b"}, &fakeTool{name: "c"})
	_, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a"), call("2", "b"), call("3", "c")})
	assert.NoErr(t, err)
	recs := log.snapshot()
	syncs, lastResult := 0, -1
	for i, r := range recs {
		if r.Typ == syncMarker {
			syncs++
			assert.Eq(t, i, len(recs)-1)
		}
		if r.Typ == EvToolResult {
			lastResult = i
		}
	}
	assert.Eq(t, syncs, 1)
	assert.Eq(t, lastResult, len(recs)-2)
}

func TestExec_DeniedBecomesResult(t *testing.T) {
	log := &fakeLog{}
	w := &fakeTool{name: "write", kind: Mutating}
	e := newExec(log, gateFunc(func(Tool) (Decision, error) { return Denied, nil }), w)
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "write")})
	assert.NoErr(t, err)
	assert.Eq(t, res[0].IsError, true)
	assert.Eq(t, w.calls, 0)
	assert.Eq(t, statuses(log)[0], "denied")
	var approval map[string]any
	for _, r := range log.snapshot() {
		if r.Typ == EvApproval {
			approval = r.Data
		}
	}
	assert.Eq(t, approval["decision"], "denied")
	assert.Eq(t, approval["call_id"], "1")
}

func TestExec_UnavailableStops(t *testing.T) {
	log := &fakeLog{}
	r := &fakeTool{name: "r"}
	w := &fakeTool{name: "write", kind: Mutating}
	e := newExec(log, gateFunc(func(Tool) (Decision, error) { return Unavailable, nil }), r, w)
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "r"), call("2", "write"), call("3", "r")})
	ee, ok := err.(*ExitError)
	if !ok || ee.Code != ExitApprovalNeeded {
		t.Fatalf("want ExitError 5, got %v", err)
	}
	assert.Eq(t, len(res), 3)
	assert.Eq(t, res[0].IsError, false)
	assert.Eq(t, res[1].IsError, true)
	assert.Eq(t, res[2].IsError, true)
	assert.Eq(t, w.calls, 0)
	assert.Eq(t, r.calls, 1)
	st := statuses(log)
	assert.Eq(t, st[1], "cancelled")
	assert.Eq(t, st[2], "cancelled")
	recs := log.snapshot()
	assert.Eq(t, recs[len(recs)-1].Typ, syncMarker)
}

func TestExec_CancelDuringReadBatch(t *testing.T) {
	slow := &fakeTool{name: "slow", delay: 5 * time.Second}
	log := &fakeLog{}
	fast := &fakeTool{name: "fast", finished: make(chan struct{})}
	e := newExec(log, AllowAll{}, slow, fast)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-fast.finished; cancel() }()
	t0 := time.Now()
	res, err := e.Run(ctx, []providers.ContentBlock{call("1", "slow"), call("2", "fast"), call("3", "slow")})
	if err != context.Canceled {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(t0) > time.Second {
		t.Fatal("did not return promptly")
	}
	assert.Eq(t, len(res), 3)
	assert.Eq(t, res[0].IsError, true)
	assert.Eq(t, res[1].IsError, false)
	assert.Eq(t, res[2].IsError, true)
	st := statuses(log)
	assert.Eq(t, st[0], "cancelled")
	assert.Eq(t, st[2], "cancelled")
	recs := log.snapshot()
	assert.Eq(t, recs[len(recs)-1].Typ, syncMarker)
}

func TestRegistry_SpecsSorted(t *testing.T) {
	r := NewRegistry(&fakeTool{name: "b"}, &fakeTool{name: "a"})
	s := r.Specs()
	assert.Eq(t, s[0].Name, "a")
	assert.Eq(t, s[1].Name, "b")
	_, ok := r.Get("a")
	assert.Eq(t, ok, true)
}

func TestExec_FilterApplied(t *testing.T) {
	e := newExec(&fakeLog{}, AllowAll{}, &fakeTool{name: "a"})
	e.Filter = func(r Result) Result { r.Output = "filtered"; return r }
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a")})
	assert.NoErr(t, err)
	assert.Eq(t, res[0].Text, "filtered")
}

// failLog fails selected operations.
type failLog struct {
	fakeLog
	failSyncAppend, failAppend, failSync bool
}

var errDisk = errors.New("disk full")

func (l *failLog) Append(typ string, data any) error {
	if l.failAppend {
		return errDisk
	}
	return l.fakeLog.Append(typ, data)
}
func (l *failLog) AppendSync(typ string, data any) error {
	if l.failSyncAppend {
		return errDisk
	}
	return l.fakeLog.AppendSync(typ, data)
}
func (l *failLog) Sync() error {
	if l.failSync {
		return errDisk
	}
	return l.fakeLog.Sync()
}

func TestExec_StartedSyncFailureSkipsMutatingCall(t *testing.T) {
	w := &fakeTool{name: "write", kind: Mutating}
	e := newExec(&failLog{failSyncAppend: true}, AllowAll{}, w)
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "write")})
	assert.Eq(t, w.calls, 0)
	assert.Eq(t, res[0].IsError, true)
	assert.Contains(t, res[0].Text, "call not run")
	if !errors.Is(err, errDisk) {
		t.Fatalf("want wrapped disk error, got %v", err)
	}
}

func TestExec_AppendFailureReturnsError(t *testing.T) {
	e := newExec(&failLog{failAppend: true}, AllowAll{}, &fakeTool{name: "a"})
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a")})
	assert.Eq(t, len(res), 1)
	if !errors.Is(err, errDisk) {
		t.Fatalf("want wrapped disk error, got %v", err)
	}
	assert.Contains(t, err.Error(), "event log")
}

func TestExec_SyncFailureReturnsError(t *testing.T) {
	e := newExec(&failLog{failSync: true}, AllowAll{}, &fakeTool{name: "a"})
	_, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "a")})
	if !errors.Is(err, errDisk) {
		t.Fatalf("want wrapped disk error, got %v", err)
	}
	assert.Contains(t, err.Error(), "event log")
}

func TestExec_LogFailureNotMaskedByUnavailable(t *testing.T) {
	g := gateFunc(func(Tool) (Decision, error) { return Unavailable, nil })
	e := newExec(&failLog{failAppend: true}, g, &fakeTool{name: "w", kind: Mutating})
	_, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "w")})
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != ExitApprovalNeeded {
		t.Fatalf("lost ExitError: %v", err)
	}
	if !errors.Is(err, errDisk) {
		t.Fatalf("lost log error: %v", err)
	}
}

func TestExec_LogFailureNotMaskedByCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := newExec(&failLog{failSync: true}, AllowAll{}, &fakeTool{name: "a"})
	_, err := e.Run(ctx, []providers.ContentBlock{call("1", "a")})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errDisk) {
		t.Fatalf("want both errors, got %v", err)
	}
}

type ctxGate struct{ entered chan struct{} }

func (g ctxGate) Allow(ctx context.Context, _ Tool, _ json.RawMessage, _ func() *Diff) (Decision, error) {
	close(g.entered)
	<-ctx.Done()
	return "", ctx.Err()
}

func TestExec_CancelDuringApproval(t *testing.T) {
	log := &fakeLog{}
	w := &fakeTool{name: "write", kind: Mutating}
	g := ctxGate{entered: make(chan struct{})}
	e := newExec(log, g, w)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-g.entered; cancel() }()
	res, err := e.Run(ctx, []providers.ContentBlock{call("1", "write"), call("2", "write")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	assert.Eq(t, w.calls, 0)
	assert.Eq(t, len(res), 2)
	st := statuses(log)
	assert.Eq(t, st[0], "cancelled")
	assert.Eq(t, st[1], "cancelled")
}

type deniedTool struct{ fakeTool }

func (*deniedTool) Call(context.Context, ExecPolicy, json.RawMessage) (Result, error) {
	return Result{Output: "refused", IsError: true, Denied: true}, nil
}

func TestExec_ToolDeniedResultRecordedDenied(t *testing.T) {
	log := &fakeLog{}
	e := newExec(log, nil, &deniedTool{fakeTool{name: "write", kind: Mutating}})
	res, err := e.Run(context.Background(), []providers.ContentBlock{call("1", "write")})
	assert.NoErr(t, err)
	assert.Eq(t, res[0].IsError, true)
	assert.Eq(t, statuses(log)[0], "denied")
}
