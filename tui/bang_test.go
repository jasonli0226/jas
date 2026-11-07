package tui

import (
	"runtime"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

// pumpBang feeds posted messages until a ! command has finished.
func (c *testChat) pumpBang() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(bangDoneMsg); return ok })
}

func TestBang_OutputAttachedToNextMessage(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}, fake.Turn{Text: "again"}))
	c.submit("! echo hi-there")
	c.pumpBang()
	assert.Eq(t, c.m.running(), false) // a command never starts a turn on its own
	assert.Eq(t, len(c.b.prov.Requests), 0)
	assert.Contains(t, c.printedText(), "! echo hi-there")
	assert.Contains(t, c.printedText(), "hi-there")

	c.submit("look")
	c.pump()
	got := requestText(c.b.prov.Requests[0])
	for _, want := range []string{"Ran `echo hi-there`", "hi-there", "look"} {
		assert.Contains(t, got, want)
	}
	assert.Contains(t, c.printedText(), "(command output attached)")

	c.submit("again")
	c.pump()
	last := c.b.prov.Requests[len(c.b.prov.Requests)-1]
	if newest := last.Messages[len(last.Messages)-1]; strings.Contains(newest.Content[0].Text, "Ran `") {
		t.Fatal("output attached twice")
	}
}

func TestBang_NoSpaceAndStacking(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}))
	c.submit("!echo one")
	c.pumpBang()
	c.submit("!echo two")
	c.pumpBang()
	c.submit("go")
	c.pump()
	got := requestText(c.b.prov.Requests[0])
	one, two := strings.Index(got, "Ran `echo one`"), strings.Index(got, "Ran `echo two`")
	if one < 0 || two < one {
		t.Fatalf("want both outputs in order:\n%s", got)
	}
}

func TestBang_DoubleBangStaysLocal(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}))
	c.submit("!!echo private-out")
	c.pumpBang()
	assert.Contains(t, c.printedText(), "private-out")
	c.submit("go")
	c.pump()
	got := requestText(c.b.prov.Requests[0])
	assert.NotContains(t, got, "private-out")
	assert.NotContains(t, c.printedText(), "attached")
}

func TestBang_NonZeroExitShown(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}))
	c.submit("! exit 3")
	c.pumpBang()
	assert.Contains(t, c.printedText(), "exit 3")
	c.submit("go")
	c.pump()
	assert.Contains(t, requestText(c.b.prov.Requests[0]), "[exit status 3]")
}

func TestBang_Redacted(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]") }
	b.redact = redact
	c := newTestChatOpts(t, b, Options{Redact: redact})
	c.submit("! echo sk-secret")
	c.pumpBang()
	c.submit("go")
	c.pump()
	assert.NotContains(t, c.printedText()+c.view(), "sk-secret")
	assert.NotContains(t, requestText(c.b.prov.Requests[0]), "sk-secret")
	assert.Contains(t, requestText(c.b.prov.Requests[0]), "[REDACTED]")
}

func TestBang_EmptyCommandHints(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.submit("!")
	assert.Eq(t, c.m.bang, (*bangRun)(nil))
	assert.Contains(t, c.statusHint(), "!command")
}

func TestBang_CtrlCKillsCommandNotChat(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}
	c := newTestChat(t, newTestBackend(t))
	c.submit("! sleep 30")
	if c.m.bang == nil {
		t.Fatal("command not running")
	}
	c.key("ctrl+c")
	c.pumpBang()
	assert.Eq(t, c.quit, false)
	assert.Eq(t, c.m.bang, (*bangRun)(nil))
	assert.Contains(t, c.printedText(), "cancelled")
}

func TestBang_OneAtATime(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sleep")
	}
	c := newTestChat(t, newTestBackend(t))
	c.submit("! sleep 30")
	c.submit("! echo second")
	assert.Contains(t, c.statusHint(), "already running")
	assert.Eq(t, c.m.input.Value(), "! echo second") // the draft stays
	c.key("ctrl+c")                                  // clears the draft
	c.key("ctrl+c")                                  // cancels the command
	c.pumpBang()
}

func TestBang_RunsWhileAgentRuns(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "partial", Block: true}, fake.Turn{Text: "next"}))
	c.submit("work")
	c.submit("! echo mid-run")
	c.pumpBang()
	assert.Contains(t, c.printedText(), "mid-run")
	assert.Eq(t, len(c.m.queued), 0) // not a steer
	c.m.run.cancel()
	c.pump()
	c.submit("next")
	c.pump()
	last := c.b.prov.Requests[len(c.b.prov.Requests)-1]
	assert.Contains(t, requestText(last), "Ran `echo mid-run`")
}

func TestBang_AgentCopyCapped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses yes and head")
	}
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok"}))
	c.submit("!yes x | head -c 100000")
	c.pumpBang()
	c.submit("go")
	c.pump()
	got := requestText(c.b.prov.Requests[0])
	if len(got) > core.DefaultMaxOutput+1024 {
		t.Fatalf("agent copy not capped: %d bytes", len(got))
	}
	assert.Contains(t, got, "truncated")
}
