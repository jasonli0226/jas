package farm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// script returns the fake turns for one attempt (1-based) of a part.
type script func(part string, attempt int) []fake.Turn

type supRig struct {
	sup     *Supervisor
	farmLog string

	mu       sync.Mutex
	attempts map[string]int
	provs    map[string][]*fake.Provider // part -> one provider per attempt
	live     atomic.Int32
	maxLive  atomic.Int32
	order    []string // parts in session-creation order
	onCreate func(w WorkerSession)
}

func final(text string) fake.Turn {
	return fake.Turn{Text: text, Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5}}
}

func call(name, args string) fake.Turn {
	return fake.Turn{Calls: []fake.Call{{Name: name, Args: args}}, Usage: providers.Usage{PromptTokens: 10, CompletionTokens: 5}}
}

func testCfg() Config {
	cfg := defaults()
	cfg.Vars = map[string]Var{}
	cfg.Budget.PerWorkerTimeout = 30 * time.Second
	cfg.Budget.FarmTimeout = time.Minute
	cfg.Budget.MaxOutputTokens = 100
	return cfg
}

// endLog wraps a session log to count live sessions.
type endLog struct {
	core.EventLog
	r *supRig
}

func (l endLog) AppendSync(typ string, data any) error {
	if typ == core.EvSessionEnded {
		l.r.live.Add(-1)
	}
	return l.EventLog.AppendSync(typ, data)
}

func newSup(t *testing.T, plan Plan, cfg Config, sc script) *supRig {
	t.Helper()
	w, base := newRepo(t)
	w.FarmID = "f1"
	plan.Base = base
	dir := filepath.Join(t.TempDir(), "farm")
	board, err := OpenBoard(filepath.Join(dir, "board.jsonl"), nil, cfg.Board)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { board.Close() })
	farmLog := filepath.Join(dir, "farm.jsonl")
	flog, err := core.OpenEventLog(farmLog, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flog.Close() })
	r := &supRig{farmLog: farmLog, attempts: map[string]int{}, provs: map[string][]*fake.Provider{}}
	r.sup = &Supervisor{
		Plan: plan, Cfg: cfg, FarmID: "f1", Dir: dir, Main: w.Main, Git: w.Git, WT: w,
		Board: board, Log: flog, Manifest: &Manifest{FarmID: "f1", PlanSHA256: "abc", Base: base},
		Pools: NewPools(cfg),
	}
	r.sup.NewSession = func(ws WorkerSession) (*core.Session, error) {
		r.mu.Lock()
		r.attempts[ws.Part.ID]++
		n := r.attempts[ws.Part.ID]
		prov := &fake.Provider{Turns: sc(ws.Part.ID, n)}
		r.provs[ws.Part.ID] = append(r.provs[ws.Part.ID], prov)
		r.order = append(r.order, ws.Part.ID)
		hook := r.onCreate
		r.mu.Unlock()
		if hook != nil {
			hook(ws)
		}
		ws.Log = endLog{ws.Log, r}
		c := ws.Config(prov, "m")
		c.MaxTurns = 10
		s, err := core.NewSession(c)
		if err == nil {
			if v := r.live.Add(1); v > r.maxLive.Load() {
				r.maxLive.Store(v)
			}
		}
		return s, err
	}
	return r
}

