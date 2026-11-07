package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/tools"
)

// bangCapture bounds the bytes a ! command keeps: a multiple of the agent
// tool's output cap, like the shell tool's capture.
const bangCapture = 4 * core.DefaultMaxOutput

// bangRun is the owner's ! command while it runs.
type bangRun struct {
	command string
	cancel  context.CancelFunc
}

// bangDoneMsg reports a finished ! command. local is a !! command, whose
// output never reaches the agent.
type bangDoneMsg struct {
	command   string
	output    string
	code      int
	err       error
	cancelled bool
	local     bool
}

// isBang reports whether submitted text is a ! command.
func isBang(text string) bool { return strings.HasPrefix(text, "!") }

// parseBang splits "!cmd" / "! cmd" / "!!cmd" into the command and whether
// its output stays out of the agent's context.
func parseBang(text string) (command string, local bool) {
	rest := strings.TrimPrefix(text, "!")
	if strings.HasPrefix(rest, "!") {
		rest, local = rest[1:], true
	}
	return strings.TrimSpace(rest), local
}

// runBang runs the owner's shell command in the project root, outside the
// approval ladder: the owner typed it. It never starts a turn; a ! command's
// output goes in front of the next message (takeNotes).
func (m *Model) runBang(text string) tea.Cmd {
	command, local := parseBang(text)
	switch {
	case command == "":
		m.hint = "type !command to run it (output goes to the agent), or !!command to keep it local"
		return nil
	case m.viewing != "":
		m.hint = "! commands run in the main chat; switch back first"
		return nil
	case m.bang != nil:
		m.hint = "a ! command is already running · Ctrl+C cancels it"
		return nil
	}
	m.input.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	m.bang = &bangRun{command: command, cancel: cancel}
	b, send := m.b, m.send
	go func() {
		defer cancel()
		out := &capped{max: bangCapture}
		code, err := b.Shell(ctx, command, out)
		send(bangDoneMsg{
			command: command, output: out.String(), code: code, err: err,
			cancelled: ctx.Err() != nil, local: local,
		})
	}()
	return nil
}

// onBangDone prints the command's row and output, and holds a ! command's
// output for the next message.
func (m *Model) onBangDone(msg bangDoneMsg) tea.Cmd {
	m.bang = nil
	output := m.o.Redact(msg.output)
	status, note := "ok", ""
	var exitErr interface{ ExitCode() int }
	switch {
	case msg.cancelled:
		status, note = "cancelled", "[cancelled]"
	case errors.As(msg.err, &exitErr):
		status, note = "error", fmt.Sprintf("[exit status %d]", msg.code)
	case msg.err != nil:
		status, note = "error", "[could not run: "+m.o.Redact(msg.err.Error())+"]"
	}
	if note != "" {
		output = strings.TrimRight(output, "\n") + "\n" + note
		output = strings.TrimLeft(output, "\n")
	}
	if !msg.local {
		m.bangNotes = append(m.bangNotes, bangNote(m.o.Redact(msg.command), output))
	}
	return m.print(m.tr.Shell(m.o.Redact(msg.command), status, output, msg.local)...)
}

// bangNote is how a command's output reaches the agent (pi's format).
func bangNote(command, output string) string {
	text := tools.Truncate([]byte(core.Sanitize(strings.TrimRight(output, "\n"))), core.DefaultMaxOutput)
	return "Ran `" + command + "`\n```\n" + text + "\n```"
}

// takeNotes returns what the next message carries in front of the owner's
// text (farm notes, then ! command outputs) and its label, and clears them.
func (m *Model) takeNotes() (note, label string) {
	var parts, labels []string
	if n, l := m.takeFarmNotes(); n != "" {
		parts, labels = append(parts, n), append(labels, l)
	}
	if len(m.bangNotes) > 0 {
		parts, labels = append(parts, m.bangNotes...), append(labels, "command output")
		m.bangNotes = nil
	}
	return strings.Join(parts, "\n\n"), strings.Join(labels, ", ")
}

// bangLine is the live-region line while a ! command runs.
func (m *Model) bangLine() string {
	if m.bang == nil {
		return ""
	}
	line := "$ running " + strings.Join(strings.Fields(core.Sanitize(m.o.Redact(m.bang.command))), " ") + " · Ctrl+C cancels"
	return dimStyle.Render(ansi.Truncate(line, m.width, "…"))
}

// capped keeps the first max bytes written and notes that more came.
type capped struct {
	sb      strings.Builder
	max     int
	dropped bool
}

var _ io.Writer = (*capped)(nil)

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.sb.Len(); room < len(p) {
		c.sb.Write(p[:max(room, 0)])
		c.dropped = true
		return len(p), nil
	}
	c.sb.Write(p)
	return len(p), nil
}

func (c *capped) String() string {
	s := strings.ToValidUTF8(c.sb.String(), "�")
	if c.dropped {
		s += "\n[truncated]"
	}
	return s
}

// Shell renders the owner's ! command: a tool-style row and an output
// preview. The row is the last tool output for Ctrl+O.
func (t *Transcript) Shell(command, status, output string, local bool) []string {
	marker := "!"
	if local {
		marker = "!!"
	}
	t.lastTool = ToolOutput{Tool: marker, Subject: core.Sanitize(command), Status: status, Output: output}
	row := fmt.Sprintf("%s %s %s", marker, core.Sanitize(command), shellOutcome(status, output))
	out := append(t.enter(roleTool), statusRow(row, status, t.Width))
	return append(out, outputPreview("shell", output, t.Width)...)
}

// shellOutcome is the row's tail: ok, the exit status or cancelled.
func shellOutcome(status, output string) string {
	switch status {
	case "ok":
		return "ok"
	case "error":
		lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
		last := lines[len(lines)-1]
		if n, ok := strings.CutPrefix(last, "[exit status "); ok {
			return "exit " + strings.TrimSuffix(n, "]")
		}
		return "failed"
	}
	return status
}
