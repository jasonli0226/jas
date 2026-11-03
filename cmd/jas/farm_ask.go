package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

// farmAsk returns the farm supervisor's prompt function for a terminal. With
// choices, an answer must name exactly one of them (the whole word or a
// prefix, either way round, so "a" and "abort" both pick "abort" and "keep"
// picks "k"); anything else is asked again rather than guessed. An empty line
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
			if c, ok := matchChoice(strings.ToLower(ans), choices); ok {
				return c, true
			}
			fmt.Fprintf(out, "answer one of: %s\n", strings.Join(choices, ", "))
		}
	}
}

// matchChoice returns the one choice ans names, if exactly one does.
func matchChoice(ans string, choices []string) (string, bool) {
	var found []string
	for _, c := range choices {
		if ans == c {
			return c, true
		}
		if strings.HasPrefix(c, ans) || strings.HasPrefix(ans, c) {
			found = append(found, c)
		}
	}
	if len(found) == 1 {
		return found[0], true
	}
	return "", false
}
