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
//
// Resolve serves write and edit, so it always applies the full deny floor:
// the profile read lift is cleared even on a scope from ProfileScope.
func (p ExecPolicy) Resolve(path string) (string, error) {
	p.liftHome = false
	resolved, err := p.resolveInRoot(path)
	if err != nil {
		return "", err
	}
	if err := p.checkDeny(path, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// resolveInRoot is Resolve without the deny check.
func (p ExecPolicy) resolveInRoot(path string) (string, error) {
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
	return resolved, nil
}

var errOutsideRoot = errors.New("path outside project root")

// ResolveRead is Resolve for read-only tools: an absolute path inside one of
// ReadRoots is allowed too. There the deny floor applies with the read root
// standing in for the project root, so a skill's .env stays denied. A path
// in the project that the full floor denies may still be read through a
// lifted scope (the active profile's skills when the project holds the jas
// home, or p itself when it came from ProfileScope).
func (p ExecPolicy) ResolveRead(path string) (string, error) {
	path = expandHome(path)
	resolved, err := p.Resolve(path)
	var abs string
	var scope ExecPolicy
	switch {
	case err == nil:
		return resolved, nil
	case errors.Is(err, errOutsideRoot) && filepath.IsAbs(path):
		a, rerr := evalExisting(filepath.Clean(path))
		if rerr != nil {
			return "", rerr
		}
		s, ok := p.readRoot(a)
		if !ok {
			return "", err
		}
		abs, scope = a, s
	case errors.Is(err, errDenied):
		a, rerr := p.resolveInRoot(path)
		if rerr != nil {
			return "", rerr
		}
		s, ok := p.liftedScope(a)
		if !ok {
			return "", err
		}
		abs, scope = a, s
	default:
		return "", err
	}
	if err := scope.checkDeny(path, abs); err != nil {
		return "", err
	}
	return abs, nil
}

// liftedScope returns the profile-lifted read scope for abs: p itself when it
// is one, or the read root inside the active profile's skills holding abs.
func (p ExecPolicy) liftedScope(abs string) (ExecPolicy, bool) {
	if p.liftHome {
		return p, true
	}
	scope, ok := p.readRoot(abs)
	return scope, ok && scope.liftHome
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
// inside the project, or p rooted at the read root that holds resolved. A
// read root inside the active profile's skills wins over the project, so the
// skills stay readable when the project holds the jas home.
func (p ExecPolicy) ReadScope(resolved string) ExecPolicy {
	if scope, ok := p.liftedScope(resolved); ok {
		return scope
	}
	if root, err := filepath.EvalSymlinks(p.Root); err == nil && within(root, resolved) {
		return p
	}
	if scope, ok := p.readRoot(resolved); ok {
		return scope
	}
	return p
}

// readRoot returns p rooted at the read root holding abs. A read root inside
// the active profile's skills gets liftHome, so its DenyMatcher lets reads
// through the jas home floor there (and only there). When that read root is
// in the project, projectRoot keeps the project's relative patterns in force.
func (p ExecPolicy) readRoot(abs string) (ExecPolicy, bool) {
	skills := p.profileSkillsDir()
	project, perr := filepath.EvalSymlinks(p.Root)
	for _, r := range p.ReadRoots {
		if within(r, abs) {
			scope := p
			scope.Root, scope.ReadRoots, scope.projectRoot = r, nil, ""
			scope.liftHome = skills != "" && within(skills, r)
			if scope.liftHome && perr == nil && within(project, r) {
				scope.projectRoot = project
			}
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

// ProfileScope is the read scope rooted at ProfileSkills, as readRoot
// returns it for a read root there; skill discovery uses its Denied. With no
// ProfileSkills it is p without the lift.
func (p ExecPolicy) ProfileScope() ExecPolicy {
	scope := p
	scope.liftHome, scope.projectRoot = false, ""
	if skills := p.profileSkillsDir(); skills != "" {
		scope.Root, scope.ReadRoots, scope.liftHome = skills, nil, true
	}
	return scope
}

// profileSkillsDir is ProfileSkills resolved, or "" when the read exception
// is off. It is off unless the directory exists, is not itself a symlink,
// and resolves to <dir>/profiles/<name>/skills: a skills link to an ancestor
// such as the jas home must not widen the lift over auth.json.
func (p ExecPolicy) profileSkillsDir() string {
	if p.ProfileSkills == "" {
		return ""
	}
	clean := filepath.Clean(p.ProfileSkills)
	if fi, err := os.Lstat(clean); err != nil || fi.Mode()&os.ModeSymlink != 0 {
		return ""
	}
	r, err := filepath.EvalSymlinks(clean)
	if err != nil || filepath.Base(r) != "skills" || filepath.Base(filepath.Dir(filepath.Dir(r))) != "profiles" {
		return ""
	}
	return r
}
