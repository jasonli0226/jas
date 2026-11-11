package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Mode is the resolved memory setting for a run.
type Mode int

const (
	ModeOn Mode = iota
	ModeRead
	ModeOff
)

// ParseMode parses a memory setting value: on, read or off.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "on":
		return ModeOn, nil
	case "read":
		return ModeRead, nil
	case "off":
		return ModeOff, nil
	}
	return ModeOn, fmt.Errorf("memory: %q is not one of on, read, off", s)
}

// ResolveMode merges the base (global+profile) and project (project+local)
// settings; project "" means unset. A project value can only narrow a base
// that is on: it never turns a base off or read back on.
func ResolveMode(base, project string) (mode Mode, hideProject bool) {
	switch base {
	case "off":
		return ModeOff, false
	case "read":
		return ModeRead, false
	}
	switch project {
	case "read":
		return ModeRead, false
	case "off":
		return ModeRead, true
	}
	return ModeOn, false
}

// Guard holds the write rules for the memory folders.
type Guard struct {
	Dirs     Dirs
	Mode     Mode
	Headless bool
	Redact   func(string) string
}

// Holds reports whether resolved (absolute, symlinks resolved) lies in the
// profile memory dir.
func (g Guard) Holds(resolved string) bool {
	return within(evalDir(g.Dirs.Profile), resolved)
}

// Check applies the write rules to a write or edit whose result is
// newContent. It returns nil for paths outside the memory dir, and for
// ModeOff (callers skip memory then).
func (g Guard) Check(tool, resolved string, existed bool, newContent string) error {
	if g.Mode == ModeOff || !g.Holds(resolved) {
		return nil
	}
	if g.Headless {
		return errors.New("memory is read-only in this run: no one can approve the write")
	}
	if g.Mode == ModeRead {
		return errors.New("memory is read-only in this session (memory: read)")
	}
	profile, project := evalDir(g.Dirs.Profile), evalDir(g.Dirs.Project)
	if dir := filepath.Dir(resolved); dir != profile && dir != project {
		return fmt.Errorf("memory notes belong directly in %s or %s, not %s", g.Dirs.Profile, g.Dirs.Project, dir)
	}
	name := filepath.Base(resolved)
	if !strings.HasSuffix(name, ".md") {
		return fmt.Errorf("memory holds .md notes only, not %s", name)
	}
	if name == indexFile {
		if tool == "write" && existed {
			return errors.New("change MEMORY.md with edit, not a whole-file write")
		}
	} else if err := ValidateNote(name, newContent); err != nil {
		return err
	}
	if g.Redact != nil && g.Redact(newContent) != newContent {
		return errors.New("memory notes cannot hold secrets; describe where the secret lives instead")
	}
	return nil
}

// evalDir resolves symlinks in the nearest existing ancestor of dir and
// re-appends the missing tail. On an unexpected error it returns dir as is.
func evalDir(dir string) string {
	dir = filepath.Clean(dir)
	var tail []string
	for cur := dir; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r
		} else if !os.IsNotExist(err) {
			return dir
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return dir
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
