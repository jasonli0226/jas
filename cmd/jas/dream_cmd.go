package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/tools"
)

const (
	dreamUsage = "usage: jas dream [--since <dur|date>] [--sessions N] [--max-tokens N] [--timeout D] [--json] [--profile NAME]\n       jas dream list [--json] | diff [id] | apply [id] [--yes] | discard [id] | undo [id]   (each takes --profile NAME)"

	dreamMaxTokens  = 400_000
	dreamTimeout    = 20 * time.Minute
	dreamMaxWindow  = 50
	dreamWorkPrefix = "jas-dream-"
	dreamProposed   = "proposed"
	dreamLogs       = "logs"
	dreamEvidence   = "evidence.md"
	dreamReport     = "report.md"
	dreamFileMode   = 0o644
	hoursPerDay     = 24
)

// dreamArgs are the jas dream run flags.
type dreamArgs struct {
	since     time.Time     // --since, parsed; zero means the watermark
	sessions  int           // --sessions: window size cap
	maxTokens int           // --max-tokens
	timeout   time.Duration // --timeout
	json      bool
	flags     map[string]string // settings flags (--profile)
}

func dreamCmd(ctx context.Context, args []string) error {
	return dreamRun(ctx, args, os.Stdout)
}

// dreamRun runs jas dream, or one of its subcommands, with its output on out.
func dreamRun(ctx context.Context, args []string, out io.Writer) error {
	if len(args) > 0 && slices.Contains(dreamSubcommands, args[0]) {
		return dreamSub(ctx, args[0], args[1:], out)
	}
	da, err := parseDream(args)
	if err != nil {
		return err
	}
	st, err := openDreamStore(ctx, da.flags)
	if err != nil {
		return err
	}
	defer st.close()
	m, stop, err := dreamSession(ctx, st.e, da, st.ws, st.live, st.dreamsDir)
	if err != nil {
		return err
	}
	if err := dreamOutput(ctx, st.e, da, out, m, st.live, st.dreamsDir); err != nil {
		return err
	}
	if stop != nil {
		return stop
	}
	return nil
}

// dreamStore is the profile a dream command works on: its environment, a
// temporary workspace (removed by close), the live memory dir and the
// dreams dir beside it.
type dreamStore struct {
	e                   *env
	ws, live, dreamsDir string
}

func (s dreamStore) close() { _ = os.RemoveAll(s.ws) }

func openDreamStore(ctx context.Context, flags map[string]string) (st dreamStore, err error) {
	ws, err := os.MkdirTemp("", dreamWorkPrefix)
	if err != nil {
		return st, fmt.Errorf("dream workspace: %w", err)
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(ws)
		}
	}()
	if r, err := filepath.EvalSymlinks(ws); err == nil {
		ws = r
	}
	e, err := prepareNoProject(ctx, flags, ws)
	if err != nil {
		return st, err
	}
	// ResolveMode cannot fail: an unknown value resolves to on. Its second
	// result hides a project's index, which a dream (no project) ignores.
	if mode, _ := memory.ResolveMode(memorySetting(e.layers, os.Getenv)); mode == memory.ModeOff {
		return st, errors.New("memory is off for this profile")
	}
	live, err := dreamLiveDir(e)
	if err != nil {
		return st, err
	}
	return dreamStore{e: e, ws: ws, live: live, dreamsDir: dream.Dir(filepath.Dir(live))}, nil
}

