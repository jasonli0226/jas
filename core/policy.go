package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// DefaultMaxOutput is the tool output cap in bytes when ExecPolicy.MaxOutput is 0.
const DefaultMaxOutput = 32768

// OutputLimit returns the effective tool output cap.
func (p ExecPolicy) OutputLimit() int {
	if p.MaxOutput <= 0 {
		return DefaultMaxOutput
	}
	return p.MaxOutput
}

// FindRoot returns the git top-level directory containing cwd, or cwd itself
// when cwd is not inside a git repository.
func FindRoot(cwd string) string {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return cwd
	}
	if root := strings.TrimSpace(string(out)); root != "" {
		return root
	}
	return cwd
}

// Resolve maps a tool-supplied path to an absolute, symlink-free path inside
// Root. Relative paths are joined with Root. For a path that does not exist
// yet, the nearest existing ancestor is resolved and the rest re-appended.
func (p ExecPolicy) Resolve(path string) (string, error) {
	if p.Root == "" {
		return "", errors.New("project root not set")
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		return "", fmt.Errorf("project root %s: %w", p.Root, err)
	}
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, abs)
	}
	resolved, err := evalExisting(filepath.Clean(abs))
	if err != nil {
		return "", err
	}
	if !within(root, resolved) {
		return "", fmt.Errorf("%w: %s", errOutsideRoot, path)
	}
	if err := p.checkDeny(path, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

var errOutsideRoot = errors.New("path outside project root")

// ResolveRead is Resolve for read-only tools: an absolute path inside one of
// ReadRoots is allowed too. There the deny floor applies with the read root
// standing in for the project root, so a skill's .env stays denied.
func (p ExecPolicy) ResolveRead(path string) (string, error) {
	path = expandHome(path)
	resolved, err := p.Resolve(path)
	if !errors.Is(err, errOutsideRoot) || !filepath.IsAbs(path) {
		return resolved, err
	}
	abs, rerr := evalExisting(filepath.Clean(path))
	if rerr != nil {
		return "", rerr
	}
	scope, ok := p.readRoot(abs)
	if !ok {
		return "", err
	}
	if err := scope.checkDeny(path, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// expandHome turns a leading ~/ (how the skill catalog writes home paths)
// into the home directory; other paths are returned as they are.
func expandHome(path string) string {
	rest, ok := strings.CutPrefix(path, "~/")
	if !ok {
		return path
	}
	if h := homeDir(); h != "" {
		return filepath.Join(h, filepath.FromSlash(rest))
	}
	return path
}

// ReadScope returns the policy for walking or listing under resolved: p
// inside the project, or p rooted at the read root that holds resolved.
func (p ExecPolicy) ReadScope(resolved string) ExecPolicy {
	if root, err := filepath.EvalSymlinks(p.Root); err == nil && within(root, resolved) {
		return p
	}
	if scope, ok := p.readRoot(resolved); ok {
		return scope
	}
	return p
}

func (p ExecPolicy) readRoot(abs string) (ExecPolicy, bool) {
	for _, r := range p.ReadRoots {
		if within(r, abs) {
			scope := p
			scope.Root, scope.ReadRoots = r, nil
			return scope, true
		}
	}
	return ExecPolicy{}, false
}

// evalExisting resolves symlinks of the longest existing prefix of path and
// re-appends the missing tail.
func evalExisting(path string) (string, error) {
	var tail []string
	cur := path
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if _, lerr := os.Lstat(cur); lerr == nil {
			return "", fmt.Errorf("path is a dangling symlink: %s", cur)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// WriteAllowed reports whether write and edit may change the resolved
// absolute path. With no Writes everything is allowed; otherwise the
// Root-relative slash path must match every glob of some rule. Matching is
// case-sensitive so an allow-list never widens on case.
func (p ExecPolicy) WriteAllowed(resolved string) bool {
	if p.Writes == nil {
		return true
	}
	root, err := filepath.EvalSymlinks(p.Root)
	if err != nil {
		root = p.Root
	}
	if !filepath.IsAbs(resolved) || !within(root, resolved) {
		return false
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil {
		return false
	}
	return MatchWriteRules(p.Writes, filepath.ToSlash(rel))
}

// MatchWriteRules reports whether the repo-relative slash path rel matches
// every glob of at least one rule.
func MatchWriteRules(rules []WriteRule, rel string) bool {
	for _, r := range rules {
		if len(r.All) == 0 {
			continue
		}
		all := true
		for _, g := range r.All {
			if ok, _ := doublestar.Match(g, rel); !ok {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}
