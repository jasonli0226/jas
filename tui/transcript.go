package tui

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
)

// transcriptDiffLines caps the diff rows a tool result prints.
const transcriptDiffLines = 30

// Transcript turns streamed text and agent events into finished, styled
// blocks to print into scrollback. It is pure: no I/O, no Bubble Tea types.
type Transcript struct {
	ShowThinking bool
	Width        int
	// Skills maps each SKILL.md path to its skill name, so a read of one
	// shows as a skill row.
	Skills map[string]string

	text      mdStream
	reasoning mdStream
	thinking  bool
	thinkFrom time.Time
	calls     map[string]map[string]any // call_id -> args from tool_started
	running   []startedCall             // tools started and not yet settled, in order
	last      role                      // group of the last printed block
	marked    bool                      // the agent marker went out this turn
}

// role groups printed blocks; a blank line separates consecutive groups so
// user, agent and tool blocks stay apart in scrollback.
type role int

const (
	roleNone role = iota
	roleUser
	roleAgent
	roleTool
	roleNotice
)

const (
	agentMarker = "● "
	skillMarker = "◆ skill "
)

// startedCall is a tool shown as running in the live region until its result.
type startedCall struct{ id, tool string }

// User renders the user's prompt as a full-width background band; later
// lines are indented under the marker, which stays for copy/paste and
// terminals without colour.
func (t *Transcript) User(text string) []string {
	body := hang(strings.TrimRight(cleanText(text), "\n"), "› ", t.Width)
	return append(t.enter(roleUser), band(userStyle, t.Width).Render(body))
}

// band stretches a background style to the terminal width, when known.
func band(st lipgloss.Style, width int) lipgloss.Style {
	if width > 0 {
		return st.Width(width)
	}
	return st
}

// Text feeds assistant text. The first text after reasoning closes the
// thinking line, which is why it takes the current time.
func (t *Transcript) Text(delta string, now time.Time) []string {
	out := t.endThinking(now)
	for _, raw := range t.text.Feed(cleanText(delta)) {
		out = append(out, t.agentBlock(raw)...)
	}
	return out
}

// Reasoning feeds a reasoning delta; it is shown dim only with ShowThinking.
func (t *Transcript) Reasoning(delta string, now time.Time) []string {
	if !t.thinking {
		t.thinking, t.thinkFrom = true, now
	}
	if !t.ShowThinking {
		return nil
	}
	var out []string
	for _, raw := range t.reasoning.Feed(cleanText(delta)) {
		out = append(out, t.enter(roleAgent)...)
		out = append(out, dimStyle.Render(raw))
	}
	return out
}

// Event handles agent events; only tool_started, tool_result and compaction
// print.
func (t *Transcript) Event(typ string, data map[string]any, now time.Time) []string {
	switch typ {
	case "tool_started":
		out := t.settle(now)
		id, _ := data["call_id"].(string)
		tool, _ := data["tool"].(string)
		args, _ := data["args"].(map[string]any)
		if t.calls == nil {
			t.calls = map[string]map[string]any{}
		}
		t.calls[id] = args
		t.running = append(t.running, startedCall{id: id, tool: tool})
		return out
	case "tool_result":
		out := t.settle(now)
		id, _ := data["call_id"].(string)
		tool, _ := data["tool"].(string)
		status, _ := data["status"].(string)
		output, _ := data["output"].(string)
		args := t.calls[id]
		delete(t.calls, id)
		t.running = slices.DeleteFunc(t.running, func(c startedCall) bool { return c.id == id })
		out = append(out, t.enter(roleTool)...)
		out = append(out, t.row(tool, args, status, output))
		if d := diffFromEvent(data); d != nil && status == "ok" {
			out = append(out, renderDiff(d, t.Width, transcriptDiffLines)...)
		}
		return out
	case "compaction":
		row := fmt.Sprintf("· compacted earlier messages (%s → %s tok)",
			formatTokens(intField(data, "tokens_before")), formatTokens(intField(data, "tokens_after")))
		out := append(t.settle(now), t.enter(roleNotice)...)
		return append(out, dimStyle.Render(row))
	}
	return nil
}

