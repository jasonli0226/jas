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

// bangDoneMsg reports a finished ! command. run.Local is a !! command, whose
// output never reaches the agent. logErr is a failure to log it.
type bangDoneMsg struct {
	run    core.ShellRun
	logErr error
}

// bangWaitNotice refuses a session switch while a ! command runs, so the
// command's log entry lands in the session it ran in.
const bangWaitNotice = "a ! command is running · wait for it or press Ctrl+C"

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
	b, send, redact, session := m.b, m.send, m.o.Redact, m.live.Session
	go func() {
		defer cancel()
		out := &capped{max: bangCapture}
		code, err := b.Shell(ctx, command, out)
		run := shellRun(command, out, code, err, ctx.Err() != nil, local, redact)
		// Logged here, not in Update: the session log forwards events to the
		// chat's queue, and posting from inside Update would hang it.
		send(bangDoneMsg{run: run, logErr: session.RecordShell(run)})
	}()
	return nil
}

// shellRun is a finished command as it is logged, redacted.
func shellRun(command string, out *capped, code int, err error, cancelled, local bool, redact func(string) string) core.ShellRun {
	r := core.ShellRun{
		Command: redact(command), Output: redact(out.String()),
		Cancelled: cancelled, Truncated: out.dropped, Local: local,
	}
	var exitErr interface{ ExitCode() int }
	switch {
	case errors.As(err, &exitErr):
		r.ExitCode = code
	case err != nil:
		r.Error = redact(err.Error())
	}
	return r
}

// shellOutput is a logged command's row status and its output with a closing
// note when it did not end ok.
func shellOutput(r core.ShellRun) (status, output string) {
	status, output = "ok", r.Output
	note := ""
	switch {
	case r.Cancelled:
		status, note = "cancelled", "[cancelled]"
	case r.Error != "":
		status, note = "error", "[could not run: "+r.Error+"]"
	case r.ExitCode != 0:
		status, note = "error", fmt.Sprintf("[exit status %d]", r.ExitCode)
	}
	if note != "" {
		output = strings.TrimLeft(strings.TrimRight(output, "\n")+"\n"+note, "\n")
	}
	return status, output
}

// pendingBangNotes are the notes a resumed session's next message carries:
// the ! commands it logged after its last message.
func pendingBangNotes(runs []core.ShellRun) []string {
	var notes []string
	for _, r := range runs {
		_, output := shellOutput(r)
		notes = append(notes, bangNote(r.Command, output))
	}
	return notes
}

// onBangDone prints the command's row and output, and holds a ! command's
// output for the next message.
func (m *Model) onBangDone(msg bangDoneMsg) tea.Cmd {
	m.bang = nil
	r := msg.run
	status, output := shellOutput(r)
	if !r.Local {
		m.bangNotes = append(m.bangNotes, bangNote(r.Command, output))
	}
	blocks := m.tr.Shell(r.Command, status, output, r.Local)
	if msg.logErr != nil {
		blocks = append(blocks, errStyle.Render("event log error: "+m.clean(msg.logErr.Error())))
	}
	return m.print(blocks...)
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
