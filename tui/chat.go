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
}

// Model is the inline chat: a transcript printed into scrollback above a live
// region holding the partial reply, queued steering, the input and a status line.
type Model struct {
	b    Backend
	o    Options
	send func(tea.Msg) // posts messages from other goroutines into Update
	gate core.Gate

	tr    Transcript
	input textarea.Model
	width int

	live   *Live
	gen    int // bumped for every new or resumed session
	window int
	usage  struct{ prompt, total int }
	run    *activeRun
	queued []string // steering sent during the current run
	err    error    // fatal error returned by Run

	lastCtrlC time.Time
	hint      string // replaces the status line until the next key
	quitting  bool
	closing   bool // Live.Close is running
	closed    bool

	printed  []string // every block handed to tea.Println, in order
	outbox   []string // blocks waiting for the in-flight print to finish
	printing bool
}

// Messages the model posts to itself.
type (
	sessionMsg struct {
		gen     int
		live    *Live
		notices []string
		err     error
	}
	printedMsg struct{}
	closedMsg  struct{ err error }
)

// New returns the chat model for b.
func New(b Backend, o Options) *Model {
	if o.Now == nil {
		o.Now = time.Now
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
	return &Model{
		b: b, o: o, input: in, width: defaultWidth,
		tr: Transcript{Width: defaultWidth},
		// Placeholder until the approval prompt exists: gated calls end the
		// run with approval_needed instead of running unasked.
		gate: &core.Approver{Level: o.Level},
	}
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
func (m *Model) Init() tea.Cmd { return m.openSession() }

// openSession opens a new session, or with Continue resumes the newest one.
func (m *Model) openSession() tea.Cmd {
	m.gen++
	gen, b, gate, o := m.gen, m.b, m.gate, m.o
	ui := &bridge{gen: gen, send: m.send}
	return func() tea.Msg {
		var notices []string
		if o.Continue {
			infos, err := b.Sessions("")
			if err != nil {
				return sessionMsg{gen: gen, err: err}
			}
			if len(infos) > 0 {
				live, notices, err := b.Resume(infos[0].Path, ui, gate)
				return sessionMsg{gen, live, notices, err}
			}
			notices = append(notices, "no saved session for this project; starting a new one")
		}
		live, err := b.NewSession(o.Model, ui, gate)
		return sessionMsg{gen, live, notices, err}
	}
}

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
		if !m.quitting {
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
	case printedMsg:
		m.printing = false
		return m, m.flush()
	case closedMsg:
		return m, m.onClosed(msg)
	}
	return m, nil
}

func (m *Model) onSession(msg sessionMsg) tea.Cmd {
	if msg.gen != m.gen {
		return nil
	}
	if msg.err != nil {
		m.err = msg.err
		m.quitting = true
		return m.print(errStyle.Render("open session: " + msg.err.Error()))
	}
	m.live = msg.live
	m.window = m.b.Window(msg.live.Model)
	if msg.live.Budget != nil && msg.live.Budget.Window > 0 {
		m.window = msg.live.Budget.Window
	}
	m.usage.prompt, m.usage.total = 0, 0
	blocks := make([]string, len(msg.notices))
	for i, n := range msg.notices {
		blocks[i] = dimStyle.Render(n)
	}
	return m.print(blocks...)
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var parts []string
	if live := m.tr.Live(); live != "" {
		parts = append(parts, live)
	}
	for _, q := range m.queued {
		line := "↳ " + strings.Join(strings.Fields(q), " ")
		parts = append(parts, dimStyle.Render(ansi.Truncate(line, m.width, "…")))
	}
	if p := m.prompt(); p != "" {
		parts = append(parts, p)
	}
	parts = append(parts, m.input.View(), dimStyle.Render(m.statusLine()))
	// The zero KeyboardEnhancements still requests basic key disambiguation
	// (Bubble Tea always sets that flag), which is what reports Shift+Enter
	// apart from Enter on terminals that support it.
	return tea.NewView(strings.Join(parts, "\n"))
}

// prompt is the slot between the queued list and the input where the
// approval prompt and pickers render; empty until they exist.
func (m *Model) prompt() string { return "" }

func (m *Model) statusLine() string {
	if m.hint != "" {
		return ansi.Truncate(m.hint, m.width, "…")
	}
	model := m.o.Model
	if m.live != nil {
		model = m.live.Model
	}
	return statusInfo{
		Model: model, Level: m.o.Level, Prompt: m.usage.prompt, Window: m.window,
		Tokens: m.usage.total, Root: m.o.Root, Running: m.running(), Queued: len(m.queued),
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
