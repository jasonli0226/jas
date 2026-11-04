package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
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
	Continue bool   // resume the newest session for this project at start
	Name     string // title for the first session (--name); "" leaves it as is
	Version  string // build version, shown once in the start banner
	Now      func() time.Time
	Redact   func(string) string // masks secrets in text the chat prints; nil is identity
	FarmBell bool                // ring the terminal bell when a farm needs the owner
	Hints    bool                // show one-time tips that teach a command when it would help
	WebTools bool                // web_fetch/web_search are on (no link hint)
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

	live      *Live
	closer    *sessionCloser // closes live
	opening   *pendingOpen   // the session being opened, until sessionMsg
	gen       int            // generation of live; output tagged otherwise is dropped
	lastGen   int            // the newest generation handed out
	model     string         // used by /new; follows /model and /resume
	window    int
	branch    string // git branch of the root, refreshed per session and run
	usage     usage
	run       *activeRun
	reloading bool            // /reload in progress (trust ask may be open)
	frame     int             // loader frame while a run is active
	tickPend  bool            // a loader tick timer is armed
	queued    []string        // steering sent during the current run
	followUps []string        // Ctrl+Q texts sent as the next message once the run ends ok
	hinted    map[string]bool // hints already shown this chat
	denials   int             // tool calls denied in a row, for the reason hint
	approval  *approvalMsg    // the open approval prompt
	picker    *picker         // the open picker, shown in place of the input
	onPick    func(value string) tea.Cmd
	// onPickCancel runs when the open picker is cancelled (Esc); nil: nothing.
	onPickCancel func() tea.Cmd

	farm    *farmState // this window's farm, running or the last one
	viewing string     // the agent the window shows; "" = the main chat
	held    int        // main-chat blocks recorded while viewing an agent
	// farmQuitting: the owner chose to stop the farm and quit; the quit
	// resumes when the farm has ended.
	farmQuitting bool
	stash        *inputStash  // the input set aside while a prompt has it
	ask          *farmAskMsg  // the open farm question
	askPending   []farmAskMsg // farm questions waiting for the main chat
	bells        int          // bells rung (test hook)
	farmNote     string       // farm summary for the next message to the main session
	farmGen      int          // generation of the newest farm started (farmMsg)
	farmEarly    []tea.Msg    // that farm's messages that beat farmStartedMsg
	printedFrom  int          // where the current session's transcript starts in printed
	screen       []string     // every block emitted to the screen (test hook)
	farmReview   *FarmPlan    // a plan under review (or being drafted)
	farmReplan   *FarmPlan    // a plan waiting for replan feedback in the input
	reply        string       // the last final reply, for /copy

	slashFor    string // input text the / menu cursor belongs to
	slashCursor int
	slashHidden string // input text at which Esc dismissed the / menu
	hist        history
	err         error // fatal error returned by Run

	lastCtrlC time.Time
	hint      string // replaces the status line until the next key
	quitting  bool
	closing   bool // Live.Close is running
	closed    bool

	lastTool      ToolOutput // most recent tool_result, for Ctrl+O
	toolExpand    bool       // live-region full-output panel open
	toolExpandOff int        // scroll offset into lastTool.Output
	height        int        // terminal rows; used to cap the expand panel

	printed  []string // every block handed to tea.Println, in order
	outbox   []string // blocks waiting for the in-flight print to finish
	inflight []string // blocks of the in-flight print, until printedMsg
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
	// Enter submits; Alt/Shift+Enter and Ctrl+J (portable LF) insert a newline.
	// On native Windows, onKey also rewrites plain Enter to shift+enter when
	// GetAsyncKeyState says Shift is held (terminals that drop the modifier).
	in.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter", "ctrl+j"))
	st := textarea.DefaultDarkStyles()
	st.Cursor.Blink = false
	st.Focused.CursorLine = lipgloss.NewStyle()
	in.SetStyles(st)
	in.SetWidth(defaultWidth)
	in.Focus()
	m := &Model{
		b: b, o: o, input: in, width: defaultWidth, model: o.Model,
		tr: Transcript{Width: defaultWidth, Skills: b.SkillFiles()},
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

// Init opens the first session, after the start banner — a resumed
// session (Continue) skips the banner.
func (m *Model) Init() tea.Cmd {
	if m.o.Continue {
		return m.openFirst()
	}
	return tea.Batch(m.openFirst(), m.print(m.banner()...))
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.tr.Width = msg.Width
		m.input.SetWidth(msg.Width)
		if m.farm != nil {
			m.farm.width = msg.Width
			for _, a := range m.farm.agents {
				a.tr.Width = msg.Width
			}
		}
	case tea.KeyPressMsg:
		return m, m.onKey(msg)
	case tea.PasteMsg:
		if !m.quitting && m.picker == nil {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}
	case hintMsg:
		m.tip(msg.id, msg.text)
		return m, nil
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
	case reloadDoneMsg:
		return m, m.onReloadDone(msg)
	case tickMsg:
		return m, m.onTick()
	case approvalMsg:
		return m, m.onApproval(msg)
	case farmPlannedMsg:
		return m, m.onFarmPlanned(msg)
	case farmEditedMsg:
		return m, m.onFarmEdited(msg)
	case farmStartedMsg:
		return m, m.onFarmStarted(msg)
	case farmEventMsg:
		return m, m.onFarmEvent(msg)
	case farmDoneMsg:
		return m, m.onFarmDone(msg)
	case farmTextMsg:
		return m, m.onFarmText(msg)
	case farmUndeliveredMsg:
		return m, m.onFarmUndelivered(msg)
	case farmCalledMsg:
		return m, m.onFarmCalled(msg)
	case farmSteerMsg:
		return m, m.onFarmSteer(msg)
	case farmQuitMsg:
		return m, m.quit()
	case farmAskMsg:
		return m, m.onFarmAsk(msg)
	case farmMsg:
		return m, m.onFarmMsg(msg)
	case farmWorkerEventMsg:
		return m, m.onFarmWorkerEvent(msg)
	case noticeMsg:
		return m, m.print(m.tr.Notice(dimStyle.Render(msg.s))...)
	case printedMsg:
		m.printing, m.inflight = false, nil
		return m, m.flush()
	case closedMsg:
		return m, m.onClosed(msg)
	}
	return m, nil
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	// Blocks not yet in scrollback stay on screen above the live region, so a
	// finished block never blinks out between leaving Live and its Println.
	parts := slices.Concat(m.inflight, m.outbox)
	live := m.tr.Live()
	if a := m.farmAgent(m.viewing); a != nil {
		live = a.tr.Live()
	}
	if live != "" {
		parts = append(parts, live)
	}
	if panel := m.toolExpandView(); panel != "" {
		parts = append(parts, panel)
	}
	// A blank line keeps the input apart from the agent text above it.
	parts = append(parts, "")
	queued, prefix := m.queued, "↳ "
	if a := m.farmAgent(m.viewing); a != nil {
		queued, prefix = a.queued, "↳ queued for "+a.id+": "
	}
	for _, q := range queued {
		line := prefix + strings.Join(strings.Fields(q), " ")
		parts = append(parts, dimStyle.Render(ansi.Truncate(line, m.width, "…")))
	}
	if m.viewing == "" {
		for _, q := range m.followUps {
			line := "↳ after run: " + strings.Join(strings.Fields(q), " ")
			parts = append(parts, dimStyle.Render(ansi.Truncate(line, m.width, "…")))
		}
	}
	if l := m.loader(); l != "" {
		parts = append(parts, l)
	}
	if p := m.prompt(); p != "" {
		parts = append(parts, p)
	}
	parts = append(parts, m.rule())
	if m.picker != nil {
		parts = append(parts, m.picker.view(m.width))
	} else {
		parts = append(parts, m.input.View())
		if menu := m.slashMenu(); menu != nil && len(menu.visible()) > 0 {
			parts = append(parts, menu.rows(m.width))
		}
	}
	parts = append(parts, m.rule(), m.statusLine())
	if m.viewing != "" && m.farm != nil {
		parts = append(parts, m.farmStrip())
	} else {
		parts = append(parts, m.farmRows()...)
	}
	// The zero KeyboardEnhancements still requests basic key disambiguation
	// (Bubble Tea always sets that flag): Kitty keyboard + modifyOtherKeys=2
	// report Shift+Enter where the terminal supports them. Windows Terminal
	// (pre-Kitty) still needs the GetAsyncKeyState fallback in onKey.
	return tea.NewView(strings.Join(parts, "\n"))
}

