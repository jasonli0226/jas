package farm

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/tools"
)

// Generated is a file a tool writes as a side effect (a lockfile): workers'
// changes to it outside their owns are undone, and Regenerate rebuilds it
// once on the merged tree.
type Generated struct {
	Path       string `yaml:"path"`       // repo-relative slash path
	Regenerate string `yaml:"regenerate"` // shell command run at the repo root
}

const npmLockRegen = "npm install --package-lock-only --no-audit --no-fund"

// lockfileDefaults are the detected root lockfiles and their commands, in
// detection order. Each keeps existing pins and upgrades nothing unrelated.
var lockfileDefaults = []Generated{
	{"package-lock.json", npmLockRegen},
	{"yarn.lock", ""}, // command depends on Berry vs classic
	{"pnpm-lock.yaml", "pnpm install --lockfile-only"},
	{"Cargo.lock", "cargo update --workspace"},
	{"uv.lock", "uv lock"},
}

const (
	yarnBerryRegen   = "yarn install --mode=update-lockfile"
	yarnClassicRegen = "yarn install --ignore-scripts --non-interactive"
)

// DetectGenerated returns the default generated files for the lockfiles at
// the root of the base commit (not the working tree), plus npm's lockfile for
// a root package.json without any JS lockfile. It never runs the tools.
func DetectGenerated(ctx context.Context, git GitRunner, main, base string) ([]Generated, error) {
	out, err := git.Run(ctx, main, "ls-tree", "--name-only", base)
	if err != nil {
		return nil, fmt.Errorf("list base files: %w", err)
	}
	root := strings.Fields(out)
	has := func(name string) bool { return slices.Contains(root, name) }
	var gen []Generated
	for _, d := range lockfileDefaults {
		if !has(d.Path) {
			continue
		}
		if d.Path == "yarn.lock" {
			d.Regenerate = yarnClassicRegen
			if has(".yarnrc.yml") {
				d.Regenerate = yarnBerryRegen
			}
		}
		gen = append(gen, d)
	}
	if has("package.json") && !has("package-lock.json") && !has("yarn.lock") && !has("pnpm-lock.yaml") {
		gen = append(gen, Generated{"package-lock.json", npmLockRegen})
	}
	return gen, nil
}

// ResolveGenerated is the run's generated list: the configured one when some
// layer set the key (possibly to []), else the detected defaults.
func ResolveGenerated(ctx context.Context, cfg Config, git GitRunner, main, base string) ([]Generated, error) {
	if cfg.generatedSet {
		return cfg.Generated, nil
	}
	return DetectGenerated(ctx, git, main, base)
}

func generatedPaths(gen []Generated) []string {
	out := make([]string, len(gen))
	for i, g := range gen {
		out[i] = g.Path
	}
	return out
}

// validateGenerated checks the configured list: relative clean paths, no
// duplicates, a command for each.
func validateGenerated(gen []Generated) error {
	seen := map[string]bool{}
	for i, g := range gen {
		p := g.Path
		switch {
		case strings.TrimSpace(p) == "":
			return fmt.Errorf("generated[%d]: path is empty", i)
		case path.IsAbs(p) || strings.HasPrefix(p, "\\") || (len(p) > 1 && p[1] == ':'):
			return fmt.Errorf("generated[%d]: path %q must be relative", i, p)
		case path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../"):
			return fmt.Errorf("generated[%d]: path %q must be a clean path inside the repo", i, p)
		case seen[p]:
			return fmt.Errorf("generated: path %q listed twice", p)
		case strings.TrimSpace(g.Regenerate) == "":
			return fmt.Errorf("generated[%d]: regenerate for %q is empty", i, p)
		}
		seen[p] = true
	}
	return nil
}

// regenFailure is a regenerate command that failed on the merged tree.
type regenFailure struct {
	gen Generated
	res core.CheckResult
}

// regenerate runs each generated file's command at the root of the merged
// tree (the integrate worktree), logs generated_regenerated, and commits the
// file alone when it changed; other tracked files the command changed are
// put back. A failing command is returned, not an error: check decides. err
// is a git failure.
func (s *Supervisor) regenerate(ctx context.Context, dir string) ([]regenFailure, error) {
	var failed []regenFailure
	for _, g := range s.Gen {
		res := core.RunCheck(ctx, s.checkSpec(g.Regenerate, dir), tools.RunShell)
		if err := s.revertOthers(ctx, dir, g.Path); err != nil {
			return nil, err
		}
		st, err := s.Git.Run(ctx, dir, "status", "--porcelain", "--", g.Path)
		if err != nil {
			return nil, err
		}
		changed := strings.TrimSpace(st) != ""
		if changed {
			msg := fmt.Sprintf("farm %s: regenerate %s\n\nJas-Farm: %s\nJas-Part: %s\n", s.FarmID, g.Path, s.FarmID, integratePart)
			if _, err := s.Git.Run(ctx, dir, "add", "--", g.Path); err != nil {
				return nil, err
			}
			if _, err := s.Git.Run(ctx, dir, "commit", "--no-verify", "-q", "-m", msg, "--", g.Path); err != nil {
				return nil, err
			}
		}
		s.event(EvGeneratedRegenerated, map[string]any{"path": g.Path, "exit_code": res.ExitCode,
			"duration_ms": res.Duration.Milliseconds(), "timed_out": res.TimedOut, "changed": changed})
		if res.ExitCode != 0 || res.TimedOut {
			failed = append(failed, regenFailure{gen: g, res: res})
		}
	}
	return failed, nil
}

// revertOthers puts back every tracked file under dir that changed, except
// keep.
func (s *Supervisor) revertOthers(ctx context.Context, dir, keep string) error {
	out, err := s.Git.Run(ctx, dir, "diff", "--name-only", "-z", "HEAD")
	if err != nil {
		return err
	}
	var back []string
	for _, p := range splitNUL(out) {
		if p != keep {
			back = append(back, p)
		}
	}
	if len(back) == 0 {
		return nil
	}
	_, err = s.Git.Run(ctx, dir, append([]string{"checkout", "HEAD", "--"}, back...)...)
	return err
}
