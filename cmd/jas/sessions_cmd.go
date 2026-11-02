package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/sessions"
)

// sessionsDenyPattern keeps file tools away from peer session records, so a
// model cannot forge or delete them (session-awareness spec, Security).
const sessionsDenyPattern = ".jas/sessions/**"

// recordTitleRunes caps a record title, as the spec's first-80-chars rule.
const recordTitleRunes = 80

// registerRecord writes this session's record. Records are advisory: a
// failure is returned for the caller to report, never fatal.
func registerRecord(ctx context.Context, root, mode, logPath, title string, redact func(string) string) (*sessions.Handle, error) {
	dir, err := sessions.Dir(root)
	if err != nil {
		return nil, err
	}
	_, _ = sessions.Prune(dir, time.Now()) // spec: a starting session clears stale records
	if r := []rune(title); len(r) > recordTitleRunes {
		title = string(r[:recordTitleRunes])
	}
	h, err := sessions.Register(dir, sessions.Record{
		SessionID: filepath.Base(filepath.Dir(logPath)), Mode: mode, Title: redact(title),
		Branch: gitBranch(ctx, root), Worktree: root, Log: logPath,
	})
	if err != nil {
		return nil, err
	}
	h.Start(ctx)
	return h, nil
}

// touchHook feeds Executor.Touched into the record as root-relative paths.
func touchHook(h *sessions.Handle, root string) func(tool, path string) {
	return func(tool, path string) {
		if tool == "shell" {
			h.ShellUsed()
			return
		}
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			h.Touch(filepath.ToSlash(rel))
		}
	}
}

// gitBranch is root's current branch, or "" outside git.
func gitBranch(ctx context.Context, root string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sessionsCmd is `jas sessions [--prune]`: list this repo's live session
// records, or delete stale ones.
func sessionsCmd(args []string) error {
	fs := flag.NewFlagSet("sessions", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	prune := fs.Bool("prune", false, "delete stale session records")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return usageErr("usage: jas sessions [--prune]")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	dir, err := sessions.Dir(core.FindRoot(cwd))
	if err != nil {
		return err
	}
	now := time.Now()
	if *prune {
		n, err := sessions.Prune(dir, now)
		if err != nil {
			return err
		}
		fmt.Printf("removed %d stale session record(s)\n", n)
		return nil
	}
	recs, err := sessions.List(dir, now)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Println("no other sessions in this repo")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SESSION\tMODE\tSTATE\tBRANCH\tTOUCHED\tTITLE")
	for _, r := range recs {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", r.SessionID, r.Mode, r.State, r.Branch, len(r.Touched), core.Sanitize(r.Title))
	}
	return w.Flush()
}