// prompt is the slot between the queued list and the input where the
// approval prompt renders.
func (m *Model) prompt() string {
	if m.approval == nil {
		return m.askPrompt()
	}
	return boldStyle.Render(ansi.Wrap(m.approval.prompt, m.width, ""))
}

// rule is the dim horizontal line framing the input above and below.
func (m *Model) rule() string {
	return dimStyle.Render(strings.Repeat("─", m.width))
}

func (m *Model) statusLine() string {
	if m.hint != "" {
		return dimStyle.Render(ansi.Truncate(m.hint, m.width, "…"))
	}
	return statusInfo{
		Model: m.model, Level: m.gate.Level(), Prompt: m.usage.prompt, Window: m.window,
		In: m.usage.in, Out: m.usage.out, Root: m.o.Root, Branch: m.branch, Title: m.title(),
		Queued: len(m.queued) + len(m.followUps), ShowThinking: m.tr.ShowThinking,
	}.Render(m.width)
}

// title is the open session's title, "" before one opens or when unset.
func (m *Model) title() string {
	if m.live == nil {
		return ""
	}
	return m.live.Session.Title()
}

// expandMaxLines caps how many output lines the Ctrl+O panel shows at once.
const expandMaxLines = 20

// toolExpandView is the live-region panel for the latest tool's full output.
func (m *Model) toolExpandView() string {
	if !m.toolExpand {
		return ""
	}
	out := strings.TrimRight(m.lastTool.Output, "\n")
	if m.lastTool.Tool == "" || out == "" {
		return dimStyle.Render("no tool output to expand  (Ctrl+O collapse)")
	}
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = core.Sanitize(line)
	}
	maxLines := expandMaxLines
	if m.height > 0 {
		// Leave room for input, rules, status, loader, and a blank.
		if room := m.height - 8; room > 4 && room < maxLines {
			maxLines = room
		}
	}
	off := m.toolExpandOff
	if off < 0 {
		off = 0
	}
	if off > len(lines)-1 {
		off = max(len(lines)-1, 0)
	}
	end := off + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	shown := lines[off:end]
	var b strings.Builder
	head := fmt.Sprintf("▸ %s %s — full output", core.Sanitize(m.lastTool.Tool), m.lastTool.Subject)
	if len(lines) > maxLines {
		head += fmt.Sprintf("  (%d–%d/%d)", off+1, end, len(lines))
	}
	head += "  (Ctrl+O collapse)"
	b.WriteString(dimStyle.Render(ansi.Truncate(head, m.width, "…")))
	for _, line := range shown {
		b.WriteByte('\n')
		if m.width > 0 {
			line = ansi.Truncate(line, m.width, "…")
		}
		b.WriteString(dimStyle.Render(line))
	}
	return b.String()
}