func (r *supRig) run(t *testing.T, ctx context.Context) Outcome {
	t.Helper()
	out, err := r.sup.RunParts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (r *supRig) part(id string) PartState {
	var ps PartState
	r.sup.Manifest.Update(func(m *Manifest) { ps = *m.Parts[id] })
	return ps
}

func (r *supRig) events(t *testing.T, typ string) []map[string]any {
	t.Helper()
	if err := r.sup.Log.Sync(); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, e := range testutil.ReadEvents(t, r.farmLog) {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

func coder(id, owns string, after ...string) Part {
	return Part{ID: id, Role: "coder", Goal: "do " + id, Owns: []string{owns}, After: after}
}

func writeCall(path, content string) fake.Turn {
	b, _ := json.Marshal(map[string]string{"path": path, "content": content})
	return call("write", string(b))
}

func sortedEq(t *testing.T, got []string, want ...string) {
	t.Helper()
	g := slices.Clone(got)
	slices.Sort(g)
	slices.Sort(want)
	if !slices.Equal(g, want) {
		t.Fatalf("got %v, want %v", g, want)
	}
}

func TestSupervisor_DAGOrderAndMaxWorkers(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{
		coder("a", "a/**"), coder("b", "b/**"), coder("c", "c/**"), coder("d", "d/**", "a"),
	}}
	cfg := testCfg()
	cfg.Split.MaxWorkers = 2
	slow := func(part string, _ int) []fake.Turn {
		t := writeCall(part+"/x.go", "package "+part+"\n")
		t.OnEmit = func() { time.Sleep(40 * time.Millisecond) }
		return []fake.Turn{t, final(part + " done")}
	}
	r := newSup(t, plan, cfg, slow)
	r.onCreate = func(ws WorkerSession) {
		if ws.Part.ID == "d" {
			if st := r.part("a").Status; st != StatusDone {
				t.Errorf("d started while a is %s", st)
			}
		}
	}
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "a", "b", "c", "d")
	if m := r.maxLive.Load(); m > 2 || m < 2 {
		t.Fatalf("max live sessions %d, want 2", m)
	}
	// d's branch has a's work merged in.
	if _, err := r.sup.Git.Run(context.Background(), r.sup.Main, "cat-file", "-e", r.sup.WT.Branch("d")+":a/x.go"); err != nil {
		t.Fatalf("d lacks a's file: %v", err)
	}
}

func TestSupervisor_DoneCommitsAndPostsDone(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("core", "core/**")}}
	r := newSup(t, plan, testCfg(), func(string, int) []fake.Turn {
		return []fake.Turn{writeCall("core/a.go", "package core\n"), final("Added core/a.go with package core.")}
	})
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "core")
	br := r.sup.WT.Branch("core")
	msg := mustGit(t, r.sup.Git, r.sup.Main, "log", "-1", "--format=%B", br)
	for _, want := range []string{"farm f1 part core: do core", "Jas-Farm: f1", "Jas-Part: core"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("commit message %q lacks %q", msg, want)
		}
	}
	files := mustGit(t, r.sup.Git, r.sup.Main, "show", "--name-only", "--format=", br)
	if files != "core/a.go" {
		t.Fatalf("committed %q", files)
	}
	var done *Post
	for _, p := range r.sup.Board.All() {
		if p.Topic == TopicDone && p.Part == "core" {
			done = &p
		}
	}
	if done == nil {
		t.Fatal("no done post")
	}
	if ds, _ := done.Fields["diffstat"].(string); !strings.Contains(ds, "1 file changed") {
		t.Fatalf("diffstat %q", ds)
	}
	if s, _ := done.Fields["summary"].(string); !strings.HasPrefix(s, "Added core/a.go") {
		t.Fatalf("summary %q", s)
	}
	ps := r.part("core")
	if ps.Status != StatusDone || ps.Diffstat == "" || ps.Tokens == 0 || ps.SessionID == "" {
		t.Fatalf("manifest part %+v", ps)
	}
	if _, err := os.Stat(filepath.Join(r.sup.Dir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisor_ShellWriteOutsideOwnsFails(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("core", "core/**"), coder("dep", "dep/**", "core")}}
	r := newSup(t, plan, testCfg(), func(string, int) []fake.Turn {
		return []fake.Turn{call("shell", `{"command":"mkdir -p cmd && touch cmd/x.go"}`), final("done")}
	})
	out := r.run(t, context.Background())
	if ps := r.part("core"); ps.FailureReason != ReasonWriteLimit {
		t.Fatalf("core %+v", ps)
	}
	sortedEq(t, out.Dropped, "core", "dep")
	if r.attempts["dep"] != 0 {
		t.Fatal("dependent started")
	}
}

