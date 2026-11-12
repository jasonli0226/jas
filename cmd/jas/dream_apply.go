package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
)

const (
	dreamApplyAsk = "apply? [y/N] "
	dreamRunAsk   = "apply? [y/N/d=diff] "
)

// validSessionID is core.NewSessionID's shape; a dream record's session id
// becomes a path, so anything else is refused.
var validSessionID = regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{6}$`)

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

// redactWarnings redacts every string the model reported before it is
// stored in dream.json.
func redactWarnings(w dream.Warnings, redact func(string) string) dream.Warnings {
	all := func(ss []string) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = redact(s)
		}
		return out
	}
	out := dream.Warnings{Unexplained: all(w.Unexplained), LargeRemoval: w.LargeRemoval, BestEffort: w.BestEffort}
	for _, u := range w.Unsupported {
		out.Unsupported = append(out.Unsupported, dream.Unsupported{Kind: redact(u.Kind), Paths: all(u.Paths), Reason: redact(u.Reason)})
	}
	return out
}

// writeDreamWarnings prints a dream's warnings for the apply screen. The
// paths and reasons come from the model, so each is sanitized.
func writeDreamWarnings(out io.Writer, w dream.Warnings) {
	if !w.Any() {
		return
	}
	fmt.Fprintln(out, "warnings:")
	if w.LargeRemoval {
		fmt.Fprintln(out, "  large removal: this proposal removes more than a third of the notes")
	}
	if len(w.Unexplained) > 0 {
		note := ""
		if w.BestEffort {
			note = " (best-effort: the run was partial)"
		}
		fmt.Fprintf(out, "  unexplained changes%s:\n", note)
		for _, p := range w.Unexplained {
			fmt.Fprintf(out, "    %s\n", core.Sanitize(p))
		}
	}
	if len(w.Unsupported) > 0 {
		fmt.Fprintln(out, "  unsupported (no valid citation):")
		for _, u := range w.Unsupported {
			paths := make([]string, len(u.Paths))
			for i, p := range u.Paths {
				paths[i] = core.Sanitize(p)
			}
			fmt.Fprintf(out, "    %s: %s (%s)\n", core.Sanitize(u.Kind), strings.Join(paths, ", "), core.Sanitize(u.Reason))
		}
	}
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
	writeDreamWarnings(out, m.Warnings)
	fmt.Fprintf(out, "report: %s\n", reportPath)
	if !e.interactive {
		return nil
	}
	return askApplyAfterRun(ctx, e, out, m, live, dreamsDir)
}

// askApplyAfterRun is the run's apply question on a terminal: d prints the
// diff and asks again, y applies. The summary already showed the warnings.
func askApplyAfterRun(ctx context.Context, e *env, out io.Writer, m dream.Meta, live, dreamsDir string) error {
	ask := func(p string) string { return askLine(ctx, e.stdin, os.Stderr, p) }
	for {
		ans := strings.ToLower(strings.TrimSpace(ask(dreamRunAsk)))
		if ctx.Err() != nil {
			return errInterrupted() // the proposal stays pending
		}
		switch ans {
		case "d", "diff":
			rows, err := dreamDiff(live, filepath.Join(dreamsDir, m.ID, dreamProposed), e.red.Redact)
			if err != nil {
				return err
			}
			for _, r := range rows {
				fmt.Fprintln(out, r)
			}
		case "y", "yes":
			req := dreamApply{home: e.home, memDir: live, dreamsDir: dreamsDir, id: m.ID, out: out,
				redact: e.red.Redact, warningsShown: true}
			if m.Warnings.Any() { // apply asks again when there is something to warn about
				req.ask = ask
			}
			err := applyDreamAndLog(req)
			if err == nil && ctx.Err() != nil {
				return errInterrupted()
			}
			return err
		default:
			fmt.Fprintf(out, "kept pending: jas dream apply %s\n", m.ID)
			return nil
		}
	}
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
		rows = append(rows, "--- "+core.Sanitize(p))
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
	// warningsShown: the caller already printed the warnings.
	warningsShown bool
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
	if !a.warningsShown {
		writeDreamWarnings(a.out, m.Warnings)
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
