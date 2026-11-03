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
