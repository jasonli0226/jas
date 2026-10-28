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
  jas run [--json] [--model M] [--base-url U] "task"
  jas --version`

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
		fmt.Fprintln(os.Stderr, usage)
		return core.ExitUsage
	}
	switch args[0] {
	case "--version", "-version", "version":
		fmt.Println("jas " + version)
		return core.ExitOK
	case "run":
		return report(runCmd(ctx, args[1:]))
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
	redact := newRedactor(os.Getenv, nil).Redact
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
