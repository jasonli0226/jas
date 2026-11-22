package farm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

// brokerTool is a fake tool; only its name and kind matter to a gate.
type brokerTool struct {
	name string
	kind core.Kind
}

func (b brokerTool) Name() string          { return b.name }
func (brokerTool) Description() string     { return "" }
func (brokerTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (b brokerTool) Kind() core.Kind       { return b.kind }
func (brokerTool) Call(context.Context, core.ExecPolicy, json.RawMessage) (core.Result, error) {
	return core.Result{}, nil
}

var (
	shellTool = brokerTool{name: "shell", kind: core.Mutating}
	goTest    = json.RawMessage(`{"command":"go test ./..."}`)
)

// recLog records appended events.
type recLog struct {
	mu  sync.Mutex
	evs []logEntry
}

type logEntry struct {
	typ  string
	data map[string]any
}

func (l *recLog) Append(typ string, data any) error {
	m, _ := data.(map[string]any)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evs = append(l.evs, logEntry{typ, m})
	return nil
}
func (l *recLog) AppendID(typ string, data any) (string, error) { return "", l.Append(typ, data) }
func (l *recLog) AppendSync(typ string, data any) error         { return l.Append(typ, data) }
func (l *recLog) Sync() error                                   { return nil }
func (l *recLog) Close() error                                  { return nil }

func (l *recLog) events() []logEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]logEntry(nil), l.evs...)
}

// allowResult is one Allow's outcome.
type allowResult struct {
	d   core.Decision
	err error
}

// allowAsync runs g.Allow on a goroutine.
func allowAsync(ctx context.Context, g core.Gate, t core.Tool, args json.RawMessage) <-chan allowResult {
	ch := make(chan allowResult, 1)
	go func() {
		d, err := g.Allow(ctx, t, args, nil)
		ch <- allowResult{d, err}
	}()
	return ch
}

func waitResult(t *testing.T, ch <-chan allowResult) allowResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("Allow did not return")
	}
	return allowResult{}
}

func noResult(t *testing.T, ch <-chan allowResult) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("Allow returned early: %v %v", r.d, r.err)
	default:
	}
}

// nextNotice reads one notice.
func nextNotice(t *testing.T, ch <-chan Notice) Notice {
	t.Helper()
	select {
	case n := <-ch:
		return n
	case <-time.After(5 * time.Second):
		t.Fatal("no notice")
	}
	return Notice{}
}

// countHost answers every request with a and counts the asks.
type countHost struct {
	mu   sync.Mutex
	n    int
	a    Answer
	reqs []ApprovalRequest
}

func (h *countHost) ask(_ context.Context, req ApprovalRequest) (Answer, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.n++
	h.reqs = append(h.reqs, req)
	return h.a, nil
}

func (h *countHost) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.n
}

// neverAfter is an After that never fires.
func neverAfter(time.Duration) <-chan time.Time { return nil }

func mustGate(t *testing.T, b *Broker, part string, log core.EventLog) core.Gate {
	t.Helper()
	g, err := b.Gate(part, "", log)
	assert.NoErr(t, err)
	return g
}

