package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestParseCommand(t *testing.T) {
	for _, tc := range []struct {
		in, name, arg string
		ok            bool
	}{
		{"/model glm", "model", "glm", true},
		{"/skill:review fix it", "skill:review", "fix it", true},
		{"/new", "new", "", true},
		{"/skill:review\nfix\nit", "skill:review", "fix\nit", true},
		{"/", "", "", false},
		{"model", "", "", false},
	} {
		name, arg, ok := parseCommand(tc.in)
		assert.Eq(t, name, tc.name)
		assert.Eq(t, arg, tc.arg)
		assert.Eq(t, ok, tc.ok)
	}
}

func TestSuggest(t *testing.T) {
	known := []string{"allowed", "approve", "copy", "help", "model", "new", "quit", "resume", "session", "thinking"}
	assert.Eq(t, suggest("modle", known), "model")
	assert.Eq(t, suggest("res", known), "resume")
	assert.Eq(t, suggest("zzz", known), "")
	assert.Eq(t, suggest("a", known), "allowed") // tie goes alphabetical
	assert.Eq(t, suggest("", known), "")
}

func (c *testChat) printedText() string { return strings.Join(plain(c.m.Printed()), "\n") }

func TestChat_UnknownCommandKeepsInput(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("/modle")
	assert.Contains(t, c.printedText(), "unknown command /modle — did you mean /model?")
	assert.Eq(t, c.m.input.Value(), "/modle")
	assert.Eq(t, len(b.prov.Requests), 0)
	assert.Eq(t, c.m.running(), false)
}

// grantShell runs a turn whose shell call is answered "always".
func (c *testChat) grantShell() {
	c.t.Helper()
	c.submit("go")
	c.pumpUntilApproval()
	c.submit("always")
	c.pump()
}

func TestChat_NewKeepsGrantsAndModel(t *testing.T) {
	b := newTestBackend(t, shellTurn("echo hi"), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.grantShell()
	c.submit("/model other")
	assert.Eq(t, c.m.live.Model, "other")
	c.submit("/approve mutating")
	c.submit("/thinking")
	oldID := c.m.live.ID
	c.submit("/new")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Eq(t, c.m.live.ID != oldID, true)
	assert.Eq(t, c.m.live.Model, "other")
	assert.Eq(t, b.closes.Load(), 1)
	eqStrings(t, c.m.gate.Grants().List(), []string{"shell: echo hi"})
	assert.Eq(t, c.m.gate.Level(), core.AllMutating)
	assert.Eq(t, c.m.tr.ShowThinking, true)
	assert.Contains(t, c.view(), "other · mutating")
}

func TestChat_ResumeResetsGrants(t *testing.T) {
	b := newTestBackend(t, shellTurn("echo hi"), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.grantShell()
	first := c.m.live
	c.submit("/new")
	c.submit("/approve mutating")
	c.submit("/resume")
	assert.Contains(t, c.view(), " go") // the first session's first message
	c.key("enter")
	assert.Eq(t, c.m.live.Path, first.Path)
	assert.Eq(t, len(c.m.gate.Grants().List()), 0)
	assert.Eq(t, c.m.gate.Level(), core.ShellOnly)
	assert.Eq(t, b.closes.Load(), 2)
	assert.Contains(t, c.printedText(), "resumed "+first.ID+" · 4 messages")
}

func TestChat_ResumeLockedKeepsSession(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	other := newTestChat(t, b)
	other.submit("hi")
	other.pump() // still open, so its log is locked
	c := newTestChat(t, b)
	cur := c.m.live
	c.submit("/resume")
	c.key("enter")
	assert.Eq(t, c.m.live, cur)
	assert.Contains(t, c.printedText(), "open in another jas process")
	assert.Eq(t, c.quit, false)
}

func TestChat_IdleOnlyCommandsRefusedWhileRunning(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChat(t, b)
	id := c.m.live.ID
	c.submit("go")
	c.submit("/new")
	assert.Contains(t, c.printedText(), "wait for the reply or press Esc first")
	assert.Eq(t, c.m.live.ID, id)
	assert.Eq(t, len(c.m.queued), 0)
}

func TestChat_SkillCommandSendsPrompt(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("/skill:review fix it")
	c.pump()
	want, _ := b.SkillPrompt("review", "fix it")
	msgs := b.prov.Requests[0].Messages
	last := msgs[len(msgs)-1]
	assert.Eq(t, last.Content[0].Text, want)
	assert.Eq(t, plain(c.m.Printed())[0], "› /skill:review fix it")
}

func TestChat_UnknownSkillIsUnknownCommand(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("/skill:reviw x")
	assert.Contains(t, c.printedText(), "unknown command /skill:reviw — did you mean /skill:review?")
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_ModelPicker(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.submit("/model")
	assert.Contains(t, c.view(), "m2")
	c.typeText("2")
	c.key("enter")
	assert.Eq(t, c.m.live.Model, "m2")
	assert.Eq(t, c.m.picker == nil, true)
}

func TestChat_ThinkingApproveSessionCopy(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "the reply"})
	c := newTestChat(t, b)
	c.submit("/copy")
	assert.Contains(t, c.printedText(), "nothing to copy")
	c.submit("/thinking")
	c.submit("/thinking")
	c.submit("/approve")
	c.submit("/approve shell")
	c.submit("/approve bogus")
	assert.Eq(t, c.m.gate.Level(), core.ShellOnly)
	c.submit("hi")
	c.pump()
	c.submit("/copy")
	c.submit("/session")
	out := c.printedText()
	for _, s := range []string{"thinking shown", "thinking hidden", "never | shell | mutating",
		"invalid approve level", "copied 9 chars (via OSC 52, if your terminal supports it)", c.m.live.Path, "% of 100000"} {
		assert.Contains(t, out, s)
	}
}

func TestChat_AllowedRemovesGrant(t *testing.T) {
	b := newTestBackend(t, shellTurn("echo hi"), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("/allowed")
	assert.Contains(t, c.printedText(), "no always grants")
	c.grantShell()
	c.submit("/allowed")
	c.key("enter")
	assert.Contains(t, c.printedText(), "removed shell: echo hi")
	assert.Eq(t, len(c.m.gate.Grants().List()), 0)
}

func TestChat_RedactsNotices(t *testing.T) {
	b := newTestBackend(t, shellTurn("echo sk-secret"), fake.Turn{Text: "ok"})
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]") }
	b.redact = redact
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly, Redact: redact})
	c.submit("go")
	c.pumpUntilApproval()
	assert.Contains(t, c.view(), "echo [REDACTED]")
	c.submit("always")
	c.pump()
	c.submit("/allowed")
	assert.Contains(t, c.view(), "echo [REDACTED]")
	assert.NotContains(t, c.view()+c.printedText(), "sk-secret")
}

