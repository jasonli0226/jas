// Package skills discovers Agent Skills (a directory holding a SKILL.md with
// name and description frontmatter, per agentskills.io) and renders the
// catalog the model sees. The model loads a skill's body on demand with the
// read tool; see docs/research/agent-skills.md for the conventions followed.
package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Scope says where a skill was found. Project skills need project trust.
type Scope int

const (
	Project Scope = iota
	User
)

func (s Scope) String() string {
	if s == Project {
		return "project"
	}
	return "user"
}

const (
	skillFileName = "SKILL.md"
	// maxSkillFile caps how much of a SKILL.md is read at startup.
	maxSkillFile = 256 * 1024
	// maxDescription is the spec's description limit, in characters.
	maxDescription = 1024
	maxNameLen     = 64
	// maxDepth bounds how far below a source a skill directory may sit, and
	// maxDirs how many directories one source may visit (spec client guide).
	maxDepth = 5
	maxDirs  = 2000
)

var validName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Source is one skills directory to scan. A directory holding a SKILL.md is
// a skill (and is not searched further); any other directory is searched for
// skills below it, skipping hidden directories and node_modules.
type Source struct {
	Dir   string
	Scope Scope
	// Confine, when set, is a symlink-resolved directory every skill must
	// resolve inside: a project skill symlinked out of the project would
	// otherwise make outside directories readable once the project is trusted.
	Confine string
}

// Skill is one discovered skill.
type Skill struct {
	Name, Description string
	// Dir is the symlink-resolved skill directory; Location is Dir/SKILL.md.
	Dir, Location string
	Scope         Scope
	// DisableModelInvocation hides the skill from the model's catalog.
	DisableModelInvocation bool
	// Content is the SKILL.md bytes read (for the project trust hash).
	Content []byte
}

// Discover scans sources in order, each depth-first in name order. On a name clash the first skill found
// wins and the rest are reported, so list project sources before user ones.
// The same resolved directory reached twice (e.g. through symlinks) is loaded
// once without a warning. Validation is lenient, as the spec's client guide
// recommends: a bad name only warns; a missing description or unparseable
// frontmatter skips the skill. denied filters SKILL.md paths (the deny
// floor). The result is sorted by name so the catalog is stable.
func Discover(sources []Source, denied func(path string) bool) ([]Skill, []string) {
	var (
		out    []Skill
		warns  []string
		byName = map[string]string{} // name -> winning location
		seen   = map[string]bool{}   // resolved dirs already loaded
	)
	for _, src := range sources {
		dirs, escaped, err := candidates(src.Dir, src.Confine)
		if err != nil {
			warns = append(warns, fmt.Sprintf("skills: %v", err))
		}
		for _, e := range escaped {
			warns = append(warns, fmt.Sprintf("skill path %s skipped: it resolves outside the project", e))
		}
		for _, dir := range dirs {
			s, ok, warn := load(dir, src.Scope, denied)
			warns = append(warns, warn...)
			if !ok || seen[s.Dir] {
				continue
			}
			seen[s.Dir] = true
			if winner, dup := byName[s.Name]; dup {
				warns = append(warns, fmt.Sprintf("skill %q at %s shadowed by %s", s.Name, s.Location, winner))
				continue
			}
			byName[s.Name] = s.Location
			out = append(out, s)
		}
	}
	sortByName(out)
	return out, warns
}

// Merge combines two Discover results; on a name clash first wins and the
// loser is reported, and a directory in both loads once. Discover project
// and user skills separately and merge only once the project is trusted, so
// an untrusted project cannot hide a user skill by reusing its name.
func Merge(first, second []Skill) ([]Skill, []string) {
	out := append([]Skill{}, first...)
	var warns []string
	byName, seen := map[string]string{}, map[string]bool{}
	for _, s := range first {
		byName[s.Name], seen[s.Dir] = s.Location, true
	}
	for _, s := range second {
		if seen[s.Dir] {
			continue
		}
		if winner, dup := byName[s.Name]; dup {
			warns = append(warns, fmt.Sprintf("skill %q at %s shadowed by %s", s.Name, s.Location, winner))
			continue
		}
		byName[s.Name], seen[s.Dir] = s.Location, true
		out = append(out, s)
	}
	sortByName(out)
	return out, warns
}

func sortByName(ss []Skill) { sort.Slice(ss, func(i, j int) bool { return ss[i].Name < ss[j].Name }) }

