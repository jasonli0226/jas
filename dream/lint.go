package dream

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/memory"
)

// Lint issue kinds. KindDanglingLink is advisory.
const (
	KindFrontmatter   = "frontmatter"
	KindMissingTarget = "missing-target"
	KindOrphan        = "orphan"
	KindDanglingLink  = "dangling-link"
	KindOverCap       = "over-cap"
	KindShadowed      = "shadowed"
	KindNotAllowed    = "not-allowed"
	KindProjectGone   = "project-gone"
	KindSecret        = "secret"
)

const (
	indexName   = "MEMORY.md"
	projectsDir = "projects"
	noteExt     = ".md"
)

var (
	indexLine = regexp.MustCompile(`^- \[[^\]]*\]\(([^)]+)\)`)
	noteLink  = regexp.MustCompile(`\[\[([a-z0-9-]+)\]\]`)
)

// Issue is one finding. Path is slash-separated, relative to the store.
type Issue struct{ Kind, Path, Detail string }

// folder is one memory folder: the profile root or projects/<key>.
type folder struct {
	rel    string            // "" for the profile root
	notes  map[string]string // note file name -> content
	index  string            // content of MEMORY.md
	hasIdx bool
}

func (f *folder) join(name string) string { return path.Join(f.rel, name) }

// Lint checks the store at root without a model. redact returns its input
// unchanged when it holds no secret; exists reports whether a project path
// is still on disk. The result is sorted by kind, then path.
func Lint(store string, redact func(string) string, exists func(path string) bool) []Issue {
	var issues []Issue
	folders := map[string]*folder{}
	add := func(kind, p, detail string) { issues = append(issues, Issue{kind, p, detail}) }

	_ = filepath.WalkDir(store, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(store, p)
		rel = filepath.ToSlash(rel)
		dir, name := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		if !allowedPath(dir, name) {
			add(KindNotAllowed, rel, "")
			return nil
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		f := folders[dir]
		if f == nil {
			f = &folder{rel: dir, notes: map[string]string{}}
			folders[dir] = f
		}
		content := string(raw)
		if redact != nil && redact(content) != content {
			add(KindSecret, rel, "")
		}
		if name == indexName {
			f.index, f.hasIdx = content, true
		} else {
			f.notes[name] = content
		}
		return nil
	})

	profile := folders[""]
	if profile == nil {
		profile = &folder{notes: map[string]string{}}
	}
	for _, key := range sortedKeys(folders) {
		f := folders[key]
		lintFolder(f, profile, add)
		if f != profile {
			lintProject(f, profile, add)
		}
	}
	lintOverCap(folders, profile, add)
	lintGone(store, exists, add)

	slices.SortStableFunc(issues, func(a, b Issue) int {
		if c := strings.Compare(a.Kind, b.Kind); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return issues
}

// allowedPath: notes and MEMORY.md directly in the root or in projects/<key>.
func allowedPath(dir, name string) bool {
	if !strings.HasSuffix(name, noteExt) {
		return false
	}
	parts := strings.Split(dir, "/")
	switch {
	case dir == "":
		return true
	case len(parts) == 2 && parts[0] == projectsDir:
		return true
	}
	return false
}

func sortedKeys(m map[string]*folder) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func lintFolder(f, profile *folder, add func(kind, p, detail string)) {
	linked := map[string]bool{}
	if f.hasIdx {
		for _, line := range memory.IndexLines(f.index) {
			m := indexLine.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			target := path.Clean(m[1])
			linked[target] = true
			if _, ok := f.notes[target]; !ok {
				add(KindMissingTarget, f.join(indexName), m[1])
			}
		}
	}
	for _, name := range sortedNotes(f) {
		content := f.notes[name]
		if !linked[name] {
			add(KindOrphan, f.join(name), "")
		}
		if err := memory.ValidateNote(name, content); err != nil {
			add(KindFrontmatter, f.join(name), err.Error())
		}
		seen := map[string]bool{}
		for _, m := range noteLink.FindAllStringSubmatch(content, -1) {
			target := m[1]
			if seen[target] {
				continue
			}
			seen[target] = true
			_, here := f.notes[target+noteExt]
			_, up := profile.notes[target+noteExt]
			if !here && !up {
				add(KindDanglingLink, f.join(name), target)
			}
		}
	}
}

func sortedNotes(f *folder) []string {
	names := make([]string, 0, len(f.notes))
	for n := range f.notes {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// lintProject reports project notes that share a name with a profile note.
func lintProject(f, profile *folder, add func(kind, p, detail string)) {
	for _, name := range sortedNotes(f) {
		if _, ok := profile.notes[name]; ok {
			add(KindShadowed, f.join(name), strings.TrimSuffix(name, noteExt))
		}
	}
}

// lintOverCap applies the session-start cap: the profile index alone, then
// the profile and project indexes together.
func lintOverCap(folders map[string]*folder, profile *folder, add func(kind, p, detail string)) {
	pLines := memory.IndexLines(profile.index)
	if over(pLines) {
		add(KindOverCap, indexName, "")
	}
	for _, key := range sortedKeys(folders) {
		f := folders[key]
		if f == profile || !f.hasIdx {
			continue
		}
		all := slices.Concat(pLines, memory.IndexLines(f.index))
		if over(all) && !over(pLines) {
			add(KindOverCap, f.join(indexName), "")
		}
	}
}

func over(lines []string) bool {
	return len(lines) > memory.MaxIndexLines || memory.IndexBytes(lines) > memory.MaxIndexBytes
}

// lintGone flags project folders whose decoded path no longer exists. A
// hashed key cannot be decoded and is never reported.
func lintGone(store string, exists func(string) bool, add func(kind, p, detail string)) {
	entries, err := os.ReadDir(filepath.Join(store, projectsDir))
	if err != nil || exists == nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		root, ok := memory.DecodeKey(e.Name())
		if ok && !exists(root) {
			add(KindProjectGone, path.Join(projectsDir, e.Name()), root)
		}
	}
}
