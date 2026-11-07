package tui

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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
	shut bool // shutdown ran; it runs once, as after the program exits
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
		// Close the session as cmd/jas does after the program exits.
		if !c.shut {
			if err := c.shutdown(); err != nil {
				t.Errorf("shutdown: %v", err)
			}
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

// printedNoBanner returns the printed blocks without the leading start
// banner, which Init and /new always print first.
func (c *testChat) printedNoBanner() []string {
	p := c.m.Printed()
	if len(p) > 0 && strings.HasPrefix(p[0], boldStyle.Render("jas")) {
		return p[1:]
	}
	return p
}

// batchCmds returns the commands inside a tea.Batch result, for tests that
// run a command the program has already left.
func batchCmds(t *testing.T, msg tea.Msg) []tea.Cmd {
	t.Helper()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		t.Fatalf("not a batch: %T", msg)
	}
	return batch
}

func (c *testChat) shutdown() error {
	c.shut = true
	return c.m.shutdown()
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
	if _, ok := msg.(tickMsg); ok {
		return // a real 130 ms timer; spinner tests post ticks by hand
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
	case "shift+enter":
		msg = tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}
	case "ctrl+j":
		msg = tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl}
	case "esc":
		msg = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		msg = tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+c":
		msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+s":
		msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+l":
		msg = tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}
	case "ctrl+o":
		msg = tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl}
	case "ctrl+q":
		msg = tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl}
	case "alt+up":
		msg = tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}
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

func TestChat_BlankLineAboveInput(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "partial", Block: true}))
	lines := strings.Split(c.view(), "\n")
	assert.Eq(t, lines[0], "")
	assert.Eq(t, strings.HasPrefix(lines[1], "─"), true)
	assert.Eq(t, strings.HasPrefix(lines[2], "›"), true)

	c.submit("hi")
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(textMsg); return ok })
	// The loader line belongs to the input area, under the blank line.
	assert.Contains(t, c.view(), "● partial\n\n⠋ working… 0s\n"+strings.Repeat("─", defaultWidth)+"\n›")
}

func TestChat_SendStreamsAndPrints(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "Hello\n\nWorld"}))
	c.submit("hi")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Contains(t, c.view(), "working…")
	c.pump()
	eqStrings(t, plain(c.printedNoBanner()), []string{"› hi", "", "● Hello", "", "  World"})
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

// Ctrl+Q while a run is active queues a follow-up: it never reaches the
// running loop and is sent as the next message once the run ends ok.
func TestChat_FollowUpWaitsForRunEnd(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"},
		fake.Turn{Text: "second"})
	c := newTestChat(t, b)
	c.submit("hi")
	c.typeText("next")
	c.key("ctrl+q")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Contains(t, c.view(), "↳ after run: next")
	assert.Contains(t, c.view(), "1 queued")
	close(release)
	c.pump() // the first run
	c.pump() // the follow-up's run
	assert.Eq(t, len(b.prov.Requests), 3)
	assert.NotContains(t, requestText(b.prov.Requests[1]), "next")
	assert.Contains(t, requestText(b.prov.Requests[2]), "next")
	assert.NotContains(t, c.view(), "↳")
	got := plain(c.printedNoBanner())
	assert.Contains(t, strings.Join(got, "\n"), "● done\n\n› next")
}

// While idle, Ctrl+Q sends like Enter.
func TestChat_FollowUpKeyIdleSends(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	c := newTestChat(t, b)
	c.typeText("hi")
	c.key("ctrl+q")
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 1)
	assert.Contains(t, requestText(b.prov.Requests[0]), "hi")
}

// A run that does not end ok (Esc) puts follow-ups back in the input after
// any undelivered steering, instead of sending them.
func TestChat_EscRestoresFollowUps(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChat(t, b)
	c.submit("go")
	c.submit("x")
	c.typeText("y")
	c.key("ctrl+q")
	c.key("esc")
	c.pump()
	assert.Eq(t, c.m.input.Value(), "x\ny")
	assert.Eq(t, len(b.prov.Requests), 1)
	assert.NotContains(t, c.view(), "↳")
}

