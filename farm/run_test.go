package farm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

// sessionScript returns the fake turns for one session (a part's attempt or
// the integrator, whose Part.ID is "integrate").
type sessionScript func(ws WorkerSession) []fake.Turn

type runRig struct {
	t        *testing.T
	main     string
	home     string
	wtDir    string
	planPath string
	opts     RunOptions
	stdout   bytes.Buffer

	mu       sync.Mutex
	sessions []string // Part.ID of every session created
}

// newRunRig makes a repo, a jas home and an approved plan over parts.
func newRunRig(t *testing.T, check string, parts []Part, sc sessionScript) *runRig {
	t.Helper()
	w, base := newRepo(t)
	g := w.Git
	for _, kv := range [][2]string{{"user.name", "Owner"}, {"user.email", "owner@example.com"}, {"commit.gpgsign", "false"}} {
		mustGit(t, g, w.Main, "config", kv[0], kv[1])
	}
	r := &runRig{t: t, main: w.Main, home: t.TempDir(), wtDir: filepath.Join(t.TempDir(), "wt")}
	r.planPath = filepath.Join(t.TempDir(), "plan.yaml")
	r.writePlan(Plan{Task: "t", Base: base, Parts: parts}, true)
	cfg := testCfg()
	cfg.Check = check
	cfg.Worktree.Dir = r.wtDir
	r.opts = RunOptions{
		PlanPath: r.planPath, Home: r.home, Main: r.main, Stdout: &r.stdout, Stderr: &bytes.Buffer{},
		Cfg: cfg, Trusted: true,
		NewSession: func(ws WorkerSession) (*core.Session, error) {
			r.mu.Lock()
			r.sessions = append(r.sessions, ws.Part.ID)
			r.mu.Unlock()
			c := ws.Config(&fake.Provider{Turns: sc(ws)}, "m")
			c.MaxTurns = 10
			return core.NewSession(c)
		},
	}
	return r
}

