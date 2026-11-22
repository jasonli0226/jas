package tui

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/tools"
)

func toolNamed(t *testing.T, name string) core.Tool {
	t.Helper()
	for _, tl := range append(tools.ReadOnly(), tools.Mutating()...) {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return nil
}

func shellArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

// newRecordingGate returns a gate whose posted messages land in the channel.
func newRecordingGate(level core.Level) (*Gate, chan tea.Msg) {
	msgs := make(chan tea.Msg, 16)
	return NewGate(level, false, nil, func(m tea.Msg) { msgs <- m }), msgs
}

type allowResult struct {
	d   core.Decision
	err error
}

func allowAsync(ctx context.Context, g *Gate, tl core.Tool, args json.RawMessage) chan allowResult {
	ch := make(chan allowResult, 1)
	go func() {
		d, err := g.Allow(ctx, tl, args, nil)
		ch <- allowResult{d, err}
	}()
	return ch
}

func nextApproval(t *testing.T, msgs chan tea.Msg) approvalMsg {
	t.Helper()
	select {
	case m := <-msgs:
		a, ok := m.(approvalMsg)
		if !ok {
			t.Fatalf("got %T, want approvalMsg", m)
		}
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("no approval prompt")
	}
	return approvalMsg{}
}

func TestGate_AlwaysStopsRepeatPrompts(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	shell := toolNamed(t, "shell")
	res := allowAsync(context.Background(), g, shell, shellArgs("go test ./..."))
	a := nextApproval(t, msgs)
	assert.Eq(t, a.prompt, "approve shell: go test ./...? [y/N/always]")
	a.reply <- " Always "
	r := <-res
	assert.NoErr(t, r.err)
	assert.Eq(t, r.d, core.Granted)
	assert.Eq(t, (<-msgs).(noticeMsg).s, "always: shell: go test (covers any arguments)")
	eqStrings(t, g.Grants().List(), []string{"shell: go test"})

	d, err := g.Allow(context.Background(), shell, shellArgs("go test ./core"), nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.AutoGranted) // the standing grant answers; nobody is asked
	assert.Eq(t, len(msgs), 0)
}

func TestGate_AlwaysOnNonSimpleCommandAllowsOnce(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	shell := toolNamed(t, "shell")
	res := allowAsync(context.Background(), g, shell, shellArgs("go test | tee x"))
	nextApproval(t, msgs).reply <- "always"
	assert.Eq(t, (<-res).d, core.Granted)
	assert.Eq(t, (<-msgs).(noticeMsg).s, "this command can't get an always grant; allowed once")
	assert.Eq(t, len(g.Grants().List()), 0)
}

func TestGate_Replies(t *testing.T) {
	cases := map[string]core.Decision{
		"y": core.Granted, "YES": core.Granted, "n": core.Denied, "no": core.Denied,
		"": core.Denied, "  ": core.Denied, "no, use make test": core.Denied, "yep": core.Denied,
	}
	shell := toolNamed(t, "shell")
	for reply, want := range cases {
		g, msgs := newRecordingGate(core.ShellOnly)
		res := allowAsync(context.Background(), g, shell, shellArgs("go test"))
		nextApproval(t, msgs).reply <- reply
		r := <-res
		assert.NoErr(t, r.err)
		assert.Eq(t, r.d, want)
		assert.Eq(t, len(g.Grants().List()), 0)
	}
}

func TestGate_UngatedNeverPrompts(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	d, err := g.Allow(context.Background(), toolNamed(t, "write"), json.RawMessage(`{"path":"a","content":"x"}`), nil)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.AutoGranted)
	assert.Eq(t, len(msgs), 0)
}

func TestGate_CtxCancelUnblocks(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	ctx, cancel := context.WithCancel(context.Background())
	res := allowAsync(ctx, g, toolNamed(t, "shell"), shellArgs("rm -rf x"))
	nextApproval(t, msgs)
	cancel()
	r := <-res
	assert.Eq(t, r.d, core.Denied)
	assert.Eq(t, errors.Is(r.err, context.Canceled), true)
}

func TestGate_OnePromptAtATime(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	shell := toolNamed(t, "shell")
	first := allowAsync(context.Background(), g, shell, shellArgs("go test ./a"))
	a := nextApproval(t, msgs)
	ctx, cancel := context.WithCancel(context.Background())
	second := allowAsync(ctx, g, shell, shellArgs("make"))
	cancel() // the second, waiting behind the first prompt, gives up on ctx
	r := <-second
	assert.Eq(t, r.d, core.Denied)
	assert.Eq(t, errors.Is(r.err, context.Canceled), true)
	a.reply <- "y"
	assert.Eq(t, (<-first).d, core.Granted)
	assert.Eq(t, len(msgs), 0) // the second never prompted
}

func TestGate_AlwaysAnswersACallWaitingBehindIt(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	shell := toolNamed(t, "shell")
	first := allowAsync(context.Background(), g, shell, shellArgs("go test ./a"))
	a := nextApproval(t, msgs)
	second := allowAsync(context.Background(), g, shell, shellArgs("go test ./b"))
	a.reply <- "always"
	assert.Eq(t, (<-first).d, core.Granted)
	assert.Eq(t, (<-second).d, core.AutoGranted) // covered by the new grant: no second prompt
	<-msgs                                       // the always notice
	assert.Eq(t, len(msgs), 0)
}

