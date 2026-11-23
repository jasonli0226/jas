package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"golang.org/x/term"
)

const (
	farmManifestFile = "manifest.json"
	farmBranchPrefix = "jas/farm/"

	// shortIDLen trims hashes in status output.
	shortIDLen = 12
)

// farmStatusCmd prints a farm's parts, tokens and check result from its
// manifest. With no farm-id it picks the newest farm.
func farmStatusCmd(args []string) error {
	if len(args) > 1 {
		return usageErr("usage: jas farm status [<farm-id>]")
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	var id string
	if len(args) == 1 {
		if id, err = matchFarm(home, args[0]); err != nil {
			return err
		}
	}
	m, err := loadFarmManifest(home, id)
	if err != nil {
		return usageErr("%v", err)
	}
	printFarmStatus(os.Stdout, m)
	return nil
}

// farmCleanCmd removes a farm's worktrees (and, with --branches, its
// jas/farm/<id>/* branches), then prunes worktree metadata.
func farmCleanCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("farm clean", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	branches := fs.Bool("branches", false, "also delete the jas/farm/<id>/* branches")
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return usageErr("%v", err)
		}
		args = nil
		if fs.NArg() > 0 {
			rest = append(rest, fs.Arg(0))
			args = fs.Args()[1:]
		}
	}
	if len(rest) > 1 {
		return usageErr("usage: jas farm clean [<farm-id>] [--branches]")
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	var id string
	if len(rest) == 1 {
		if id, err = matchFarm(home, rest[0]); err != nil {
			return err
		}
	}
	m, err := loadFarmManifest(home, id)
	if err != nil {
		return usageErr("%v", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)
	git := farm.NewGit()

	paths := map[string]bool{}
	for _, p := range m.Parts {
		if p.Worktree != "" {
			paths[p.Worktree] = true
		}
	}
	if m.Integrator != nil && m.Integrator.Worktree != "" {
		paths[m.Integrator.Worktree] = true
	}
	ordered := make([]string, 0, len(paths))
	for p := range paths {
		ordered = append(ordered, p)
	}
	sort.Strings(ordered)
	for _, p := range ordered {
		if _, err := os.Stat(p); errors.Is(err, os.ErrNotExist) {
			continue // already removed
		}
		if _, err := git.Run(ctx, root, "worktree", "remove", "--force", p); err != nil {
			fmt.Fprintf(os.Stderr, "jas: could not remove worktree %s: %v\n", p, err)
			continue
		}
		fmt.Printf("removed worktree %s\n", p)
	}
	if err := farm.Prune(ctx, git, root); err != nil {
		return fmt.Errorf("git worktree prune: %w", err)
	}
	if *branches {
		if err := deleteFarmBranches(ctx, git, root, m.FarmID); err != nil {
			return err
		}
	}
	switch err := farm.RemoveFarmLock(home, m.FarmID); {
	case errors.Is(err, core.ErrLocked):
		fmt.Fprintln(os.Stderr, "jas: farm still running; lock file kept")
	case err != nil:
		fmt.Fprintf(os.Stderr, "jas: could not remove the farm's lock file: %v\n", err)
	}
	return nil
}

// deleteFarmBranches deletes every jas/farm/<id>/* branch.
func deleteFarmBranches(ctx context.Context, git farm.GitRunner, root, id string) error {
	if id == "" {
		return nil
	}
	out, err := git.Run(ctx, root, "for-each-ref", "refs/heads/"+farmBranchPrefix+id+"/", "--format=%(refname)")
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	for _, ref := range strings.Fields(out) {
		if _, err := git.Run(ctx, root, "update-ref", "-d", ref); err != nil {
			return fmt.Errorf("delete branch %s: %w", ref, err)
		}
		fmt.Printf("deleted branch %s\n", strings.TrimPrefix(ref, "refs/heads/"))
	}
	return nil
}

// farmVarsCmd prints each farm variable's value and where it came from.
func farmVarsCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("farm vars", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	trust := fs.Bool("trust-project", false, "load the project farm.yaml without asking")
	if err := fs.Parse(args); err != nil {
		return usageErr("%v", err)
	}
	if fs.NArg() != 0 {
		return usageErr("usage: jas farm vars [--trust-project]")
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)
	red := newRedactor("")
	warn := func(w string) { fmt.Fprintln(os.Stderr, core.Sanitize(red.Redact("jas: "+w))) }
	interactive := term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stderr.Fd()))
	ask := func(p string) string { return askLine(ctx, bufio.NewReader(os.Stdin), os.Stderr, p) }
	pc, err := loadProjectContext(root, home, nil, nil, profileState{}, interactive, *trust, ask, red.Redact, warn)
	if err != nil {
		return err
	}
	cfg, notices, err := farm.LoadConfig(root, home, pc.trusted)
	if err != nil {
		return usageErr("farm config: %v", err)
	}
	for _, n := range notices {
		fmt.Fprintln(os.Stderr, core.Sanitize(red.Redact("jas: "+n)))
	}
	for _, name := range sortedVarNames(cfg.Vars) {
		v := cfg.Vars[name]
		fmt.Printf("%-12s %-9s %s\n", name, v.Source, strings.Join(v.Value, ", "))
	}
	return nil
}

