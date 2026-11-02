package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/jasonli0226/jas/farm"
)

const (
	approverOwner = "owner"
	reviewPrompt  = "[a]pprove  [e]dit  [r]eplan  [q]uit: "
)

// reviewIO is everything the review loop touches, so tests can script it.
type reviewIO struct {
	In     *bufio.Reader
	Out    io.Writer
	Edit   func(path string) error                                                // opens path for the owner
	Replan func(ctx context.Context, feedback string) (farm.PlannerResult, error) // sends feedback to the planner
	// Recheck re-parses and re-validates edited plan bytes.
	Recheck func(raw []byte) farm.PlannerResult
	Now     func() time.Time
}

// reviewPlan shows res and loops on the owner's choice. Approve writes the
// plan with an approved: block to outPath and returns those bytes; quit (or
// end of input) approves nothing and writes nothing.
func reviewPlan(ctx context.Context, r reviewIO, res farm.PlannerResult, outPath string) (final []byte, approved bool, err error) {
	for {
		showPlan(r.Out, res)
		line := askLine(ctx, r.In, r.Out, reviewPrompt)
		if line == "" { // end of input or cancelled: nothing approved
			return nil, false, ctx.Err()
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "a":
			if res.Refusal != nil || len(res.Problems) > 0 {
				fmt.Fprintln(r.Out, "cannot approve: the plan has problems; edit or replan")
				continue
			}
			final = farm.Approve(res.Raw, approverOwner, r.Now())
			if err := os.WriteFile(outPath, final, 0o644); err != nil {
				return nil, false, fmt.Errorf("write plan: %w", err)
			}
			return final, true, nil
		case "q":
			return nil, false, nil
		case "e":
			if res, err = editPlan(r, res); err != nil {
				return nil, false, err
			}
		case "r":
			feedback := strings.TrimSpace(askLine(ctx, r.In, r.Out, "feedback: "))
			if feedback == "" || r.Replan == nil {
				continue
			}
			next, err := r.Replan(ctx, feedback)
			if err != nil {
				return nil, false, err
			}
			res = next
		}
	}
}

// editPlan lets the owner edit the raw bytes and re-checks them.
func editPlan(r reviewIO, res farm.PlannerResult) (farm.PlannerResult, error) {
	f, err := os.CreateTemp("", "jas-plan-*.yaml")
	if err != nil {
		return res, err
	}
	defer os.Remove(f.Name())
	_, werr := f.Write(res.Raw)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return res, werr
	}
	if err := r.Edit(f.Name()); err != nil {
		return res, fmt.Errorf("editor: %w", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		return res, err
	}
	if r.Recheck == nil {
		return res, fmt.Errorf("cannot re-validate edited plan")
	}
	return r.Recheck(raw), nil
}

// showPlan prints a summary of the plan, or why it cannot be approved.
func showPlan(w io.Writer, res farm.PlannerResult) {
	p := res.Plan
	fmt.Fprintf(w, "\nPlan: %s\n", p.Task)
	for _, pt := range p.Parts {
		after := ""
		if len(pt.After) > 0 {
			after = " after " + strings.Join(pt.After, ",")
		}
		fmt.Fprintf(w, "  %-12s %-7s owns %s%s\n    %s\n", pt.ID, pt.Role, strings.Join(pt.Owns, ", "), after, pt.Goal)
	}
	if len(p.Shared) > 0 {
		fmt.Fprintf(w, "  shared (integrator only): %s\n", strings.Join(p.Shared, ", "))
	}
	if p.Contracts != "" {
		fmt.Fprintf(w, "Contracts:\n%s\n", indent(p.Contracts))
	}
	for _, i := range p.Interfaces {
		fmt.Fprintf(w, "Interface %s (%s): %s\n", i.Name, i.Owner, i.Shape)
	}
	if len(p.ProposedRoles) > 0 {
		names := make([]string, 0, len(p.ProposedRoles))
		for n := range p.ProposedRoles {
			names = append(names, n)
		}
		fmt.Fprintf(w, "Proposed roles: %s\n", strings.Join(names, ", "))
	}
	if p.WhySplit != "" {
		fmt.Fprintf(w, "Why split: %s\n", p.WhySplit)
	}
	if len(res.Problems) > 0 {
		fmt.Fprintf(w, "Problems:\n%s", farm.FormatProblems(res.Problems))
	}
	if res.Refusal != nil {
		fmt.Fprintf(w, "Refused: %s: %s\n", res.Refusal.Reason, res.Refusal.Detail)
	}
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}

// runEditor opens path in $EDITOR (vi if unset), wired to the terminal.
func runEditor(path string) error {
	ed := os.Getenv("EDITOR")
	if ed == "" {
		ed = "vi"
	}
	parts := strings.Fields(ed)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stderr, os.Stderr
	return cmd.Run()
}
