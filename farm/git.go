package farm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// Farm commit identity.
const (
	farmGitName  = "jas farm"
	farmGitEmail = "jas-farm@localhost"
)

// GitRunner runs git in dir and returns its stdout. The error carries git's
// stderr. Run has the func shape DiffViolations takes, so a method value fits.
type GitRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

// gitMu serializes every mutating git command in the process: they all touch
// the shared .git (index.lock, refs, worktree metadata).
var gitMu sync.Mutex

var mutatingGit = map[string]bool{
	"worktree": true, "commit": true, "merge": true, "branch": true, "add": true,
	"checkout": true, "reset": true, "update-ref": true,
}

// signedGit are the subcommands that create commits.
var signedGit = map[string]bool{"commit": true, "merge": true}

type execGit struct{}

// NewGit returns the real git runner.
func NewGit() GitRunner { return execGit{} }

// gitSubcommand returns the subcommand, skipping leading -c/-C options.
func gitSubcommand(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" || args[i] == "-C" {
			i++
			continue
		}
		return args[i]
	}
	return ""
}

func (execGit) Run(ctx context.Context, dir string, args ...string) (string, error) {
	sub := gitSubcommand(args)
	if mutatingGit[sub] {
		gitMu.Lock()
		defer gitMu.Unlock()
	}
	full := args
	if signedGit[sub] {
		full = append([]string{"-c", "commit.gpgsign=false"}, args...)
	}
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_AUTHOR_NAME="+farmGitName, "GIT_AUTHOR_EMAIL="+farmGitEmail,
		"GIT_COMMITTER_NAME="+farmGitName, "GIT_COMMITTER_EMAIL="+farmGitEmail,
	)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return stdout.String(), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return stdout.String(), nil
}
