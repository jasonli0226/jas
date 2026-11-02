package farm

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const repoIDHexLen = 12

func exists(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, name))
	return err == nil
}

// Detect inspects marker files at root and returns variable values
// (check, test_globs). It is deterministic and never calls a model.
func Detect(root string) map[string][]string {
	out := map[string][]string{}
	switch {
	case exists(root, "go.mod"):
		out["check"] = []string{"go build ./... && go test ./..."}
		out["test_globs"] = []string{"**/*_test.go"}
	case exists(root, "package.json"):
		check := "npm test"
		if exists(root, "tsconfig.json") {
			check += " && npx tsc --noEmit"
		}
		out["check"] = []string{check}
		out["test_globs"] = []string{"**/*.test.*", "**/*.spec.*", "**/__tests__/**"}
	case exists(root, "pyproject.toml"), exists(root, "requirements.txt"):
		out["check"] = []string{"pytest"}
		out["test_globs"] = []string{"**/test_*.py", "**/*_test.py", "tests/**"}
	case exists(root, "Cargo.toml"):
		out["check"] = []string{"cargo build && cargo test"}
		out["test_globs"] = []string{"tests/**"}
	}
	return out
}

// ResolveWorktreeDir returns the base directory for part worktrees:
// cfg.Worktree.Dir or <cache>/jas/worktrees/<repo-id>. The result has
// symlinks resolved and must lie outside the main checkout (and its linked
// worktrees) and outside jasHome.
func ResolveWorktreeDir(cfg Config, mainRoot, jasHome string) (string, error) {
	root, err := realAbs(mainRoot)
	if err != nil {
		return "", fmt.Errorf("repo root: %w", err)
	}
	dir := cfg.Worktree.Dir
	if dir == "" {
		base := os.Getenv("XDG_CACHE_HOME")
		if base == "" {
			if base, err = os.UserCacheDir(); err != nil {
				return "", fmt.Errorf("cache dir: %w", err)
			}
		}
		dir = filepath.Join(base, "jas", "worktrees", repoID(root))
	}
	resolved, err := realAbs(dir)
	if err != nil {
		return "", fmt.Errorf("worktree dir %s: %w", dir, err)
	}
	forbidden := append([]string{root}, linkedWorktrees(root)...)
	for _, f := range forbidden {
		if within(resolved, f) {
			return "", fmt.Errorf("worktree dir %s is inside the repo (%s)", resolved, f)
		}
	}
	home, err := realAbs(jasHome)
	if err != nil {
		return "", fmt.Errorf("jas home: %w", err)
	}
	if within(resolved, home) {
		return "", fmt.Errorf("worktree dir %s is inside the jas home (%s)", resolved, home)
	}
	return resolved, nil
}

func repoID(absRoot string) string {
	sum := sha256.Sum256([]byte(absRoot))
	return hex.EncodeToString(sum[:])[:repoIDHexLen] + "-" + filepath.Base(absRoot)
}

// within reports whether p equals or is under parent.
func within(p, parent string) bool {
	rel, err := filepath.Rel(parent, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// realAbs makes p absolute and resolves symlinks of its nearest existing
// ancestor, re-appending the not-yet-existing tail.
func realAbs(p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	var tail []string
	cur := abs
	for {
		r, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				r = filepath.Join(r, tail[i])
			}
			return r, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// linkedWorktrees lists other worktree paths of the repo; best effort.
func linkedWorktrees(root string) []string {
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			if r, err := realAbs(p); err == nil {
				paths = append(paths, r)
			}
		}
	}
	return paths
}
