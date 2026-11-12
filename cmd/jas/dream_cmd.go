package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
	"github.com/jasonli0226/jas/memory"
	"github.com/jasonli0226/jas/tools"
)

const (
	dreamUsage = "usage: jas dream [--since <dur|date>] [--sessions N] [--max-tokens N] [--timeout D] [--json] [--profile NAME]"

	dreamMaxTokens  = 400_000
	dreamTimeout    = 20 * time.Minute
	dreamMaxWindow  = 50
	dreamWorkPrefix = "jas-dream-"
	dreamProposed   = "proposed"
	dreamLogs       = "logs"
	dreamEvidence   = "evidence.md"
	dreamReport     = "report.md"
	dreamFileMode   = 0o644
	dreamApplyAsk   = "apply? [y/N] "
	dreamRunAsk     = "apply? [y/N/d=diff] "
	warningsHeading = "## Warnings"
	hoursPerDay     = 24
)

// validSessionID is core.NewSessionID's shape; a dream record's session id
// becomes a path, so anything else is refused.
var validSessionID = regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{6}$`)

// dreamArgs are the jas dream run flags.
type dreamArgs struct {
	since     string        // --since: "" means the watermark
	sessions  int           // --sessions: window size cap
	maxTokens int           // --max-tokens
	timeout   time.Duration // --timeout
	json      bool
	flags     map[string]string // settings flags (--profile)
}

func dreamCmd(ctx context.Context, args []string) error {
	return dreamRun(ctx, args, os.Stdout)
}

// dreamRun runs jas dream with its summary or JSON object on out.
func dreamRun(ctx context.Context, args []string, out io.Writer) error {
	da, err := parseDream(args)
	if err != nil {
		return err
	}
	ws, err := os.MkdirTemp("", dreamWorkPrefix)
	if err != nil {
		return fmt.Errorf("dream workspace: %w", err)
	}
	defer os.RemoveAll(ws)
	if r, err := filepath.EvalSymlinks(ws); err == nil {
		ws = r
	}
	e, err := prepareNoProject(ctx, da.flags, ws)
	if err != nil {
		return err
	}
	if mode, _ := memory.ResolveMode(memorySetting(e.layers, os.Getenv)); mode == memory.ModeOff {
		return errors.New("memory is off for this profile")
	}
	live, err := dreamLiveDir(e)
	if err != nil {
		return err
	}
	dreamsDir := dream.Dir(filepath.Dir(live))

	m, err := dreamSession(ctx, e, da, ws, live, dreamsDir)
	if err != nil {
		return err
	}
	if err := dreamOutput(ctx, e, da, out, m, live, dreamsDir); err != nil {
		return err
	}
	if m.Partial {
		return &core.ExitError{Code: core.ExitBudget, Reason: "dream stopped at its cap; the proposal is partial"}
	}
	return nil
}

func parseDream(args []string) (dreamArgs, error) {
	fs := flag.NewFlagSet("dream", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	da := dreamArgs{sessions: dreamMaxWindow, maxTokens: dreamMaxTokens, timeout: dreamTimeout}
	fs.StringVar(&da.since, "since", "", "read sessions since this duration ago (36h, 7d) or date, instead of the watermark")
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
	if da.since != "" {
		if _, err := parseSince(da.since, time.Now()); err != nil {
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
// pass ended or hit its cap.
func dreamSession(ctx context.Context, e *env, da dreamArgs, ws, live, dreamsDir string) (dream.Meta, error) {
	in, err := dreamPrepare(e, da, ws, live, dreamsDir)
	if err != nil {
		return dream.Meta{}, err
	}
	changes := &dream.Changes{}
	tokens, partial, err := dreamModelPass(ctx, e, da, in, changes)
	if err != nil {
		return dream.Meta{}, err
	}
	if ctx.Err() != nil {
		return dream.Meta{}, &core.ExitError{Code: core.ExitInterrupted, Reason: "interrupted"}
	}
	return dreamPersist(e, in, changes.All(), tokens, partial, dreamsDir)
}

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

	in.since = dream.Watermark(dreamsDir, now)
	if da.since != "" {
		if in.since, err = parseSince(da.since, now); err != nil {
			return in, usageErr("%v", err)
		}
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
// the tokens used and whether the pass stopped at a cap; an interrupt or
// any other stop is an error.
func dreamModelPass(ctx context.Context, e *env, da dreamArgs, in dreamInputs, changes *dream.Changes) (int, bool, error) {
	runCtx, cancel := context.WithTimeout(ctx, da.timeout)
	defer cancel()
	var ui core.UI = core.NewQuietUI()
	if !da.json && e.interactive { // progress on the terminal only
		ui = core.NewPrinter(os.Stderr, false)
	}
	ui = core.NewRedactingUI(ui, e.red.Redact)
	log, err := core.OpenEventLog(dreamLogPath(e.home, in.sessionID), nil, e.red.Redact)
	if err != nil {
		return 0, false, err
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
		return 0, false, closeErr
	}
	var ee *core.ExitError
	switch {
	case err == nil:
		return pool.Used(), false, nil
	case errors.As(err, &ee) && ee.Code == core.ExitBudget:
		return pool.Used(), true, nil
	}
	return 0, false, err
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

// appendDreamEvent reopens a dream's session log and appends one event.
func appendDreamEvent(home, sessionID, typ string, data map[string]any, redact func(string) string) error {
	if !validSessionID.MatchString(sessionID) {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	log, err := core.OpenEventLog(dreamLogPath(home, sessionID), nil, redact)
	if err != nil {
		return err
	}
	return errors.Join(log.Append(typ, data), log.Close())
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

// dreamJSON is jas dream --json's one object: dream.json plus the report
// path and the open lint count.
type dreamJSON struct {
	dream.Meta
	Report   string `json:"report"`
	LintOpen int    `json:"lint_open"`
}

// dreamOutput prints the JSON object, or the summary and, on a terminal,
// the apply question.
func dreamOutput(ctx context.Context, e *env, da dreamArgs, out io.Writer, m dream.Meta, live, dreamsDir string) error {
	reportPath := filepath.Join(dreamsDir, m.ID, dreamReport)
	if da.json {
		data, err := json.Marshal(dreamJSON{Meta: m, Report: reportPath, LintOpen: m.Counts.LintOpen})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	}
	c := m.Counts
	fmt.Fprintf(out, "dream %s: %d changed, %d added, %d removed; lint %d fixed, %d open; %d promotion candidates\n",
		m.ID, c.Changed, c.Added, c.Removed, c.LintFixed, c.LintOpen, c.Promote)
	if m.Partial {
		fmt.Fprintln(out, "partial: the dream stopped at its cap")
	}
	warnings := reportWarnings(reportPath)
	if warnings != "" {
		fmt.Fprintln(out, warnings)
	}
	fmt.Fprintf(out, "report: %s\n", reportPath)
	if !e.interactive {
		return nil
	}
	ask := func(p string) string { return askLine(ctx, e.stdin, os.Stderr, p) }
	for {
		switch strings.ToLower(strings.TrimSpace(ask(dreamRunAsk))) {
		case "d", "diff":
			rows, err := dreamDiff(live, filepath.Join(dreamsDir, m.ID, dreamProposed), e.red.Redact)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, r)
			}
		case "y", "yes":
			req := dreamApply{home: e.home, memDir: live, dreamsDir: dreamsDir, id: m.ID, out: out, redact: e.red.Redact}
			if warnings != "" { // apply asks again when the report warns
				req.ask = ask
			}
			return applyDreamAndLog(req)
		default:
			fmt.Fprintf(out, "kept pending: jas dream apply %s\n", m.ID)
			return nil
		}
	}
}

// reportWarnings is the report's Warnings section, or "" when it has none.
func reportWarnings(reportPath string) string {
	data, err := os.ReadFile(reportPath)
	if err != nil {
		return ""
	}
	s := string(data)
	i := strings.Index(s, "\n"+warningsHeading+"\n")
	if i < 0 {
		return ""
	}
	s = s[i+1:]
	if j := strings.Index(s[len(warningsHeading):], "\n## "); j >= 0 {
		s = s[:len(warningsHeading)+j]
	}
	return strings.TrimSpace(s)
}

// dreamDiff renders, per path that differs between the live store and a
// proposal, a "--- path" header and the plain diff rows.
func dreamDiff(live, proposed string, redact func(string) string) ([]string, error) {
	a, err := dream.HashTree(live)
	if err != nil {
		return nil, err
	}
	b, err := dream.HashTree(proposed)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, p := range dream.ChangedPaths(a, b) {
		old, err := readOptional(filepath.Join(live, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		nw, err := readOptional(filepath.Join(proposed, filepath.FromSlash(p)))
		if err != nil {
			return nil, err
		}
		rows = append(rows, "--- "+p)
		rows = append(rows, core.FormatDiffPlain(core.ComputeDiff(core.Change{Path: p, Old: old, New: nw}, redact))...)
	}
	return rows, nil
}

// readOptional reads a file, "" when it does not exist.
func readOptional(p string) (string, error) {
	data, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	return string(data), err
}

// dreamApply is one apply of a pending dream.
type dreamApply struct {
	home, memDir, dreamsDir, id string
	out                         io.Writer
	// ask, when set, asks the owner to confirm after the diff stat and
	// warnings; nil means they already agreed (--yes).
	ask    func(prompt string) string
	redact func(string) string
}

// applyDreamAndLog shows a pending dream's diff stat and warnings, confirms,
// applies it and logs dream_applied in the dream's session log. A
// *dream.StaleError is returned (exit 1); a *dream.FollowUpError means the
// apply took effect, so it is logged and only warned about.
func applyDreamAndLog(a dreamApply) error {
	m, err := dream.Load(a.dreamsDir, a.id)
	if err != nil {
		return err
	}
	if m.State != dream.Pending {
		return fmt.Errorf("dream %s is %s, not pending", m.ID, m.State)
	}
	c := m.Counts
	fmt.Fprintf(a.out, "dream %s: %d changed, %d added, %d removed\n", m.ID, c.Changed, c.Added, c.Removed)
	if w := reportWarnings(filepath.Join(a.dreamsDir, m.ID, dreamReport)); w != "" {
		fmt.Fprintln(a.out, w)
	}
	if a.ask != nil {
		if ans := strings.ToLower(strings.TrimSpace(a.ask(dreamApplyAsk))); ans != "y" && ans != "yes" {
			fmt.Fprintf(a.out, "kept pending: jas dream apply %s\n", m.ID)
			return nil
		}
	}
	applied, err := dream.Apply(a.memDir, a.dreamsDir, m)
	var follow *dream.FollowUpError
	if err != nil && !errors.As(err, &follow) {
		return err
	}
	warn := func(w string) { fmt.Fprintln(os.Stderr, core.Sanitize(a.redact("jas: warning: "+w))) }
	if err := appendDreamEvent(a.home, applied.SessionID, core.EvDreamApplied, map[string]any{
		"dream_id": applied.ID, "counts": applied.Counts,
	}, a.redact); err != nil {
		warn("dream " + applied.ID + ": log dream_applied: " + err.Error())
	}
	if follow != nil {
		warn(follow.Error())
	}
	fmt.Fprintf(a.out, "applied dream %s (undo: jas dream undo %s)\n", applied.ID, applied.ID)
	return nil
}
