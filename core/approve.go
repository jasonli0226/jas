package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"
)

// Level is how much the approval ladder gates (design §3.1).
type Level int

const (
	Never       Level = iota // nothing is gated
	ShellOnly                // only the shell tool is gated
	AllMutating              // every mutating tool is gated
)

const summaryMax = 2000

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
	prompt := fmt.Sprintf("approve %s: %s? [y/N] ", t.Name(), summarize(t.Name(), args))

	type answer struct {
		line string
		err  error
		werr error // prompt write failure
	}
	ch := make(chan answer, 1)
	// The reader goroutine cannot be interrupted: after a cancel it stays
	// blocked on In until input or EOF arrives. That is acceptable because a
	// cancelled run ends the process. The buffered channel lets it exit
	// without a receiver; the mutex keeps a later prompt from racing it.
	go func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		// Written under the lock so concurrent prompts cannot interleave.
		if _, err := io.WriteString(a.Out, prompt); err != nil {
			ch <- answer{werr: fmt.Errorf("write approval prompt: %w", err)}
			return
		}
		if a.br == nil {
			a.br = bufio.NewReader(a.In)
		}
		line, err := a.br.ReadString('\n')
		ch <- answer{line: line, err: err}
	}()
	select {
	case <-ctx.Done():
		return Denied, ctx.Err()
	case ans := <-ch:
		if ans.werr != nil {
			return Denied, ans.werr
		}
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
// the command for shell, the path for write/edit, or the raw args when that
// field is absent. Control and format runes are shown as visible escapes so
// the text cannot rewrite the prompt; a cut is marked explicitly.
func summarize(name string, args json.RawMessage) string {
	var m map[string]any
	_ = json.Unmarshal(args, &m)
	key := "path"
	if name == "shell" {
		key = "command"
	}
	raw, _ := m[key].(string)
	s := sanitize(raw)
	if s == "" {
		s = sanitize(string(args))
	}
	if r := []rune(s); len(r) > summaryMax {
		s = fmt.Sprintf("%s …[+%d more chars]", string(r[:summaryMax]), len(r)-summaryMax)
	}
	return s
}

// sanitize escapes control and format runes (ESC, backspace, NUL, bidi
// overrides, ...) and collapses whitespace to single spaces.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			b.WriteByte(' ')
		case unicode.IsControl(r) || unicode.Is(unicode.Cf, r):
			if r < 0x100 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				fmt.Fprintf(&b, "\\u%04x", r)
			}
		default:
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