func (r *runRig) writePlan(p Plan, approve bool) {
	r.t.Helper()
	raw := planYAML(r.t, p)
	if approve {
		raw = Approve(raw, "owner", time.Now())
	}
	if err := os.WriteFile(r.planPath, raw, 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func planYAML(t *testing.T, p Plan) []byte {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "task: %s\nbase: %s\n", p.Task, p.Base)
	if len(p.Shared) > 0 {
		fmt.Fprintf(&b, "shared: [%s]\n", quoteAll(p.Shared))
	}
	b.WriteString("parts:\n")
	for _, pt := range p.Parts {
		fmt.Fprintf(&b, "  - id: %s\n    role: %s\n    goal: %s\n    owns: [%s]\n", pt.ID, pt.Role, pt.Goal, quoteAll(pt.Owns))
		if len(pt.After) > 0 {
			fmt.Fprintf(&b, "    after: [%s]\n", strings.Join(pt.After, ", "))
		}
	}
	b.WriteString("why_split: test\n")
	return []byte(b.String())
}

func quoteAll(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return strings.Join(q, ", ")
}

func (r *runRig) run(ctx context.Context) (int, string) {
	r.t.Helper()
	code, reason, err := RunFarm(ctx, r.opts)
	if err != nil {
		r.t.Fatalf("RunFarm: %v (exit %d %s)", err, code, reason)
	}
	return code, reason
}

// farmDir is the one farm directory under home.
func (r *runRig) farmDir() string {
	r.t.Helper()
	dirs, _ := filepath.Glob(filepath.Join(r.home, "farms", "*"))
	if len(dirs) == 0 {
		r.t.Fatal("no farm dir")
	}
	return dirs[len(dirs)-1]
}

func (r *runRig) farmID() string { return filepath.Base(r.farmDir()) }

func (r *runRig) events(typ string) []map[string]any {
	r.t.Helper()
	var out []map[string]any
	for _, e := range testutil.ReadEvents(r.t, filepath.Join(r.farmDir(), "farm.jsonl")) {
		if typ == "" || e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

func (r *runRig) last(typ string) map[string]any {
	r.t.Helper()
	ev := r.events(typ)
	if len(ev) == 0 {
		r.t.Fatalf("no %s event", typ)
	}
	return ev[len(ev)-1]
}

func (r *runRig) git(args ...string) (string, error) {
	out, err := NewGit().Run(context.Background(), r.main, args...)
	return strings.TrimSpace(out), err
}

func writes(files map[string]string, text string) []fake.Turn {
	var turns []fake.Turn
	for p, c := range files {
		turns = append(turns, writeCall(p, c))
	}
	return append(turns, final(text))
}

func twoParts() []Part { return []Part{coder("a", "a/**"), coder("b", "b/**")} }

func twoWriters(ws WorkerSession) []fake.Turn {
	return writes(map[string]string{ws.Part.ID + "/x.txt": ws.Part.ID + "\n"}, ws.Part.ID+" done")
}

func TestRunFarm_HappyPathExit0(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	code, reason := r.run(context.Background())
	if code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s", code, reason)
	}
	br := branchPrefix + r.farmID() + "/integrate"
	for _, f := range []string{"a/x.txt", "b/x.txt"} {
		if _, err := r.git("cat-file", "-e", br+":"+f); err != nil {
			t.Fatalf("integrate branch lacks %s: %v", f, err)
		}
	}
	dec := r.last(EvOwnerDecision)
	if dec["choice"] != "keep" || dec["by"] != "policy:no-tty" {
		t.Fatalf("owner_decision %v", dec)
	}
	end := r.last(EvFarmEnded)
	if end["exit_code"] != float64(0) || end["reason"] != "ok" || end["farm_id"] != r.farmID() {
		t.Fatalf("farm_ended %v", end)
	}
	if st := r.last(EvFarmStarted); st["plan_sha256"] == "" || st["roles_digest"] == nil || st["config_digest"] == nil || st["trust"] != true {
		t.Fatalf("farm_started %v", st)
	}
	for _, p := range []string{"a", "b", "integrate"} {
		if _, err := os.Stat(filepath.Join(r.wtDir, r.farmID(), p)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("worktree %s not removed: %v", p, err)
		}
	}
	m, err := LoadManifest(filepath.Join(r.farmDir(), manifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if m.Check == nil || m.Check.ExitCode != 0 || m.FinalChoice != "keep" || m.Reason != "ok" {
		t.Fatalf("manifest %+v check %+v", m, m.Check)
	}
	if _, err := os.Stat(filepath.Join(r.farmDir(), "plan.yaml")); err != nil {
		t.Fatal(err)
	}
}

func TestRunFarm_CheckFailsExit6(t *testing.T) {
	r := newRunRig(t, "false", twoParts(), twoWriters)
	code, reason := r.run(context.Background())
	if code != core.ExitUnmet || reason != "check_failed" {
		t.Fatalf("exit %d %s", code, reason)
	}
	if ev := r.last(EvCheckRan); ev["exit_code"] != float64(1) {
		t.Fatalf("check_ran %v", ev)
	}
}

func TestRunFarm_DroppedPartExit6(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return twoWriters(ws)
	})
	code, reason := r.run(context.Background())
	if code != core.ExitUnmet || reason != "part_dropped" {
		t.Fatalf("exit %d %s", code, reason)
	}
	ev := r.last(EvIntegrateStarted)
	if fmt.Sprint(ev["merge"]) != "[b]" {
		t.Fatalf("integrate_started %v", ev)
	}
	br := branchPrefix + r.farmID() + "/integrate"
	if _, err := r.git("cat-file", "-e", br+":b/x.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestRunFarm_HashMismatchExit2(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	f, err := os.OpenFile(r.planPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(f, "# edited after approval")
	f.Close()
	code, reason, err := RunFarm(context.Background(), r.opts)
	if code != core.ExitUsage || err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("exit %d %s %v", code, reason, err)
	}
	if _, err := os.Stat(r.wtDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree dir created: %v", err)
	}
	if dirs, _ := filepath.Glob(filepath.Join(r.home, "farms", "*")); len(dirs) != 0 {
		t.Fatalf("farm dir created: %v", dirs)
	}
}

func TestRunFarm_UnapprovedPlanExit2(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	head, _ := r.git("rev-parse", "HEAD")
	r.writePlan(Plan{Task: "t", Base: head, Parts: twoParts()}, false)
	code, _, err := RunFarm(context.Background(), r.opts)
	if code != core.ExitUsage || err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("exit %d %v", code, err)
	}
	if len(r.sessions) != 0 {
		t.Fatal("sessions started")
	}
}

func TestRunFarm_RefusesBeforeWorktrees(t *testing.T) {
	head := func(r *runRig) string { h, _ := r.git("rev-parse", "HEAD"); return h }
	cases := map[string]func(r *runRig){
		"base not ancestor": func(r *runRig) {
			mustGit(t, NewGit(), r.main, "checkout", "-q", "-b", "side")
			writeFile(t, filepath.Join(r.main, "s.txt"), "s\n")
			mustGit(t, NewGit(), r.main, "add", "-A")
			mustGit(t, NewGit(), r.main, "commit", "-q", "-m", "side")
			side := head(r)
			mustGit(t, NewGit(), r.main, "checkout", "-q", "main")
			r.writePlan(Plan{Task: "t", Base: side, Parts: twoParts()}, true)
		},
		"unknown role": func(r *runRig) {
			r.writePlan(Plan{Task: "t", Base: head(r), Parts: []Part{{ID: "a", Role: "wizard", Goal: "g", Owns: []string{"a/**"}}}}, true)
		},
		"dirty tree": func(r *runRig) { writeFile(t, filepath.Join(r.main, "junk.txt"), "x") },
		"no check":   func(r *runRig) { r.opts.Cfg.Check = "" },
		"proposed roles": func(r *runRig) {
			raw := string(planYAML(t, Plan{Task: "t", Base: head(r), Parts: twoParts()})) + "proposed_roles:\n  docs:\n    tools: [read]\n"
			_ = os.WriteFile(r.planPath, Approve([]byte(raw), "owner", time.Now()), 0o644)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := newRunRig(t, "true", twoParts(), twoWriters)
			mutate(r)
			code, _, err := RunFarm(context.Background(), r.opts)
			if code != core.ExitUsage || err == nil {
				t.Fatalf("exit %d %v", code, err)
			}
			if _, err := os.Stat(r.wtDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("worktree dir created: %v", err)
			}
		})
	}
}

