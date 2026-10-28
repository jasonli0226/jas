package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Level is how much the approval ladder gates (design §3.1).
type Level int

const (
	Never       Level = iota // nothing is gated
	ShellOnly                // only the shell tool is gated
	AllMutating              // every mutating tool is gated
)

const summaryMax = 200

// String returns the flag spelling of l.
func (l Level) String() string {
	switch l {
	case ShellOnly:
		return "shell"
	case AllMutating:
		return "mutating"
	default:
		return "never"
	}
}

// ParseLevel parses never|shell|mutating.
func ParseLevel(s string) (Level, error) {
	switch s {
	case "never":
		return Never, nil
	case "shell":
		return ShellOnly, nil
	case "mutating":
		return AllMutating, nil
	}
	return Never, fmt.Errorf("invalid approve level %q: want never|shell|mutating", s)
}

// Approver is the Gate for the approval ladder. Gated calls are put to the
// user on Out and answered on In; without a terminal they are Unavailable.
type Approver struct {
	Level       Level
	In          io.Reader
	Out         io.Writer
	Interactive bool

	mu sync.Mutex // serialises prompts and guards br
	br *bufio.Reader
}

var _ Gate = (*Approver)(nil)

func (a *Approver) gated(t Tool) bool {
	switch a.Level {
	case AllMutating:
		return t.Kind() == Mutating
	case ShellOnly:
		return t.Name() == "shell"
	}
	return false
}

// Allow implements Gate.
func (a *Approver) Allow(ctx context.Context, t Tool, args json.RawMessage) (Decision, error) {
	if !a.gated(t) {
		return Granted, nil
	}
	if !a.Interactive {
		return Unavailable, nil
	}
	fmt.Fprintf(a.Out, "approve %s: %s? [y/N] ", t.Name(), summarize(t.Name(), args))

	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	// The reader goroutine cannot be interrupted: after a cancel it stays
	// blocked on In until input or EOF arrives. That is acceptable because a
	// cancelled run ends the process. The buffered channel lets it exit
	// without a receiver; the mutex keeps a later prompt from racing it.
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.br == nil {
			a.br = bufio.NewReader(a.In)
		}
		line, err := a.br.ReadString('\n')
		ch <- answer{line, err}
	}()
	select {
	case <-ctx.Done():
		return Denied, ctx.Err()
	case ans := <-ch:
		// EOF with no answer, or a read error, is a refusal (the default is N).
		if ans.err != nil && ans.line == "" {
			return Denied, nil
		}
		switch strings.ToLower(strings.TrimSpace(ans.line)) {
		case "y", "yes":
			return Granted, nil
		}
		return Denied, nil
	}
}

// summarize is the one-line, length-capped description shown in the prompt:
// the command for shell, the path for write/edit.
func summarize(name string, args json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	key := "path"
	if name == "shell" {
		key = "command"
	}
	s, _ := m[key].(string)
	s = strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(s)), " ")
	if r := []rune(s); len(r) > summaryMax {
		s = string(r[:summaryMax])
	}
	return s
}