// Alt+Up pulls every queued steer and follow-up back into the input for
// editing; none of them is delivered.
func TestChat_AltUpPopsQueue(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("go")
	c.submit("x")
	c.typeText("y")
	c.key("ctrl+q")
	c.typeText("draft")
	c.key("alt+up")
	assert.Eq(t, c.m.input.Value(), "x\ny\ndraft")
	assert.NotContains(t, c.view(), "↳")
	assert.NotContains(t, c.view(), "queued")
	close(release)
	c.pump()
	assert.Eq(t, len(b.prov.Requests), 2)
	assert.NotContains(t, requestText(b.prov.Requests[1]), "x\n")
}

func TestChat_SteerPrintsUserBlock(t *testing.T) {
	release := make(chan struct{})
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, OnEmit: func() { <-release }},
		fake.Turn{Text: "done"})
	c := newTestChat(t, b)
	c.submit("hi")
	c.submit("also")
	close(release)
	c.pump()
	got := plain(c.printedNoBanner())
	assert.Eq(t, len(got), 7)
	eqStrings(t, append(got[:2:2], got[3:]...), []string{"› hi", "", "", "› also", "", "● done"})
	assert.Eq(t, strings.HasPrefix(got[2], "▸ ls"), true)
}

func TestChat_NoticeSeparatesUserTurns(t *testing.T) {
	started := make(chan struct{})
	c := newTestChat(t, newTestBackend(t,
		fake.Turn{Block: true, OnEmit: func() { close(started) }}, fake.Turn{Text: "ok"}))
	c.submit("go")
	<-started // Esc before the request starts would leave the Block turn for "retry"
	c.key("esc")
	c.pump()
	c.submit("retry")
	c.pump()
	eqStrings(t, plain(c.printedNoBanner()), []string{"› go", "", "interrupted", "", "› retry", "", "● ok"})
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
	assert.Eq(t, len(c.printedNoBanner()), 0)
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

func TestChat_ShiftEnterInsertsNewline(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.typeText("a")
	c.key("shift+enter")
	c.typeText("b")
	assert.Eq(t, c.m.input.Value(), "a\nb")
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_CtrlJInsertsNewline(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.typeText("a")
	c.key("ctrl+j")
	c.typeText("b")
	assert.Eq(t, c.m.input.Value(), "a\nb")
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 0)
}

// Native Windows terminals collapse Shift+Enter to plain Enter; onKey
// rewrites it when physicalShift reports the key is held (GetAsyncKeyState
// on windows; stubbed here).
func TestChat_WindowsShiftEnterFallback(t *testing.T) {
	old := physicalShift
	physicalShift = func() bool { return true }
	t.Cleanup(func() { physicalShift = old })

	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.typeText("a")
	c.key("enter") // arrives as plain enter; Shift is "held"
	c.typeText("b")
	assert.Eq(t, c.m.input.Value(), "a\nb")
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 0)
}

func TestChat_EnterStillSendsWithoutShift(t *testing.T) {
	old := physicalShift
	physicalShift = func() bool { return false }
	t.Cleanup(func() { physicalShift = old })

	b := newTestBackend(t, fake.Turn{Text: "ok"})
	c := newTestChat(t, b)
	c.typeText("hi")
	c.key("enter")
	assert.Eq(t, c.m.input.Value(), "")
	assert.Eq(t, c.m.running(), true)
	c.pump()
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, len(b.prov.Requests), 1)
}

func TestChat_EmptyAndSlashInputDoNotRun(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("   ")
	assert.Eq(t, len(c.printedNoBanner()), 0)
	c.submit("/foo bar")
	eqStrings(t, plain(c.printedNoBanner()), []string{"unknown command /foo"})
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
	assert.Contains(t, c.view(), "m · never · 25% · ↑25.0k ↓1.0k · "+filepath.Base(c.b.root))
}

func TestChat_DropsMessagesFromOldSession(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(textMsg{gen: c.m.gen - 1, s: "stale\n\nmore"})
	c.update(eventMsg{gen: c.m.gen - 1, typ: core.EvUsage, data: map[string]any{"prompt_tokens": 50000}})
	assert.Eq(t, len(c.printedNoBanner()), 0)
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
	assert.Contains(t, c.printedText(), "resumed "+first.m.live.ID+" · 2 messages")
}

