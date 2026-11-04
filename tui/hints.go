package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

// Hints teach a command at the moment it would help. Each shows once per
// chat in the status slot (the next key press clears it) and never replaces
// another message there. Options.Hints (setting hints) turns them off.
const (
	hintSteer    = "steer"
	hintURL      = "url"
	hintRestored = "restored"
	hintTurnCap  = "turn_cap"
	hintContext  = "context"
	hintApproval = "approval"
	hintDenials  = "denials"
	hintExpand   = "expand"
	hintWindow   = "window"
	hintResume   = "resume"
)

const (
	hintContextPercent = 80 // context use at which /compact is suggested
	hintDenialRun      = 3  // denied calls in a row before the reason hint
)

// hintMsg shows a hint worked out off the event loop.
type hintMsg struct{ id, text string }

// tip shows text as hint id unless hints are off, id was shown already, or
// the status slot is busy.
func (m *Model) tip(id, text string) {
	if !m.o.Hints || m.hinted[id] || m.hint != "" {
		return
	}
	if m.hinted == nil {
		m.hinted = map[string]bool{}
	}
	m.hinted[id] = true
	m.hint = "tip: " + text
}

// tipURL suggests web_tools when text has a link the model cannot open.
func (m *Model) tipURL(text string) {
	if !m.o.WebTools && (strings.Contains(text, "http://") || strings.Contains(text, "https://")) {
		m.tip(hintURL, "web tools are off · /settings web_tools true lets the model open links")
	}
}

// tipWindow suggests setting the window of a model the catalog lacks.
func (m *Model) tipWindow(model string, known bool) {
	if !known {
		m.tip(hintWindow, fmt.Sprintf("context window of %s is a guess (%d) · /settings models.%s.context_window <n>", model, m.window, model))
	}
}

// tipRunEnd picks the hint a finished run earned, most useful first.
func (m *Model) tipRunEnd(code int, restored bool) {
	if restored {
		m.tip(hintRestored, "queued text is back in the input · Enter sends it, Ctrl+C clears it")
	}
	if code == core.ExitTurnCap {
		m.tip(hintTurnCap, "turn cap reached · /settings max_turns 100 raises it")
	}
	if m.window > 0 && m.usage.prompt*100/m.window >= hintContextPercent {
		m.tip(hintContext, fmt.Sprintf("context %d%% full · /compact frees it", m.usage.prompt*100/m.window))
	}
}

// tipToolResult counts denials in a row and notices output cut on screen.
func (m *Model) tipToolResult(status string) {
	if status == "denied" {
		m.denials++
	} else {
		m.denials = 0
	}
	if m.denials >= hintDenialRun {
		m.tip(hintDenials, "at the prompt, type why instead of n · the model reads it and changes course")
	}
	if previewTools(m.lastTool.Tool) && outputLines(m.lastTool.Output) > transcriptPreviewLines {
		m.tip(hintExpand, "Ctrl+O expands the latest tool output")
	}
}

// tipResume checks off the event loop whether this project has earlier
// sessions to reopen.
func (m *Model) tipResume(current string) tea.Cmd {
	if !m.o.Hints || m.hinted[hintResume] {
		return nil
	}
	b := m.b
	return func() tea.Msg {
		if list, err := b.Sessions(current); err == nil && len(list) > 0 {
			return hintMsg{hintResume, "/resume or jas --continue reopens an earlier session"}
		}
		return nil
	}
}
