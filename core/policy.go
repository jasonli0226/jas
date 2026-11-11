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
// Resolve serves write and edit, so it applies the full deny floor, with
// one exception: a path that resolves inside the active profile's skills or
// memory directory is allowed outside Root and through the jas home floor
// (see profileWrite). A leading ~/ is expanded first, as ResolveRead does.
func (p ExecPolicy) Resolve(path string) (string, error) {
	p.liftHome = false
	path = expandHome(path)
	resolved, err := p.resolveInRoot(path)
	if err == nil {
		err = p.checkDeny(path, resolved)
	}
	if err == nil {
		return resolved, nil
	}
	if !errors.Is(err, errOutsideRoot) && !errors.Is(err, errDenied) {
		return "", err
	}
	if abs, ok, werr := p.profileWrite(path); ok {
		return abs, werr
	}
	return "", err
}

// profileWrite resolves path for a write in the active profile's skills or
// memory directory. ok reports that path resolves inside one; the deny
// floor then applies there without JasHomeFloor and HomeDeny, rooted at
// that directory (and at the project too when the project holds it), so a
// skill's or note's .env or a link out to auth.json stays refused.
func (p ExecPolicy) profileWrite(path string) (_ string, ok bool, _ error) {
	ls := p.lifts()
	if len(ls) == 0 {
		return "", false, nil
	}
	abs := path
	if !filepath.IsAbs(abs) {
		root, err := filepath.EvalSymlinks(p.Root)
		if err != nil {
			return "", false, nil
		}
		abs = filepath.Join(root, abs)
	}
	resolved, err := evalExisting(filepath.Clean(abs))
	if err != nil {
		return "", false, nil
	}
	for _, l := range ls {
		if within(l.resolved, resolved) {
			return p.liftedWrite(l, path, resolved)
		}
	}
	return "", false, nil
}

// liftedWrite applies the deny floor to a write resolved inside l.
func (p ExecPolicy) liftedWrite(l lift, path, resolved string) (_ string, ok bool, _ error) {
	scope := p
	scope.Root, scope.ReadRoots, scope.liftHome, scope.projectRoot = l.resolved, nil, true, ""
	if project, err := filepath.EvalSymlinks(p.Root); err == nil && within(project, l.resolved) {
		scope.projectRoot = project
	}
	if err := scope.checkDeny(path, resolved); err != nil {
		return "", true, err
	}
	return resolved, true, nil
}

// InProfileSkills reports whether the resolved path lies in the active
// profile's skills directory, where writes are always put to the user.
func (p ExecPolicy) InProfileSkills(resolved string) bool {
	l, _ := p.skillsLift()
	return l.resolved != "" && within(l.resolved, resolved)
}

