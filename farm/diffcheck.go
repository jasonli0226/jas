// Package farm holds the farm orchestration pieces.
package farm

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonli0226/jas/core"
)

// DiffViolations returns the repo-relative slash paths in dir that changed
// since base (tracked changes, staged or not, plus untracked files not
// ignored) and fall outside rules. It catches writes the file tools never
// saw, such as shell commands. Renames count both the old and new path. nil
// rules mean unrestricted. The result is sorted and free of duplicates.
func DiffViolations(ctx context.Context, git func(ctx context.Context, dir string, args ...string) (string, error), dir, base string, rules []core.WriteRule) ([]string, error) {
	if rules == nil {
		return nil, nil
	}
	diff, err := git(ctx, dir, "diff", "--name-status", "-z", "-M", base)
	if err != nil {
		return nil, fmt.Errorf("diff against %s: %w", base, err)
	}
	changed, err := parseNameStatus(diff)
	if err != nil {
		return nil, err
	}
	untracked, err := git(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", err)
	}
	changed = append(changed, splitNUL(untracked)...)

	seen := map[string]bool{}
	var out []string
	for _, p := range changed {
		if p == "" || seen[p] || core.MatchWriteRules(rules, p) {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// parseNameStatus reads `git diff --name-status -z` output: a status field
// then one path, or two for renames and copies.
func parseNameStatus(s string) ([]string, error) {
	f := splitNUL(s)
	var paths []string
	for i := 0; i < len(f); {
		status := f[i]
		n := 1
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			n = 2
		}
		if i+n >= len(f) {
			return nil, fmt.Errorf("malformed git diff output near %q", status)
		}
		paths = append(paths, f[i+1:i+1+n]...)
		i += 1 + n
	}
	return paths, nil
}

func splitNUL(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == 0 })
}
