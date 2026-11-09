package tui

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

const (
	waitNotice     = "wait for the reply or press Esc first"
	compactFailed  = "compaction failed: "
	maxSuggestDist = 2 // furthest typo suggest still corrects
	timeLayout     = "2006-01-02 15:04"
)

// commands are the slash commands; /skill:<name> is handled apart.
var commands = map[string]func(*Model, string) tea.Cmd{
	"help":     (*Model).cmdHelp,
	"quit":     func(m *Model, _ string) tea.Cmd { return m.quit() },
	"new":      func(m *Model, _ string) tea.Cmd { return m.newSession() },
	"model":    (*Model).cmdModel,
	"name":     (*Model).cmdName,
	"thinking": (*Model).cmdThinking,
	"approve":  (*Model).cmdApprove,
	"allowed":  (*Model).cmdAllowed,
	"session":  (*Model).cmdSession,
	"copy":     (*Model).cmdCopy,
	"resume":   (*Model).cmdResume,
	"compact":  (*Model).startCompact,
	"reload":   (*Model).cmdReload,
	"settings": (*Model).cmdSettings,
	"farm":     (*Model).cmdFarm,
}

// idleOnly commands are refused during a run. That is what keeps
// Session.SetModel, Session.SetTitle, Session.SetSystem, Session.Messages,
// Session.Compact and Live.Close off a session while its Send is running, and
// what keeps the generation steady during a run.
var idleOnly = map[string]bool{"new": true, "model": true, "name": true, "resume": true, "compact": true, "reload": true}

const helpText = `commands
  /help                  this list
  /new                   start a fresh conversation and clear the screen (model, thinking, approval level and grants are kept)
  /model [name]          switch model; bare /model opens a picker
  /name [title]          name this session (shown in /resume); bare /name shows it
  /resume                continue a recent session in this project (grants and level reset)
  /compact [focus]       summarize older messages to free context; focus steers the summary
  /reload                reload AGENTS.md, skills and project context into this session
  /settings              open settings picker (enums get a value picker)
  /settings <key> [val]  show or set a setting (default write: local; global in an untrusted project)
  /settings unset <key>  remove a setting from local (or --global/--project)
  /session               session file, tokens used, context use
  /thinking              show or hide thinking
  /approve [level]       approval level: never | shell | mutating
  /allowed               list and remove always grants
  /copy                  copy the last reply to the clipboard
  /farm "<task>"         plan a farm of parallel agents for task; approve, edit, replan or quit
  /farm run <plan.yaml>  start an approved plan
  /farm                  the farm dashboard: every agent, status, activity, tokens
  /farm attach <part|N>  show that agent (as Ctrl+N); /farm back returns to the main chat
  /farm pause|resume|drop <part|N>   hold an agent before its next request, release it, or drop it and its dependents
  /farm stop             stop the farm gracefully; again to cancel now
  /skill:<name> [text]   run a skill
  /quit                  save the session and quit
an always grant on a shell command covers any arguments to the first two words
  (always on "go test ./core" also allows "go test ./...");
  shells, interpreters, sudo/env/xargs-style wrappers, find and rm never get one
keys
  Enter send or steer · Alt+Enter, Shift+Enter or Ctrl+J newline · Esc stop the run
  Up/Down recall earlier input (from the first or last line of the input)
  Ctrl-L clear the screen (keep session)
  Ctrl-Q while a reply runs: queue a follow-up, sent when the run ends
  Alt-Up pull queued messages back into the input
  Ctrl-O expand or collapse the latest tool output
  Ctrl+1..9 / Alt+1..9 show farm agent N · Ctrl+0 / Alt+0 back to the main chat
  Ctrl-C clear the input; on an empty box stop a running ! command, else twice quits
  !cmd run a shell command; its output goes with your next message · !!cmd keeps it local
  in the / menu: Up/Down move · Tab or Enter complete · Esc hide
  in a picker: type to filter · Up/Down move · Enter choose · Esc or Ctrl+C cancel
  in the /model picker: Ctrl+S also sets the default for new sessions
full manual: docs/manual.md in the jas repo, or ask jas ("how do I …?")`

