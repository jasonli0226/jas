package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
)

func modKey(r rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: mod} }

func TestFlip_CtrlAndAltDigits(t *testing.T) {
	assert.Eq(t, modKey('1', tea.ModCtrl).String(), "ctrl+1")
	assert.Eq(t, modKey('0', tea.ModAlt).String(), "alt+0")
	agents := []*farmAgent{{id: "a", num: 1}, {id: "b", num: 2}}
	for _, tc := range []struct {
		key, part string
		ok        bool
	}{
		{"ctrl+1", "a", true}, {"alt+2", "b", true}, {"ctrl+0", "", true}, {"alt+0", "", true},
		{"ctrl+9", "", false}, {"ctrl+x", "", false}, {"1", "", false},
	} {
		part, ok := flipTarget(tc.key, agents)
		if part != tc.part || ok != tc.ok {
			t.Fatalf("%s: got %q %v", tc.key, part, ok)
		}
	}
}

func TestFlip_NoFarmHint(t *testing.T) {
	c, _ := farmChat(t)
	c.update(modKey('1', tea.ModCtrl))
	assert.Contains(t, c.m.hint, "no farm running")
	assert.Eq(t, c.m.viewing, "")
}

// screenSince returns the blocks emitted to the screen from index i on.
func (c *testChat) screenSince(i int) string { return strings.Join(c.m.Screen()[i:], "\n") }

func TestFlip_ShowsAgentAndHoldsMain(t *testing.T) {
	c, _ := farmChat(t)
	startFarm(t, c)
	mark := len(c.m.Screen())
	c.update(modKey('1', tea.ModCtrl))
	assert.Eq(t, c.m.viewing, "a")
	assert.Contains(t, ansi.Strip(c.screenSince(mark)), "── a · coder")
	c.update(noticeMsg{"main note"})
	if strings.Contains(c.screenSince(mark), "main note") {
		t.Fatal("main-chat output reached the screen while viewing an agent")
	}
	assert.Contains(t, c.view(), "main chat: 1 new")
	mark = len(c.m.Screen())
	c.update(modKey('0', tea.ModCtrl))
	assert.Eq(t, c.m.viewing, "")
	assert.Contains(t, c.screenSince(mark), "main note")
	assert.NotContains(t, c.view(), "main chat: 1 new")
}

// toolPair plays a read of path through a worker UI.
func toolPair(c *testChat, f *fakeFarmer, part, path string) {
	ui := f.hooks.Worker(part)
	ui.Event("tool_started", map[string]any{"call_id": "1", "tool": "read", "args": map[string]any{"path": path}})
	ui.Event("tool_result", map[string]any{"call_id": "1", "tool": "read", "status": "ok", "output": "x"})
	c.drain()
}

func TestFlip_AgentOutputOnlyWhileViewed(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	c.update(modKey('1', tea.ModCtrl))
	mark := len(c.m.Screen())
	toolPair(c, f, "b", "b/x.go")
	if strings.Contains(c.screenSince(mark), "b/x.go") {
		t.Fatal("agent b's output reached the screen while viewing a")
	}
	assert.Eq(t, c.m.farm.agent("b").activity, "→ read b/x.go") // shown in the main chat's rows
	mark = len(c.m.Screen())
	c.update(modKey('2', tea.ModCtrl))
	assert.Contains(t, c.screenSince(mark), "b/x.go")
}

func TestFlip_ResizeRedrawsAtNewWidth(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	toolPair(c, f, "a", "a/"+strings.Repeat("long/", 15)+"x.go")
	c.update(modKey('1', tea.ModCtrl))
	c.update(tea.WindowSizeMsg{Width: 40, Height: 30})
	c.update(modKey('0', tea.ModCtrl))
	mark := len(c.m.Screen())
	c.update(modKey('1', tea.ModCtrl))
	for _, l := range strings.Split(c.screenSince(mark), "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("line %d cells wide after resize to 40: %q", w, ansi.Strip(l))
		}
	}
}

func TestFlip_AttachCommandAndUnknownPart(t *testing.T) {
	c, _ := farmChat(t)
	startFarm(t, c)
	c.submit("/farm attach 2")
	assert.Eq(t, c.m.viewing, "b")
	c.submit("/farm back")
	assert.Eq(t, c.m.viewing, "")
	c.submit("/farm attach zz")
	assert.Eq(t, c.m.viewing, "")
	assert.Contains(t, strings.Join(c.printedNoBanner(), "\n"), "no agent zz")
}

func (r *fakeRun) callList() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "|")
}

func TestSteer_RoutesToViewedAgent(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	f.event("part_started", map[string]any{"part": "a", "role": "coder"})
	c.drain()
	c.update(modKey('1', tea.ModCtrl))
	assert.Contains(t, c.m.input.Prompt, "a ›")
	c.submit("use slog")
	assert.Eq(t, f.run.callList(), "steer a use slog")
	assert.Contains(t, c.view(), "queued for a")
	c.update(modKey('0', tea.ModCtrl))
	assert.Eq(t, c.m.input.Prompt, "› ")
}

func TestSteer_DoneAgentRefused(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	f.event("part_ended", map[string]any{"part": "a", "status": "done"})
	c.drain()
	c.update(modKey('1', tea.ModCtrl))
	c.submit("more")
	assert.Eq(t, f.run.callList(), "")
	assert.Contains(t, strings.Join(c.m.Printed(), "\n")+c.m.hint, "a is done")
}

func TestEscAndCtrlC_NeverStopAgents(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	f.event("part_started", map[string]any{"part": "a", "role": "coder"})
	c.drain()
	c.update(modKey('1', tea.ModCtrl))
	c.typeText("half")
	c.key("esc")
	assert.Eq(t, c.m.input.Value(), "")
	c.typeText("again")
	c.key("ctrl+c")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Eq(t, f.run.callList(), "")
	assert.Eq(t, c.m.quitting, false)
}

func TestQuit_ConfirmsWithRunningFarm(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	c.submit("/quit")
	if c.m.picker == nil || !strings.Contains(c.m.picker.title, "a farm is running — stop it and quit?") {
		t.Fatalf("no quit confirm; picker %+v", c.m.picker)
	}
	c.typeText("n")
	c.key("enter")
	assert.Eq(t, c.m.quitting, false)
	assert.Eq(t, f.run.callList(), "")
	c.submit("/quit")
	c.typeText("y")
	c.key("enter")
	assert.Eq(t, f.run.callList(), "stop")
	f.run.finish(farmResult("interrupted", 130))
	c.drain()
	assert.Eq(t, c.m.quitting, true)
}

func TestFarmStop_SecondIsHard(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	c.submit("/farm stop")
	c.submit("/farm stop")
	assert.Eq(t, f.run.callList(), "stop|stop hard")
}

func TestFarmPauseResumeDropByNameOrNumber(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	c.submit("/farm pause 1")
	c.submit("/farm resume a")
	c.submit("/farm drop b")
	assert.Eq(t, f.run.callList(), "pause a|resume a|drop b")
}

func TestSteer_HandBackWhileTyping(t *testing.T) {
	c, f := farmChat(t)
	startFarm(t, c)
	c.update(modKey('1', tea.ModCtrl))
	c.typeText("half")
	f.hooks.Undelivered("a", []string{"late"})
	c.drain()
	assert.Contains(t, strings.Join(c.m.Printed(), "\n")+strings.Join(c.m.Screen(), "\n"), "[a] not delivered (part finished)")
	v := c.m.input.Value()
	if !strings.Contains(v, "half") || !strings.Contains(v, "late") {
		t.Fatalf("input %q", v)
	}
}
