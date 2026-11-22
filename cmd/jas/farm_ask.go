package main

import (
	"context"
	"runtime"
)

// windowsGrants is core.NewGrants's windows flag for this platform.
var windowsGrants = runtime.GOOS == "windows"

// farmAsk returns the farm supervisor's prompt function for the farm's
// terminal. With choices, an answer must name exactly one of them (see
// farm.MatchChoice: "a" and "abort" both pick "abort", "don't" picks
// nothing); anything else is asked again rather than guessed. An empty line
// passes through for the caller's default. End of input or a cancelled ctx is
// no answer (ok false). It reads through tty's one line reader, so it never
// competes with an approval prompt for stdin.
func farmAsk(ctx context.Context, tty *farmTTY) func(string, []string) (string, bool) {
	return func(prompt string, choices []string) (string, bool) {
		return tty.ask(ctx, prompt, choices)
	}
}