// within reports whether path is dir or below it (both symlink-resolved).
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// candidates walks root and returns the directories holding a SKILL.md,
// depth-first in name order. Symlinked directories are followed, each
// resolved directory visited once, so a link loop ends. With confine set, an
// entry resolving outside it is neither searched nor loaded, and is returned
// in escaped.
func candidates(root, confine string) (out, escaped []string, _ error) {
	var (
		visited = map[string]bool{}
		budget  = maxDirs
	)
	var walk func(dir string, depth int) error
	walk = func(dir string, depth int) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if depth == 0 && !os.IsNotExist(err) {
				return err
			}
			return nil // missing source, or an unreadable subdirectory: skip it
		}
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				continue
			}
			path := filepath.Join(dir, name)
			real, err := filepath.EvalSymlinks(path)
			if err != nil || visited[real] {
				continue
			}
			if confine != "" && !within(confine, real) {
				escaped = append(escaped, path)
				continue
			}
			if info, err := os.Stat(real); err != nil || !info.IsDir() {
				continue
			}
			if budget--; budget < 0 {
				return fmt.Errorf("%s: stopped after %d directories", root, maxDirs)
			}
			visited[real] = true
			if _, err := os.Stat(filepath.Join(real, skillFileName)); err == nil {
				out = append(out, path)
				continue
			}
			if depth+1 < maxDepth {
				if err := walk(path, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if confine != "" {
		if real, err := filepath.EvalSymlinks(root); err == nil && !within(confine, real) {
			return nil, []string{root}, nil
		}
	}
	err := walk(root, 0)
	return out, escaped, err
}

// load reads one candidate skill directory. ok is false when it is not a
// skill or must be skipped.
func load(dir string, scope Scope, denied func(string) bool) (Skill, bool, []string) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return Skill{}, false, nil
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return Skill{}, false, nil
	}
	loc := filepath.Join(real, skillFileName)
	info, err := os.Stat(loc)
	if err != nil || !info.Mode().IsRegular() || denied(filepath.Join(dir, skillFileName)) || denied(loc) {
		return Skill{}, false, nil
	}
	content, err := readCapped(loc)
	if err != nil {
		return Skill{}, false, []string{fmt.Sprintf("skill %s: %v", loc, err)}
	}
	fm, err := parseFrontmatter(content)
	if err != nil {
		return Skill{}, false, []string{fmt.Sprintf("skill %s skipped: %v", loc, err)}
	}
	var warns []string
	dirName := filepath.Base(dir)
	name := fm["name"]
	if name == "" {
		name = dirName
	}
	if len(name) > maxNameLen || !validName.MatchString(name) {
		warns = append(warns, fmt.Sprintf("skill %s: invalid name %q (want lowercase letters, digits and single hyphens, at most %d)", loc, name, maxNameLen))
	}
	if name != dirName {
		warns = append(warns, fmt.Sprintf("skill %s: name %q does not match directory %q", loc, name, dirName))
	}
	desc := fm["description"]
	if desc == "" {
		return Skill{}, false, append(warns, fmt.Sprintf("skill %s skipped: missing description", loc))
	}
	if r := []rune(desc); len(r) > maxDescription {
		warns = append(warns, fmt.Sprintf("skill %s: description longer than %d characters; truncated", loc, maxDescription))
		desc = string(r[:maxDescription])
	}
	return Skill{
		Name: name, Description: desc, Dir: real, Location: loc, Scope: scope,
		DisableModelInvocation: strings.EqualFold(fm["disable-model-invocation"], "true"),
		Content:                content,
	}, true, warns
}

func readCapped(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxSkillFile))
}

// ProjectSources lists the project skills directories, confined to root:
// .agents/skills (the cross-agent location pi and opencode share), then
// .claude/skills (where existing Claude Code skills live).
func ProjectSources(root string) []Source {
	confine := root
	if r, err := filepath.EvalSymlinks(root); err == nil {
		confine = r
	}
	return []Source{
		{Dir: filepath.Join(root, ".agents", "skills"), Scope: Project, Confine: confine},
		{Dir: filepath.Join(root, ".claude", "skills"), Scope: Project, Confine: confine},
	}
}

// UserSources lists the user skills directories under home, in the same
// order; an empty home lists none.
func UserSources(home string) []Source {
	if home == "" {
		return nil
	}
	return []Source{
		{Dir: filepath.Join(home, ".agents", "skills"), Scope: User},
		{Dir: filepath.Join(home, ".claude", "skills"), Scope: User},
	}
}
