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
	assert.Eq(t, d, core.Granted)
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
	assert.Eq(t, d, core.Granted)
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
	time.Sleep(20 * time.Millisecond)
	assert.Eq(t, len(msgs), 0) // the second waits for the first prompt
	cancel()                   // and gives up on ctx without prompting
	r := <-second
	assert.Eq(t, r.d, core.Denied)
	assert.Eq(t, errors.Is(r.err, context.Canceled), true)
	a.reply <- "y"
	assert.Eq(t, (<-first).d, core.Granted)
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
	assert.Eq(t, d, core.Granted)
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
