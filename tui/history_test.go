package tui

import (
	"fmt"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestHistory_PrevNextAndDraft(t *testing.T) {
	var h history
	h.add("a")
	h.add("b")
	v, ok := h.prev("draft")
	assert.Eq(t, ok, true)
	assert.Eq(t, v, "b")
	v, _ = h.prev("b")
	assert.Eq(t, v, "a")
	_, ok = h.prev("a") // at the oldest entry
	assert.Eq(t, ok, false)
	v, _ = h.next()
	assert.Eq(t, v, "b")
	v, _ = h.next() // past the newest: back to what was being typed
	assert.Eq(t, v, "draft")
	_, ok = h.next()
	assert.Eq(t, ok, false)
}

func TestHistory_EmptyDoesNothing(t *testing.T) {
	var h history
	_, ok := h.prev("x")
	assert.Eq(t, ok, false)
	_, ok = h.next()
	assert.Eq(t, ok, false)
}

func TestHistory_DedupeAndCap(t *testing.T) {
	var h history
	h.add("same")
	h.add("same")
	h.add("")
	assert.Eq(t, len(h.items), 1)
	for i := range historyCap + 5 {
		h.add(fmt.Sprint(i))
	}
	assert.Eq(t, len(h.items), historyCap)
	assert.Eq(t, h.items[len(h.items)-1], fmt.Sprint(historyCap+4))
}

func TestChat_InputHistoryUpDown(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "A"}, fake.Turn{Text: "B"}))
	c.submit("a")
	c.pump()
	c.submit("b")
	c.pump()
	c.typeText("dr")
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "b")
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "a")
	c.key("down")
	assert.Eq(t, c.m.input.Value(), "b")
	c.key("down")
	assert.Eq(t, c.m.input.Value(), "dr")
}

// A recalled slash command does not open the / menu, so Up keeps walking
// history instead of moving the menu.
func TestChat_InputHistoryRecallsSlashCommands(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.submit("/help")
	c.submit("/thinking")
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "/thinking")
	assert.NotContains(t, c.view(), "/allowed")
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "/help")
}

func TestChat_InputHistoryUpInsideMultilineMovesCursor(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.submit("/help")
	c.typeText("x")
	c.key("alt+enter")
	c.typeText("y")
	c.key("up") // the cursor goes to the first line
	assert.Eq(t, c.m.input.Value(), "x\ny")
	c.key("up") // now on the first line: history
	assert.Eq(t, c.m.input.Value(), "/help")
}

func TestChat_ResumeSeedsInputHistory(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "A"}, fake.Turn{Text: "B"})
	first := newTestChat(t, b)
	first.submit("one")
	first.pump()
	first.submit("two")
	first.pump()
	first.key("ctrl+c")
	first.key("ctrl+c")

	c := newTestChatOpts(t, b, Options{Continue: true})
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "two")
	c.key("up")
	assert.Eq(t, c.m.input.Value(), "one")
}

// add ends any browsing, so the next Up starts from the newest entry again.
func TestHistory_AddResetsBrowsing(t *testing.T) {
	var h history
	h.add("a")
	h.add("b")
	h.prev("")
	h.prev("b")
	h.add("c")
	v, _ := h.prev("")
	assert.Eq(t, v, "c")
}
