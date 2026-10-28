package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		return "", fmt.Errorf("path outside project root: %s", path)
	}
	if err := p.checkDeny(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// checkDeny is the seam for the deny-list (Task 11).
func (p ExecPolicy) checkDeny(string) error { return nil }

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
