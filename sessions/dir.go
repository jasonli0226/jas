package sessions

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// excludeLine hides the records from git status in the main checkout.
const excludeLine = "/.jas/sessions/"

const gitTimeout = 5 * time.Second

// Dir returns the records directory for the project at root: the main
// checkout's .jas/sessions (so linked worktrees share it), or root's own
// when root is not in a git repository. Inside git it makes sure the
// directory is listed in info/exclude.
func Dir(root string) (string, error) {
	common, ok := gitCommonDir(root)
	if !ok {
		return filepath.Join(root, ".jas", "sessions"), nil
	}
	main := root
	if filepath.Base(common) == ".git" {
		main = filepath.Dir(common)
	}
	if err := ensureExcluded(filepath.Join(common, "info", "exclude")); err != nil {
		return "", err
	}
	return filepath.Join(main, ".jas", "sessions"), nil
}

// gitCommonDir is the repository's shared git dir, or false outside git.
func gitCommonDir(root string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), gitTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil { // git before 2.31 has no --path-format; its answer may be relative
		out, err = exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--git-common-dir").Output()
	}
	if err != nil {
		return "", false
	}
	return CommonDirFromOutput(root, string(out)), true
}

// CommonDirFromOutput turns `git rev-parse --git-common-dir` output, which
// older git prints relative to root, into an absolute path.
func CommonDirFromOutput(root, out string) string {
	p := strings.TrimSpace(out)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	return filepath.Clean(p)
}

// ensureExcluded appends excludeLine to path unless a line already equals it.
func ensureExcluded(path string) error {
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(l) == excludeLine {
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	prefix := ""
	if len(b) > 0 && !strings.HasSuffix(string(b), "\n") {
		prefix = "\n"
	}
	_, werr := f.WriteString(prefix + excludeLine + "\n")
	return errors.Join(werr, f.Close())
}
