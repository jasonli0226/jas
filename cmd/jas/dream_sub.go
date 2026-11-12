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
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/dream"
)

// dreamSubcommands are the words after jas dream that are not run flags.
var dreamSubcommands = []string{"list", "diff", "apply", "discard", "undo"}

// subArgs are one subcommand's parsed arguments.
type subArgs struct {
	id    string
	json  bool
	yes   bool
	flags map[string]string
}

// parseDreamSub reads a subcommand's flags and optional <id> (flags may
// come before or after the id). Only list takes --json and only apply
// takes --yes.
func parseDreamSub(cmd string, args []string) (subArgs, error) {
	var sa subArgs
	fs := flag.NewFlagSet("dream "+cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	if cmd == "list" {
		fs.BoolVar(&sa.json, "json", false, "")
	}
	if cmd == "apply" {
		fs.BoolVar(&sa.yes, "yes", false, "")
	}
	profile := fs.String("profile", "", "")
	var pos []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return sa, usageErr("%s", dreamUsage)
		}
		args = fs.Args()
		if len(args) > 0 {
			pos, args = append(pos, args[0]), args[1:]
		}
	}
	max := 1
	if cmd == "list" {
		max = 0
	}
	if len(pos) > max {
		return sa, usageErr("%s", dreamUsage)
	}
	if len(pos) == 1 {
		if !dream.ValidID(pos[0]) {
			return sa, usageErr("invalid dream id %q (want <UTC timestamp>-<4 hex>)", core.Sanitize(pos[0]))
		}
		sa.id = pos[0]
	}
	sa.flags = map[string]string{}
	if *profile != "" {
		sa.flags["profile"] = *profile
	}
	return sa, nil
}

func dreamSub(ctx context.Context, cmd string, args []string, out io.Writer) error {
	sa, err := parseDreamSub(cmd, args)
	if err != nil {
		return err
	}
	st, err := openDreamStore(ctx, sa.flags)
	if err != nil {
		return err
	}
	defer st.close()
	switch cmd {
	case "list":
		return dreamList(st, sa, out)
	case "diff":
		return dreamDiffCmd(st, sa, out)
	case "apply":
		return dreamApplyCmd(ctx, st, sa, out)
	case "discard":
		return dreamDiscardCmd(st, sa, out)
	default:
		return dreamUndoCmd(st, sa, out)
	}
}

// resolveDream loads the dream named by id, or the newest in state def.
func resolveDream(st dreamStore, id string, def dream.State) (dream.Meta, error) {
	if id != "" {
		return dream.Load(st.dreamsDir, id)
	}
	ms, err := dream.List(st.dreamsDir)
	if err != nil {
		return dream.Meta{}, err
	}
	m, ok := dream.Newest(ms, def)
	if !ok {
		return m, fmt.Errorf("no %s dream", def)
	}
	return m, nil
}

// isStaleNow reports whether the live store differs from the store a
// pending dream started from. It saves nothing: apply decides for real.
func isStaleNow(live string, m dream.Meta) ([]string, error) {
	now, err := dream.HashTree(live)
	if err != nil {
		return nil, err
	}
	return dream.ChangedPaths(m.Base, now), nil
}

type listRow struct {
	dream.Meta
	Stale bool `json:"stale"`
}

func dreamList(st dreamStore, sa subArgs, out io.Writer) error {
	ms, err := dream.List(st.dreamsDir)
	if err != nil {
		return err
	}
	rows := make([]listRow, 0, len(ms))
	for _, m := range ms {
		r := listRow{Meta: m}
		if m.State == dream.Pending {
			changed, err := isStaleNow(st.live, m)
			if err != nil {
				return err
			}
			r.Stale = len(changed) > 0
		}
		rows = append(rows, r)
	}
	if sa.json {
		data, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	}
	for _, r := range rows {
		state := string(r.State)
		if r.Stale {
			state += " (stale)"
		}
		c := r.Counts
		fmt.Fprintf(out, "%s  %-16s %d changed, %d added, %d removed\n", r.ID, state, c.Changed, c.Added, c.Removed)
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "no dreams")
	}
	return nil
}