func parseDream(args []string) (dreamArgs, error) {
	fs := flag.NewFlagSet("dream", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	da := dreamArgs{sessions: dreamMaxWindow, maxTokens: dreamMaxTokens, timeout: dreamTimeout}
	since := fs.String("since", "", "read sessions since this duration ago (36h, 7d) or date, instead of the watermark")
	sessions := fs.String("sessions", "", "read at most this many sessions")
	maxTokens := fs.String("max-tokens", "", "stop with exit 7 before a request that would cross this many total tokens")
	timeout := fs.String("timeout", "", "stop with exit 7 after this wall-clock duration")
	fs.BoolVar(&da.json, "json", false, "print the dream record as one JSON object")
	profile := fs.String("profile", "", "dream for this profile")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return da, usageErr("%s", dreamUsage)
	}
	var err error
	if *sessions != "" {
		if da.sessions, err = strconv.Atoi(*sessions); err != nil || da.sessions <= 0 {
			return da, usageErr("--sessions must be a positive integer, got %q", *sessions)
		}
	}
	if *maxTokens != "" {
		if da.maxTokens, err = strconv.Atoi(*maxTokens); err != nil || da.maxTokens <= 0 {
			return da, usageErr("--max-tokens must be a positive integer, got %q", *maxTokens)
		}
	}
	if *timeout != "" {
		if da.timeout, err = time.ParseDuration(*timeout); err != nil || da.timeout <= 0 {
			return da, usageErr("--timeout must be a positive duration such as 30m, got %q", *timeout)
		}
	}
	if *since != "" {
		if da.since, err = parseSince(*since, time.Now()); err != nil {
			return da, usageErr("%v", err)
		}
	}
	da.flags = map[string]string{}
	if *profile != "" {
		da.flags["profile"] = *profile
	}
	return da, nil
}

// parseSince reads --since: a duration back from now (Go syntax, or whole
// days as "7d"), a date (local midnight) or an RFC 3339 time.
func parseSince(s string, now time.Time) (time.Time, error) {
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return now.Add(-d), nil
	}
	if n, err := strconv.Atoi(strings.TrimSuffix(s, "d")); err == nil && strings.HasSuffix(s, "d") && n > 0 {
		return now.Add(-time.Duration(n) * hoursPerDay * time.Hour), nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("--since must be a duration (36h, 7d), a date (2006-01-02) or an RFC 3339 time, got %q", s)
}

// dreamLiveDir is the active profile's memory dir, refused when the store's
// read exception would ignore it (a link into the jas home, or onto skills).
func dreamLiveDir(e *env) (string, error) {
	dirs, err := memoryDirs(e.home, e.profile, e.root)
	if err != nil {
		return "", err
	}
	p := e.policy
	p.ProfileMemory = dirs.Profile
	if why := p.ProfileMemoryIgnored(); why != "" {
		return "", errors.New(why)
	}
	return dirs.Profile, nil
}

// dreamInputs is what the model pass starts from.
type dreamInputs struct {
	id, sessionID      string
	started            time.Time
	since              time.Time
	base               map[string]string
	before             []dream.Issue
	logs               []dream.LogRef
	seqs               dream.Seqs
	stats              dream.EvidenceStats
	ws, proposed, live string
}

// dreamSession snapshots the store, builds the evidence, runs the model
// pass and persists the proposal. Nothing lands under dreamsDir until the
// pass ended or was stopped by a cap; stop is that cap's exit error (the
// proposal is then partial), nil when the model finished.
func dreamSession(ctx context.Context, e *env, da dreamArgs, ws, live, dreamsDir string) (m dream.Meta, stop *core.ExitError, err error) {
	in, err := dreamPrepare(e, da, ws, live, dreamsDir)
	if err != nil {
		return m, nil, err
	}
	changes := &dream.Changes{}
	tokens, stop, err := dreamModelPass(ctx, e, da, in, changes)
	if err != nil {
		return m, nil, err
	}
	if ctx.Err() != nil {
		return m, nil, errInterrupted()
	}
	m, err = dreamPersist(e, in, changes.All(), tokens, stop != nil, dreamsDir)
	return m, stop, err
}

func errInterrupted() error {
	return &core.ExitError{Code: core.ExitInterrupted, Reason: "interrupted"}
}

// dreamStops are the exits that end the model pass early but keep its
// work: a token or time cap, the turn cap, a full context.
var dreamStops = []int{core.ExitBudget, core.ExitTurnCap, core.ExitContextFull}