func TestChat_ResumeReprintsTranscript(t *testing.T) {
	b := newTestBackend(t,
		fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		fake.Turn{Text: "hi there"})
	first := newTestChat(t, b)
	first.submit("hello")
	first.pump()
	first.key("ctrl+c")
	first.key("ctrl+c")

	c := newTestChatOpts(t, b, Options{Continue: true})
	got := plain(c.m.Printed())
	assert.Eq(t, len(got), 7)
	eqStrings(t, got[:2], []string{"› hello", ""})
	assert.Eq(t, strings.HasPrefix(got[2], "▸ ls ."), true)
	eqStrings(t, got[3:], []string{"", "● hi there", "", "resumed " + c.m.live.ID + " · 4 messages"})
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
	assert.NoErr(t, c.shutdown())
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ShutdownClosesSessionWhoseMessageWasLost(t *testing.T) {
	b := newTestBackend(t)
	c, init := newUnopenedChat(t, b, Options{})
	for _, cmd := range batchCmds(t, init()) { // the program is gone: run the batch but drop the messages
		cmd()
	}
	assert.NoErr(t, c.shutdown())
	assert.Eq(t, b.closes.Load(), 1)
}

func TestChat_ShutdownClaimsOpenThatNeverRan(t *testing.T) {
	b := newTestBackend(t)
	c, init := newUnopenedChat(t, b, Options{})
	assert.NoErr(t, c.shutdown())
	for _, cmd := range batchCmds(t, init()) { // the late command opens nothing
		cmd()
	}
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
	assert.NoErr(t, c.shutdown()) // closes it rather than returning early
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

func TestChat_UsageThroughRedactingUI(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	ui := core.NewRedactingUI(&bridge{gen: c.m.gen, send: c.update}, func(s string) string { return s })
	ui.Event(core.EvUsage, map[string]any{"prompt_tokens": testWindow / 4, "completion_tokens": 1000, "cached_tokens": 0})
	assert.Contains(t, c.view(), "· 25% · ↑25.0k ↓1.0k ·")
}

func TestIntField_Shapes(t *testing.T) {
	data := map[string]any{"i": 3, "f": 4.9, "n": json.Number("12"), "nf": json.Number("1.5e3"), "bad": json.Number("x")}
	assert.Eq(t, intField(data, "i"), 3)
	assert.Eq(t, intField(data, "f"), 4)
	assert.Eq(t, intField(data, "n"), 12)
	assert.Eq(t, intField(data, "nf"), 1500)
	assert.Eq(t, intField(data, "bad"), 0)
	assert.Eq(t, intField(data, "missing"), 0)
}

func TestChat_ToolRunningShownInView(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolStarted, data: map[string]any{
		"call_id": "c", "tool": "shell", "args": map[string]any{"command": "make test"}}})
	assert.Contains(t, c.view(), "▸ shell make test running")
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolResult, data: map[string]any{
		"call_id": "c", "tool": "shell", "status": "ok"}})
	assert.NotContains(t, c.view(), "running")
	assert.Contains(t, strings.Join(plain(c.m.Printed()), "\n"), "▸ shell make test ok")
}

// Blocks handed to Println stay in the view until the print is acknowledged,
// so a finished block is never missing from both the live region and
// scrollback.
func TestChat_StreamingNoBlankFlash(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	// Update without running the returned commands: the Println is in flight.
	c.m.Update(textMsg{gen: c.m.gen, s: "Hello\n\n"})
	assert.Contains(t, c.view(), "● Hello")
	c.m.Update(textMsg{gen: c.m.gen, s: "World\n\n"}) // waits behind the first print
	assert.Contains(t, c.view(), "● Hello\n\n  World")
	c.m.Update(printedMsg{}) // the first print landed; the second goes out
	assert.NotContains(t, c.view(), "Hello")
	assert.Contains(t, c.view(), "  World")
	c.m.Update(printedMsg{})
	assert.NotContains(t, c.view(), "World")
}