// noteLastTool copies the transcript's latest tool into the expand source.
func (m *Model) noteLastTool() {
	if info := m.tr.LastTool(); info.Tool != "" {
		m.lastTool = info
		if m.toolExpand {
			m.toolExpandOff = 0
		}
	}
}

// Printed returns every block of the main chat's transcript, in order,
// including blocks held back while an agent was viewed (test hook).
func (m *Model) Printed() []string { return m.printed }

// Screen returns every block handed to tea.Println, in order (test hook).
func (m *Model) Screen() []string { return m.screen }

// print records blocks in the main chat's transcript and shows them, unless
// the window is on a farm agent: then they are held until Ctrl+0.
func (m *Model) print(blocks ...string) tea.Cmd {
	m.printed = append(m.printed, blocks...)
	if m.viewing != "" && !m.quitting {
		m.held += len(blocks)
		return nil
	}
	return m.emit(blocks...)
}

// emit queues blocks for scrollback. Commands run concurrently, so only one
// Println is in flight at a time; it acknowledges itself with printedMsg and
// the next batch goes out then, which keeps blocks in order.
func (m *Model) emit(blocks ...string) tea.Cmd {
	m.screen = append(m.screen, blocks...)
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
	if done {
		m.outbox = nil
		return tea.Sequence(tea.Println(body), tea.Quit)
	}
	m.inflight, m.outbox = m.outbox, nil
	m.printing = true
	return tea.Sequence(tea.Println(body), func() tea.Msg { return printedMsg{} })
}
