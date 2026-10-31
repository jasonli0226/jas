package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/core"
)

const (
	defaultWidth   = 80
	inputMaxHeight = 10
)

// Options configures the chat.
type Options struct {
	Level    core.Level
	Model    string
	Root     string // project folder shown in the status line
	Windows  bool
	Continue bool // resume the newest session for this project at start
	Now      func() time.Time
	Redact   func(string) string // masks secrets in text the chat prints; nil is identity
}

// Model is the inline chat: a transcript printed into scrollback above a live
// region holding the partial reply, queued steering, the input and a status line.
type Model struct {
	b    Backend
	o    Options
	send func(tea.Msg) // posts messages from other goroutines into Update
	gate *Gate         // one per chat, so grants outlive /new

	tr    Transcript
	input textarea.Model
	width int

	live     *Live
	closer   *sessionCloser // closes live
	opening  *pendingOpen   // the session being opened, until sessionMsg
	gen      int            // generation of live; output tagged otherwise is dropped
	lastGen  int            // the newest generation handed out
	model    string         // used by /new; follows /model and /resume
	window   int
	branch   string // git branch of the root, refreshed per session and run
	usage    usage
	run      *activeRun
	queued   []string     // steering sent during the current run
	approval *approvalMsg // the open approval prompt
	picker   *picker      // the open picker, shown in place of the input
	onPick   func(value string) tea.Cmd
	reply    string // the last final reply, for /copy
	err      error  // fatal error returned by Run

	lastCtrlC time.Time
	hint      string // replaces the status line until the next key
	quitting  bool
	closing   bool // Live.Close is running
	closed    bool

	printed  []string // every block handed to tea.Println, in order
	outbox   []string // blocks waiting for the in-flight print to finish
	printing bool
}

// usage is the token use seen in this chat's runs on the current session.
type usage struct {
	prompt  int // prompt tokens of the last request
	in, out int // totals
}

// Messages the model posts to itself.
type (
	printedMsg struct{}
	closedMsg  struct{ err error }
)

// New returns the chat model for b.
func New(b Backend, o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	in := textarea.New()
	in.Prompt = "› "
	in.ShowLineNumbers = false
	in.DynamicHeight = true
	in.MinHeight = 1
	in.MaxHeight = inputMaxHeight
	in.SetHeight(1)
	// Enter submits; only Alt+Enter and Shift+Enter insert a newline.
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter"))
	st := textarea.DefaultDarkStyles()
	st.Cursor.Blink = false
	st.Focused.CursorLine = lipgloss.NewStyle()
	in.SetStyles(st)
	in.SetWidth(defaultWidth)
	in.Focus()
	m := &Model{
		b: b, o: o, input: in, width: defaultWidth, model: o.Model,
		tr: Transcript{Width: defaultWidth},
	}
	// send is set after New (Run needs the program first), so read it late.
	m.gate = NewGate(o.Level, o.Windows, o.Redact, func(msg tea.Msg) { m.send(msg) })
	return m
}

// Run runs the chat on in/out until the user quits or ctx ends.
func Run(ctx context.Context, b Backend, o Options, in io.Reader, out io.Writer) error {
	m := New(b, o)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))
	m.send = p.Send
	_, err := p.Run()
	return errors.Join(m.err, err, m.shutdown())
}

// Init opens the first session.
func (m *Model) Init() tea.Cmd { return m.openFirst() }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.tr.Width = msg.Width
		m.input.SetWidth(msg.Width)
	case tea.KeyPressMsg:
		return m, m.onKey(msg)
	case tea.PasteMsg:
		if !m.quitting && m.picker == nil {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case sessionMsg:
		return m, m.onSession(msg)
	case textMsg:
		if msg.gen == m.gen {
			return m, m.print(m.tr.Text(msg.s, m.o.Now())...)
		}
	case reasoningMsg:
		if msg.gen == m.gen {
			return m, m.print(m.tr.Reasoning(msg.s, m.o.Now())...)
		}
	case eventMsg:
		if msg.gen == m.gen {
			return m, m.onEvent(msg)
		}
	case runDoneMsg:
		if msg.gen == m.gen {
			return m, m.onRunDone(msg)
		}
	case compactDoneMsg:
		if msg.gen == m.gen {
			return m, m.onCompactDone(msg)
		}
	case approvalMsg:
		m.onApproval(msg)
	case noticeMsg:
		return m, m.print(m.tr.Notice(dimStyle.Render(msg.s))...)
	case printedMsg:
		m.printing = false
		return m, m.flush()
	case closedMsg:
		return m, m.onClosed(msg)
	}
	return m, nil
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var parts []string
	if live := m.tr.Live(); live != "" {
		parts = append(parts, live)
	}
	// A blank line keeps the input apart from the agent text above it.
	parts = append(parts, "")
	for _, q := range m.queued {
		line := "↳ " + strings.Join(strings.Fields(q), " ")
		parts = append(parts, dimStyle.Render(ansi.Truncate(line, m.width, "…")))
	}
	if p := m.prompt(); p != "" {
		parts = append(parts, p)
	}
	if m.picker != nil {
		parts = append(parts, m.picker.view(m.width))
	} else {
		parts = append(parts, m.input.View())
		for _, name := range m.completions() {
			parts = append(parts, dimStyle.Render(ansi.Truncate("  /"+name, m.width, "…")))
		}
	}
	parts = append(parts, m.statusLine())
	// The zero KeyboardEnhancements still requests basic key disambiguation
	// (Bubble Tea always sets that flag), which is what reports Shift+Enter
	// apart from Enter on terminals that support it.
	return tea.NewView(strings.Join(parts, "\n"))
}

// prompt is the slot between the queued list and the input where the
// approval prompt renders.
func (m *Model) prompt() string {
	if m.approval == nil {
		return ""
	}
	return boldStyle.Render(ansi.Wrap(m.approval.prompt, m.width, ""))
}

func (m *Model) statusLine() string {
	if m.hint != "" {
		return dimStyle.Render(ansi.Truncate(m.hint, m.width, "…"))
	}
	return statusInfo{
		Model: m.model, Level: m.gate.Level(), Prompt: m.usage.prompt, Window: m.window,
		In: m.usage.in, Out: m.usage.out, Root: m.o.Root, Branch: m.branch,
		Running: m.running(), Queued: len(m.queued),
		Thinking: m.tr.thinking, ShowThinking: m.tr.ShowThinking,
	}.Render(m.width)
}

// Printed returns every block handed to tea.Println, in order (test hook).
func (m *Model) Printed() []string { return m.printed }

// print queues blocks for scrollback. Commands run concurrently, so only one
// Println is in flight at a time; it acknowledges itself with printedMsg and
// the next batch goes out then, which keeps blocks in order.
func (m *Model) print(blocks ...string) tea.Cmd {
	m.printed = append(m.printed, blocks...)
	m.outbox = append(m.outbox, blocks...)
	return m.flush()
}

// flush sends the outbox if no print is in flight. Once quitting with
// nothing left to do, it ends the program after the last print.
func (m *Model) flush() tea.Cmd {
	if m.printing {
		return nil
	}
	done := m.quitting && !m.running() && !m.closing
	if len(m.outbox) == 0 {
		if done {
			return tea.Quit
		}
		return nil
	}
	body := strings.Join(m.outbox, "\n")
	m.outbox = nil
	if done {
		return tea.Sequence(tea.Println(body), tea.Quit)
	}
	m.printing = true
	return tea.Sequence(tea.Println(body), func() tea.Msg { return printedMsg{} })
}