func TestRunFarm_ConflictGoesToIntegrator(t *testing.T) {
	parts := []Part{coder("a", "a.txt"), coder("b", "a.txt")}
	var intMsg string
	r := newRunRig(t, "grep -q AB a.txt", parts, func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case "a", "b":
			return writes(map[string]string{"a.txt": strings.ToUpper(ws.Part.ID) + "\n"}, "done")
		}
		br := branchPrefix + ws.Meta["farm_id"].(string) + "/b"
		merge, _ := json.Marshal(map[string]string{"command": "git merge --no-ff --no-verify --no-edit " + br})
		// Resolve by overwriting the conflicted file: a plain write works on
		// every OS (cmd /d /s /c has no printf and no `;` separator).
		write, _ := json.Marshal(map[string]string{"path": "a.txt", "content": "AB\n"})
		commit, _ := json.Marshal(map[string]string{"command": "git add a.txt && git commit --no-verify -q -m resolve"})
		intMsg = ws.System
		return []fake.Turn{call("shell", string(merge)), call("write", string(write)),
			call("shell", string(commit)), final("merged b, fixed a.txt")}
	})
	r.opts.Cfg.Split.DisjointFiles = false
	code, reason := r.run(context.Background())
	if code != 0 {
		ev := r.events("")
		t.Fatalf("exit %d %s; events %v", code, reason, ev)
	}
	if !strings.Contains(intMsg, "integrator") {
		t.Fatalf("integrator system prompt %q", intMsg)
	}
	br := branchPrefix + r.farmID() + "/integrate"
	if got, _ := r.git("show", br+":a.txt"); got != "AB" {
		t.Fatalf("a.txt on integrate = %q", got)
	}
	ended := r.last(EvIntegrateEnded)
	if ended["status"] != StatusDone {
		t.Fatalf("integrate_ended %v", ended)
	}
	if fmt.Sprint(ended["conflicts"]) != "[b]" {
		t.Fatalf("integrate_ended conflicts %v", ended["conflicts"])
	}
}

func TestRunFarm_IntegratorFirstMessageHasBoard(t *testing.T) {
	parts := []Part{coder("a", "a.txt"), coder("b", "a.txt")}
	var first string
	r := newRunRig(t, "true", parts, func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case "a":
			return []fake.Turn{
				call("board_post", `{"topic":"interface","fields":{"name":"Store","shape":"func Get() string"}}`),
				writeCall("a.txt", "A\n"), final("done")}
		case "b":
			return writes(map[string]string{"a.txt": "B\n"}, "done")
		}
		return []fake.Turn{final("gave up")}
	})
	r.opts.Cfg.Split.DisjointFiles = false
	r.opts.NewSession = wrapFirst(r.opts.NewSession, &first)
	code, reason := r.run(context.Background())
	if code != core.ExitUnmet || reason != "part_failed" {
		t.Fatalf("exit %d %s", code, reason)
	}
	for _, want := range []string{"jas/farm/", "/b", "a.txt", "Store", BoardLabel} {
		if !strings.Contains(first, want) {
			t.Fatalf("integrator first message lacks %q:\n%s", want, first)
		}
	}
	if ev := r.last(EvIntegrateEnded); ev["status"] != StatusFailed {
		t.Fatalf("integrate_ended %v", ev)
	}
	if len(r.events(EvCheckRan)) != 0 {
		t.Fatal("check ran after a failed integration")
	}
}

// wrapFirst records the integrator's first user message.
func wrapFirst(next func(WorkerSession) (*core.Session, error), first *string) func(WorkerSession) (*core.Session, error) {
	return func(ws WorkerSession) (*core.Session, error) {
		if ws.Part.ID == integratePart {
			inner := ws.Log
			ws.Log = msgLog{EventLog: inner, first: first}
		}
		return next(ws)
	}
}

type msgLog struct {
	core.EventLog
	first *string
}

func (l msgLog) Append(typ string, data any) error {
	if typ == core.EvUserMessage && *l.first == "" {
		b, _ := json.Marshal(data)
		*l.first = string(b)
	}
	return l.EventLog.Append(typ, data)
}

func (l msgLog) AppendID(typ string, data any) (string, error) {
	if typ == core.EvUserMessage && *l.first == "" {
		b, _ := json.Marshal(data)
		*l.first = string(b)
	}
	return l.EventLog.AppendID(typ, data)
}

func TestRunFarm_JSONOnlyFarmEvents(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	r.opts.JSON = true
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	lines := strings.Split(strings.TrimSpace(r.stdout.String()), "\n")
	if len(lines) < 4 {
		t.Fatalf("stdout %q", r.stdout.String())
	}
	session := map[string]bool{core.EvSessionStarted: true, core.EvAssistantMessage: true, core.EvToolStarted: true,
		core.EvToolResult: true, core.EvSessionEnded: true, core.EvUsage: true, core.EvUserMessage: true}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		if m["farm_id"] != r.farmID() {
			t.Fatalf("line without farm_id: %q", l)
		}
		if session[m["type"].(string)] {
			t.Fatalf("session event on stdout: %q", l)
		}
	}
}