func TestChat_SlashListingAndTab(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("/s")
	v := c.view()
	assert.Contains(t, v, "/session")
	assert.Contains(t, v, "/skill:review")
	assert.NotContains(t, v, "/model")
	c.update(tea.KeyPressMsg{Code: tea.KeyTab})
	assert.Eq(t, c.m.input.Value(), "/session ")
	assert.NotContains(t, c.view(), "/skill:review")
}

func TestChat_HelpMentionsGrantScope(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.submit("/help")
	assert.Contains(t, c.printedText(), "covers any arguments to the first two words")
}

// pendingNew types /new and returns its open command without running it.
func (c *testChat) pendingNew() tea.Cmd {
	c.t.Helper()
	c.typeText("/new")
	_, cmd := c.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		c.t.Fatal("no open command")
	}
	return cmd
}

func TestChat_InputWaitsForNewSession(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	open := c.pendingNew()
	c.submit("hi")
	assert.Eq(t, c.m.input.Value(), "hi")
	assert.Eq(t, c.m.running(), false)
	c.run(open)
	c.key("enter")
	assert.Eq(t, c.m.running(), true)
	c.pump()
}

func TestChat_QuitDuringNewClosesBoth(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	open := c.pendingNew()
	c.key("ctrl+c")
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false) // waits for the open
	c.run(open)
	assert.Eq(t, c.quit, true)
	assert.Eq(t, b.closes.Load(), 2)
	assert.NoErr(t, c.m.shutdown())
	assert.Eq(t, b.closes.Load(), 2)
}

