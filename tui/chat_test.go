package tui

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// testChat drives a Model by hand: messages the model posts land in msgs,
// and commands returned by Update run synchronously.
type testChat struct {
	t    *testing.T
	m    *Model
	b    *testBackend
	msgs chan tea.Msg
	now  time.Time
	quit bool
}

func newTestChat(t *testing.T, b *testBackend) *testChat {
	return newTestChatOpts(t, b, Options{})
}

func newTestChatOpts(t *testing.T, b *testBackend, o Options) *testChat {
	t.Helper()
	c, init := newUnopenedChat(t, b, o)
	c.run(init)
	if c.m.live == nil {
		t.Fatal("no session opened")
	}
	t.Cleanup(func() {
		if c.m.run != nil {
			c.m.run.cancel()
			c.pump()
		}
	})
	return c
}

// newUnopenedChat returns a chat whose Init command (opening the session)
// has not run yet.
func newUnopenedChat(t *testing.T, b *testBackend, o Options) (*testChat, tea.Cmd) {
	c := &testChat{t: t, b: b, msgs: make(chan tea.Msg, 1024), now: t0}
	o.Model, o.Root = "m", b.root
	o.Now = func() time.Time { return c.now }
	c.m = New(b, o)
	c.m.send = func(msg tea.Msg) { c.msgs <- msg }
	return c, c.m.Init()
}

func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

func (c *testChat) update(msg tea.Msg) {
	_, cmd := c.m.Update(msg)
	c.run(cmd)
}

// run executes cmd and feeds its message back, expanding batches and
// sequences (both are []tea.Cmd underneath) in order.
func (c *testChat) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		c.quit = true
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		for i := range v.Len() {
			c.run(v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	c.update(msg)
}

// pumpUntil feeds posted messages into the model until stop accepts one.
func (c *testChat) pumpUntil(stop func(tea.Msg) bool) {
	c.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case msg := <-c.msgs:
			c.update(msg)
			if stop(msg) {
				return
			}
		case <-timeout:
			c.t.Fatal("timed out waiting for the model")
		}
	}
}

// pump feeds posted messages until the run ends.
func (c *testChat) pump() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(runDoneMsg); return ok })
}

func (c *testChat) key(k string) {
	var msg tea.KeyPressMsg
	switch k {
	case "enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter}
	case "alt+enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	default:
		c.t.Fatalf("unknown key %q", k)
	}
	assert.Eq(c.t, msg.String(), k)
	c.update(msg)
}

func (c *testChat) typeText(s string) {
	for _, r := range s {
		c.update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func (c *testChat) submit(s string) {
	c.typeText(s)
	c.key("enter")
}

func (c *testChat) view() string { return ansi.Strip(c.m.View().Content) }

func requestText(req providers.Request) string {
	var sb strings.Builder
	for _, msg := range req.Messages {
		for _, b := range msg.Content {
			sb.WriteString(b.Text + "\n")
		}
	}
	return sb.String()
}

func TestChat_SendStreamsAndPrints(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "Hello\n\nWorld"}))
	c.submit("hi")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Contains(t, c.view(), "working…")
	c.pump()
	eqStrings(t, plain(c.m.Printed()), []string{"› hi", "Hello", "World"})
	assert.NotContains(t, c.view(), "working…")
}

func TestChat_SteerWhileRunning(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("hi")
	c.submit("also")
	assert.Contains(t, c.view(), "↳ also")
	assert.Contains(t, c.view(), "1 queued")
	close(release)

	c.pumpUntil(func(msg tea.Msg) bool {
		ev, ok := msg.(eventMsg)
		return ok && ev.typ == core.EvUserMessage && ev.data["steer"] == true
	})
	assert.NotContains(t, c.view(), "↳ also")
	assert.NotContains(t, c.view(), "queued")
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 2)
	assert.Contains(t, requestText(b.prov.Requests[1]), "also")
}

func TestChat_EscRestoresQueuedText(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Block: true}))
	c.submit("go")
	c.submit("x")
	c.submit("y")
	c.key("esc")
	c.pump()
	assert.Eq(t, c.m.input.Value(), "x\ny")
	assert.NotContains(t, c.view(), "↳")
	assert.Contains(t, strings.Join(plain(c.m.Printed()), "\n"), "interrupted")
}

func TestChat_EscWhileIdleDoesNothing(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("abc")
	c.key("esc")
	assert.Eq(t, c.m.input.Value(), "abc")
	assert.Eq(t, len(c.m.Printed()), 0)
}