func TestGate_ResetClearsGrantsAndSetsLevel(t *testing.T) {
	g, msgs := newRecordingGate(core.ShellOnly)
	res := allowAsync(context.Background(), g, toolNamed(t, "shell"), shellArgs("go test"))
	nextApproval(t, msgs).reply <- "always"
	<-res
	assert.Eq(t, len(g.Grants().List()), 1)
	g.Reset(core.AllMutating)
	assert.Eq(t, g.Level(), core.AllMutating)
	assert.Eq(t, len(g.Grants().List()), 0)
	g.SetLevel(core.Never)
	assert.Eq(t, g.Level(), core.Never)
}

func TestGate_ApprovalCarriesDiff(t *testing.T) {
	calls := 0
	preview := func() *core.Diff {
		calls++
		return core.ComputeDiff(core.Change{Path: "a", Old: "x\n", New: "y\n"}, nil)
	}
	edit, args := toolNamed(t, "edit"), json.RawMessage(`{"path":"a"}`)

	g, msgs := newRecordingGate(core.Never)
	d, err := g.Allow(context.Background(), edit, args, preview)
	assert.NoErr(t, err)
	assert.Eq(t, d, core.AutoGranted)
	assert.Eq(t, calls, 0)
	assert.Eq(t, len(msgs), 0)

	g, msgs = newRecordingGate(core.AllMutating)
	ch := make(chan allowResult, 1)
	go func() {
		d, err := g.Allow(context.Background(), edit, args, preview)
		ch <- allowResult{d, err}
	}()
	a := nextApproval(t, msgs)
	if a.diff == nil {
		t.Fatal("approvalMsg has no diff")
	}
	assert.Eq(t, a.header, "edit a")
	a.reply <- "n"
	assert.Eq(t, (<-ch).d, core.Denied)
	assert.Eq(t, calls, 1)
}

func TestGate_MustAskIgnoresGrants(t *testing.T) {
	args := json.RawMessage(`{"path":"~/.jas/profiles/ds/skills/x/SKILL.md","content":"x"}`)
	write := toolNamed(t, "write")
	g, msgs := newRecordingGate(core.AllMutating)
	_, ok := g.Grants().Add("write", args)
	assert.Eq(t, ok, true)                // a standing write grant must not cover it
	for _, gate := range []core.Gate{g} { // a farm part's grants live in its broker (farm tests)
		res := make(chan allowResult, 1)
		go func() {
			d, err := gate.Allow(context.Background(), core.AskAlways(write), args, nil)
			res <- allowResult{d, err}
		}()
		nextApproval(t, msgs).reply <- "always"
		r := <-res
		assert.NoErr(t, r.err)
		assert.Eq(t, r.d, core.Granted)
		assert.Eq(t, (<-msgs).(noticeMsg).s, mustAskNotice)
	}
	eqStrings(t, g.Grants().List(), []string{"write"})
}

func TestGate_AskFarmWithdrawn(t *testing.T) {
	c, _ := farmWithAgents(t, Options{})
	shell := toolNamed(t, "shell")
	type res struct {
		text string
		err  error
	}
	ask := func(ctx context.Context, cmd string) chan res {
		ch := make(chan res, 1)
		go func() {
			text, err := c.m.gate.AskFarm(ctx, "a", shell, shellArgs(cmd), nil)
			ch <- res{text, err}
		}()
		return ch
	}
	ctx, cancel := context.WithCancel(context.Background())
	first := ask(ctx, "npm install zod")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[a] approve shell: npm install zod?")
	cancel()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(approvalWithdrawnMsg); return ok })
	if c.m.approval != nil {
		t.Fatalf("withdrawn prompt still open: %+v", c.m.approval)
	}
	assert.Eq(t, c.m.farm.agent("a").needsApproval, false)
	select {
	case r := <-first:
		if !errors.Is(r.err, context.Canceled) {
			t.Fatalf("first AskFarm = %+v, want context.Canceled", r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskFarm did not return on cancel")
	}
	second := ask(context.Background(), "go test ./...")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "go test ./...")
	c.submit("always")
	select {
	case r := <-second:
		assert.NoErr(t, r.err)
		assert.Eq(t, r.text, "always")
	case <-time.After(5 * time.Second):
		t.Fatal("second AskFarm got no reply")
	}
}

// A call waiting for the prompt slot that is withdrawn never shows.
func TestGate_AskFarmWithdrawnWhileQueued(t *testing.T) {
	g, msgs := newRecordingGate(core.Never)
	shell := toolNamed(t, "shell")
	first := make(chan string, 1)
	go func() {
		text, _ := g.AskFarm(context.Background(), "a", shell, shellArgs("one"), nil)
		first <- text
	}()
	open := nextApproval(t, msgs)
	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() {
		_, err := g.AskFarm(ctx, "b", shell, shellArgs("two"), nil)
		queued <- err
	}()
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued AskFarm err %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued AskFarm did not return")
	}
	open.reply <- "y"
	assert.Eq(t, <-first, "y")
	select {
	case m := <-msgs:
		t.Fatalf("the withdrawn queued call posted %T", m)
	default:
	}
}