// InProfileMemory reports whether the resolved path lies in the active
// profile's memory directory, where writes are always put to the user.
func (p ExecPolicy) InProfileMemory(resolved string) bool {
	l, _ := p.memoryLift()
	return l.resolved != "" && within(l.resolved, resolved)
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
// lifted scope (the active profile's skills or memory when the project
// holds the jas home, or p itself when it came from ProfileScope).
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
		if errors.Is(rerr, errOutsideRoot) { // denied in a profile lift
			return "", err
		}
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
// is one, or the read root inside the active profile's skills or memory
// holding abs.
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
// read root inside the active profile's skills or memory wins over the
// project, so they stay readable when the project holds the jas home.
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
// the active profile's skills or memory gets liftHome, so its DenyMatcher
// lets reads through the jas home floor in those directories (and only
// there). When that read root is in the project, projectRoot keeps the
// project's relative patterns in force.
func (p ExecPolicy) readRoot(abs string) (ExecPolicy, bool) {
	ls := p.lifts()
	project, perr := filepath.EvalSymlinks(p.Root)
	for _, r := range p.ReadRoots {
		if within(r, abs) {
			scope := p
			scope.Root, scope.ReadRoots, scope.projectRoot = r, nil, ""
			scope.liftHome = liftedRoot(ls, r)
			if scope.liftHome && perr == nil && within(project, r) {
				scope.projectRoot = project
			}
			return scope, true
		}
	}
	return ExecPolicy{}, false
}

// liftedRoot reports whether the read root r lies inside one of ls.
func liftedRoot(ls []lift, r string) bool {
	for _, l := range ls {
		if within(l.resolved, r) {
			return true
		}
	}
	return false
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
// ProfileSkills lift it is p without the lift. Like any lifted scope it
// also lifts the profile memory (see denyMatch).
func (p ExecPolicy) ProfileScope() ExecPolicy {
	scope := p
	scope.liftHome, scope.projectRoot = false, ""
	if skills := p.profileSkillsDir(); skills != "" {
		scope.Root, scope.ReadRoots, scope.liftHome = skills, nil, true
	}
	return scope
}

// profileSkillsDir is the resolved ProfileSkills directory, or "" when the
// read exception is off; see profileLift.
func (p ExecPolicy) profileSkillsDir() string {
	l, _ := p.skillsLift()
	return l.resolved
}

// ProfileSkillsIgnored explains why an existing ProfileSkills directory gets
// no read exception (its skills are then not loaded), or returns "" when it
// is lifted, missing or unset.
func (p ExecPolicy) ProfileSkillsIgnored() string {
	_, why := p.skillsLift()
	return why
}

// ProfileMemoryIgnored explains why an existing ProfileMemory directory gets
// no exception (memory is then unreadable and unwritable), or returns ""
// when it is lifted, missing or unset.
func (p ExecPolicy) ProfileMemoryIgnored() string {
	_, why := p.memoryLift()
	return why
}

// Profile lift leaves: the directories under <home>/profiles/<name> that
// get the read and write exception.
const (
	skillsLeaf = "skills"
	memoryLeaf = "memory"
)

// lift is a profile read exception: the active profile's skills or memory
// directory as spelled (<home>/profiles/<name>/<leaf>) and as it resolves
// through symlinks. A path inside either is read without the jas home floor.
type lift struct{ lexical, resolved string }

func (l lift) holds(abs string) bool {
	return l.resolved != "" && (within(l.resolved, abs) || within(l.lexical, abs))
}

// holdsAny reports whether one of ls holds abs.
func holdsAny(ls []lift, abs string) bool {
	for _, l := range ls {
		if l.holds(abs) {
			return true
		}
	}
	return false
}

// skillsLift is the ProfileSkills lift; see profileLift.
func (p ExecPolicy) skillsLift() (lift, string) {
	return p.profileLift(skillsLeaf)
}

// memoryLift is the ProfileMemory lift; see profileLift.
func (p ExecPolicy) memoryLift() (lift, string) {
	return p.profileLift(memoryLeaf)
}

// lifts returns the profile lifts that are on: skills, then memory.
func (p ExecPolicy) lifts() []lift {
	var out []lift
	for _, leaf := range []string{skillsLeaf, memoryLeaf} {
		if l, _ := p.profileLift(leaf); l.resolved != "" {
			out = append(out, l)
		}
	}
	return out
}

// spelled returns the ProfileSkills or ProfileMemory field for leaf.
func (p ExecPolicy) spelled(leaf string) string {
	if leaf == skillsLeaf {
		return p.ProfileSkills
	}
	return p.ProfileMemory
}

// profileLift resolves the lift for leaf (skills: ProfileSkills, memory:
// ProfileMemory), then refuses it when it resolves to, inside or above the
// other leaf's lift, so neither can alias the other (a memory link onto
// the skills tree, or both linked into one folder).
func (p ExecPolicy) profileLift(leaf string) (_ lift, why string) {
	l, why := liftDir(p.spelled(leaf), leaf)
	if l.resolved == "" {
		return l, why
	}
	other := memoryLeaf
	if leaf == memoryLeaf {
		other = skillsLeaf
	}
	o, _ := liftDir(p.spelled(other), other)
	if o.resolved != "" && (within(o.resolved, l.resolved) || within(l.resolved, o.resolved)) {
		return lift{}, fmt.Sprintf("profile %s %s ignored: it resolves to %s, which is or overlaps the profile %s at %s; point it at a separate folder", leaf, l.lexical, l.resolved, other, o.resolved)
	}
	return l, ""
}

// liftDir resolves one profile directory spelled as dir. The exception is
// on when the directory exists (or is missing with no link on its way, so a
// write can create it), is spelled <home>/profiles/<name>/<leaf>, and
// resolves either to <home>/profiles/<name>/<leaf> under the resolved jas
// home (so a symlinked $HOME, jas home or profile directory still works) or
// to a place that neither holds nor sits inside the jas home or ~/.jas (a
// link to a dotfiles folder). A link to the jas home, its profiles
// directory or another profile's directory would widen the lift over
// auth.json or other profiles, so it is refused and why names the reason.
func liftDir(dir, leaf string) (_ lift, why string) {
	if dir == "" {
		return lift{}, ""
	}
	clean := filepath.Clean(dir)
	profileDir := filepath.Dir(clean)
	name, profiles := filepath.Base(profileDir), filepath.Dir(profileDir)
	if !filepath.IsAbs(clean) || filepath.Base(clean) != leaf || filepath.Base(profiles) != "profiles" {
		return lift{}, ""
	}
	jasHome, err := filepath.EvalSymlinks(filepath.Dir(profiles))
	if err != nil {
		return lift{}, ""
	}
	want := filepath.Join(jasHome, "profiles", name, leaf)
	r, err := filepath.EvalSymlinks(clean)
	if os.IsNotExist(err) {
		// No <leaf>/ yet (the first personal skill or note creates it):
		// lift only when nothing on the way there is a link elsewhere.
		if r, err := evalExisting(clean); err == nil && r == want {
			return lift{lexical: clean, resolved: r}, ""
		}
		return lift{}, ""
	}
	if err != nil {
		return lift{}, ""
	}
	if fi, err := os.Stat(r); err != nil || !fi.IsDir() {
		return lift{}, ""
	}
	if r == want {
		return lift{lexical: clean, resolved: r}, ""
	}
	guarded := []string{jasHome}
	if h := homeDir(); h != "" {
		floor := filepath.Join(h, ".jas")
		if fr, err := filepath.EvalSymlinks(floor); err == nil {
			floor = fr
		}
		guarded = append(guarded, floor)
	}
	for _, g := range guarded {
		if within(g, r) || within(r, g) {
			return lift{}, fmt.Sprintf("profile %s %s ignored: it resolves to %s, which is or overlaps the jas home (auth, config, other profiles); point it at a folder outside %s", leaf, clean, r, g)
		}
	}
	return lift{lexical: clean, resolved: r}, ""
}