func TestChat_PasteWithNewlinesDoesNotSubmit(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.update(tea.PasteMsg{Content: "a\nb"})
	assert.Eq(t, c.m.input.Value(), "a\nb")
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_AltEnterInsertsNewline(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.typeText("a")
	c.key("alt+enter")
	c.typeText("b")
	assert.Eq(t, c.m.input.Value(), "a\nb")
	assert.Eq(t, c.m.running(), false)
}

func TestChat_EmptyAndSlashInputDoNotRun(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("   ")
	assert.Eq(t, len(c.m.Printed()), 0)
	c.submit("/foo bar")
	eqStrings(t, plain(c.m.Printed()), []string{"unknown command /foo"})
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_CtrlCClearsThenQuits(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.typeText("abc")
	c.key("ctrl+c")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Eq(t, c.quit, false)

	// Clearing did not arm the quit: the next press only warns.
	assert.Eq(t, c.m.lastCtrlC.IsZero(), true)
	c.now = c.now.Add(500 * time.Millisecond)
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false)
	assert.Contains(t, c.view(), "press Ctrl-C again to quit")

	c.now = c.now.Add(500 * time.Millisecond)
	c.key("ctrl+c")
	assert.Eq(t, c.quit, true)
	assert.Eq(t, b.closes.Load(), 1)
	printed := plain(c.m.Printed())
	assert.Eq(t, printed[len(printed)-1], "session "+c.m.live.ID+" saved · jas --continue to resume")
}

func TestChat_SlowSecondCtrlCOnlyWarns(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.key("ctrl+c")
	assert.Contains(t, c.view(), "press Ctrl-C again to quit")
	c.now = c.now.Add(2 * time.Second)
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false)
	assert.Eq(t, b.closes.Load(), 0)
	c.typeText("a")
	assert.NotContains(t, c.view(), "press Ctrl-C again")
}

func TestChat_QuitWhileRunningWaitsForRun(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChat(t, b)
	c.submit("go")
	c.key("ctrl+c")
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false)
	assert.Eq(t, b.closes.Load(), 0)
	c.pump()
	assert.Eq(t, c.quit, true)
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_RunEndNotices(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Err: errors.New("boom")}))
	c.submit("hi")
	c.pump()
	printed := plain(c.m.Printed())
	assert.Contains(t, printed[len(printed)-1], "failed: ")
	assert.Contains(t, printed[len(printed)-1], "boom")
}

func TestChat_UsageFeedsStatusLine(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok",
		Usage: providers.Usage{PromptTokens: testWindow / 4, CompletionTokens: 1000}}))
	c.submit("hi")
	c.pump()
	assert.Contains(t, c.view(), "m · never · 25% · 26.0k tok · "+filepath.Base(c.b.root))
}

func TestChat_DropsMessagesFromOldSession(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(textMsg{gen: c.m.gen - 1, s: "stale\n\nmore"})
	c.update(eventMsg{gen: c.m.gen - 1, typ: core.EvUsage, data: map[string]any{"prompt_tokens": 50000}})
	assert.Eq(t, len(c.m.Printed()), 0)
	assert.Eq(t, c.m.tr.Live(), "")
	assert.Contains(t, c.view(), "· 0% ·")
}

func TestChat_ContinueWithoutSessionStartsNew(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t), Options{Continue: true})
	eqStrings(t, plain(c.m.Printed()), []string{"no saved session for this project; starting a new one"})
}

func TestChat_ContinueResumesNewest(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "A"})
	first := newTestChat(t, b)
	first.submit("hi")
	first.pump()
	first.key("ctrl+c")
	first.key("ctrl+c")
	assert.Eq(t, first.quit, true)

	c := newTestChatOpts(t, b, Options{Continue: true})
	assert.Eq(t, c.m.live.Path, first.m.live.Path)
	assert.Eq(t, c.m.gen > 0, true)
}