func TestChat_InputFramedByRules(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	lines := strings.Split(c.view(), "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "› ") })
	rule := strings.Repeat("─", defaultWidth)
	assert.Eq(t, strings.TrimRight(lines[i-1], " "), rule)
	assert.Eq(t, strings.TrimRight(lines[i+1], " "), rule)
	assert.Contains(t, lines[i+2], "· ") // status line under the bottom rule
}

func TestChat_SlashMenuInsideRules(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("/")
	lines := strings.Split(c.view(), "\n")
	rule := strings.Repeat("─", defaultWidth)
	last := len(lines) - 1 // status line
	assert.Eq(t, strings.TrimRight(lines[last-1], " "), rule)
	assert.Contains(t, strings.Join(lines[:last-1], "\n"), "help")
}

func TestChat_ApprovalPrintsDiffAboveQuestion(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "I'll fix d.", Calls: []fake.Call{{Name: "edit",
		Args: `{"path":"f.txt","old_string":"d","new_string":"D"}`}}}, fake.Turn{Text: "ok"})
	assert.NoErr(t, os.WriteFile(filepath.Join(b.root, "f.txt"), []byte("a\nb\nc\nd\ne\n"), 0o644))
	c := newTestChatOpts(t, b, Options{Level: core.AllMutating})
	c.submit("edit it")
	c.pumpUntilApproval()
	printed := plain(c.m.Printed())
	at := func(s string) int {
		t.Helper()
		i := slices.Index(printed, s)
		if i < 0 {
			t.Fatalf("%q not printed in %q", s, printed)
		}
		return i
	}
	user, agent, header := at("› edit it"), at("● I'll fix d."), at("edit f.txt")
	del, add := at("     4 - d"), at("     4 + D")
	assert.Eq(t, user < agent && agent < header && header < del && del < add, true)
	assert.Eq(t, printed[header-1], "● I'll fix d.") // agent→tool: no blank
	v := c.view()
	assert.Contains(t, v, "[y/N/always]")
	assert.NotContains(t, v, "I'll fix d.")
	assert.NotContains(t, v, "     4 - d")
}

func TestChat_OptionsCarryVersion(t *testing.T) {
	c, _ := newUnopenedChat(t, newTestBackend(t), Options{Version: "9.9.9"})
	if c.m.o.Version != "9.9.9" {
		t.Fatalf("version %q", c.m.o.Version)
	}
}

func TestChat_BannerOnStart(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t), Options{Version: "1.0.0"})
	printed := plain(c.m.Printed())
	if len(printed) == 0 {
		t.Fatal("no blocks printed")
	}
	assert.Contains(t, printed[0], "v1.0.0")
	assert.Contains(t, printed[0], "jas")
	assert.Contains(t, strings.Join(printed, "\n"), testHints)
}

func TestChat_NoBannerOnContinue(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "A"})
	first := newTestChatOpts(t, b, Options{Version: "9.9.9"})
	first.submit("hi")
	first.pump()
	first.key("ctrl+c")
	first.key("ctrl+c")
	assert.Eq(t, first.quit, true)

	c := newTestChatOpts(t, b, Options{Version: "1.0.0", Continue: true})
	assert.Contains(t, c.printedText(), "resumed "+first.m.live.ID)
	assert.NotContains(t, c.printedText(), "1.0.0")
	assert.NotContains(t, c.printedText(), "[Skills]")
	assert.NotContains(t, c.printedText(), testHints)
}

func TestChat_BannerOnNew(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t), Options{Version: "1.0.0"})
	c.submit("/new")
	assert.Eq(t, strings.Count(c.printedText(), "v1.0.0"), 2)
}

// cmdHasType reports whether cmd, expanded through Batch/Sequence, produces
// a message whose dynamic type name is typ (e.g. "tea.clearScreenMsg").
func cmdHasType(cmd tea.Cmd, typ string) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if msg == nil {
		return false
	}
	if fmt.Sprintf("%T", msg) == typ {
		return true
	}
	v := reflect.ValueOf(msg)
	if v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeFor[tea.Cmd]() {
		for i := range v.Len() {
			if cmdHasType(v.Index(i).Interface().(tea.Cmd), typ) {
				return true
			}
		}
	}
	return false
}

