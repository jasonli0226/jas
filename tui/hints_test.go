package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

func hintsOn() Options { return Options{Hints: true} }

// statusHint is the status slot's text when it shows a hint.
func (c *testChat) statusHint() string { return c.m.hint }

func TestHints_SteerOnceAndOffSwitch(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChatOpts(t, b, hintsOn())
	c.submit("go")
	c.submit("x")
	assert.Contains(t, c.statusHint(), "Ctrl+Q")
	c.typeText("a") // any key clears the hint
	assert.Eq(t, c.statusHint(), "")
	c.key("enter")
	assert.Eq(t, c.statusHint(), "") // once per chat
	c.key("esc")
	c.pump()

	off := newTestChat(t, newTestBackend(t, fake.Turn{Block: true}))
	off.submit("go")
	off.submit("x")
	assert.Eq(t, off.statusHint(), "")
	off.key("esc")
	off.pump()
}

func TestHints_EscRestoredQueue(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t, fake.Turn{Block: true}), hintsOn())
	c.submit("go")
	c.typeText("later")
	c.key("ctrl+q")
	c.key("esc")
	c.pump()
	assert.Eq(t, c.m.input.Value(), "later")
	assert.Contains(t, c.statusHint(), "back in the input")
}

func TestHints_URLWithWebToolsOff(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t, fake.Turn{Text: "ok"}, fake.Turn{Text: "ok"}), hintsOn())
	c.submit("read https://example.com")
	c.pump()
	assert.Contains(t, c.statusHint(), "web_tools")

	o := hintsOn()
	o.WebTools = true
	on := newTestChatOpts(t, newTestBackend(t, fake.Turn{Text: "ok"}), o)
	on.submit("read https://example.com")
	on.pump()
	assert.Eq(t, on.statusHint(), "")
}

func TestHints_TurnCap(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}})
	b.maxTurns = 1
	c := newTestChatOpts(t, b, hintsOn())
	c.submit("go")
	c.pump()
	assert.Contains(t, c.statusHint(), "max_turns")
}

func TestHints_ContextFilling(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok", Usage: providers.Usage{PromptTokens: 900}})
	b.window = 1000
	c := newTestChatOpts(t, b, hintsOn())
	c.submit("go")
	c.pump()
	assert.Contains(t, c.statusHint(), "/compact")
}

func TestHints_UnknownWindow(t *testing.T) {
	b := newTestBackend(t)
	b.windowGuess = true
	c := newTestChatOpts(t, b, hintsOn())
	assert.Contains(t, c.statusHint(), "context_window")
}

func TestHints_ResumeWhenEarlierSessions(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	first := newTestChat(t, b)
	first.submit("hi")
	first.pump()
	assert.NoErr(t, first.shutdown())

	c := newTestChatOpts(t, b, hintsOn())
	assert.Contains(t, c.statusHint(), "/resume")
}

func TestHints_ApprovalAndRepeatedDenials(t *testing.T) {
	b := newTestBackend(t, shellTurn("make a"), shellTurn("make b"), shellTurn("make c"), fake.Turn{Text: "ok"})
	o := hintsOn()
	o.Level = core.ShellOnly
	c := newTestChatOpts(t, b, o)
	c.submit("build")
	c.pumpUntilApproval()
	assert.Contains(t, c.statusHint(), "always")
	for i := 0; i < 3; i++ {
		if i > 0 {
			c.pumpUntilApproval()
		}
		c.submit("n")
	}
	c.pump()
	assert.Contains(t, c.statusHint(), "type why")
}

func TestHints_LongToolOutput(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}, fake.Turn{Text: "ok"})
	for i := 0; i < transcriptPreviewLines+5; i++ {
		assert.NoErr(t, os.WriteFile(filepath.Join(b.root, fmt.Sprintf("f%02d", i)), nil, 0o600))
	}
	c := newTestChatOpts(t, b, hintsOn())
	c.submit("list")
	c.pump()
	assert.Contains(t, c.statusHint(), "Ctrl+O")
}