func TestChat_QuitBeforeSessionOpensClosesIt(t *testing.T) {
	b := newTestBackend(t)
	c, init := newUnopenedChat(t, b, Options{})
	c.key("ctrl+c")
	c.key("ctrl+c")
	assert.Eq(t, c.quit, false) // waits for the session being opened
	c.run(init)
	assert.Eq(t, c.quit, true)
	assert.Eq(t, b.closes.Load(), 1)
	assert.NoErr(t, c.m.shutdown())
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ShutdownClosesSessionWhoseMessageWasLost(t *testing.T) {
	b := newTestBackend(t)
	c, init := newUnopenedChat(t, b, Options{})
	init() // the session opens but the program is gone before sessionMsg
	assert.NoErr(t, c.m.shutdown())
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ShutdownClaimsOpenThatNeverRan(t *testing.T) {
	b := newTestBackend(t)
	c, init := newUnopenedChat(t, b, Options{})
	assert.NoErr(t, c.m.shutdown())
	assert.Eq(t, init(), tea.Msg(nil)) // the late command opens nothing
	assert.Eq(t, b.closes.Load(), 0)
	infos, err := b.Sessions("")
	assert.NoErr(t, err)
	assert.Eq(t, len(infos), 0)
}

func TestChat_ShutdownWaitsForQuitClose(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.m.Update(ctrlC())
	_, closeCmd := c.m.Update(ctrlC()) // the close command, not yet run
	assert.Eq(t, closeCmd != nil, true)
	assert.Eq(t, b.closes.Load(), 0)
	assert.NoErr(t, c.m.shutdown()) // closes it rather than returning early
	assert.Eq(t, b.closes.Load(), 1)
	c.run(closeCmd) // the late command does not close again
	assert.Eq(t, b.closes.Load(), 1)
}

// pumpUntilApproval feeds posted messages until the approval prompt opens.
func (c *testChat) pumpUntilApproval() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(approvalMsg); return ok })
}

func shellTurn(cmd string) fake.Turn {
	return fake.Turn{Calls: []fake.Call{{Name: "shell", Args: string(shellArgs(cmd))}}}
}

func TestChat_ApprovalAnswerWithTextSteers(t *testing.T) {
	b := newTestBackend(t, shellTurn("go test ./..."), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("test it")
	c.pumpUntilApproval()
	assert.Contains(t, c.view(), "approve shell: go test ./...? [y/N/always]")

	c.submit("no, use make test")
	assert.Eq(t, c.m.input.Value(), "")
	assert.NotContains(t, c.view(), "approve shell")
	assert.Contains(t, c.view(), "↳ no, use make test")

	var status string
	c.pumpUntil(func(msg tea.Msg) bool {
		ev, ok := msg.(eventMsg)
		if ok && ev.typ == core.EvToolResult {
			status, _ = ev.data["status"].(string)
		}
		return ok && ev.typ == core.EvToolResult
	})
	assert.Eq(t, status, "denied")
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 2)
	req := requestText(b.prov.Requests[1])
	assert.Contains(t, req, "no, use make test")
}

func TestChat_ApprovalYesRunsAndAlwaysNotice(t *testing.T) {
	b := newTestBackend(t, shellTurn("echo hi"), shellTurn("echo hi there"), fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("go")
	c.pumpUntilApproval()
	c.submit("always")
	assert.Eq(t, len(c.m.queued), 0)
	c.pump()
	out := strings.Join(plain(c.m.Printed()), "\n")
	assert.Contains(t, out, "always: shell: echo hi (covers any arguments)")
	assert.NotContains(t, out, "denied")
	assert.Eq(t, len(b.prov.Requests), 3)
}

func TestChat_EscClosesApprovalPrompt(t *testing.T) {
	b := newTestBackend(t, shellTurn("go test ./..."), fake.Turn{Text: "again"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("test it")
	c.pumpUntilApproval()
	c.key("esc")
	assert.NotContains(t, c.view(), "approve shell")

	var done runDoneMsg
	c.pumpUntil(func(msg tea.Msg) bool {
		d, ok := msg.(runDoneMsg)
		done = d
		return ok
	})
	assert.Eq(t, done.res.Status, "interrupted")
	assert.NotContains(t, c.view(), "approve shell")

	c.submit("y")
	assert.Eq(t, c.m.running(), true)
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 2)
	assert.Contains(t, requestText(b.prov.Requests[1]), "y")
	assert.Contains(t, strings.Join(plain(c.m.Printed()), "\n"), "› y")
}

func TestChat_QueuedTextSurvivesApproval(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "shell", Args: string(shellArgs("make"))}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "ok"})
	c := newTestChatOpts(t, b, Options{Level: core.ShellOnly})
	c.submit("go")
	c.submit("earlier")
	close(release)
	c.pumpUntilApproval()
	assert.Contains(t, c.view(), "↳ earlier")
	c.submit("y")
	assert.Contains(t, c.view(), "↳ earlier")
	c.pump()
	assert.Contains(t, requestText(b.prov.Requests[1]), "earlier")
}