func dreamDiffCmd(st dreamStore, sa subArgs, out io.Writer) error {
	m, err := resolveDream(st, sa.id, dream.Pending)
	if err != nil {
		return err
	}
	idDir := filepath.Join(st.dreamsDir, m.ID)
	var rows []string
	switch m.State {
	case dream.Pending:
		changed, err := isStaleNow(st.live, m)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			fmt.Fprintf(out, "warning: memory changed since the dream started (%s); apply will refuse\n", sanitizeJoin(changed))
		}
		rows, err = dreamDiff(st.live, filepath.Join(idDir, dreamProposed), st.e.red.Redact)
		if err != nil {
			return err
		}
	case dream.Applied:
		rows, err = dreamDiff(filepath.Join(idDir, "before"), st.live, st.e.red.Redact)
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("dream %s is %s; only pending and applied dreams have a diff", m.ID, m.State)
	}
	for _, r := range rows {
		fmt.Fprintln(out, r)
	}
	if m.State == dream.Pending {
		report, err := readOptional(filepath.Join(idDir, dreamReport))
		if err != nil {
			return err
		}
		fmt.Fprintln(out)
		for _, line := range strings.Split(report, "\n") {
			fmt.Fprintln(out, core.SanitizeLine(st.e.red.Redact(line)))
		}
	}
	return nil
}

func sanitizeJoin(ss []string) string {
	c := make([]string, len(ss))
	for i, s := range ss {
		c[i] = core.Sanitize(s)
	}
	return strings.Join(c, ", ")
}

func dreamApplyCmd(ctx context.Context, st dreamStore, sa subArgs, out io.Writer) error {
	if !sa.yes && !st.e.interactive {
		return usageErr("apply needs a terminal to confirm; pass --yes to apply without asking")
	}
	m, err := resolveDream(st, sa.id, dream.Pending)
	if err != nil {
		return err
	}
	req := dreamApply{home: st.e.home, memDir: st.live, dreamsDir: st.dreamsDir, id: m.ID, out: out, redact: st.e.red.Redact}
	if !sa.yes {
		req.ask = func(p string) string { return askLine(ctx, st.e.stdin, os.Stderr, p) }
	}
	err = applyDreamAndLog(req)
	if err == nil && ctx.Err() != nil {
		return errInterrupted()
	}
	return err
}

func dreamDiscardCmd(st dreamStore, sa subArgs, out io.Writer) error {
	m, err := resolveDream(st, sa.id, dream.Pending)
	if err != nil {
		return err
	}
	return finishDreamChange(st, out, dream.Discard, core.EvDreamDiscarded, "discarded", m)
}

func dreamUndoCmd(st dreamStore, sa subArgs, out io.Writer) error {
	m, err := resolveDream(st, sa.id, dream.Applied)
	if err != nil {
		return err
	}
	if m.State != dream.Applied {
		return fmt.Errorf("dream %s is %s, not applied", m.ID, m.State)
	}
	return finishDreamChange(st, out, func(dir string, m dream.Meta) (dream.Meta, error) {
		return dream.Undo(st.live, dir, m)
	}, core.EvDreamUndone, "undone", m)
}

// finishDreamChange runs a discard or undo, logs its event in the dream's
// session log and prints the result. A *dream.FollowUpError means the change
// took effect: it is logged and only warned about.
func finishDreamChange(st dreamStore, out io.Writer, do func(dir string, m dream.Meta) (dream.Meta, error), ev, verb string, m dream.Meta) error {
	done, err := do(st.dreamsDir, m)
	var follow *dream.FollowUpError
	if err != nil && !errors.As(err, &follow) {
		return err
	}
	warn := func(w string) { fmt.Fprintln(os.Stderr, core.Sanitize(st.e.red.Redact("jas: warning: "+w))) }
	if err := appendDreamEvent(st.e.home, done.SessionID, ev, map[string]any{"dream_id": done.ID}, st.e.red.Redact); err != nil {
		warn("dream " + done.ID + ": log " + ev + ": " + err.Error())
	}
	if follow != nil {
		warn(follow.Error())
	}
	fmt.Fprintf(out, "%s dream %s\n", verb, done.ID)
	return nil
}