func TestRunFarm_MergeAnywayRequiresPhrase(t *testing.T) {
	r := newRunRig(t, "false", twoParts(), twoWriters)
	before, _ := r.git("rev-parse", "HEAD")
	r.opts.Ask = scripted("m", "yes")
	if code, _ := r.run(context.Background()); code != core.ExitUnmet {
		t.Fatalf("exit %d", code)
	}
	if after, _ := r.git("rev-parse", "HEAD"); after != before {
		t.Fatal("merged without the phrase")
	}
	if ev := r.last(EvOwnerDecision); ev["choice"] != "keep" || ev["by"] != "owner" {
		t.Fatalf("owner_decision %v", ev)
	}

	r.home = t.TempDir() // farm ids made in the same second do not sort by time
	r.opts.Home = r.home
	r.opts.Ask = scripted("m", "merge anyway")
	if code, _ := r.run(context.Background()); code != core.ExitUnmet {
		t.Fatalf("exit %d", code)
	}
	msg, _ := r.git("log", "-1", "--format=%B")
	if !strings.Contains(msg, "Jas-Farm-Status: unmet (check_failed)") {
		t.Fatalf("merge commit message %q", msg)
	}
	author, _ := r.git("log", "-1", "--format=%an <%ae>")
	if author != "Owner <owner@example.com>" {
		t.Fatalf("owner merge author %q", author)
	}
	if _, err := os.Stat(filepath.Join(r.main, "a", "x.txt")); err != nil {
		t.Fatal(err)
	}
	if ev := r.last(EvOwnerDecision); ev["choice"] != "merge" {
		t.Fatalf("owner_decision %v", ev)
	}
}

func TestRunFarm_DiscardDeletesBranches(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	r.opts.Ask = scripted("d")
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if out, _ := r.git("branch", "--list", "jas/farm/*"); out != "" {
		t.Fatalf("branches left: %q", out)
	}
}

// scripted answers each Ask with the next answer, then "" (end of input).
func scripted(answers ...string) func(string, []string) (string, bool) {
	var mu sync.Mutex
	return func(string, []string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		if len(answers) == 0 {
			return "", true
		}
		a := answers[0]
		answers = answers[1:]
		return a, true
	}
}

func TestRunFarm_FarmTimeoutExit7(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(WorkerSession) []fake.Turn {
		return []fake.Turn{{Block: true}}
	})
	r.opts.Cfg.Budget.FarmTimeout = 100 * time.Millisecond
	code, reason := r.run(context.Background())
	if code != core.ExitBudget || reason != "timeout" {
		t.Fatalf("exit %d %s", code, reason)
	}
	if ev := r.last(EvFarmEnded); ev["reason"] != "timeout" {
		t.Fatalf("farm_ended %v", ev)
	}
	if len(r.events(EvIntegrateStarted)) != 0 {
		t.Fatal("integrated after a budget stop")
	}
}

func TestRunFarm_InterruptedExit130(t *testing.T) {
	started := make(chan struct{}, 2)
	r := newRunRig(t, "true", twoParts(), func(WorkerSession) []fake.Turn {
		return []fake.Turn{{Block: true, OnEmit: func() { started <- struct{}{} }}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	hard, hardCancel := context.WithCancel(context.Background())
	defer hardCancel()
	r.opts.Hard = hard
	go func() {
		<-started
		cancel()
		time.Sleep(50 * time.Millisecond)
		hardCancel()
	}()
	code, reason := r.run(ctx)
	if code != core.ExitInterrupted || reason != "interrupted" {
		t.Fatalf("exit %d %s", code, reason)
	}
	if ev := r.last(EvFarmEnded); ev["status"] != "interrupted" {
		t.Fatalf("farm_ended %v", ev)
	}
	if _, err := os.Stat(filepath.Join(r.wtDir, r.farmID(), "part-a")); err != nil {
		t.Fatalf("worktree removed after interrupt: %v", err)
	}
}

func TestRunFarm_IntegratorFarmPoolExit7(t *testing.T) {
	parts := []Part{coder("a", "a.txt"), coder("b", "a.txt")}
	r := newRunRig(t, "true", parts, func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case integratePart:
			return []fake.Turn{final("never reached")}
		case "b": // b's last reply settles past the farm pool
			last := final("done")
			last.Usage.PromptTokens = 60000
			return []fake.Turn{writeCall("a.txt", "B\n"), last}
		}
		return writes(map[string]string{"a.txt": "A\n"}, "done")
	})
	r.opts.Cfg.Split.DisjointFiles = false
	r.opts.Cfg.Split.MaxWorkers = 1
	r.opts.Cfg.Budget.FarmTokens = 50000
	code, reason := r.run(context.Background())
	if code != core.ExitBudget || reason != "tokens" {
		t.Fatalf("exit %d %s; integrate_ended %v", code, reason, r.events(EvIntegrateEnded))
	}
	if ev := r.last(EvIntegrateEnded); ev["reason"] != "tokens" {
		t.Fatalf("integrate_ended %v", ev)
	}
}

// brokenCheck fails while any file in the worktree contains BROKEN; it works
// under sh and cmd alike.
const brokenCheck = "git grep -q --untracked BROKEN && exit 1 || exit 0"

func TestRunFarm_PartCheckFailureGetsOneFixRound(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "BROKEN\n"), final("a done"), writeCall("a/x.txt", "ok\n"), final("a fixed")}
	})
	r.opts.Cfg.PartCheck = brokenCheck
	code, reason := r.run(context.Background())
	if code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(""))
	}
	var codes []any
	for _, ev := range r.events(EvPartCheck) {
		if ev["part"] == "a" {
			codes = append(codes, ev["exit_code"])
		}
	}
	if fmt.Sprint(codes) != "[1 0]" {
		t.Fatalf("part a checks %v", codes)
	}
	br := branchPrefix + r.farmID() + "/a"
	if got, _ := r.git("show", br+":a/x.txt"); got != "ok" {
		t.Fatalf("a/x.txt on part branch = %q", got)
	}
}

