// Command jas is a small coding-agent harness.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/jasonli0226/jas/core"
)

// version is set with -ldflags "-X main.version=..." (release builds and
// make). Without it, init falls back to the version Go stamps into the
// build (see buildVersion).
var version = "dev"

func init() {
	info, _ := debug.ReadBuildInfo()
	version = buildVersion(version, info)
}

// buildVersion returns v when -ldflags set it. Otherwise it returns the
// main module version Go stamps into the build: the tag for
// `go install ...@v1.2.0`, or a pseudo-version with the commit (and
// "+dirty" for uncommitted changes) for a build in a git checkout. It
// stays "dev" when Go stamped nothing.
func buildVersion(v string, info *debug.BuildInfo) string {
	if v != "dev" || info == nil {
		return v
	}
	if mv := info.Main.Version; mv != "" && mv != "(devel)" {
		return mv
	}
	return v
}

const usage = `usage:
  jas [--continue] [--name T] [--model M] [--profile NAME] [--provider P] [--base-url U] [--approve L] [--max-turns N] [--max-output-tokens N] [--trust-project]
  jas run [--json] [--name T] [--model M] [--profile NAME] [--provider P] [--base-url U] [--approve L] [--max-turns N] [--max-output-tokens N]
          [--max-tokens N] [--timeout D] [--compact] [--trust-project] "task"
  jas farm plan "task" [-o plan.yaml] [--allow-dirty]
  jas farm run plan.yaml [--json] [--allow-dirty]
  jas farm "task"
  jas farm status|clean|vars
  jas attach [<farm-id> [<part>]]
  jas config set|get|unset|list|init
  jas profile list [--json] | use <name> | new <name> | show [name]
  jas auth add|list|rm|rotate
  jas sessions [--prune]
  jas memory [--path]
  jas dream [--since D] [--sessions N] [--max-tokens N] [--timeout D] [--json] [--profile NAME]
  jas --version

full reference: docs/manual.md in the jas repo

example: jas config set model z-ai/glm-5.3`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// After the first signal, restore default handling so a second Ctrl-C
	// kills the process even if shutdown is stuck.
	go func() { <-ctx.Done(); stop() }()
	code := dispatch(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func dispatch(ctx context.Context, args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-version", "version":
			fmt.Println("jas " + version)
			return core.ExitOK
		}
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return report(chatCmd(ctx, args))
	}
	switch args[0] {
	case "run":
		return report(runCmd(ctx, args[1:]))
	case "config":
		return report(configCmd(args[1:]))
	case "profile":
		return report(profileCmd(args[1:]))
	case "auth":
		return report(authCmd(args[1:]))
	case "farm":
		return report(farmCmd(ctx, args[1:]))
	case "memory":
		return report(memoryCmd(args[1:]))
	case "sessions":
		return report(sessionsCmd(args[1:]))
	case "dream":
		return report(dreamCmd(ctx, args[1:]))
	case "attach":
		return report(attachCmd(ctx, args[1:]))
	default:
		fmt.Fprintf(os.Stderr, "jas: unknown command %q\n%s\n", args[0], usage)
		return core.ExitUsage
	}
}

// report maps an error to an exit code, printing it to stderr.
func report(err error) int {
	if err == nil {
		return core.ExitOK
	}
	redactor := reportRedactor
	if redactor == nil {
		redactor = newRedactor(os.Getenv("JAS_API_KEY"))
	}
	redact := redactor.Redact
	var ee *core.ExitError
	if errors.As(err, &ee) {
		if ee.Code != core.ExitInterrupted {
			fmt.Fprintln(os.Stderr, redact("jas: "+err.Error()))
		}
		return ee.Code
	}
	fmt.Fprintln(os.Stderr, redact("jas: "+err.Error()))
	return core.ExitFailure
}

func usageErr(format string, a ...any) error {
	return &core.ExitError{Code: core.ExitUsage, Reason: fmt.Sprintf(format, a...)}
}