func TestBroker_ParseAnswer(t *testing.T) {
	for in, want := range map[string]Answer{
		"y": AnswerYes, "YES": AnswerYes, "n": AnswerNo, "No": AnswerNo, "": AnswerNo,
		"always": AnswerAlways, "a": AnswerAlways, "farm": AnswerFarm, " F ": AnswerFarm,
	} {
		got, ok := ParseAnswer(in)
		if !ok || got != want {
			t.Errorf("ParseAnswer(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseAnswer("maybe"); ok {
		t.Error("ParseAnswer(maybe) ok")
	}
	for a, s := range map[Answer]string{AnswerYes: "yes", AnswerNo: "no", AnswerAlways: "always", AnswerFarm: "farm"} {
		assert.Eq(t, a.String(), s)
	}
}

func TestBroker_NotGatedAuto(t *testing.T) {
	h := &countHost{a: AnswerYes}
	b := NewBroker(BrokerOptions{Level: core.Never, Host: h.ask})
	log := &recLog{}
	d, err := mustGate(t, b, "p1", log).Allow(context.Background(), shellTool, goTest, nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.AutoGranted)
	assert.Eq(t, len(log.events()), 0)
	assert.Eq(t, h.count(), 0)
}

func TestBroker_HostAnswers(t *testing.T) {
	h := &countHost{a: AnswerYes}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	log := &recLog{}
	d, err := mustGate(t, b, "p1", log).Allow(context.Background(), shellTool, goTest, nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.Granted)
	evs := log.events()
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	assert.Eq(t, evs[0].typ, EvApprovalRequested)
	assert.Eq(t, evs[0].data["approval_id"], any("a1"))
	assert.Eq(t, evs[0].data["tool"], any("shell"))
	assert.Eq(t, evs[0].data["summary"], any("go test ./..."))
	assert.Eq(t, evs[1].typ, EvApprovalClosed)
	assert.Eq(t, evs[1].data["approval_id"], any("a1"))
	assert.Eq(t, evs[1].data["answer"], any("yes"))
	assert.Eq(t, evs[1].data["by"], any("host"))
	assert.Eq(t, h.reqs[0].Part, "p1")
	assert.Eq(t, len(b.Pending()), 0)
}

func TestBroker_ClientWinsHostWithdrawn(t *testing.T) {
	asked := make(chan ApprovalRequest, 1)
	withdrawn := make(chan struct{})
	host := func(ctx context.Context, req ApprovalRequest) (Answer, error) {
		asked <- req
		<-ctx.Done()
		close(withdrawn)
		return AnswerYes, ctx.Err()
	}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: host})
	log := &recLog{}
	res := allowAsync(context.Background(), mustGate(t, b, "p1", log), shellTool, goTest)
	req := <-asked
	assert.Eq(t, req.ID, "a1")
	ok, msg := b.Answer("a1", AnswerNo, "attach:c1")
	if !ok || msg != "" {
		t.Fatalf("Answer = %v %q", ok, msg)
	}
	r := waitResult(t, res)
	assert.NoErr(t, r.err)
	assert.Eq(t, r.d, core.Denied)
	select {
	case <-withdrawn:
	case <-time.After(5 * time.Second):
		t.Fatal("host ctx not cancelled")
	}
	ok, msg = b.Answer("a1", AnswerYes, "host")
	assert.Eq(t, ok, false)
	assert.Eq(t, msg, "already answered (no, by attach:c1)")
	evs := log.events()
	last := evs[len(evs)-1]
	assert.Eq(t, last.typ, EvApprovalClosed)
	assert.Eq(t, last.data["by"], any("attach:c1"))
	assert.Eq(t, last.data["answer"], any("no"))
}

func TestBroker_UnknownID(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly})
	ok, msg := b.Answer("a9", AnswerYes, "attach:c1")
	assert.Eq(t, ok, false)
	assert.Eq(t, msg, "unknown approval")
}

func TestBroker_AlwaysCoversPart(t *testing.T) {
	h := &countHost{a: AnswerAlways}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	p1, p2 := mustGate(t, b, "p1", &recLog{}), mustGate(t, b, "p2", &recLog{})
	ctx := context.Background()
	d, err := p1.Allow(ctx, shellTool, goTest, nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.Granted)
	d, _ = p1.Allow(ctx, shellTool, goTest, nil)
	assert.Eq(t, d, core.AutoGranted)
	assert.Eq(t, h.count(), 1)
	d, _ = p2.Allow(ctx, shellTool, goTest, nil)
	assert.Eq(t, d, core.Granted)
	assert.Eq(t, h.count(), 2)
}

func TestBroker_FarmCoversAll(t *testing.T) {
	h := &countHost{a: AnswerFarm}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	p1, p2 := mustGate(t, b, "p1", &recLog{}), mustGate(t, b, "p2", &recLog{})
	ctx := context.Background()
	d, _ := p1.Allow(ctx, shellTool, goTest, nil)
	assert.Eq(t, d, core.Granted)
	d, _ = p2.Allow(ctx, shellTool, goTest, nil)
	assert.Eq(t, d, core.AutoGranted)
	assert.Eq(t, h.count(), 1)
}