func TestRunFarm_PartCheckStillFailingFailsPart(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "BROKEN\n"), final("a done"), final("tried")}
	})
	r.opts.Cfg.PartCheck = brokenCheck
	code, _ := r.run(context.Background())
	if code != core.ExitUnmet {
		t.Fatalf("exit %d", code)
	}
	ended := r.events(EvPartEnded)
	var got string
	for _, ev := range ended {
		if ev["part"] == "a" {
			got = fmt.Sprint(ev["status"], " ", ev["reason"])
		}
	}
	if got != "failed check" {
		t.Fatalf("part a ended %q; events %v", got, ended)
	}
}

// A worker that answers the fix round with BLOCKED: is done; the merged
// check is the verdict.
func TestRunFarm_PartCheckBlockedDefersToMergedCheck(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "BROKEN\n"), final("a done"), final("BLOCKED: the check needs part b")}
	})
	r.opts.Cfg.PartCheck = brokenCheck
	code, reason := r.run(context.Background())
	if code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(""))
	}
	ev := r.last(EvPartCheckDeferred)
	if ev["part"] != "a" || ev["reason"] != "the check needs part b" {
		t.Fatalf("part_check_deferred %v", ev)
	}
}

// A part_check that writes outside the part's owns (npm install --prefix
// other/ makes other/package-lock.json) is undone and not blamed on the
// worker, even when the worker reruns the check itself.
func TestRunFarm_PartCheckArtifactsOutsideOwnsNotBlamed(t *testing.T) {
	const check = "mkdir -p b && echo lock > b/lock.json && exit 1"
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return append(twoWriters(ws), final("BLOCKED: needs a"))
		}
		rerun, _ := json.Marshal(map[string]string{"command": check + " || true"})
		return []fake.Turn{writeCall("a/x.txt", "a\n"), final("a done"),
			call("shell", string(rerun)), final("It needs b.\nBLOCKED: b/ is another part's")}
	})
	r.opts.Cfg.PartCheck = check
	code, reason := r.run(context.Background())
	if code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(EvPartEnded))
	}
	br := branchPrefix + r.farmID() + "/a"
	if out, _ := r.git("ls-tree", "-r", "--name-only", br); strings.Contains(out, "b/lock.json") {
		t.Fatalf("check artifact committed on a's branch:\n%s", out)
	}
}

// The worker running the check before its first answer leaves the same
// artifact; the supervisor's own run rewrites it, so it is still not blamed.
func TestRunFarm_WorkerRanCheckFirstArtifactNotBlamed(t *testing.T) {
	const check = "mkdir -p b && echo lock > b/lock.json && exit 1"
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return append(twoWriters(ws), final("BLOCKED: needs a"))
		}
		run, _ := json.Marshal(map[string]string{"command": check + " || true"})
		return []fake.Turn{writeCall("a/x.txt", "a\n"), call("shell", string(run)), final("a done"),
			final("BLOCKED: b/ is another part's")}
	})
	r.opts.Cfg.PartCheck = check
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(EvPartEnded))
	}
}

// A write outside owns and a failing cross-part check in the same answer go
// back in one message, so the worker can undo the write and answer BLOCKED:
// in its one fix round.
func TestRunFarm_WriteAndCheckFailureInOneFixRound(t *testing.T) {
	const check = "test -d a && test -d b"
	var fix string
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return append(twoWriters(ws), final("BLOCKED: needs a"))
		}
		dirty, _ := json.Marshal(map[string]string{"command": "echo x > root.lock"})
		undo, _ := json.Marshal(map[string]string{"command": "rm root.lock"})
		return []fake.Turn{writeCall("a/x.txt", "a\n"), call("shell", string(dirty)), final("a done"),
			call("shell", string(undo)), final("BLOCKED: b/ is another part's")}
	})
	r.opts.Cfg.PartCheck = check
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(EvPartEnded))
	}
	raw, err := os.ReadFile(filepath.Join(r.farmDir(), "parts", "a", "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(raw), "\n") {
		var ev struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(l), &ev) == nil && ev.Type == "user_message" && len(ev.Content) > 0 &&
			strings.Contains(ev.Content[0].Text, "root.lock") {
			fix = ev.Content[0].Text
		}
	}
	for _, want := range []string{"root.lock", "test -d a && test -d b", blockedPrefix} {
		if !strings.Contains(fix, want) {
			t.Fatalf("fix message lacks %q:\n%s\nchecks %v deferred %v", want, fix, r.events(EvPartCheck), r.events(EvPartCheckDeferred))
		}
	}
}