// dreamPrepare fills the workspace: proposed/ (a copy of the live store),
// logs/ (the window's event logs) and evidence.md.
func dreamPrepare(e *env, da dreamArgs, ws, live, dreamsDir string) (dreamInputs, error) {
	now := time.Now().UTC()
	in := dreamInputs{
		id: dream.NewID(now), sessionID: core.NewSessionID(now), started: now,
		ws: ws, proposed: filepath.Join(ws, dreamProposed), live: live,
	}
	var err error
	if in.base, err = dream.HashTree(live); err != nil {
		return in, err
	}
	if err := dream.CopyTree(live, in.proposed); err != nil {
		return in, err
	}
	if err := os.MkdirAll(in.proposed, newDirPerm); err != nil {
		return in, err
	}
	in.before = dreamLint(e, in.proposed)

	in.since = da.since
	if in.since.IsZero() {
		in.since = dream.Watermark(dreamsDir, now)
	}
	logs, notices, err := dream.SelectWindow(filepath.Join(e.home, sessionsDir), e.profile, in.since, da.sessions)
	if err != nil {
		return in, err
	}
	in.logs = logs
	for _, l := range logs {
		if err := copyRegular(l.Path, filepath.Join(ws, dreamLogs, l.SessionID+".jsonl")); err != nil {
			return in, fmt.Errorf("copy session log %s: %w", l.SessionID, err)
		}
	}
	evidence, stats, err := dream.BuildEvidence(logs, notices, live, e.red.Redact, dream.DigestCap)
	if err != nil {
		return in, err
	}
	in.stats = stats
	if err := os.WriteFile(filepath.Join(ws, dreamEvidence), []byte(evidence), dreamFileMode); err != nil {
		return in, err
	}
	in.seqs, err = dream.IndexSeqs(logs)
	return in, err
}

// newDirPerm is the mode of folders a dream creates.
const newDirPerm = 0o755

