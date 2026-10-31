package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

// Transcript turns streamed text and agent events into finished, styled
// blocks to print into scrollback. It is pure: no I/O, no Bubble Tea types.
type Transcript struct {
	ShowThinking bool
	Width        int

	text      mdStream
	reasoning mdStream
	thinking  bool
	thinkFrom time.Time
	calls     map[string]map[string]any // call_id -> args from tool_started
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

const agentMarker = "● "

// User renders the user's prompt; later lines are indented under the marker.
func (t *Transcript) User(text string) []string {
	lines := strings.Split(strings.TrimRight(cleanText(text), "\n"), "\n")
	return append(t.enter(roleUser), userStyle.Render("› "+strings.Join(lines, "\n"+hangIndent)))
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
		args, _ := data["args"].(map[string]any)
		if t.calls == nil {
			t.calls = map[string]map[string]any{}
		}
		t.calls[id] = args
		return out
	case "tool_result":
		out := t.settle(now)
		id, _ := data["call_id"].(string)
		tool, _ := data["tool"].(string)
		status, _ := data["status"].(string)
		output, _ := data["output"].(string)
		args := t.calls[id]
		delete(t.calls, id)
		out = append(out, t.enter(roleTool)...)
		return append(out, toolRow(tool, args, status, output, t.Width))
	case "compaction":
		row := fmt.Sprintf("· compacted earlier messages (%s → %s tok)",
			formatTokens(intField(data, "tokens_before")), formatTokens(intField(data, "tokens_after")))
		out := append(t.settle(now), t.enter(roleNotice)...)
		return append(out, dimStyle.Render(row))
	}
	return nil
}

// Flush ends a run: it prints the partial block and any pending thinking line.
func (t *Transcript) Flush(now time.Time) []string { return t.settle(now) }

// Live is the rendered partial block for the live region, with the gap and
// chrome it will have once printed so the text does not shift on handoff.
func (t *Transcript) Live() string {
	var parts []string
	if t.thinking && t.ShowThinking {
		if p := t.reasoning.Pending(); strings.TrimSpace(p) != "" {
			parts = append(parts, dimStyle.Render(p))
		}
	}
	if p := t.text.Pending(); strings.TrimSpace(p) != "" {
		parts = append(parts, hang(renderBlock(p), t.nextAgentPrefix()))
	}
	if len(parts) > 0 && t.last != roleNone && t.last != roleAgent {
		parts = append([]string{""}, parts...)
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
// the agent marker, the rest hang under it.
func (t *Transcript) agentBlock(raw string) []string {
	out := t.enter(roleAgent)
	prefix := t.nextAgentPrefix()
	t.marked = true
	return append(out, hang(renderBlock(raw), prefix))
}

func (t *Transcript) nextAgentPrefix() string {
	if t.last == roleAgent && t.marked {
		return hangIndent
	}
	return assistantStyle.Render(agentMarker)
}

// hang prefixes the first line of block with first and indents the rest.
func hang(block, first string) string {
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

// toolRow renders "▸ <tool> <subject> <outcome>", cut to width with an ellipsis.
func toolRow(tool string, args map[string]any, status, output string, width int) string {
	row := fmt.Sprintf("▸ %s %s %s", core.Sanitize(tool), subject(tool, args), outcome(tool, status, output))
	row = strings.Join(strings.Fields(row), " ")
	if width > 0 {
		row = ansi.Truncate(row, width, "…")
	}
	switch status {
	case "error", "denied", "cancelled":
		return errStyle.Render(row)
	}
	return dimStyle.Render(row)
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
