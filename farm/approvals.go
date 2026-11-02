package farm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/jasonli0226/jas/core"
)

// Approvals serializes approval prompts from parallel workers on one
// terminal. Each part gets its own core.Approver whose output lines start
// with "[<part>] "; a prompt holds the shared lock until it is answered, so
// prompts never interleave while other workers keep running.
type Approvals struct {
	Default     core.Level // used when a role sets no approve level
	Interactive bool

	in  *bufio.Reader // shared so no approver's buffer swallows another's answer
	out io.Writer
	sem chan struct{} // the shared lock; a channel so waiting can be cancelled
}

// NewApprovals returns the shared approval state. Without a TTY
// (interactive false) a gated call is refused as approval_needed.
func NewApprovals(in io.Reader, out io.Writer, interactive bool, def core.Level) *Approvals {
	return &Approvals{Default: def, Interactive: interactive,
		in: bufio.NewReader(in), out: out, sem: make(chan struct{}, 1)}
}

// lock takes the shared prompt lock, or fails when ctx ends first.
func (a *Approvals) lock(ctx context.Context) error {
	select {
	case a.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Approvals) unlock() { <-a.sem }

// Gate returns the gate for one part at the role's approve level ("" means
// the default level).
func (a *Approvals) Gate(part, approve string) (core.Gate, error) {
	level := a.Default
	if approve != "" {
		l, err := core.ParseLevel(approve)
		if err != nil {
			return nil, err
		}
		level = l
	}
	ap := &core.Approver{Level: level, In: a.in, Out: &prefixWriter{w: a.out, prefix: "[" + part + "] "}, Interactive: a.Interactive}
	return &partGate{a: a, ap: ap}, nil
}

// Ask runs a TTY question under the shared prompt lock, so it never
// interleaves with an approval prompt.
func (a *Approvals) Ask(ctx context.Context, ask func() (string, bool)) (string, bool) {
	if err := a.lock(ctx); err != nil {
		return "", false
	}
	defer a.unlock()
	return ask()
}

type partGate struct {
	a  *Approvals
	ap *core.Approver
}

// Allow implements core.Gate. Ungated calls and calls without a TTY never
// take the shared lock.
func (g *partGate) Allow(ctx context.Context, t core.Tool, args json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if !core.Gated(g.ap.Level, t) || !g.ap.Interactive {
		return g.ap.Allow(ctx, t, args, preview)
	}
	if err := g.a.lock(ctx); err != nil {
		return core.Denied, err
	}
	defer g.a.unlock()
	return g.ap.Allow(ctx, t, args, preview)
}

// prefixWriter starts every output line with prefix.
type prefixWriter struct {
	w      io.Writer
	prefix string
	mid    bool // inside a line
}

func (p *prefixWriter) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		if !p.mid {
			if _, err := io.WriteString(p.w, p.prefix); err != nil {
				return n, err
			}
			p.mid = true
		}
		end := len(b)
		for i, c := range b {
			if c == '\n' {
				end = i + 1
				p.mid = false
				break
			}
		}
		m, err := p.w.Write(b[:end])
		n += m
		if err != nil {
			return n, fmt.Errorf("approval prompt: %w", err)
		}
		b = b[end:]
	}
	return n, nil
}