// BLOCKED: only counts as the answer to a failed check, not as a first answer.
func TestRunFarm_BlockedFirstAnswerStillChecked(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "BROKEN\n"), final("BLOCKED: skip it"), final("tried")}
	})
	r.opts.Cfg.PartCheck = brokenCheck
	if code, _ := r.run(context.Background()); code != core.ExitUnmet {
		t.Fatalf("exit %d", code)
	}
	if evs := r.events(EvPartCheckDeferred); len(evs) != 0 {
		t.Fatalf("deferred %v", evs)
	}
}

func TestRunFarm_CleanMergeFailingCheckGoesToIntegrator(t *testing.T) {
	var intMsg string
	r := newRunRig(t, brokenCheck, twoParts(), func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case "a":
			return writes(map[string]string{"a/x.txt": "BROKEN\n"}, "a done")
		case "b":
			return twoWriters(ws)
		}
		intMsg = ws.System
		commit, _ := json.Marshal(map[string]string{"command": "git add -A && git commit --no-verify -q -m fix"})
		return []fake.Turn{writeCall("a/x.txt", "ok\n"), call("shell", string(commit)), final("fixed a/x.txt")}
	})
	code, reason := r.run(context.Background())
	if code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(""))
	}
	if intMsg == "" {
		t.Fatal("integrator never ran")
	}
	if ev := r.last(EvIntegrateCheck); ev["exit_code"] != float64(1) {
		t.Fatalf("integrate_check %v", ev)
	}
}

func TestRunFarm_PrintsProgress(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	out := r.stdout.String()
	for _, want := range []string{
		"[a] started (coder)",
		"[a] → write a/x.txt",
		"[b] done",
		"integrate: merging a, b",
		"check: passed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("progress lacks %q:\n%s", want, out)
		}
	}
}

func TestRunFarm_CleanMergeRunsCheckOnce(t *testing.T) {
	count := filepath.Join(t.TempDir(), "count")
	r := newRunRig(t, "echo x>>"+count, twoParts(), twoWriters)
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	b, err := os.ReadFile(count)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("check ran %d times on a clean merge", n)
	}
	if ev := r.last(EvCheckRan); ev["exit_code"] != float64(0) {
		t.Fatalf("check_ran %v", ev)
	}
}

// recWatcher records farm and worker events; safe for concurrent use.
type recWatcher struct {
	mu     sync.Mutex
	farm   []string // "typ part"
	worker []string // "part typ"
}

func (w *recWatcher) FarmEvent(typ string, m map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.farm = append(w.farm, fmt.Sprint(typ, " ", m["part"]))
}

func (w *recWatcher) WorkerEvent(part, typ string, _ map[string]any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.worker = append(w.worker, part+" "+typ)
}

func (w *recWatcher) SteerUndelivered(string, []string) {}

func TestRunFarm_WatcherSeesFarmAndWorkerEvents(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	w := &recWatcher{}
	r.opts.Watch = w
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	if !slices.Contains(w.farm, EvPartStarted+" a") || !slices.Contains(w.worker, "a "+core.EvToolStarted) {
		t.Fatalf("farm %v worker %v", w.farm, w.worker)
	}
	if strings.Contains(r.stdout.String(), "[a] started") {
		t.Fatalf("progress printed with a watcher set:\n%s", r.stdout.String())
	}
}

// recUI records text and event types for one part.
type recUI struct {
	mu     sync.Mutex
	text   strings.Builder
	events []string
}

func (u *recUI) Text(d string)    { u.mu.Lock(); u.text.WriteString(d); u.mu.Unlock() }
func (u *recUI) Reasoning(string) {}
func (u *recUI) Event(typ string, _ any) {
	u.mu.Lock()
	u.events = append(u.events, typ)
	u.mu.Unlock()
}

func TestRunFarm_WorkerUIPerPart(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	var mu sync.Mutex
	uis := map[string]*recUI{}
	r.opts.WorkerUI = func(part string) core.UI {
		mu.Lock()
		defer mu.Unlock()
		u := &recUI{}
		uis[part] = u
		return u
	}
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	for _, p := range []string{"a", "b"} {
		u := uis[p]
		if u == nil || !strings.Contains(u.text.String(), p+" done") || !slices.Contains(u.events, core.EvToolStarted) {
			t.Fatalf("part %s ui %+v", p, u)
		}
	}
}