// parseCommand splits "/name arg..." at the first whitespace. ok is false
// for input that is not a slash command with a name.
func parseCommand(input string) (name, arg string, ok bool) {
	rest, isCmd := strings.CutPrefix(input, "/")
	if !isCmd {
		return "", "", false
	}
	name, arg = rest, ""
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		name, arg = rest[:i], strings.TrimSpace(rest[i:])
	}
	return name, arg, name != ""
}

// suggest returns the known name that has name as its prefix or, failing
// that, the one nearest by edit distance (at most maxSuggestDist). Ties go
// alphabetical; no match gives "".
func suggest(name string, known []string) string {
	if name == "" {
		return ""
	}
	sorted := slices.Sorted(slices.Values(known))
	for _, k := range sorted {
		if strings.HasPrefix(k, name) {
			return k
		}
	}
	best, bestDist := "", maxSuggestDist+1
	for _, k := range sorted {
		if d := levenshtein(name, k); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := range ra {
		cur := make([]int, len(rb)+1)
		cur[0] = i + 1
		for j := range rb {
			cost := 1
			if ra[i] == rb[j] {
				cost = 0
			}
			cur[j+1] = min(prev[j+1]+1, cur[j]+1, prev[j]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

// commandNames returns every command and skill:<name> entry, sorted.
func (m *Model) commandNames() []string {
	names := slices.Collect(maps.Keys(commands))
	for _, s := range m.b.SkillNames() {
		names = append(names, "skill:"+m.clean(s)) // redacted, then sanitized, like every shown text
	}
	slices.Sort(names)
	return names
}

// commandDesc is the short note the / menu shows beside each command.
var commandDesc = map[string]string{
	"help":     "list commands and keys",
	"quit":     "save the session and quit",
	"new":      "start a fresh conversation",
	"model":    "switch model",
	"name":     "name this session",
	"thinking": "show or hide thinking",
	"approve":  "set the approval level",
	"allowed":  "list and remove always grants",
	"session":  "session file, tokens and context use",
	"copy":     "copy the last reply",
	"resume":   "continue a recent session",
	"compact":  "summarize older messages",
	"reload":   "reload AGENTS.md and skills",
	"settings": "open settings picker",
	"farm":     "plan or show a farm",
}

// slashMenu is the / menu for the input: open while the input is a / name
// with no argument yet, unless Esc dismissed it at this exact text. Its
// cursor survives only while the input stays the same.
func (m *Model) slashMenu() *picker {
	v := m.input.Value()
	if !strings.HasPrefix(v, "/") || strings.IndexFunc(v, unicode.IsSpace) >= 0 ||
		m.approval != nil || m.picker != nil || v == m.slashHidden {
		return nil
	}
	var items []pickItem
	for _, n := range m.commandNames() {
		desc, ok := commandDesc[n]
		if !ok {
			desc = "run a skill"
		}
		items = append(items, pickItem{label: n, value: n, desc: desc})
	}
	p := &picker{mark: "/", items: items, filter: v[1:]}
	if m.slashFor == v {
		p.cursor = min(m.slashCursor, len(p.visible())-1)
	}
	return p
}

// onSlashKey handles the keys the open / menu owns and reports whether it
// took the key. Enter on a full command name submits it as typed; on a
// partial one it completes the highlighted name, as Tab does.
func (m *Model) onSlashKey(menu *picker, msg tea.KeyPressMsg) bool {
	v := m.input.Value()
	vis := menu.visible()
	switch msg.String() {
	case "up", "down":
		menu.key(msg)
		m.slashFor, m.slashCursor = v, menu.cursor
		return true
	case "esc":
		m.slashHidden = v
		return true
	case "enter":
		if slices.Contains(m.commandNames(), strings.TrimPrefix(v, "/")) {
			return false
		}
		fallthrough
	case "tab":
		if len(vis) == 0 {
			return msg.String() == "tab"
		}
		m.input.SetValue("/" + vis[menu.cursor].value + " ")
		return true
	}
	return false
}

// command runs a slash command. Unknown or refused ones leave the input as
// typed.
func (m *Model) command(text string) tea.Cmd {
	name, arg, _ := parseCommand(text)
	if skill, ok := strings.CutPrefix(name, "skill:"); ok {
		return m.skillCommand(skill, arg, text)
	}
	run, ok := commands[name]
	if !ok {
		return m.unknownCommand(name)
	}
	if idleOnly[name] && (m.running() || m.reloading) {
		return m.notice(waitNotice)
	}
	m.input.Reset()
	return run(m, arg)
}

func (m *Model) unknownCommand(name string) tea.Cmd {
	msg := "unknown command /" + m.clean(name)
	if s := suggest(name, m.commandNames()); s != "" {
		msg += " — did you mean /" + s + "?"
	}
	return m.print(errStyle.Render(msg))
}

// skillCommand sends the skill's prompt as the user message, shown as typed
// under the skill marker.
func (m *Model) skillCommand(skill, arg, text string) tea.Cmd {
	prompt, ok := m.b.SkillPrompt(skill, arg)
	if !ok {
		return m.unknownCommand("skill:" + skill)
	}
	if m.running() {
		return m.notice(waitNotice)
	}
	m.input.Reset()
	return m.startRun(prompt, m.tr.SkillUser(skill, text))
}

// clean masks secrets and escapes control characters in one line of text.
func (m *Model) clean(s string) string { return core.Sanitize(m.o.Redact(s)) }

// notice prints dim lines, each cleaned. It answers the owner, so in a farm
// agent's view it shows there too (and is kept in the main transcript).
func (m *Model) notice(lines ...string) tea.Cmd {
	return m.answer(m.tr.Notice(dimStyle.Render(m.cleanLines(lines)))...)
}

// answer prints an answer to the owner's own command or keystroke: shown in
// whatever view the window is on, and kept in the main transcript.
func (m *Model) answer(blocks ...string) tea.Cmd {
	if m.viewing != "" {
		m.printed = append(m.printed, blocks...)
		return m.emit(blocks...)
	}
	return m.print(blocks...)
}

// heldNotice prints dim lines into the main chat; in an agent's view they
// are held like any other main-chat output (farm event lines).
func (m *Model) heldNotice(lines ...string) tea.Cmd {
	return m.print(m.tr.Notice(dimStyle.Render(m.cleanLines(lines)))...)
}

func (m *Model) cleanLines(lines []string) string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = m.clean(l)
	}
	return strings.Join(out, "\n")
}

func (m *Model) openPicker(title string, items []pickItem, onPick func(string) tea.Cmd) {
	m.openPickerC(title, items, onPick, nil)
}

// openPickerC opens a picker with a cancel handler. While a prompt holds the
// input the picker waits behind it (in the stash) and opens afterwards.
func (m *Model) openPickerC(title string, items []pickItem, onPick func(string) tea.Cmd, onCancel func() tea.Cmd) {
	p := &picker{title: title, items: items}
	if m.stash != nil && (m.approval != nil || m.ask != nil) {
		m.stash.picker, m.stash.onPick, m.stash.onCancel = p, onPick, onCancel
		return
	}
	m.picker, m.onPick, m.onPickCancel = p, onPick, onCancel
}

// onPickerKey feeds a key to the open picker and runs onPick on a choice;
// Ctrl+S on a canDefault picker also stores the choice as the default.
func (m *Model) onPickerKey(msg tea.KeyPressMsg) tea.Cmd {
	res, value := m.picker.key(msg)
	if res == pickOpen {
		return nil
	}
	onPick, onCancel := m.onPick, m.onPickCancel
	m.picker, m.onPick, m.onPickCancel = nil, nil, nil
	switch res {
	case pickCancelled:
		if onCancel != nil {
			return onCancel()
		}
	case pickChosen:
		return onPick(value)
	case pickSetDefault:
		return m.setDefaultModel(value)
	}
	return nil
}

// cmdReload rebuilds the system prompt from disk (AGENTS.md, skills) and
// swaps it into the live session. Trust prompts reuse the farm ask UI. The
// work runs on a goroutine (not spawn): spawn would mark the chat running
// and block the ask UI from opening.
func (m *Model) cmdReload(string) tea.Cmd {
	if m.reloading {
		return m.notice("reload already in progress")
	}
	if m.live == nil {
		return nil
	}
	m.reloading = true
	send, live, b := m.send, m.live, m.b
	go func() {
		ask := func(prompt string) string {
			ch := make(chan string, 1)
			send(farmAskMsg{prompt: prompt, reply: ch})
			ans, ok := <-ch
			if !ok {
				return ""
			}
			return ans
		}
		notices, err := b.Reload(live.Session, ask)
		send(reloadDoneMsg{notices: notices, err: err})
	}()
	return nil
}

func (m *Model) onReloadDone(msg reloadDoneMsg) tea.Cmd {
	m.reloading = false
	if msg.err != nil {
		return m.print(errStyle.Render("reload: " + m.clean(msg.err.Error())))
	}
	if len(msg.notices) == 0 {
		return m.notice("reloaded")
	}
	return m.notice(msg.notices...)
}

// cmdSettings opens a settings picker, or shows/sets/unsets layered settings.
// Default write scope is local (<project>/.jas/config.local.json). Scope flags:
// --global, --project, --local. Bare /settings opens a filterable picker;
// closed-set keys open a second value picker; free-text keys prefill the input.
func (m *Model) cmdSettings(arg string) tea.Cmd {
	scope, rest := takeSettingsScope(arg)
	if len(rest) == 0 {
		m.openSettingsPicker()
		return nil
	}
	if rest[0] == "unset" {
		if len(rest) != 2 {
			return m.print(errStyle.Render("usage: /settings unset [--global|--project|--local] <key>"))
		}
		if err := m.b.UnsetSetting(scope, rest[1]); err != nil {
			return m.print(errStyle.Render(m.clean(err.Error())))
		}
		if scope == "" {
			scope = m.b.DefaultSettingsScope()
		}
		return m.notice(fmt.Sprintf("unset %s from %s config", rest[1], scope))
	}
	if len(rest) == 1 {
		line, err := m.b.GetSetting(rest[0])
		if err != nil {
			return m.print(errStyle.Render(m.clean(err.Error())))
		}
		return m.notice(fmt.Sprintf("%s  %s  (%s)", line.Key, line.Value, line.Source))
	}
	if len(rest) != 2 {
		return m.print(errStyle.Render("usage: /settings [--global|--project|--local] <key> <value>"))
	}
	return m.applySetting(scope, rest[0], rest[1])
}

// openSettingsPicker shows every effective setting; choosing a closed-set key
// opens a value picker, choosing a free-text key prefills /settings <key>.
func (m *Model) openSettingsPicker() {
	lines := m.b.ListSettings()
	items := make([]pickItem, len(lines))
	for i, l := range lines {
		desc := fmt.Sprintf("%s (%s)", l.Value, l.Source)
		if len(l.Options) > 0 {
			desc += " · " + strings.Join(l.Options, "|")
		}
		items[i] = pickItem{label: l.Key, value: l.Key, desc: desc}
	}
	m.openPicker("settings", items, m.onSettingKey)
}

// onSettingKey handles a choice from the settings key picker.
func (m *Model) onSettingKey(key string) tea.Cmd {
	line := m.settingLine(key)
	if len(line.Options) == 0 {
		m.input.SetValue("/settings " + key + " ")
		return nil
	}
	items := make([]pickItem, len(line.Options))
	for i, o := range line.Options {
		desc := ""
		if o == line.Value {
			desc = "current"
		}
		items[i] = pickItem{label: o, value: o, desc: desc}
	}
	m.openPicker(key, items, func(value string) tea.Cmd {
		return m.applySetting("", key, value)
	})
	return nil
}

// settingLine looks up one setting for the picker (value + options).
func (m *Model) settingLine(key string) SettingLine {
	for _, l := range m.b.ListSettings() {
		if l.Key == key {
			return l
		}
	}
	if line, err := m.b.GetSetting(key); err == nil {
		return line
	}
	return SettingLine{Key: key}
}

// applySetting writes key=value and updates live approve when needed.
func (m *Model) applySetting(scope, key, value string) tea.Cmd {
	if err := m.b.SetSetting(scope, key, value); err != nil {
		return m.print(errStyle.Render(m.clean(err.Error())))
	}
	if scope == "" {
		scope = m.b.DefaultSettingsScope()
	}
	cmds := []tea.Cmd{m.notice(fmt.Sprintf("%s=%s (%s config)", key, m.clean(value), scope))}
	if key == "approve" {
		if level, err := core.ParseLevel(value); err == nil {
			m.gate.SetLevel(level)
		}
	}
	return tea.Batch(cmds...)
}

// takeSettingsScope pulls --global|--project|--local from the /settings arg.
func takeSettingsScope(arg string) (scope string, rest []string) {
	for _, tok := range strings.Fields(arg) {
		switch tok {
		case "--global", "--project", "--local":
			scope = strings.TrimPrefix(tok, "--")
		default:
			rest = append(rest, tok)
		}
	}
	return scope, rest
}

func (m *Model) cmdHelp(string) tea.Cmd {

	return m.notice(strings.Split(helpText, "\n")...)
}

// cmdName sets the session title, or with no argument shows it. The
// session-log append runs in a Cmd (see setModel: posting the forwarded
// event from inside Update would hang the program).
func (m *Model) cmdName(title string) tea.Cmd {
	if title == "" {
		if t := m.live.Session.Title(); t != "" {
			return m.notice("title: " + t)
		}
		return m.notice("no title; /name <title> sets one")
	}
	live := m.live
	return tea.Batch(
		func() tea.Msg {
			if err := live.Session.SetTitle(title); err != nil {
				return noticeMsg{"event log error: " + m.clean(err.Error())}
			}
			return nil
		},
		m.notice("session named "+m.clean(title)),
	)
}

func (m *Model) cmdModel(name string) tea.Cmd {
	if name != "" {
		return m.setModel(name)
	}
	var items []pickItem
	for _, n := range m.b.Models() {
		items = append(items, pickItem{label: m.clean(n), value: n})
	}
	m.openPicker("model", items, m.setModel)
	m.picker.canDefault = true
	return nil
}

// setDefaultModel stores name as the config default, then switches this
// session to it like setModel. The store runs first but the cross-provider
// refusal comes before it: a default that cannot be used is not stored.
func (m *Model) setDefaultModel(name string) tea.Cmd {
	if cmd := m.refuseCrossProvider(name); cmd != nil {
		return cmd
	}
	if err := m.b.SetDefaultModel(name); err != nil {
		return m.print(errStyle.Render("set default: " + m.clean(err.Error())))
	}
	return tea.Batch(m.setModel(name), m.notice("default model "+m.clean(name)))
}

// setModel switches the current session's model and its context window. The
// session-log append runs in a Cmd: the log forwards model_changed to the
// chat's own message queue, and posting a message from inside Update would
// block on the program's unbuffered Send and hang it.
//
// A model on another provider is refused: the provider and its key are
// wired at session start, so half-migrating a live session would send the
// new model's id to the old endpoint. /new picks up the new provider.
func (m *Model) setModel(name string) tea.Cmd {
	if cmd := m.refuseCrossProvider(name); cmd != nil {
		return cmd
	}
	bare := m.b.BareModel(name) // requests carry the bare id, never the provider prefix
	m.model, m.live.Model = name, bare
	if w, known := m.b.Window(bare); w > 0 { // a zero window would make every request too big
		m.window = w
		if m.live.Budget != nil {
			m.live.Budget.Window = w
		}
		m.tipWindow(bare, known)
	}
	live := m.live
	return tea.Batch(
		func() tea.Msg {
			if err := live.Session.SetModel(bare); err != nil {
				return noticeMsg{"event log error: " + m.clean(err.Error())}
			}
			return nil
		},
		m.notice("model "+name),
	)
}

// refuseCrossProvider is the notice that refuses switching a live session to
// a model on another provider, or nil when the switch may go ahead.
func (m *Model) refuseCrossProvider(name string) tea.Cmd {
	if prov := m.b.ProviderOf(name); prov != "" && m.live.Provider != "" && prov != m.live.Provider {
		return m.notice(fmt.Sprintf("%s runs on %s — /new for a %s session", m.clean(name), prov, prov))
	}
	return nil
}

func (m *Model) cmdThinking(string) tea.Cmd {
	m.tr.ShowThinking = !m.tr.ShowThinking
	if m.tr.ShowThinking {
		return m.notice("thinking shown")
	}
	return m.notice("thinking hidden")
}

func (m *Model) cmdApprove(arg string) tea.Cmd {
	if arg == "" {
		return m.notice(fmt.Sprintf("approval level %s · never | shell | mutating", m.gate.Level()))
	}
	level, err := core.ParseLevel(arg)
	if err != nil {
		return m.print(errStyle.Render(m.clean(err.Error())))
	}
	m.gate.SetLevel(level)
	return m.notice("approval level " + level.String())
}

func (m *Model) cmdAllowed(string) tea.Cmd {
	grants := m.gate.Grants().List()
	if len(grants) == 0 {
		return m.notice("no always grants")
	}
	items := make([]pickItem, len(grants))
	for i, g := range grants {
		items[i] = pickItem{label: m.clean(g), value: g}
	}
	m.openPicker("remove always grant", items, func(g string) tea.Cmd {
		if !m.gate.Grants().Remove(g) {
			return nil // a /resume emptied the grants meanwhile
		}
		return m.notice("removed " + g)
	})
	return nil
}

func (m *Model) cmdSession(string) tea.Cmd {
	pct := 0
	if m.window > 0 {
		pct = m.usage.prompt * 100 / m.window
	}
	return m.notice(m.live.Path,
		fmt.Sprintf("in %d · out %d tokens", m.usage.in, m.usage.out),
		fmt.Sprintf("%d%% of %d", pct, m.window))
}

func (m *Model) cmdCopy(string) tea.Cmd {
	if m.reply == "" {
		return m.notice("nothing to copy")
	}
	s := m.o.Redact(m.reply)
	return tea.Batch(tea.SetClipboard(s),
		m.notice(fmt.Sprintf("copied %d chars (via OSC 52, if your terminal supports it)", utf8.RuneCountInString(s))))
}

func (m *Model) cmdResume(string) tea.Cmd {
	infos, err := m.b.Sessions(m.live.Path)
	if err != nil {
		return m.print(errStyle.Render("list sessions: " + m.clean(err.Error())))
	}
	if len(infos) == 0 {
		return m.notice("no other saved session in this project")
	}
	items := make([]pickItem, len(infos))
	for i, in := range infos {
		label := in.FirstMessage
		if in.Title != "" {
			label = in.Title
		}
		items[i] = pickItem{label: m.clean(in.Started.Local().Format(timeLayout) + " " + label), value: in.Path}
	}
	m.openPicker("resume", items, m.resumeSession)
	return nil
}
