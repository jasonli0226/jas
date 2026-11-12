package dream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/memory"
)

const (
	projectsFolder = "projects"
)

// Guard confines the dream model's writes to the proposed store copy and
// applies the live memory write rules inside it. Its methods fit
// core.Executor.WritePrecheck and WriteGuard.
type Guard struct {
	Proposed string
	Redact   func(string) string
}

// Precheck refuses a write outside Proposed, outside the two allowed folder
// levels, or to a non-.md file. resolved is absolute with symlinks resolved.
func (g Guard) Precheck(tool, resolved string) error {
	root := evalDir(g.Proposed)
	if !within(root, resolved) || resolved == root {
		return errors.New("dreams write only inside proposed/")
	}
	if dir := filepath.Dir(resolved); !allowedFolder(root, dir) {
		return fmt.Errorf("notes belong directly in proposed/ or proposed/projects/<key>/, not %s", dir)
	}
	if name := filepath.Base(resolved); !strings.HasSuffix(name, ".md") && !isIndex(name) {
		return fmt.Errorf("memory holds .md notes only, not %s", name)
	}
	return nil
}

// Check applies every write rule (Precheck's first) to a write or edit whose
// result is newContent. MEMORY.md may be written whole.
func (g Guard) Check(tool, resolved string, existed bool, newContent string) error {
	if err := g.Precheck(tool, resolved); err != nil {
		return err
	}
	if name := filepath.Base(resolved); !isIndex(name) {
		if err := memory.ValidateNote(name, newContent); err != nil {
			return err
		}
	}
	if g.Redact != nil && g.Redact(newContent) != newContent {
		return errors.New("memory notes cannot hold secrets; describe where the secret lives instead")
	}
	return nil
}

// allowedFolder reports whether dir is root or root/projects/<key>.
func allowedFolder(root, dir string) bool {
	if dir == root {
		return true
	}
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	return len(parts) == 2 && parts[0] == projectsFolder && parts[1] != "" && parts[1] != ".."
}

func isIndex(name string) bool { return strings.EqualFold(name, indexName) }

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