func TestBroker_MustAskNoGrant(t *testing.T) {
	h := &countHost{a: AnswerFarm}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	p1, p2 := mustGate(t, b, "p1", &recLog{}), mustGate(t, b, "p2", &recLog{})
	write := core.AskAlways(brokerTool{name: "write", kind: core.Mutating})
	args := json.RawMessage(`{"path":"skills/x/SKILL.md"}`)
	ctx := context.Background()
	d, _ := p1.Allow(ctx, write, args, nil)
	assert.Eq(t, d, core.Granted)
	d, _ = p1.Allow(ctx, write, args, nil)
	assert.Eq(t, d, core.Granted)
	d, _ = p2.Allow(ctx, write, args, nil)
	assert.Eq(t, d, core.Granted)
	assert.Eq(t, h.count(), 3)
}

func TestBroker_NoHostNoClientRefuses(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: neverAfter})
	log := &recLog{}
	d, err := mustGate(t, b, "p1", log).Allow(context.Background(), shellTool, goTest, nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.Unavailable)
	evs := log.events()
	if len(evs) != 2 {
		t.Fatalf("events = %+v", evs)
	}
	assert.Eq(t, evs[0].typ, EvApprovalRequested)
	assert.Eq(t, evs[1].typ, EvApprovalClosed)
	assert.Eq(t, evs[1].data["answer"], any("refused"))
	assert.Eq(t, evs[1].data["by"], any("no_client"))
	_, msg := b.Answer("a1", AnswerYes, "attach:c1")
	assert.Eq(t, msg, "already answered (refused, by no_client)")
}

func TestBroker_HeldWhileAttached(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: neverAfter})
	b.SetClients(1)
	notes, stop := b.Subscribe()
	defer stop()
	res := allowAsync(context.Background(), mustGate(t, b, "p1", &recLog{}), shellTool, goTest)
	n := nextNotice(t, notes)
	assert.Eq(t, n.Type, EvApprovalRequested)
	noResult(t, res)
	if p := b.Pending(); len(p) != 1 || p[0].ID != "a1" {
		t.Fatalf("Pending = %+v", p)
	}
	ok, _ := b.Answer(n.Request.ID, AnswerYes, "attach:c1")
	assert.Eq(t, ok, true)
	r := waitResult(t, res)
	assert.NoErr(t, r.err)
	assert.Eq(t, r.d, core.Granted)
}

func TestBroker_LastClientLeaves(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: neverAfter})
	b.SetClients(1)
	notes, stop := b.Subscribe()
	defer stop()
	log := &recLog{}
	res := allowAsync(context.Background(), mustGate(t, b, "p1", log), shellTool, goTest)
	nextNotice(t, notes)
	b.SetClients(2)
	b.SetClients(1)
	noResult(t, res)
	b.SetClients(0)
	r := waitResult(t, res)
	assert.NoErr(t, r.err)
	assert.Eq(t, r.d, core.Unavailable)
	evs := log.events()
	assert.Eq(t, evs[len(evs)-1].data["by"], any("no_client"))
	assert.Eq(t, evs[len(evs)-1].data["answer"], any("refused"))
}

func TestBroker_WindowElapses(t *testing.T) {
	fire := make(chan time.Time, 1)
	got := make(chan time.Duration, 1)
	after := func(d time.Duration) <-chan time.Time { got <- d; return fire }
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: after})
	b.SetClients(1)
	log := &recLog{}
	res := allowAsync(context.Background(), mustGate(t, b, "p1", log), shellTool, goTest)
	assert.Eq(t, <-got, defaultApprovalWindow)
	noResult(t, res)
	fire <- time.Now()
	r := waitResult(t, res)
	assert.NoErr(t, r.err)
	assert.Eq(t, r.d, core.Unavailable)
	evs := log.events()
	assert.Eq(t, evs[len(evs)-1].data["by"], any("timeout"))
	assert.Eq(t, evs[len(evs)-1].data["answer"], any("refused"))
}

func TestBroker_WaitHoldsWithoutClients(t *testing.T) {
	got := make(chan time.Duration, 1)
	after := func(d time.Duration) <-chan time.Time { got <- d; return nil }
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Wait: 5 * time.Minute, After: after})
	notes, stop := b.Subscribe()
	defer stop()
	res := allowAsync(context.Background(), mustGate(t, b, "p1", &recLog{}), shellTool, goTest)
	n := nextNotice(t, notes)
	assert.Eq(t, <-got, 5*time.Minute)
	b.SetClients(1)
	b.SetClients(0) // with a wait set, the last client leaving does not refuse
	noResult(t, res)
	ok, _ := b.Answer(n.Request.ID, AnswerYes, "attach:c1")
	assert.Eq(t, ok, true)
	assert.Eq(t, waitResult(t, res).d, core.Granted)
}

