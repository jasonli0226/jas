package farm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/tools"
)

const (
	tmpRoot       = "jas-farm"         // under the system temp dir; see TmpDir
	excludeTrees  = "/.jas/worktrees/" // the default worktree dir, inside main
	branchPrefix  = "jas/farm/"
	setupMaxBytes = 32 * 1024
)

// MergeConflictError reports that merging dep into part's new worktree conflicted.
type MergeConflictError struct {
	Part, Dep string
	Files     []string
}

func (e *MergeConflictError) Error() string {
	return fmt.Sprintf("part %s: merging dependency %s conflicts in %s", e.Part, e.Dep, strings.Join(e.Files, ", "))
}

// Worktrees manages per-part git worktrees of one main checkout.
type Worktrees struct {
	Git          GitRunner
	Main, Dir    string // Dir is the repo-id directory holding <FarmID>/<part>
	FarmID       string
	Copy         []string
	Setup        string
	Deny         func(abs string) bool
	excludeOnce  sync.Once
	excludeError error
}

// TmpDir is the private TMPDIR for a worktree <dir>/<farm-id>/<part>:
// <system temp>/jas-farm/<farm-id>/<part>. It stays outside every checkout,
// so temp dirs made by the project's own tests are not inside a git repo.
func TmpDir(worktree string) string {
	return filepath.Join(os.TempDir(), tmpRoot, filepath.Base(filepath.Dir(worktree)), filepath.Base(worktree))
}

// Branch returns the branch name for a part.
func (w *Worktrees) Branch(part string) string { return branchPrefix + w.FarmID + "/" + part }

// Path returns the worktree path for a part.
func (w *Worktrees) Path(part string) string { return filepath.Join(w.Dir, w.FarmID, part) }

// Create makes the part's branch from base in a new worktree, then merges each
// dep branch (--no-ff, in order). A conflict aborts the merge and returns
// *MergeConflictError.
func (w *Worktrees) Create(ctx context.Context, part, base string, deps []string) (path, branch string, err error) {
	path, branch = w.Path(part), w.Branch(part)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	if _, err := w.Git.Run(ctx, w.Main, "worktree", "add", "-b", branch, path, base); err != nil {
		return "", "", err
	}
	if err := w.populate(ctx, path, part, deps); err != nil {
		w.discard(path, branch)
		return "", "", err
	}
	return path, branch, nil
}

// populate sets up excludes and the worker's TMPDIR, then merges the dep
// branches into the new worktree.
func (w *Worktrees) populate(ctx context.Context, path, part string, deps []string) error {
	if err := w.ensureExclude(ctx); err != nil {
		return err
	}
	if err := os.MkdirAll(TmpDir(path), 0o700); err != nil {
		return err
	}
	for _, dep := range deps {
		_, mergeErr := w.Git.Run(ctx, path, "merge", "--no-ff", "--no-edit", "--no-verify", w.Branch(dep))
		if mergeErr == nil {
			continue
		}
		out, _ := w.Git.Run(context.Background(), path, "diff", "--name-only", "--diff-filter=U")
		files := strings.Fields(out)
		_, _ = w.Git.Run(context.Background(), path, "merge", "--abort")
		if len(files) > 0 {
			return &MergeConflictError{Part: part, Dep: dep, Files: files}
		}
		return mergeErr
	}
	return nil
}

// discard removes a half-made worktree and its branch so Create can be retried.
func (w *Worktrees) discard(path, branch string) {
	ctx := context.Background()
	_, _ = w.Git.Run(ctx, w.Main, "worktree", "remove", "--force", path)
	_, _ = w.Git.Run(ctx, w.Main, "branch", "-D", branch)
}

// ensureExclude adds the farm's lines to the shared info/exclude once.
func (w *Worktrees) ensureExclude(ctx context.Context) error {
	w.excludeOnce.Do(func() {
		out, err := w.Git.Run(ctx, w.Main, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
		if err != nil {
			w.excludeError = err
			return
		}
		p := strings.TrimSpace(out)
		data, err := os.ReadFile(p)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			w.excludeError = err
			return
		}
		have := map[string]bool{}
		for _, l := range strings.Split(string(data), "\n") {
			have[strings.TrimSpace(l)] = true
		}
		var add []byte
		for _, l := range []string{excludeTrees} {
			if !have[l] {
				add = append(add, l+"\n"...)
			}
		}
		if len(add) == 0 {
			return
		}
		if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
			data = append(data, '\n')
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			w.excludeError = err
			return
		}
		w.excludeError = os.WriteFile(p, append(data, add...), 0o644)
	})
	return w.excludeError
}