func TestRunFarm_GatesHookPerPart(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	var mu sync.Mutex
	var got []string
	r.opts.Gates = func(part, approve string) (core.Gate, error) {
		mu.Lock()
		got = append(got, part+":"+approve)
		mu.Unlock()
		return &core.Approver{Level: core.Never}, nil
	}
	if code, reason := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d %s", code, reason)
	}
	sortedEq(t, got, "a:", "b:")
}

// withGenerated sets the run's generated list as if farm.yaml listed it.
func withGenerated(cfg *Config, gen ...Generated) {
	cfg.Generated, cfg.generatedSet = gen, true
}

func shellTurn(cmd string) fake.Turn {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return call("shell", string(b))
}

func (r *runRig) partEvents(typ, part string) []map[string]any {
	var out []map[string]any
	for _, ev := range r.events(typ) {
		if ev["part"] == part {
			out = append(out, ev)
		}
	}
	return out
}

// A generated file the worker writes outside its owns is undone without a
// fix round: the script has no turn left for one.
func TestRunFarm_GeneratedWriteUndoneNoFixRound(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "a\n"), shellTurn("echo x > root.lock"), final("a done")}
	})
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "true"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; ended %v", code, reason, r.events(EvPartEnded))
	}
	evs := r.partEvents(EvGeneratedRestored, "a")
	if len(evs) != 1 || fmt.Sprint(evs[0]["paths"]) != "[root.lock]" {
		t.Fatalf("generated_restored %v", r.events(EvGeneratedRestored))
	}
	if _, err := r.git("cat-file", "-e", branchPrefix+r.farmID()+"/a:root.lock"); err == nil {
		t.Fatal("root.lock committed on a's branch")
	}
}

func TestRunFarm_GeneratedRestoredOnBlocked(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return append(twoWriters(ws), final("BLOCKED: needs a"))
		}
		return []fake.Turn{writeCall("a/x.txt", "a\n"), final("a done"),
			shellTurn("echo x > root.lock"), final("BLOCKED: needs b")}
	})
	r.opts.Cfg.PartCheck = "test -d a && test -d b"
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "true"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; ended %v", code, reason, r.events(EvPartEnded))
	}
	if len(r.partEvents(EvPartCheckDeferred, "a")) != 1 || len(r.partEvents(EvGeneratedRestored, "a")) != 1 {
		t.Fatalf("deferred %v restored %v", r.events(EvPartCheckDeferred), r.events(EvGeneratedRestored))
	}
	if _, err := r.git("cat-file", "-e", branchPrefix+r.farmID()+"/a:root.lock"); err == nil {
		t.Fatal("root.lock committed on a's branch")
	}
}

// a.txt is tracked at base (newRepo): a deleted generated file comes back.
func TestRunFarm_GeneratedDeleteRestored(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "a\n"), shellTurn("rm a.txt"), final("a done")}
	})
	withGenerated(&r.opts.Cfg, Generated{Path: "a.txt", Regenerate: "true"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; ended %v", code, reason, r.events(EvPartEnded))
	}
	if got, _ := r.git("show", branchPrefix+r.farmID()+"/a:a.txt"); got != "one\ntwo\nthree" {
		t.Fatalf("a.txt on a's branch = %q", got)
	}
}

func TestRunFarm_GeneratedInsideOwnsIsCommitted(t *testing.T) {
	a := coder("a", "a/**")
	a.Owns = append(a.Owns, "root.lock")
	r := newRunRig(t, "true", []Part{a, coder("b", "b/**")}, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID != "a" {
			return twoWriters(ws)
		}
		return []fake.Turn{writeCall("a/x.txt", "a\n"), writeCall("root.lock", "mine\n"), final("a done")}
	})
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "true"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; ended %v", code, reason, r.events(EvPartEnded))
	}
	if evs := r.events(EvGeneratedRestored); len(evs) != 0 {
		t.Fatalf("generated_restored %v", evs)
	}
	if got, _ := r.git("show", branchPrefix+r.farmID()+"/a:root.lock"); got != "mine" {
		t.Fatalf("root.lock on a's branch = %q", got)
	}
}

// part_check's own write to a generated file is undone as the check's, not
// logged again as a generated restore.
func TestRunFarm_GeneratedWrittenByCheckNotRestoredTwice(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	r.opts.Cfg.PartCheck = "echo y > root.lock"
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "true"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; ended %v", code, reason, r.events(EvPartEnded))
	}
	if evs := r.events(EvGeneratedRestored); len(evs) != 0 {
		t.Fatalf("generated_restored %v", evs)
	}
}

// seqOf is the seq of the last event of typ.
func (r *runRig) seqOf(typ string) float64 {
	ev := r.last(typ)
	if ev == nil {
		r.t.Fatalf("no %s event", typ)
	}
	return ev["seq"].(float64)
}

func (r *runRig) integrateBranch() string { return branchPrefix + r.farmID() + "/" + integratePart }

