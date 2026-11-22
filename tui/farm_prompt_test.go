package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
)

// broker is the fake farm's approval broker at level shell, hosted by the
// chat's Host hook as a real farm's is.
func (f *fakeFarmer) broker() *farm.Broker {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.b == nil {
		var b *farm.Broker
		b = farm.NewBroker(farm.BrokerOptions{Level: core.ShellOnly,
			Host: f.hooks.Host(func(id string) (string, string, bool) { return b.Closed(id) })})
		f.b = b
	}
	return f.b
}

// farmAllow asks the farm gate for part to allow a shell call; the decision
// arrives on the returned channel.
func farmAllow(t *testing.T, f *fakeFarmer, part string) chan core.Decision {
	t.Helper()
	g, err := f.broker().Gate(part, "", nil)
	assert.NoErr(t, err)
	ch := make(chan core.Decision, 1)
	tl := toolNamed(t, "shell")
	go func() {
		d, _ := g.Allow(context.Background(), tl, shellArgs("go test ./..."), nil)
		ch <- d
	}()
	return ch
}

func (c *testChat) pumpApproval() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(approvalMsg); return ok })
}

func farmWithAgents(t *testing.T, o Options) (*testChat, *fakeFarmer) {
	t.Helper()
	b := newTestBackend(t)
	f := newFakeFarmer()
	b.farmer = f
	c := newTestChatOpts(t, b, o)
	startFarm(t, c)
	for _, p := range []string{"a", "b"} {
		f.event("part_started", map[string]any{"part": p, "role": "coder"})
	}
	c.drain()
	return c, f
}

func TestFarmApproval_TakesInputAndRestoresDraft(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.update(modKey('1', tea.ModCtrl))
	c.typeText("half")
	got := farmAllow(t, f, "b")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[b]")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Eq(t, c.m.farm.agent("b").needsApproval, true)
	c.submit("y")
	assert.Eq(t, <-got, core.Granted)
	assert.Eq(t, c.m.input.Value(), "half")
	assert.Contains(t, c.m.input.Prompt, "a ›")
	assert.Eq(t, c.m.farm.agent("b").needsApproval, false)
}

func TestFarmApproval_QueueOldestFirst(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	first := farmAllow(t, f, "a")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[a]")
	second := farmAllow(t, f, "b")
	c.submit("y")
	assert.Eq(t, <-first, core.Granted)
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[b]")
	c.submit("n")
	assert.Eq(t, <-second, core.Denied)
}

func TestFarmApproval_WhileSlashMenuOpen(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("/mo")
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	assert.Eq(t, c.m.slashMenu() == nil, true)
	c.submit("n")
	assert.Eq(t, <-got, core.Denied)
	assert.Eq(t, c.m.input.Value(), "/mo")
}

func TestFarmApproval_RestoresPlanReview(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	f.run.finish(farmResult("ok", 0)) // the first farm ends, so a new plan may start
	c.drain()
	f.run = newFakeRun()
	c.submit(`/farm "next"`)
	if c.m.picker == nil {
		t.Fatal("no review picker")
	}
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.submit("n")
	<-got
	if c.m.picker == nil || c.m.picker.title != "farm plan" {
		t.Fatalf("plan review not restored after the approval: %+v", c.m.picker)
	}
}

func farmAsk(f *fakeFarmer, prompt string, choices []string) chan string {
	ch := make(chan string, 1)
	go func() {
		ans, _ := f.hooks.Ask(prompt, choices)
		ch <- ans
	}()
	return ch
}

func (c *testChat) pumpAsk() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(farmAskMsg); return ok })
}

func TestFarmAsk_WaitsForMainTurnAndView(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.update(modKey('1', tea.ModCtrl))
	got := farmAsk(f, "part a failed; retry, drop or abort? ", []string{"retry", "drop", "abort"})
	c.pumpAsk()
	assert.Eq(t, c.m.ask == nil, true)
	assert.Contains(t, c.view(), "farm needs you · ctrl+0")
	c.m.run = &activeRun{cancel: func() {}, done: make(chan struct{})} // the main agent is replying
	c.update(modKey('0', tea.ModCtrl))
	assert.Eq(t, c.m.ask == nil, true)
	c.update(runDoneMsg{gen: c.m.gen, res: core.TurnResult{Status: "ok"}})
	if c.m.ask == nil {
		t.Fatal("farm question not opened after the main turn ended")
	}
	c.submit("dr")
	assert.Eq(t, <-got, "drop")
}

func TestFarmAsk_ReasksUnknownAnswer(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	got := farmAsk(f, "retry, drop or abort? ", []string{"retry", "drop", "abort"})
	c.pumpAsk()
	c.submit("end")
	if c.m.ask == nil {
		t.Fatal("unknown answer closed the question")
	}
	assert.Contains(t, c.m.hint, "answer one of: retry, drop, abort")
	c.submit("abort")
	assert.Eq(t, <-got, "abort")
}

func TestFarmAsk_TextStartingWithAChoiceIsAskedAgain(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	got := farmAsk(f, "[m]erge, [k]eep or [d]iscard? ", []string{"m", "k", "d"})
	c.pumpAsk()
	for _, text := range []string{"don't", "dunno"} {
		c.submit(text)
		if c.m.ask == nil {
			t.Fatalf("%q closed the question", text)
		}
		assert.Contains(t, c.m.hint, "answer one of: m, k, d")
	}
	c.submit("k")
	assert.Eq(t, <-got, "k")
}

func TestFarmBell_OncePerPromptAndOff(t *testing.T) {
	c, f := farmWithAgents(t, Options{FarmBell: true})
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	c.submit("y")
	<-got
	ans := farmAsk(f, "keep? ", []string{"m", "k", "d"})
	c.pumpAsk()
	c.submit("k")
	<-ans
	assert.Eq(t, c.m.bells, 2)

	c2, f2 := farmWithAgents(t, Options{})
	got = farmAllow(t, f2, "a")
	c2.pumpApproval()
	c2.submit("y")
	<-got
	assert.Eq(t, c2.m.bells, 0)
	assert.Eq(t, strings.Contains(c2.view(), "\a"), false)
}

func TestChatFarm_AttachAnswerClosesPrompt(t *testing.T) {
	c, f := farmWithAgents(t, Options{})
	c.typeText("half")
	got := farmAllow(t, f, "a")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[a] approve [a1] shell: go test ./...?")
	assert.Eq(t, c.m.farm.agent("a").needsApproval, true)
	ok, _ := f.broker().Answer("a1", farm.AnswerYes, "attach:c1")
	assert.Eq(t, ok, true)
	select {
	case d := <-got:
		assert.Eq(t, d, core.Granted)
	case <-time.After(5 * time.Second):
		t.Fatal("gate never returned")
	}
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(noticeMsg); return ok })
	if c.m.approval != nil {
		t.Fatalf("prompt still open: %+v", c.m.approval)
	}
	assert.Eq(t, c.m.farm.agent("a").needsApproval, false)
	assert.Eq(t, c.m.input.Value(), "half")
	assert.Contains(t, c.printedText(), "[a] [a1] answered from attach: yes")
	// The next approval prompts and takes its own answer.
	next := farmAllow(t, f, "b")
	c.pumpApproval()
	assert.Contains(t, c.m.approval.prompt, "[b] approve [a2]")
	c.submit("n")
	assert.Eq(t, <-next, core.Denied)
}
