package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
	"github.com/jasonli0226/jas/tools"
	"github.com/jasonli0226/jas/tui"
)

// sessionListLimit caps the sessions offered by /resume.
const sessionListLimit = 20

// isTerminal reports whether fd is a terminal; tests replace it.
var isTerminal = term.IsTerminal

type chatArgs struct {
	cont  bool // --continue: resume this project's newest session
	trust bool // --trust-project
	flags map[string]string
}

// chatCmd runs the inline chat: `jas [--continue] [flags]`.
func chatCmd(ctx context.Context, args []string) error {
	ca, err := parseChat(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, usage)
		return nil
	}
	if err != nil {
		return err
	}
	if err := onboardIfNeeded(); err != nil {
		return err
	}
	// Checked before prepare, whose trust prompt would otherwise read a pipe.
	if !isTerminal(int(os.Stdin.Fd())) || !isTerminal(int(os.Stdout.Fd())) {
		return usageErr(`the chat needs a terminal; use jas run "task"`)
	}
	e, err := prepare(ctx, ca.flags, ca.trust)
	if err != nil {
		return err
	}
	e.warnUnknownWindow(e.cfg.Model)
	return tui.Run(ctx, &chatBackend{e: e}, tui.Options{
		Level: e.level, Model: e.cfg.Model, Root: e.root, Windows: runtime.GOOS == "windows",
		Continue: ca.cont, Now: time.Now, Redact: e.red.Redact,
	}, os.Stdin, os.Stdout)
}

// parseChat reads the chat flags: the setting flags plus --continue and
// --trust-project. A task argument belongs to `jas run`.
func parseChat(args []string) (chatArgs, error) {
	fs := flag.NewFlagSet("jas", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var ca chatArgs
	fs.BoolVar(&ca.cont, "continue", false, "resume this project's newest session")
	fs.BoolVar(&ca.trust, "trust-project", false, "load the project context file without asking")
	for _, k := range settings.Keys {
		if k.Flag {
			fs.String(strings.ReplaceAll(k.Name, "_", "-"), "", k.Name+" (overrides "+k.EnvName()+" and stored config)")
		}
	}
	if err := fs.Parse(args); errors.Is(err, flag.ErrHelp) {
		return ca, err
	} else if err != nil {
		return ca, usageErr("%v", err)
	}
	if fs.NArg() > 0 {
		return ca, usageErr(`unexpected argument %q; to run one task use jas run "task"`, fs.Arg(0))
	}
	ca.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "continue" && f.Name != "trust-project" {
			ca.flags[strings.ReplaceAll(f.Name, "-", "_")] = f.Value.String()
		}
	})
	return ca, nil
}

// chatBackend is the chat's tui.Backend over the real settings, provider,
// redaction, trust decision and skills.
type chatBackend struct{ e *env }

var _ tui.Backend = (*chatBackend)(nil)

// NewSession starts a session in a new directory under <home>/sessions.
func (b *chatBackend) NewSession(model string, ui core.UI, gate core.Gate) (*tui.Live, error) {
	path := filepath.Join(b.e.home, sessionsDir, core.NewSessionID(time.Now()), eventsFile)
	log, err := core.OpenEventLog(path, nil, b.e.red.Redact)
	if err != nil {
		return nil, err
	}
	c := b.config(model, log, ui, gate)
	s, err := core.NewSession(c)
	if err != nil {
		return nil, errors.Join(err, log.Close())
	}
	return b.live(path, model, log, s, c.Budget), nil
}