func TestSupervisor_FailureNoTTYDrop(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**", "a"), coder("c", "c/**")}}
	r := newSup(t, plan, testCfg(), func(part string, _ int) []fake.Turn {
		if part == "a" {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return []fake.Turn{final(part + " ok")}
	})
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "c")
	sortedEq(t, out.Dropped, "a", "b")
	if ps := r.part("a"); ps.Status != StatusDropped || ps.FailureReason != ReasonError {
		t.Fatalf("a %+v", ps)
	}
	if ps := r.part("b"); ps.Status != StatusDropped {
		t.Fatalf("b %+v", ps)
	}
	if len(r.events(t, EvOwnerDecision)) == 0 {
		t.Fatal("no owner_decision for on_failure")
	}
}

func TestSupervisor_FailureNoTTYAbort(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("c", "c/**")}}
	cfg := testCfg()
	cfg.OnFailure = onFailureAbort
	started := make(chan struct{})
	r := newSup(t, plan, cfg, func(part string, _ int) []fake.Turn {
		if part == "a" {
			return []fake.Turn{{Err: errors.New("boom"), OnEmit: nil}}
		}
		return []fake.Turn{{Block: true, OnEmit: func() { close(started) }}}
	})
	r.onCreate = func(ws WorkerSession) {
		if ws.Part.ID == "a" {
			<-started // a fails only once c is mid-request
		}
	}
	done := make(chan Outcome, 1)
	go func() { done <- r.run(t, context.Background()) }()
	select {
	case out := <-done:
		sortedEq(t, out.Failed, "a")
		if slices.Contains(out.Done, "c") {
			t.Fatal("c should have been cancelled")
		}
		if ps := r.part("c"); ps.Status == StatusRunning || ps.Status == StatusDone {
			t.Fatalf("c %+v", ps)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("abort did not cancel the running part")
	}
}

func TestSupervisor_FailureTTYRetry(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**")}}
	r := newSup(t, plan, testCfg(), func(_ string, attempt int) []fake.Turn {
		if attempt == 1 {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return []fake.Turn{writeCall("a/x.go", "package a\n"), final("ok")}
	})
	var asked []string
	r.sup.Ask = func(prompt string, choices []string) (string, bool) {
		asked = append(asked, prompt)
		if !slices.Equal(choices, []string{"retry", "drop", "abort"}) {
			t.Errorf("choices %v", choices)
		}
		return "retry", true
	}
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "a")
	if len(asked) != 1 || r.attempts["a"] != 2 {
		t.Fatalf("asked %d times, %d attempts", len(asked), r.attempts["a"])
	}
	first := r.provs["a"][1].Requests[0].Messages[0].Content[0].Text
	if !strings.Contains(first, "previous attempt failed") || !strings.Contains(first, "boom") {
		t.Fatalf("retry message %q", first)
	}
}

func TestSupervisor_PerWorkerBudgetFailsPart(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{{ID: "a", Role: "tiny", Goal: "a", Owns: []string{"a/**"}}, coder("b", "b/**")}}
	cfg := testCfg()
	cfg.Roles = map[string]RoleDef{"tiny": {Tools: builtinTools(), Budget: 10}}
	r := newSup(t, plan, cfg, func(part string, _ int) []fake.Turn { return []fake.Turn{final(part)} })
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "b")
	if ps := r.part("a"); ps.FailureReason != ReasonBudget {
		t.Fatalf("a %+v", ps)
	}
	if out.BudgetStop != "" {
		t.Fatalf("budget stop %q", out.BudgetStop)
	}
}

func TestSupervisor_FarmPoolStopsScheduling(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**")}}
	cfg := testCfg()
	cfg.Split.MaxWorkers = 1
	cfg.Budget.FarmTokens = 50
	r := newSup(t, plan, cfg, func(part string, _ int) []fake.Turn { return []fake.Turn{final(part)} })
	out := r.run(t, context.Background())
	if out.BudgetStop != "tokens" {
		t.Fatalf("budget stop %q", out.BudgetStop)
	}
	if r.attempts["b"] != 0 || len(out.Done) != 0 {
		t.Fatalf("b started or something done: %+v", out)
	}
	if ps := r.part("a"); ps.Status == StatusFailed {
		t.Fatalf("farm stop is not a part failure: %+v", ps)
	}
	if len(r.events(t, EvBudgetStop)) == 0 {
		t.Fatal("no budget_stop in farm log")
	}
}

func TestSupervisor_BoardDeliveredToDependents(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**")}}
	posted := make(chan struct{})
	r := newSup(t, plan, testCfg(), func(part string, _ int) []fake.Turn {
		if part == "a" {
			last := final("a done")
			last.OnEmit = func() { close(posted) }
			return []fake.Turn{
				call("board_post", `{"topic":"interface","fields":{"name":"Store","shape":"func Get(k string) string"}}`),
				last,
			}
		}
		first := call("ls", `{"path":"."}`)
		first.OnEmit = func() { <-posted }
		return []fake.Turn{first, final("b done")}
	})
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "a", "b")
	req := r.provs["b"][0].Requests[1]
	last := req.Messages[len(req.Messages)-1]
	var sawBoard bool
	for _, c := range last.Content {
		if strings.Contains(c.Text, BoardLabel) && strings.Contains(c.Text, "Store") {
			sawBoard = true
		}
	}
	if !sawBoard {
		t.Fatalf("b never saw the board: %+v", last.Content)
	}
	var forB bool
	for _, e := range r.events(t, EvBoardDelivered) {
		if e["part"] == "b" {
			forB = true
		}
	}
	if !forB {
		t.Fatal("no board_delivered{part:b}")
	}
}

func TestSupervisor_ApprovalNoTTYFailsPart(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{{ID: "a", Role: "gated", Goal: "a", Owns: []string{"a/**"}}}}
	cfg := testCfg()
	cfg.Roles = map[string]RoleDef{"gated": {Tools: builtinTools(), Approve: "shell"}}
	r := newSup(t, plan, cfg, func(string, int) []fake.Turn {
		return []fake.Turn{call("shell", `{"command":"echo hi"}`), final("x")}
	})
	r.sup.Approvals = NewApprovals(strings.NewReader(""), &strings.Builder{}, false, core.Never)
	out := r.run(t, context.Background())
	sortedEq(t, out.Dropped, "a")
	if ps := r.part("a"); ps.FailureReason != ReasonApprovalNeeded || ps.ExitCode != core.ExitApprovalNeeded {
		t.Fatalf("a %+v", ps)
	}
}

func TestSupervisor_InterruptKeepsWorktrees(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**")}}
	cfg := testCfg()
	cfg.Split.MaxWorkers = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newSup(t, plan, cfg, func(string, int) []fake.Turn {
		first := writeCall("a/x.go", "package a\n")
		first.OnEmit = cancel // first Ctrl-C while the request is in flight
		return []fake.Turn{first, final("never")}
	})
	out := r.run(t, ctx)
	if !out.Interrupted {
		t.Fatalf("outcome %+v", out)
	}
	if r.attempts["b"] != 0 {
		t.Fatal("b scheduled after interrupt")
	}
	// The in-flight request finished and its tool ran; no further request.
	if n := len(r.provs["a"][0].Requests); n != 1 {
		t.Fatalf("%d requests after interrupt", n)
	}
	if _, err := os.Stat(filepath.Join(r.sup.WT.Path("a"), "a", "x.go")); err != nil {
		t.Fatalf("worktree not kept: %v", err)
	}
	if ps := r.part("a"); ps.Status != StatusPaused {
		t.Fatalf("a %+v", ps)
	}
}

// fakeClock advances by d on every After, so waits are instant.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- now
	return ch
}

func TestLimiter_SlotsSpaced(t *testing.T) {
	clk := &fakeClock{now: time.Unix(0, 0)}
	l := NewLimiter(60, clk)
	var got []time.Time
	for i := 0; i < 3; i++ {
		if err := l.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		got = append(got, clk.Now())
	}
	for i := 1; i < len(got); i++ {
		if d := got[i].Sub(got[i-1]); d < time.Second {
			t.Fatalf("requests %v apart", d)
		}
	}
	if NewLimiter(0, clk) != nil {
		t.Fatal("perMinute 0 must disable the limiter")
	}
}

func TestSupervisor_SharedRateLimit(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**"), coder("c", "c/**")}}
	cfg := testCfg()
	cfg.Budget.RequestsPerMinute = 60
	clk := &fakeClock{now: time.Unix(0, 0)}
	r := newSup(t, plan, cfg, func(part string, _ int) []fake.Turn {
		return []fake.Turn{call("ls", `{"path":"."}`), final(part)}
	})
	r.sup.Clock = clk
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "a", "b", "c")
	n := 0
	for _, ps := range r.provs {
		n += len(ps[0].Requests)
	}
	// One shared limiter: n requests need n-1 one-second slots. Per-worker
	// limiters would overlap and advance the shared clock far less.
	if got, want := clk.Now().Sub(time.Unix(0, 0)), time.Duration(n-1)*time.Second; got < want {
		t.Fatalf("clock advanced %v for %d requests, want >= %v", got, n, want)
	}
}

func TestWorkerPrompt(t *testing.T) {
	plan := Plan{Task: "build it", Contracts: "Use errors.Is.", Interfaces: []Interface{{Name: "Store", Owner: "a", Shape: "Get(k) string"}},
		Parts: []Part{coder("a", "a/**"), coder("b", "b/**", "a")}}
	role := Role{Name: "coder", Prompt: "ROLE PROMPT"}
	got := WorkerPrompt(plan, plan.Parts[1], role, "abc123")
	for _, want := range []string{"ROLE PROMPT", "do b", "Use errors.Is.", "Store", "Get(k) string", "a/**", "b/**", "abc123"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, got)
		}
	}
}

func TestWorkerPolicy(t *testing.T) {
	base := core.ExecPolicy{Root: "/main", Deny: []string{"~/.jas/**"}, Env: []string{"GOPATH"}}
	p := WorkerPolicy(base, Role{}, "/wt/a")
	if p.Root != "/wt/a" || p.Writes == nil {
		t.Fatalf("policy %+v", p)
	}
	if !slices.Contains(p.Deny, ".jas/sessions/**") || !slices.Contains(p.Deny, "~/.jas/**") {
		t.Fatalf("deny %v", p.Deny)
	}
	for _, want := range []string{"GOPATH", "TMPDIR=" + filepath.Join("/wt/a", ".jas", "tmp"), "GIT_CONFIG_NOSYSTEM=1"} {
		if !slices.Contains(p.Env, want) {
			t.Fatalf("env %v lacks %s", p.Env, want)
		}
	}
	if len(base.Deny) != 1 {
		t.Fatal("base policy mutated")
	}
}

func TestRelevant(t *testing.T) {
	plan := Plan{Interfaces: []Interface{{Name: "Store"}},
		Parts: []Part{coder("a", "a/**"), coder("b", "b/**", "a"), coder("c", "c/**", "b"), coder("x", "x/**")}}
	rel := relevance(plan, plan.Parts[2]) // c: deps b, a (transitive)
	cases := []struct {
		p    Post
		want bool
	}{
		{Post{Part: "a", Topic: TopicBlocked, Fields: map[string]any{"on": "x", "need": "y"}}, true},
		{Post{Part: "x", Topic: TopicInterface, Fields: map[string]any{"name": "Other"}}, true},
		{Post{Part: "x", Topic: TopicOwns, Fields: map[string]any{"paths": []string{"c/main.go"}}}, true},
		{Post{Part: "x", Topic: TopicBlocked, Fields: map[string]any{"on": "shared:go.mod", "need": "touch c/z.go please"}}, true},
		{Post{Part: "x", Topic: TopicBlocked, Fields: map[string]any{"on": "b", "need": "the Store type"}}, true},
		{Post{Part: "x", Topic: TopicOwns, Fields: map[string]any{"paths": []string{"x/main.go"}}}, false},
	}
	for i, c := range cases {
		if got := rel(c.p); got != c.want {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestSupervisor_Timeouts(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**")}}
	block := func(string, int) []fake.Turn { return []fake.Turn{{Block: true}} }

	cfg := testCfg()
	cfg.Budget.PerWorkerTimeout = 50 * time.Millisecond
	r := newSup(t, plan, cfg, block)
	out := r.run(t, context.Background())
	if ps := r.part("a"); ps.FailureReason != ReasonTimeout || out.BudgetStop != "" {
		t.Fatalf("per-worker: %+v %+v", ps, out)
	}

	cfg = testCfg()
	cfg.Budget.FarmTimeout = 50 * time.Millisecond
	r = newSup(t, plan, cfg, block)
	out = r.run(t, context.Background())
	if out.BudgetStop != "timeout" || len(out.Failed) != 0 {
		t.Fatalf("farm: %+v", out)
	}
}

func TestSupervisor_ApprovalsSerializedAndLabelled(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{
		{ID: "a", Role: "gated", Goal: "a", Owns: []string{"a/**"}},
		{ID: "b", Role: "gated", Goal: "b", Owns: []string{"b/**"}},
	}}
	cfg := testCfg()
	cfg.Roles = map[string]RoleDef{"gated": {Tools: builtinTools(), Approve: "shell"}}
	r := newSup(t, plan, cfg, func(part string, _ int) []fake.Turn {
		return []fake.Turn{call("shell", `{"command":"echo `+part+`"}`), final(part)}
	})
	var out strings.Builder
	r.sup.Approvals = NewApprovals(strings.NewReader("y\ny\n"), &syncWriter{w: &out}, true, core.Never)
	res := r.run(t, context.Background())
	sortedEq(t, res.Done, "a", "b")
	text := out.String()
	for _, want := range []string{"[a] approve shell", "[b] approve shell"} {
		if !strings.Contains(text, want) {
			t.Fatalf("prompts %q lack %q", text, want)
		}
	}
}

type syncWriter struct {
	mu sync.Mutex
	w  *strings.Builder
}

func (s *syncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

func TestSupervisor_RetryRedeliversBoard(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**"), coder("b", "b/**", "a")}}
	cfg := testCfg()
	cfg.Board.MaxRounds = 1
	r := newSup(t, plan, cfg, func(part string, attempt int) []fake.Turn {
		if part == "a" {
			return []fake.Turn{
				call("board_post", `{"topic":"interface","fields":{"name":"Store","shape":"Get"}}`),
				final("a done"),
			}
		}
		if attempt == 1 {
			return []fake.Turn{call("ls", `{"path":"."}`), {Err: errors.New("boom")}}
		}
		return []fake.Turn{call("ls", `{"path":"."}`), final("b done")}
	})
	r.sup.Ask = func(string, []string) (string, bool) { return "retry", true }
	out := r.run(t, context.Background())
	sortedEq(t, out.Done, "a", "b")
	for attempt, prov := range r.provs["b"] {
		req := prov.Requests[1]
		last := req.Messages[len(req.Messages)-1]
		var saw bool
		for _, c := range last.Content {
			saw = saw || strings.Contains(c.Text, "Store")
		}
		if !saw {
			t.Fatalf("attempt %d of b did not receive the interface post", attempt+1)
		}
	}
}

func TestSupervisor_WorkerPostLoggedAndPartEndedTotals(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**")}}
	r := newSup(t, plan, testCfg(), func(string, int) []fake.Turn {
		return []fake.Turn{
			call("board_post", `{"topic":"interface","fields":{"name":"Store","shape":"Get"}}`),
			writeCall("a/x.go", "package a\n"),
			final("a done"),
		}
	})
	r.run(t, context.Background())
	var iface map[string]any
	for _, e := range r.events(t, EvBoardPosted) {
		if e["topic"] == TopicInterface {
			iface = e
		}
	}
	if iface == nil || iface["part"] != "a" || iface["seq"] == nil || iface["name"] != "Store" {
		t.Fatalf("board_posted %v", iface)
	}
	if _, dup := iface["fields"]; dup {
		t.Fatal("board_posted must not repeat the post text")
	}
	ended := r.events(t, EvPartEnded)
	if len(ended) != 1 {
		t.Fatalf("part_ended %v", ended)
	}
	if tok, _ := ended[0]["tokens"].(float64); tok <= 0 {
		t.Fatalf("part_ended tokens %v", ended[0])
	}
	if ds, _ := ended[0]["diffstat"].(string); !strings.Contains(ds, "1 file changed") {
		t.Fatalf("part_ended diffstat %v", ended[0])
	}
}

// commitCanceller cancels the hard context when jas commits, as a second
// Ctrl-C landing during finishDone would.
type commitCanceller struct {
	GitRunner
	cancel context.CancelFunc
}

func (g commitCanceller) Run(ctx context.Context, dir string, args ...string) (string, error) {
	if gitSubcommand(args) == "commit" {
		g.cancel()
		return "", context.Canceled
	}
	return g.GitRunner.Run(ctx, dir, args...)
}

func TestSupervisor_HardCancelDuringCommitPauses(t *testing.T) {
	plan := Plan{Task: "t", Parts: []Part{coder("a", "a/**")}}
	r := newSup(t, plan, testCfg(), func(string, int) []fake.Turn {
		return []fake.Turn{writeCall("a/x.go", "package a\n"), final("a done")}
	})
	hard, cancel := context.WithCancel(context.Background())
	defer cancel()
	r.sup.Hard = hard
	r.sup.WT.Git = commitCanceller{GitRunner: r.sup.WT.Git, cancel: cancel}
	out := r.run(t, context.Background())
	if ps := r.part("a"); ps.Status != StatusPaused || ps.FailureReason != "" {
		t.Fatalf("a %+v", ps)
	}
	if !out.Interrupted || len(out.Failed) != 0 {
		t.Fatalf("outcome %+v", out)
	}
}