func TestChat_ShutdownWithNewThatNeverRan(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	open := c.pendingNew()
	assert.NoErr(t, c.m.shutdown())
	assert.Eq(t, b.closes.Load(), 1) // the current session
	assert.Eq(t, open(), tea.Msg(nil))
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ShutdownWithNewWhoseMessageWasLost(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	open := c.pendingNew()
	open()                          // closes the old session, opens the new one
	assert.NoErr(t, c.m.shutdown()) // closes the new one
	assert.Eq(t, b.closes.Load(), 2)
}

func TestChat_StaleSessionIsClosed(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	cur := c.m.live
	stale, err := b.NewSession("m", core.NewQuietUI(), core.AllowAll{})
	assert.NoErr(t, err)
	c.update(sessionMsg{gen: c.m.gen + 99, live: stale})
	assert.Eq(t, c.m.live, cur)
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ApprovalClosesPicker(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t, shellTurn("echo hi"), fake.Turn{Text: "ok"},
		fake.Turn{Calls: []fake.Call{{Name: "shell", Args: string(shellArgs("make"))}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.grantShell()
	c.submit("build")
	c.submit("/allowed")
	assert.Eq(t, c.m.picker != nil, true)
	close(release)
	c.pumpUntilApproval()
	assert.Eq(t, c.m.picker == nil, true)
	assert.Contains(t, c.view(), "approve shell: make?")
	c.submit("y")
	var status string
	c.pumpUntil(func(msg tea.Msg) bool {
		ev, ok := msg.(eventMsg)
		if ok && ev.typ == core.EvToolResult {
			status, _ = ev.data["status"].(string)
		}
		return ok && ev.typ == core.EvToolResult
	})
	assert.Eq(t, status, "ok")
	c.pump()
	eqStrings(t, c.m.gate.Grants().List(), []string{"shell: echo hi"})
	assert.Eq(t, c.m.picker == nil, true)
}

// compactBackend is a test backend with a small window and a Keep small
// enough that three exchanges have something to compact.
func compactBackend(t *testing.T, turns ...fake.Turn) *testBackend {
	b := newTestBackend(t, turns...)
	b.window, b.keep = 1000, 1
	return b
}

func TestChat_CompactCommand(t *testing.T) {
	b := compactBackend(t,
		fake.Turn{Text: "a1"}, fake.Turn{Text: "a2"},
		fake.Turn{Text: "a3", Usage: providers.Usage{PromptTokens: 600}},
		fake.Turn{Text: "## Goal\nthe summary"})
	c := newTestChat(t, b)
	for _, q := range []string{"q1", "q2", "q3"} {
		c.submit(q)
		c.pump()
	}
	assert.Contains(t, c.view(), " 60% ")
	c.submit("/compact keep the API notes")
	assert.Contains(t, c.printedText(), "compacting…")
	assert.Eq(t, c.m.running(), true)
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(compactDoneMsg); return ok })
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 4)
	assert.Contains(t, requestText(b.prov.Requests[3]), "Pay special attention to: keep the API notes")
	out := c.printedText()
	assert.Contains(t, out, "compacted 4 messages · ")
	assert.Contains(t, out, " → ")
	assert.NotContains(t, out, "· compacted earlier messages") // the command's own line is enough
	if c.m.usage.prompt >= 600 {
		t.Fatalf("ctx%% did not drop: prompt %d", c.m.usage.prompt)
	}
	assert.NotContains(t, c.view(), " 60% ")
}

func TestChat_CompactNothingYet(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("/compact")
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(compactDoneMsg); return ok })
	assert.Contains(t, c.printedText(), "nothing to compact yet")
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_CompactRefusedWhileRunning(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChat(t, b)
	c.submit("go")
	c.submit("/compact")
	assert.Contains(t, c.printedText(), "wait for the reply or press Esc first")
	assert.NotContains(t, c.printedText(), "compacting…")
}

func TestChat_CompactEscCancels(t *testing.T) {
	b := compactBackend(t, fake.Turn{Text: "a1"}, fake.Turn{Text: "a2"}, fake.Turn{Block: true})
	c := newTestChat(t, b)
	for _, q := range []string{"q1", "q2"} {
		c.submit(q)
		c.pump()
	}
	c.submit("/compact")
	assert.Eq(t, c.m.running(), true)
	c.key("esc")
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(compactDoneMsg); return ok })
	assert.Eq(t, c.m.running(), false)
	assert.Contains(t, c.printedText(), "compaction failed: ")
	assert.Eq(t, len(c.m.live.Session.Messages()), 4) // history unchanged
}

func TestChat_QuitDuringCompactWaits(t *testing.T) {
	b := compactBackend(t, fake.Turn{Text: "a1"}, fake.Turn{Text: "a2"}, fake.Turn{Block: true})
	c := newTestChat(t, b)
	for _, q := range []string{"q1", "q2"} {
		c.submit(q)
		c.pump()
	}
	c.submit("/compact")
	c.key("ctrl+c")
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false)
	assert.Eq(t, b.closes.Load(), 0)
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(compactDoneMsg); return ok })
	assert.Eq(t, c.quit, true)
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_CompactListedAndInHelp(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("/comp")
	assert.Contains(t, c.view(), "/compact")
	c.m.input.Reset()
	c.submit("/help")
	assert.Contains(t, c.printedText(), "/compact [focus]")
}

func TestChat_AutoCompactionPrintsRow(t *testing.T) {
	b := compactBackend(t,
		fake.Turn{Text: "a1", Usage: providers.Usage{PromptTokens: 600}},
		fake.Turn{Text: "the summary"}, fake.Turn{Text: "a2"})
	c := newTestChat(t, b)
	c.submit("q1")
	c.pump()
	c.submit("q2") // over half the window: compacts before the request
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 3)
	assert.Contains(t, c.printedText(), "· compacted earlier messages (")
	assert.NotContains(t, c.view(), " 60% ")
}