// sortedVarNames returns the variable names in print order.
func sortedVarNames(vars map[string]farm.Var) []string {
	names := make([]string, 0, len(vars))
	for n := range vars {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// farmIDArg validates a farm-id path element.
func farmIDArg(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", usageErr("farm id %q is not a farm id", id)
	}
	return id, nil
}

// loadFarmManifest reads <home>/farms/<id>/manifest.json; id "" picks the
// newest farm that has one.
func loadFarmManifest(home, id string) (farm.Manifest, error) {
	base := filepath.Join(home, farmsDir)
	if id == "" {
		entries, err := os.ReadDir(base)
		if err != nil {
			return farm.Manifest{}, fmt.Errorf("no farms under %s yet", base)
		}
		for i := len(entries) - 1; i >= 0; i-- { // ReadDir sorts by name; ids start with a timestamp
			if !entries[i].IsDir() {
				continue
			}
			p := filepath.Join(base, entries[i].Name(), farmManifestFile)
			if _, err := os.Stat(p); err == nil {
				return farm.LoadManifest(p)
			}
		}
		return farm.Manifest{}, fmt.Errorf("no farm with a manifest under %s", base)
	}
	m, err := farm.LoadManifest(filepath.Join(base, id, farmManifestFile))
	if err != nil {
		return farm.Manifest{}, fmt.Errorf("farm %s: %w", id, err)
	}
	return m, nil
}

// printFarmStatus prints a manifest as one row per part plus the verdict.
func printFarmStatus(w io.Writer, m farm.Manifest) {
	fmt.Fprintf(w, "farm %s  base %s  plan %s\n", m.FarmID, shortID(m.Base), shortID(m.PlanSHA256))
	fmt.Fprintf(w, "%-14s %-8s %-8s %8s  %s\n", "part", "role", "status", "tokens", "branch")
	ids := make([]string, 0, len(m.Parts))
	for id := range m.Parts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		p := m.Parts[id]
		fmt.Fprintf(w, "%-14s %-8s %-8s %8d  %s\n", id, p.Role, p.Status, p.Tokens, p.Branch)
		if p.FailureReason != "" {
			fmt.Fprintf(w, "    failure: %s (exit %d)\n", p.FailureReason, p.ExitCode)
		}
	}
	if m.Integrator != nil {
		i := m.Integrator
		fmt.Fprintf(w, "%-14s %-8s %-8s %8d  %s\n", "integrator", "coder", i.Status, i.Tokens, i.Branch)
	}
	switch {
	case m.Check == nil:
		fmt.Fprintln(w, "check: not run")
	case m.Check.TimedOut:
		fmt.Fprintf(w, "check: timed out after %s\n", m.Check.Duration)
	default:
		fmt.Fprintf(w, "check: exit %d in %s\n", m.Check.ExitCode, m.Check.Duration.Round(time.Millisecond))
	}
	printReviewStatus(w, m.Review)
	choice, reason := m.FinalChoice, m.Reason
	if reason == "" { // no outcome yet: end() writes Reason last, after any FinalChoice
		fmt.Fprintf(w, "final: none yet  total tokens %d\n", m.TotalTokens)
		return
	}
	if choice == "" {
		choice = "-"
	}
	if reason == "" {
		reason = "-"
	}
	fmt.Fprintf(w, "final: %s (%s) exit %d  total tokens %d\n", choice, reason, m.ExitCode, m.TotalTokens)
}

// printReviewStatus prints the review verdict and each open finding.
func printReviewStatus(w io.Writer, r *farm.ReviewState) {
	if r == nil {
		return
	}
	fmt.Fprintf(w, "review: %s  open %d\n", r.Status, len(r.Open))
	byID := map[string]farm.Finding{}
	for _, rd := range r.Rounds {
		for _, f := range rd.Findings {
			byID[f.ID] = f
		}
	}
	for _, id := range r.Open {
		f, ok := byID[id]
		if !ok {
			fmt.Fprintf(w, "    %s\n", id)
			continue
		}
		fmt.Fprintf(w, "    %s [%s] %s %s: %s\n", f.ID, f.Kind, f.PlanRef, f.Where, f.Text)
	}
}

// shortID trims a hash for display.
func shortID(s string) string {
	if len(s) > shortIDLen {
		return s[:shortIDLen]
	}
	return s
}
