// Command jas is a small coding-agent harness.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/jasonli0226/jas/core"
)

// version is set with -ldflags "-X main.version=...".
var version = "dev"

const usage = `usage:
  jas run [--json] [--model M] [--provider P] [--base-url U] [--approve L] [--max-turns N] [--trust-project] "task"
  jas config set|get|unset|list|init
  jas auth add|list|rm|rotate
  jas --version

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
	if len(args) == 0 {
		return report(onboardOrUsage())
	}
	switch args[0] {
	case "--version", "-version", "version":
		fmt.Println("jas " + version)
		return core.ExitOK
	case "run":
		return report(runCmd(ctx, args[1:]))
	case "config":
		return report(configCmd(args[1:]))
	case "auth":
		return report(authCmd(args[1:]))
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
		redactor = newRedactor(os.Getenv("JAS_API_KEY"), nil)
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