// History renders saved messages with the live chrome, for a resume. Tool
// results pair with their tool_use by id for the row's subject.
func (t *Transcript) History(msgs []providers.Message, redact func(string) string, now time.Time) []string {
	var out []string
	uses := map[string]providers.ContentBlock{}
	for _, msg := range msgs {
		if _, ok := core.IsSummary(msg); ok {
			out = append(out, t.Notice(dimStyle.Render("· compacted earlier messages"))...)
			continue
		}
		for _, b := range msg.Content {
			switch {
			case b.Type == providers.BlockToolUse:
				uses[b.ToolUseID] = b
			case b.Type == providers.BlockToolResult:
				out = append(out, t.historyTool(uses[b.ToolUseID], b, redact)...)
			case b.Type == providers.BlockText && msg.Role == providers.RoleUser:
				out = append(out, t.User(redact(b.Text))...)
			case b.Type == providers.BlockText:
				out = append(out, t.Text(redact(b.Text), now)...)
				out = append(out, t.settle(now)...)
			}
		}
	}
	return out
}

// historyTool renders a saved tool result as the final row of its call.
func (t *Transcript) historyTool(use, res providers.ContentBlock, redact func(string) string) []string {
	var args map[string]any
	_ = json.Unmarshal([]byte(redact(string(use.Input))), &args) // no args: the row has no subject
	status := "ok"
	if res.IsError {
		status = "error"
	}
	return append(t.enter(roleTool), t.row(use.Name, args, status, redact(res.Text)))
}

// Notice prints jas-owned notice blocks as their own group.
func (t *Transcript) Notice(blocks ...string) []string {
	return append(t.enter(roleNotice), blocks...)
}

// Flush ends a run: it prints the partial block and any pending thinking
// line, and drops running rows whose result never came.
func (t *Transcript) Flush(now time.Time) []string {
	t.running = nil
	return t.settle(now)
}

// Live is the rendered partial block and the rows of tools still running, for
// the live region. Each has the gap and chrome it will have once printed, so
// nothing shifts when its final form lands in scrollback.
func (t *Transcript) Live() string {
	var parts []string
	if t.thinking && t.ShowThinking {
		if p := t.reasoning.Pending(); strings.TrimSpace(p) != "" {
			parts = append(parts, dimStyle.Render(p))
		}
	}
	if p := t.text.Pending(); strings.TrimSpace(p) != "" {
		parts = append(parts, hang(renderBlock(p), t.nextAgentPrefix(), t.Width))
	}
	if len(parts) > 0 && t.last != roleNone && (t.last != roleAgent || t.marked) {
		parts = append([]string{""}, parts...)
	}
	if len(t.running) > 0 && (len(parts) > 0 || t.last != roleNone && t.last != roleTool) {
		parts = append(parts, "")
	}
	for _, c := range t.running {
		parts = append(parts, t.row(c.tool, t.calls[c.id], "running", ""))
	}
	return strings.Join(parts, "\n")
}

// enter switches the printed group to r, returning the blank separator line
// when the group changes after something was already printed.
func (t *Transcript) enter(r role) []string {
	if t.last == r {
		return nil
	}
	prev := t.last
	t.last, t.marked = r, false
	if prev == roleNone {
		return nil
	}
	return []string{""}
}

// agentBlock renders a finished assistant block: the turn's first block gets
// the agent marker, the rest hang under it after a blank line.
func (t *Transcript) agentBlock(raw string) []string {
	out := t.enter(roleAgent)
	if t.marked {
		out = append(out, "")
	}
	prefix := t.nextAgentPrefix()
	t.marked = true
	return append(out, hang(renderBlock(raw), prefix, t.Width))
}

func (t *Transcript) nextAgentPrefix() string {
	if t.last == roleAgent && t.marked {
		return hangIndent
	}
	return assistantStyle.Render(agentMarker)
}

// hang prefixes the first line of block with first and indents the rest,
// wrapping to width first so wrapped rows stay under the marker too.
func hang(block, first string, width int) string {
	if w := width - len(hangIndent); w > 0 {
		block = ansi.Wrap(block, w, "")
	}
	lines := strings.Split(block, "\n")
	for i := range lines {
		if i == 0 {
			lines[i] = first + lines[i]
		} else {
			lines[i] = hangIndent + lines[i]
		}
	}
	return strings.Join(lines, "\n")
}

// Settle prints what settle holds back, for output that must land after
// the model's text but before its next tool row (an approval diff).
func (t *Transcript) Settle(now time.Time) []string { return t.settle(now) }