// Both parts touch the generated a.txt (tracked at base); jas regenerates it
// once on the merged tree and commits it before integrate_check.
func TestIntegrate_RegenerateCommitsBeforeCheck(t *testing.T) {
	r := newRunRig(t, "grep -q v2 a.txt", twoParts(), func(ws WorkerSession) []fake.Turn {
		return []fake.Turn{writeCall(ws.Part.ID+"/x.txt", "x\n"), shellTurn("echo x > a.txt"), final("done")}
	})
	withGenerated(&r.opts.Cfg, Generated{Path: "a.txt", Regenerate: "echo v2 > a.txt"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; integrate %v", code, reason, r.events(EvIntegrateEnded))
	}
	evs := r.events(EvGeneratedRegenerated)
	if len(evs) != 1 || evs[0]["path"] != "a.txt" || evs[0]["changed"] != true || num(evs[0]["exit_code"]) != 0 {
		t.Fatalf("generated_regenerated %v", evs)
	}
	if r.seqOf(EvGeneratedRegenerated) > r.seqOf(EvIntegrateCheck) {
		t.Fatal("regenerated after integrate_check")
	}
	if log, _ := r.git("log", "--format=%s", r.integrateBranch()); !strings.Contains(log, "regenerate a.txt") {
		t.Fatalf("integrate log:\n%s", log)
	}
}

func TestIntegrate_RegenerateUnchangedNoCommit(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	withGenerated(&r.opts.Cfg, Generated{Path: "a.txt", Regenerate: "true"})
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ev := r.last(EvGeneratedRegenerated); ev["changed"] != false {
		t.Fatalf("generated_regenerated %v", ev)
	}
	if log, _ := r.git("log", "--format=%s", r.integrateBranch()); strings.Contains(log, "regenerate") {
		t.Fatalf("integrate log:\n%s", log)
	}
}

// A missing tool (exit 127) is logged; the check decides.
func TestIntegrate_RegenerateFailureNotFatal(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	withGenerated(&r.opts.Cfg, Generated{Path: "a.txt", Regenerate: "exit 127"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s", code, reason)
	}
	if ev := r.last(EvGeneratedRegenerated); num(ev["exit_code"]) != 127 {
		t.Fatalf("generated_regenerated %v", ev)
	}
}

// On a clean merge whose check fails, the integrator hears about the failed
// regenerate too.
func TestIntegrate_RegenerateFailureReachesIntegrator(t *testing.T) {
	r := newRunRig(t, "false", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == integratePart {
			return []fake.Turn{final("tried")}
		}
		return twoWriters(ws)
	})
	withGenerated(&r.opts.Cfg, Generated{Path: "a.txt", Regenerate: "echo nope; exit 3"})
	r.run(context.Background())
	raw, err := os.ReadFile(filepath.Join(r.farmDir(), integratePart, eventsFile))
	if err != nil {
		t.Fatal(err)
	}
	var first string
	for _, l := range strings.Split(string(raw), "\n") {
		var ev struct {
			Type    string `json:"type"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal([]byte(l), &ev) == nil && ev.Type == "user_message" && len(ev.Content) > 0 {
			first = ev.Content[0].Text
			break
		}
	}
	for _, want := range []string{"a.txt", "exit 3", "nope"} {
		if !strings.Contains(first, want) {
			t.Fatalf("integrator message lacks %q:\n%s", want, first)
		}
	}
}

// With conflicts the integrator merges first; regenerate runs on its result.
func TestIntegrate_RegenerateAfterIntegratorOnConflict(t *testing.T) {
	parts := []Part{coder("a", "a.txt"), coder("b", "a.txt")}
	r := newRunRig(t, "grep -q AB a.txt && grep -q v2 root.lock", parts, func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case "a", "b":
			return writes(map[string]string{"a.txt": strings.ToUpper(ws.Part.ID) + "\n"}, "done")
		}
		br := branchPrefix + ws.Meta["farm_id"].(string) + "/b"
		return []fake.Turn{shellTurn("git merge --no-ff --no-verify --no-edit " + br), writeCall("a.txt", "AB\n"),
			shellTurn("git add a.txt && git commit --no-verify -q -m resolve"), final("merged b, fixed a.txt")}
	})
	r.opts.Cfg.Split.DisjointFiles = false
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "echo v2 > root.lock"})
	if code, reason := r.run(context.Background()); code != 0 || reason != "ok" {
		t.Fatalf("exit %d %s; integrate %v", code, reason, r.events(EvIntegrateEnded))
	}
	if r.seqOf(EvGeneratedRegenerated) > r.seqOf(EvCheckRan) {
		t.Fatal("regenerated after the verdict")
	}
	if head, _ := r.git("log", "-1", "--format=%s", r.integrateBranch()); !strings.Contains(head, "regenerate root.lock") {
		t.Fatalf("integrate head %q, want the regenerate commit on top of the integrator's", head)
	}
}

// regenerate commits only its path; other tracked files it changed go back.
func TestIntegrate_RegenerateCommitsOnlyItsPath(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	withGenerated(&r.opts.Cfg, Generated{Path: "root.lock", Regenerate: "echo v2 > root.lock && echo junk >> .gitignore"})
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got, _ := r.git("show", r.integrateBranch()+":root.lock"); got != "v2" {
		t.Fatalf("root.lock = %q", got)
	}
	if got, _ := r.git("show", r.integrateBranch()+":.gitignore"); strings.Contains(got, "junk") {
		t.Fatalf(".gitignore = %q", got)
	}
}