func dreamLint(e *env, store string) []dream.Issue {
	return dream.Lint(store, e.red.Redact, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
}

// dreamRegistry is everything the dream model may call: the read tools,
// write and edit (the guard confines them to proposed/), and the two
// dream-only tools. No shell and no web.
func dreamRegistry(proposed string, changes *dream.Changes) *core.Registry {
	return core.NewRegistry(tools.Read{}, tools.Ls{}, tools.Grep{}, tools.Write{}, tools.Edit{},
		dream.ReportChange(changes), dream.RemoveNote(proposed))
}

// dreamModelPass runs the model session in its own event log. It returns
// the tokens used and, when a cap stopped the pass (dreamStops), that exit;
// an interrupt or any other stop is an error.
func dreamModelPass(ctx context.Context, e *env, da dreamArgs, in dreamInputs, changes *dream.Changes) (int, *core.ExitError, error) {
	runCtx, cancel := context.WithTimeout(ctx, da.timeout)
	defer cancel()
	var ui core.UI = core.NewQuietUI()
	if !da.json && e.interactive { // progress on the terminal only
		ui = core.NewPrinter(os.Stderr, false)
	}
	ui = core.NewRedactingUI(ui, e.red.Redact)
	log, err := core.OpenEventLog(dreamLogPath(e.home, in.sessionID), nil, e.red.Redact)
	if err != nil {
		return 0, nil, err
	}
	defer log.Close() // idempotent; closed below before dream_proposed reopens it
	log = core.NewUILog(log, ui)

	guard := dream.Guard{Proposed: in.proposed, Redact: e.red.Redact}
	policy := core.ExecPolicy{Root: in.ws, Deny: denyPatterns(e.home), MaxOutput: e.cfg.MaxToolOutput, RedactSkip: e.cfg.RedactSkipPaths}
	exec := &core.Executor{
		Reg: dreamRegistry(in.proposed, changes), Policy: policy,
		Log: log, Filter: e.red.Filter, Redact: e.red.Redact,
		WritePrecheck: guard.Precheck, WriteGuard: guard.Check, GuardAllWrites: true,
	}
	meta := e.sessionMeta(e.cfg.Model)
	meta["dream"] = map[string]any{"id": in.id, "window_sessions": len(in.logs)}
	pool := core.NewTokenPool("dream", da.maxTokens, nil)
	_, err = core.Run(runCtx, core.RunConfig{
		Provider: e.provider, Model: e.cfg.Model,
		System: dream.SystemPrompt(in.ws), Prompt: dream.Kickoff(in.stats, in.before),
		Exec: exec, Log: log, UI: ui, MaxTurns: e.cfg.MaxTurns, Budget: e.budget(e.cfg.Model),
		SessionMeta: meta, MaxOutput: e.cfg.MaxOutputTokens, Tokens: pool,
	})
	if closeErr := log.Close(); closeErr != nil && err == nil {
		return 0, nil, closeErr
	}
	var ee *core.ExitError
	switch {
	case err == nil:
		return pool.Used(), nil, nil
	case errors.As(err, &ee) && slices.Contains(dreamStops, ee.Code):
		return pool.Used(), &core.ExitError{Code: ee.Code, Reason: "dream stopped early (" + ee.Reason + "); the proposal is partial"}, nil
	}
	return 0, nil, err
}

func dreamLogPath(home, sessionID string) string {
	return filepath.Join(home, sessionsDir, sessionID, eventsFile)
}

// dreamPersist verifies the pass and writes <dreamsDir>/<id>/. On failure
// the half-written folder is removed.
func dreamPersist(e *env, in dreamInputs, changes []dream.Change, tokens int, partial bool, dreamsDir string) (m dream.Meta, err error) {
	after := dreamLint(e, in.proposed)
	proposed, err := dream.HashTree(in.proposed)
	if err != nil {
		return m, err
	}
	f := dream.Verify(in.base, proposed, changes, in.seqs, in.before, after, partial)

	idDir := filepath.Join(dreamsDir, in.id)
	if err := os.MkdirAll(dreamsDir, newDirPerm); err != nil {
		return m, err
	}
	if err := os.Mkdir(idDir, newDirPerm); err != nil {
		return m, err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(idDir) // a failed run leaves no <id>/
		}
	}()
	kept := filepath.Join(idDir, dreamProposed)
	if err := dream.CopyTree(in.proposed, kept); err != nil {
		return m, err
	}
	if err := copyRegular(filepath.Join(in.ws, dreamEvidence), filepath.Join(idDir, dreamEvidence)); err != nil {
		return m, err
	}
	m = dream.Meta{
		ID: in.id, Profile: e.profile, State: dream.Pending,
		StartedAt: in.started, EndedAt: time.Now().UTC(), SessionID: in.sessionID,
		Window: dream.Window{Since: in.since.UTC(), Until: in.started, Sessions: sessionIDs(in.logs)},
		Base:   in.base, Counts: f.Counts, Partial: partial,
		Warnings: redactWarnings(dream.NewWarnings(f, changes), e.red.Redact),
	}
	// Apply checks the copy it will swap in, so hash that, not the workspace.
	if m.Proposed, err = dream.HashTree(kept); err != nil {
		return m, err
	}
	report := e.red.Redact(dream.RenderReport(m, e.cfg.Model, tokens, changes, f, in.logs))
	reportPath := filepath.Join(idDir, dreamReport)
	if err := os.WriteFile(reportPath, []byte(report), dreamFileMode); err != nil {
		return m, err
	}
	if err := dream.Save(dreamsDir, m); err != nil {
		return m, err
	}
	// The proposal is complete; later steps only warn.
	if err := appendDreamEvent(e.home, m.SessionID, core.EvDreamProposed, map[string]any{
		"dream_id": m.ID, "counts": m.Counts, "partial": m.Partial, "report": reportPath,
	}, e.red.Redact); err != nil {
		e.warn("dream " + m.ID + ": log dream_proposed: " + err.Error())
	}
	if err := dream.Prune(dreamsDir, dream.KeepDreams); err != nil {
		e.warn(err.Error())
	}
	return m, nil
}

func sessionIDs(logs []dream.LogRef) []string {
	ids := make([]string, 0, len(logs))
	for _, l := range logs {
		ids = append(ids, l.SessionID)
	}
	return ids
}

// copyRegular copies one file, creating dst's folder.
func copyRegular(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), newDirPerm); err != nil {
		return err
	}
	return os.WriteFile(dst, data, dreamFileMode)
}
