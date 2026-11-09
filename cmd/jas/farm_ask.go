package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jasonli0226/jas/farm"
)

// farmAsk returns the farm supervisor's prompt function for a terminal. With
// choices, an answer must name exactly one of them (see farm.MatchChoice:
// "a" and "abort" both pick "abort", "don't" picks nothing); anything else is
// asked again rather than guessed. An empty line
// passes through for the caller's default. End of input or a cancelled ctx is
// no answer (ok false).
func farmAsk(ctx context.Context, in *bufio.Reader, out io.Writer) func(string, []string) (string, bool) {
	return func(prompt string, choices []string) (string, bool) {
		for {
			line := askLine(ctx, in, out, prompt)
			if !strings.HasSuffix(line, "\n") {
				return "", false
			}
			ans := strings.TrimSpace(line)
			if len(choices) == 0 || ans == "" {
				return ans, true
			}
			if c, ok := farm.MatchChoice(ans, choices); ok {
				return c, true
			}
			fmt.Fprintf(out, "answer one of: %s\n", strings.Join(choices, ", "))
		}
	}
}