// Resume reopens a saved session. The log is opened first: its lock tells
// whether another jas has the session open.
func (b *chatBackend) Resume(path string, ui core.UI, gate core.Gate) (*tui.Live, []string, error) {
	id := filepath.Base(filepath.Dir(path))
	log, err := core.OpenEventLog(path, nil, b.e.red.Redact)
	if errors.Is(err, core.ErrSessionLocked) {
		return nil, nil, fmt.Errorf("session %s is open in another jas process: %w", id, err)
	}
	if err != nil {
		return nil, nil, err
	}
	st, err := core.Replay(path)
	if err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	model, notice := core.ResumeModel(st, b.e.cfg.BaseURL, b.e.cfg.Model)
	c := b.config(model, log, ui, gate)
	s, err := core.ResumeSession(c, st)
	if err != nil {
		return nil, nil, errors.Join(err, log.Close())
	}
	notices := slices.Clone(st.Notices)
	if notice != "" {
		notices = append(notices, notice)
	}
	return b.live(path, model, log, s, c.Budget), notices, nil
}

// config is the session setup shared by NewSession and Resume. The system
// prompt always comes from the current trust decision, never from the file.
func (b *chatBackend) config(model string, log core.EventLog, ui core.UI, gate core.Gate) core.SessionConfig {
	e := b.e
	ui = core.NewRedactingUI(ui, e.red.Redact)
	log = core.NewUILog(log, ui)
	exec := &core.Executor{
		Reg:    core.NewRegistry(append(tools.ReadOnly(), tools.Mutating()...)...),
		Policy: e.policy, Gate: gate, Log: log, Filter: e.red.Filter,
	}
	return core.SessionConfig{
		Provider: e.provider, Model: model, System: e.pc.system,
		Exec: exec, Log: log, UI: ui, MaxTurns: e.cfg.MaxTurns,
		Budget: e.budget(model), Meta: e.sessionMeta(model),
	}
}

func (b *chatBackend) live(path, model string, log core.EventLog, s *core.Session, bud *core.Budget) *tui.Live {
	return &tui.Live{
		Session: s, ID: filepath.Base(filepath.Dir(path)), Path: path, Model: model, Budget: bud,
		Close: func(status string, code int) error {
			return errors.Join(s.Close(status, code), log.Close())
		},
	}
}

// Sessions lists this project's saved sessions, newest first, leaving out
// the one at path exclude.
func (b *chatBackend) Sessions(exclude string) ([]core.SessionInfo, error) {
	all, err := core.ListSessions(filepath.Join(b.e.home, sessionsDir), b.e.root, sessionListLimit)
	if err != nil {
		return nil, err
	}
	if exclude == "" {
		return all, nil
	}
	exclude = filepath.Clean(exclude)
	return slices.DeleteFunc(all, func(i core.SessionInfo) bool { return filepath.Clean(i.Path) == exclude }), nil
}

// Models offers the configured model, the models with stored overrides and,
// on OpenRouter, the context-window catalog.
func (b *chatBackend) Models() []string {
	names := append(b.e.file.ModelNames(), b.e.cfg.Model)
	if b.e.cfg.Provider == settings.ProviderOpenRouter {
		names = append(names, settings.CatalogModels()...)
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// Window is model's context window, DefaultContextWindow when unknown.
func (b *chatBackend) Window(model string) int {
	w, _ := b.e.window(model)
	return w
}

// SkillNames lists every skill the user can invoke, including those hidden
// from the model.
func (b *chatBackend) SkillNames() []string {
	names := make([]string, 0, len(b.e.pc.skills))
	for _, s := range b.e.pc.skills {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

// SkillPrompt is the user message that invokes skill name with text.
func (b *chatBackend) SkillPrompt(name, text string) (string, bool) {
	i := slices.IndexFunc(b.e.pc.skills, func(s skills.Skill) bool { return s.Name == name })
	if i < 0 {
		return "", false
	}
	s := b.e.pc.skills[i]
	p := fmt.Sprintf("Use the \"%s\" skill for this request. Its instructions (%s):\n\n%s", s.Name, s.Location, strings.TrimSpace(string(s.Content)))
	if text = strings.TrimSpace(text); text != "" {
		p += "\n\n" + text
	}
	return b.e.red.Redact(p), true
}
