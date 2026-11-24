package tui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

var errNoProfile = errors.New("no such profile")

func (c *testChat) pumpProfileDone() {
	c.t.Helper()
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(profileDoneMsg); return ok })
}

func TestCmdProfile_PickerListsAndMarksActive(t *testing.T) {
	b := newTestBackend(t)
	b.profiles = []string{"default", "ds"}
	b.profile = "ds"
	c := newTestChat(t, b)
	c.submit("/profile")
	if c.m.picker == nil {
		t.Fatal("no picker")
	}
	assert.Eq(t, c.m.picker.canDefault, true)
	var active string
	for _, it := range c.m.picker.items {
		if it.desc == "active" {
			active = it.value
		}
	}
	assert.Eq(t, active, "ds")
	assert.Eq(t, len(c.m.picker.items), 2)
}

func TestCmdProfile_SwitchUpdatesStatusAndAppliesLive(t *testing.T) {
	b := newTestBackend(t)
	b.profSwitch = ProfileSwitch{Notices: []string{"profile ds loaded"}, Live: map[string]string{"approve": "shell"}}
	c := newTestChatOpts(t, b, Options{Level: core.Never})
	c.submit("/profile ds")
	assert.Eq(t, c.m.reloading, true)
	c.pumpProfileDone()
	assert.Eq(t, c.m.reloading, false)
	assert.Eq(t, c.m.gate.Level(), core.ShellOnly)
	assert.Contains(t, c.view(), "ds · ")
	assert.Contains(t, c.printedText(), "profile ds loaded")
}

func TestCmdProfile_SwitchPrintsProfileRow(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("/profile ds")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "[Profile] ds")
}

func TestCmdProfile_DeferredNotice(t *testing.T) {
	b := newTestBackend(t)
	b.profSwitch = ProfileSwitch{Deferred: []string{"max_steps", "tools"}}
	c := newTestChat(t, b)
	c.submit("/profile ds")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "max_steps, tools take effect on /new")
}

func TestCmdProfile_RefusedWhileRunning(t *testing.T) {
	b := newTestBackend(t, fake.Turn{Block: true})
	c := newTestChat(t, b)
	c.submit("go")
	c.submit("/profile ds")
	assert.Contains(t, c.printedText(), "wait for the reply or press Esc first")
	assert.Eq(t, c.m.reloading, false)
	assert.Eq(t, b.Profile(), "default")
}

func TestCmdProfile_SameNameReloads(t *testing.T) {
	b := newTestBackend(t)
	b.profSwitch = ProfileSwitch{Notices: []string{"reloaded · AGENTS.md loaded"}}
	c := newTestChat(t, b)
	c.submit("/profile default")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "reloaded · AGENTS.md loaded")
	assert.Eq(t, b.Profile(), "default")
}

func TestCmdProfile_SwitchError(t *testing.T) {
	b := newTestBackend(t)
	b.profErr = errNoProfile
	c := newTestChat(t, b)
	c.submit("/profile nope")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "profile: no such profile")
	assert.Eq(t, c.m.reloading, false)
}

func TestCmdProfile_SwitchInFlightRefusesOthers(t *testing.T) {
	b := newTestBackend(t)
	b.farmer = &fakeFarmer{available: true}
	c := newTestChat(t, b)
	c.m.reloading = true // a switch is in flight
	for _, cmd := range []string{`/farm "do it"`, "/farm run plan.yaml", "/new", "/resume", "/reload", "/profile ds"} {
		c.m.input.Reset()
		before := len(c.printedText())
		c.submit(cmd)
		assert.Contains(t, c.printedText()[before:], "wait")
	}
	assert.Eq(t, b.farmer.(*fakeFarmer).started, "")
	assert.Eq(t, len(b.farmer.(*fakeFarmer).tasks), 0)
}

func TestPicker_CtrlSProfileSetsDefault(t *testing.T) {
	b := newTestBackend(t)
	b.profiles = []string{"default", "ds"}
	c := newTestChat(t, b)
	c.submit("/profile")
	c.key("down")
	c.key("ctrl+s")
	c.pumpProfileDone()
	assert.Eq(t, len(b.profDefaults), 1)
	assert.Eq(t, b.profDefaults[0], "ds")
	assert.Eq(t, b.Profile(), "ds")
}

func TestPicker_CtrlSModelStillSetsDefaultModel(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.submit("/model")
	c.key("ctrl+s")
	assert.Eq(t, len(b.defaults), 1)
}

func TestCmdProfile_RenderDuringSwitchLeavesBackendAlone(t *testing.T) {
	b := newTestBackend(t)
	b.profGate = make(chan struct{})
	c := newTestChat(t, b)
	c.submit("/profile ds")
	for !b.switching.Load() {
		time.Sleep(time.Millisecond)
	}
	assert.Contains(t, c.view(), "default · ")
	c.m.banner()
	c.m.statusLine()
	assert.Eq(t, b.touched.Load(), int32(0))
	close(b.profGate)
	c.pumpProfileDone()
	assert.Contains(t, c.view(), "ds · ")
}

func TestCmdProfile_InputRefusedWhileSwitching(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	c.m.reloading = true
	for _, in := range []string{"hello", "/skill:review x", "!echo hi"} {
		c.m.input.Reset()
		before := len(c.printedText())
		c.submit(in)
		assert.Contains(t, c.printedText()[before:], "wait for the reply or press Esc first")
		assert.Eq(t, c.m.running(), false)
		assert.Eq(t, c.m.bang == nil, true)
	}
}

func TestPicker_CtrlSProfileStoreFailureDoesNotSwitch(t *testing.T) {
	b := newTestBackend(t)
	b.profiles = []string{"default", "ds"}
	b.profDefErr = errors.New("disk full")
	c := newTestChat(t, b)
	c.submit("/profile")
	c.key("down")
	c.key("ctrl+s")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "set default: disk full")
	assert.Eq(t, b.Profile(), "default")
}

func TestPicker_CtrlSProfileSwitchFailureSaysStored(t *testing.T) {
	b := newTestBackend(t)
	b.profiles = []string{"default", "ds"}
	b.profErr = errNoProfile
	c := newTestChat(t, b)
	c.submit("/profile")
	c.key("down")
	c.key("ctrl+s")
	c.pumpProfileDone()
	assert.Contains(t, c.printedText(), "the default profile was stored")
}

func TestCmdProfile_RefreshesTranscriptSkills(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	b.skillFiles = map[string]string{"/p/ds/skills/demo/SKILL.md": "demo"}
	c.submit("/profile ds")
	c.pumpProfileDone()
	assert.Eq(t, c.m.tr.Skills["/p/ds/skills/demo/SKILL.md"], "demo")
	assert.Eq(t, c.m.tr.Skills["/skills/review/SKILL.md"], "")
}

func TestCmdReload_RefreshesTranscriptSkills(t *testing.T) {
	b := newTestBackend(t)
	c := newTestChat(t, b)
	b.skillFiles = map[string]string{"/new/skills/fresh/SKILL.md": "fresh"}
	c.submit("/reload")
	c.pumpAsk()
	c.submit("y")
	c.pumpUntil(func(msg tea.Msg) bool { _, ok := msg.(reloadDoneMsg); return ok })
	assert.Eq(t, c.m.tr.Skills["/new/skills/fresh/SKILL.md"], "fresh")
}