func TestChat_CtrlLClearsScreenKeepsSession(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "hello"})
	c := newTestChatOpts(t, b, Options{Version: "1.0.0"})
	c.submit("hi")
	c.pump()
	id, gen := c.m.live.ID, c.m.gen
	banners := strings.Count(c.printedText(), "v1.0.0")

	_, cmd := c.m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !cmdHasType(cmd, "tea.clearScreenMsg") {
		t.Fatal("Ctrl+L did not issue ClearScreen")
	}
	// clearView prints the banner while building the cmd; session stays.
	assert.Eq(t, c.m.live.ID, id)
	assert.Eq(t, c.m.gen, gen)
	assert.Eq(t, c.m.running(), false)
	assert.Eq(t, strings.Count(c.printedText(), "v1.0.0"), banners+1)
	assert.Contains(t, c.printedText(), testHints)
	c.run(cmd)
}

func TestChat_CtrlLDuringRunKeepsRun(t *testing.T) {
	c := newTestChat(t, newTestBackend(t, fake.Turn{Text: "ok", Block: true}))
	c.submit("hi")
	assert.Eq(t, c.m.running(), true)
	id, gen := c.m.live.ID, c.m.gen
	_, cmd := c.m.Update(tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl})
	if !cmdHasType(cmd, "tea.clearScreenMsg") {
		t.Fatal("Ctrl+L did not issue ClearScreen during a run")
	}
	assert.Eq(t, c.m.live.ID, id)
	assert.Eq(t, c.m.gen, gen)
	assert.Eq(t, c.m.running(), true)
	c.run(cmd)
	c.m.run.cancel()
	c.pump()
}

func TestChat_NewClearsScreen(t *testing.T) {
	c := newTestChatOpts(t, newTestBackend(t), Options{Version: "1.0.0"})
	oldID := c.m.live.ID
	c.typeText("/new")
	_, cmd := c.m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !cmdHasType(cmd, "tea.clearScreenMsg") {
		t.Fatal("/new did not issue ClearScreen")
	}
	c.run(cmd)
	assert.Eq(t, c.m.live.ID != oldID, true)
	assert.Eq(t, strings.Count(c.printedText(), "v1.0.0"), 2)
}

func TestChat_NoBannerOnResume(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Text: "ok"})
	first := newTestChatOpts(t, b, Options{Version: "9.9.9"})
	first.submit("hi")
	first.pump()
	first.key("ctrl+c")
	first.key("ctrl+c")
	assert.Eq(t, first.quit, true)

	c := newTestChatOpts(t, b, Options{Version: "1.0.0"})
	c.submit("/resume")
	c.key("enter")
	assert.Eq(t, c.m.live.Path, first.m.live.Path)
	assert.Eq(t, strings.Count(c.printedText(), "v1.0.0"), 1)
}

func TestChat_ToolPreviewPrintsUnderRow(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolStarted, data: map[string]any{
		"call_id": "c", "tool": "shell", "args": map[string]any{"command": "seq 3"}}})
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolResult, data: map[string]any{
		"call_id": "c", "tool": "shell", "status": "ok", "output": "1\n2\n3"}})
	printed := strings.Join(plain(c.m.Printed()), "\n")
	assert.Contains(t, printed, "▸ shell seq 3 ok")
	assert.Contains(t, printed, "1\n2\n3")
	assert.Eq(t, c.m.lastTool.Output, "1\n2\n3")
}

func TestChat_CtrlOTogglesExpandPanel(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolStarted, data: map[string]any{
		"call_id": "c", "tool": "shell", "args": map[string]any{"command": "echo hi"}}})
	c.update(eventMsg{gen: c.m.gen, typ: core.EvToolResult, data: map[string]any{
		"call_id": "c", "tool": "shell", "status": "ok", "output": "hi\nthere"}})
	c.key("ctrl+o")
	view := c.view()
	assert.Contains(t, view, "full output")
	assert.Contains(t, view, "Ctrl+O collapse")
	assert.Contains(t, view, "hi")
	assert.Contains(t, view, "there")
	c.key("esc")
	assert.NotContains(t, c.view(), "full output")
	c.key("ctrl+o")
	assert.Contains(t, c.view(), "full output")
	c.key("ctrl+o")
	assert.NotContains(t, c.view(), "full output")
}

func TestChat_CtrlOWithoutToolHints(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.key("ctrl+o")
	assert.Contains(t, c.view(), "no tool output to expand")
}