// ToolGroup enters the tool group, returning its blank separator if any.
func (t *Transcript) ToolGroup() []string { return t.enter(roleTool) }

// settle closes thinking and prints the partial text block, preserving order
// before a tool row or at the end of a run.
func (t *Transcript) settle(now time.Time) []string {
	out := t.endThinking(now)
	if raw, ok := t.text.Flush(); ok {
		out = append(out, t.agentBlock(raw)...)
	}
	return out
}

func (t *Transcript) endThinking(now time.Time) []string {
	if !t.thinking {
		return nil
	}
	t.thinking = false
	out := t.enter(roleAgent)
	if raw, ok := t.reasoning.Flush(); ok && t.ShowThinking {
		out = append(out, dimStyle.Render(raw))
	}
	secs := max(int(now.Sub(t.thinkFrom)/time.Second), 1)
	return append(out, dimStyle.Render(fmt.Sprintf("· thought for %ds", secs)))
}

// row renders a tool call, or a skill row when it reads a known SKILL.md.
func (t *Transcript) row(tool string, args map[string]any, status, output string) string {
	if name, ok := t.skillRead(tool, args); ok {
		out := "loaded"
		if status != "ok" {
			out = outcome(tool, status, output)
		}
		return statusRow(fmt.Sprintf("%s%s %s", skillMarker, core.Sanitize(name), out), status, t.Width)
	}
	return toolRow(tool, args, status, output, t.Width)
}

// skillRead returns the skill whose SKILL.md a read call loads.
func (t *Transcript) skillRead(tool string, args map[string]any) (string, bool) {
	p, _ := args["path"].(string)
	if tool != "read" || p == "" {
		return "", false
	}
	// Clean both sides: on Windows Clean also turns / into \, so a raw
	// key would never match.
	p = filepath.Clean(p)
	for loc, name := range t.Skills {
		if filepath.Clean(loc) == p {
			return name, true
		}
	}
	return "", false
}

// SkillUser renders a /skill invocation: the skill marker, then the prompt
// as typed, in one group.
func (t *Transcript) SkillUser(name, shown string) []string {
	out := append(t.enter(roleUser), dimStyle.Render(skillMarker+core.Sanitize(name)))
	return append(out, t.User(shown)...)
}

// toolRow renders "▸ <tool> <subject> <outcome>", cut to width with an
// ellipsis, on a band coloured by status.
func toolRow(tool string, args map[string]any, status, output string, width int) string {
	row := fmt.Sprintf("▸ %s %s %s", core.Sanitize(tool), subject(tool, args), outcome(tool, status, output))
	return statusRow(row, status, width)
}

// statusRow cuts row to width and puts it on a band coloured by status.
func statusRow(row, status string, width int) string {
	row = strings.Join(strings.Fields(row), " ")
	if width > 0 {
		row = ansi.Truncate(row, width, "…")
	}
	bg := toolOtherBg
	switch status {
	case "ok":
		bg = toolOkBg
	case "error", "denied", "cancelled":
		bg = toolErrBg
	}
	return band(toolStyle.Background(bg), width).Render(row)
}

func argString(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return core.Sanitize(s)
}

func subject(tool string, args map[string]any) string {
	switch tool {
	case "read", "write", "edit":
		return argString(args, "path")
	case "ls":
		if p := argString(args, "path"); p != "" {
			return p
		}
		return "."
	case "grep":
		return argString(args, "pattern")
	case "shell":
		return argString(args, "command")
	}
	raw, _ := json.Marshal(args)
	return core.SummarizeCall(tool, raw)
}

func outcome(tool, status, output string) string {
	switch status {
	case "ok":
		n := outputLines(output)
		switch tool {
		case "read":
			return fmt.Sprintf("(%d lines)", n)
		case "ls":
			return fmt.Sprintf("(%d entries)", n)
		case "grep":
			return fmt.Sprintf("(%d matches)", n)
		}
		return "ok"
	case "error":
		first, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
		if first = core.Sanitize(first); first != "" {
			return "failed: " + first
		}
		return "failed"
	}
	return core.Sanitize(status)
}

func outputLines(output string) int {
	output = strings.TrimRight(output, "\n")
	if output == "" {
		return 0
	}
	return strings.Count(output, "\n") + 1
}