func TestBroker_TimeoutWins(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: neverAfter})
	b.SetClients(1)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	d, err := mustGate(t, b, "p1", &recLog{}).Allow(ctx, shellTool, goTest, nil)
	assert.Eq(t, d, core.Denied)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	assert.Eq(t, len(b.Pending()), 0)
}

func TestBroker_ConcurrentRequests(t *testing.T) {
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, After: neverAfter})
	b.SetClients(1)
	notes, stop := b.Subscribe()
	defer stop()
	ctx := context.Background()
	r1 := allowAsync(ctx, mustGate(t, b, "p1", &recLog{}), shellTool, goTest)
	r2 := allowAsync(ctx, mustGate(t, b, "p2", &recLog{}), shellTool, goTest)
	byPart := map[string]string{}
	for range 2 {
		n := nextNotice(t, notes)
		byPart[n.Request.Part] = n.Request.ID
	}
	if byPart["p1"] == byPart["p2"] || len(byPart) != 2 {
		t.Fatalf("ids = %v", byPart)
	}
	for _, id := range byPart {
		if id != "a1" && id != "a2" {
			t.Fatalf("ids = %v", byPart)
		}
	}
	ok, _ := b.Answer(byPart["p2"], AnswerYes, "attach:c1")
	assert.Eq(t, ok, true)
	assert.Eq(t, waitResult(t, r2).d, core.Granted)
	noResult(t, r1)
	if p := b.Pending(); len(p) != 1 || p[0].ID != byPart["p1"] {
		t.Fatalf("Pending = %+v", p)
	}
	b.Answer(byPart["p1"], AnswerNo, "attach:c1")
	assert.Eq(t, waitResult(t, r1).d, core.Denied)
}

func TestBroker_SummaryRedacted(t *testing.T) {
	redact := func(s string) string { return strings.ReplaceAll(s, "sekret123", "[REDACTED]") }
	h := &countHost{a: AnswerNo}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask, Redact: redact})
	log := &recLog{}
	args := json.RawMessage(`{"command":"curl -H token:sekret123 example.com"}`)
	d, _ := mustGate(t, b, "p1", log).Allow(context.Background(), shellTool, args, nil)
	assert.Eq(t, d, core.Denied)
	sum, _ := log.events()[0].data["summary"].(string)
	assert.NotContains(t, sum, "sekret123")
	assert.Contains(t, sum, "[REDACTED]")
	assert.NotContains(t, h.reqs[0].Summary, "sekret123")
}

func TestBroker_Notices(t *testing.T) {
	h := &countHost{a: AnswerYes}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	notes, stop := b.Subscribe()
	_, err := mustGate(t, b, "p1", &recLog{}).Allow(context.Background(), shellTool, goTest, nil)
	assert.NoErr(t, err)
	n := nextNotice(t, notes)
	assert.Eq(t, n.Type, EvApprovalRequested)
	assert.Eq(t, n.Request.ID, "a1")
	n = nextNotice(t, notes)
	assert.Eq(t, n.Type, EvApprovalClosed)
	assert.Eq(t, n.Answer, "yes")
	assert.Eq(t, n.By, "host")
	stop()
	stop() // idempotent
	if _, open := <-notes; open {
		t.Fatal("channel open after stop")
	}
}

func TestBroker_SlowSubscriberNeverBlocks(t *testing.T) {
	h := &countHost{a: AnswerYes}
	b := NewBroker(BrokerOptions{Level: core.ShellOnly, Host: h.ask})
	_, stop := b.Subscribe() // never read
	defer stop()
	g := mustGate(t, b, "p1", &recLog{})
	for range 200 {
		d, _ := g.Allow(context.Background(), shellTool, json.RawMessage(`{"command":"rm -rf x"}`), nil)
		assert.Eq(t, d, core.Granted)
	}
}

func TestBroker_RequestJSON(t *testing.T) {
	r := ApprovalRequest{ID: "a1", Part: "p", CallID: "c", Tool: "shell", Summary: "s", Diff: &core.Diff{Path: "x"}}
	out, err := json.Marshal(r)
	assert.NoErr(t, err)
	assert.JSONEq(t, string(out), `{"id":"a1","part":"p","call_id":"c","tool":"shell","summary":"s"}`)
}