// CopyFiles copies each Copy entry from Main into path. An entry must be a
// local relative path, exist, be ignored by git, and not be denied (checked on
// both the lexical and the symlink-resolved path, which must stay inside Main).
// The destination must stay inside path.
func (w *Worktrees) CopyFiles(path string) error {
	ctx := context.Background()
	mainReal, err := filepath.EvalSymlinks(w.Main)
	if err != nil {
		return err
	}
	pathReal, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	for _, rel := range w.Copy {
		if err := w.copyOne(ctx, mainReal, pathReal, path, rel); err != nil {
			return fmt.Errorf("worktree.copy %q: %w", rel, err)
		}
	}
	return nil
}

func (w *Worktrees) copyOne(ctx context.Context, mainReal, pathReal, path, rel string) error {
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return errors.New("must be a relative path inside the repository")
	}
	src := filepath.Join(w.Main, filepath.FromSlash(rel))
	real, err := filepath.EvalSymlinks(src)
	if err != nil {
		return err
	}
	if !within(real, mainReal) {
		return errors.New("resolves outside the repository")
	}
	if w.Deny != nil && (w.Deny(src) || w.Deny(real)) {
		return errors.New("denied by the deny list")
	}
	info, err := os.Stat(real)
	if err != nil {
		return err
	}
	if _, err := w.Git.Run(ctx, w.Main, "check-ignore", "-q", "--", rel); err != nil {
		return fmt.Errorf("not ignored by git: %w", err)
	}
	if info.IsDir() {
		return errors.New("directories are not supported")
	}
	dst := filepath.Join(path, filepath.FromSlash(rel))
	if err := destInside(pathReal, dst); err != nil {
		return err
	}
	if err := copyFile(real, dst, info.Mode().Perm()); err != nil {
		return err
	}
	return destInside(pathReal, dst)
}

// destInside checks that dst, resolved through its deepest existing ancestor,
// stays inside root (already symlink-resolved).
func destInside(root, dst string) error {
	cur := dst
	for {
		if _, err := os.Lstat(cur); err == nil {
			real, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return err
			}
			if !within(real, root) {
				return errors.New("destination resolves outside the worktree")
			}
			return nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return errors.New("destination has no existing parent")
		}
		cur = parent
	}
}

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// RunSetup runs w.Setup in path with a per-worktree TMPDIR. An empty Setup is
// a no-op; a non-zero exit returns the result and an error.
func (w *Worktrees) RunSetup(ctx context.Context, path string) (core.CheckResult, error) {
	if strings.TrimSpace(w.Setup) == "" {
		return core.CheckResult{}, nil
	}
	tmp := TmpDir(path) // made by Create
	env := append(tools.ShellEnv(nil), "TMPDIR="+tmp, "GIT_CONFIG_NOSYSTEM=1")
	res := core.RunCheck(ctx, core.CheckSpec{Cmd: w.Setup, Dir: path, Env: env, MaxOutput: setupMaxBytes}, tools.RunShell)
	if res.ExitCode != 0 {
		return res, fmt.Errorf("worktree.setup failed (exit %d)", res.ExitCode)
	}
	return res, nil
}

// Commit stages everything except .jas/tmp and commits with the farm trailers.
// With nothing to commit it returns the current HEAD.
func (w *Worktrees) Commit(ctx context.Context, path, part, msg string) (string, error) {
	if err := w.ensureExclude(ctx); err != nil {
		return "", err
	}
	if _, err := w.Git.Run(ctx, path, "add", "-A"); err != nil {
		return "", err
	}
	staged, err := w.Git.Run(ctx, path, "diff", "--cached", "--name-only")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(staged) != "" {
		full := strings.TrimRight(msg, "\n") + "\n\nJas-Farm: " + w.FarmID + "\nJas-Part: " + part + "\n"
		if _, err := w.Git.Run(ctx, path, "commit", "--no-verify", "-m", full); err != nil {
			return "", err
		}
	}
	head, err := w.Git.Run(ctx, path, "rev-parse", "HEAD")
	return strings.TrimSpace(head), err
}

// Remove deletes the part's worktree; its branch is kept.
func (w *Worktrees) Remove(ctx context.Context, part string) error {
	_, err := w.Git.Run(ctx, w.Main, "worktree", "remove", "--force", w.Path(part))
	_ = os.RemoveAll(TmpDir(w.Path(part)))
	return err
}

// Dirty reports whether main has uncommitted changes (untracked files count).
func Dirty(ctx context.Context, git GitRunner, main string) (bool, error) {
	out, err := git.Run(ctx, main, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// Prune drops metadata of worktrees whose directories are gone.
func Prune(ctx context.Context, git GitRunner, main string) error {
	_, err := git.Run(ctx, main, "worktree", "prune")
	return err
}
