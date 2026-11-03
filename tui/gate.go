package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
)

// Messages the gate posts to the chat.
type (
	// approvalMsg opens the approval prompt; the chat sends the user's answer
	// on reply, which is buffered so the send never blocks.
	approvalMsg struct {
		part   string // the farm part asking; "" = the main session
		prompt string
		header string     // "<tool> <path>" above the diff
		diff   *core.Diff // nil: nothing to show
		reply  chan<- string
	}
	// noticeMsg is a dim line for scrollback.
	noticeMsg struct{ s string }
)

const noGrantNotice = "this command can't get an always grant; allowed once"

// Gate is the chat's core.Gate. Gated calls not covered by a session grant
// are put to the user as an inline prompt. One Gate lives as long as the
// chat, so it is shared by every session the chat opens.
type Gate struct {
	windows bool
	redact  func(string) string // applied to every prompt and notice
	send    func(tea.Msg)
	ask     chan struct{} // holds a token while a prompt is open

	mu     sync.Mutex // guards level and grants
	level  core.Level
	grants *core.Grants
	farm   *core.Grants // "always for this farm" grants, shared by every part
}

var _ core.Gate = (*Gate)(nil)

// NewGate returns a gate at level that posts prompts and notices with send,
// masked by redact (nil is identity).
func NewGate(level core.Level, windows bool, redact func(string) string, send func(tea.Msg)) *Gate {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return &Gate{
		windows: windows, redact: redact, send: send, ask: make(chan struct{}, 1),
		level: level, grants: core.NewGrants(windows),
	}
}

// Level returns the approval level.
func (g *Gate) Level() core.Level {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.level
}

// SetLevel changes the approval level; grants are kept.
func (g *Gate) SetLevel(level core.Level) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.level = level
}

// Reset clears every grant and sets the level.
func (g *Gate) Reset(level core.Level) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.level, g.grants = level, core.NewGrants(g.windows)
}

// Grants returns the session's always grants.
func (g *Gate) Grants() *core.Grants {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.grants
}

// Allow implements core.Gate. It returns on ctx done even while another
// call's prompt is open.
func (g *Gate) Allow(ctx context.Context, t core.Tool, args json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if !core.Gated(g.Level(), t) || g.Grants().Covers(t.Name(), args) {
		return core.Granted, nil
	}
	select {
	case g.ask <- struct{}{}:
	case <-ctx.Done():
		return core.Denied, ctx.Err()
	}
	defer func() { <-g.ask }()
	// An answer to the prompt we waited behind may have granted this call.
	if !core.Gated(g.Level(), t) || g.Grants().Covers(t.Name(), args) {
		return core.Granted, nil
	}
	var (
		diff   *core.Diff
		header string
	)
	if preview != nil {
		diff = preview()
	}
	if diff != nil {
		header = g.redact(core.Sanitize(t.Name()) + " " + core.Sanitize(diff.Path))
	}
	reply := make(chan string, 1)
	g.send(approvalMsg{
		prompt: g.redact(fmt.Sprintf("approve %s: %s? [y/N/always]", t.Name(), core.SummarizeCall(t.Name(), args))),
		header: header,
		diff:   diff,
		reply:  reply,
	})
	select {
	case <-ctx.Done():
		return core.Denied, ctx.Err()
	case text := <-reply:
		return g.decide(t, args, text), nil
	}
}

// decide turns the user's answer into a decision, recording an always grant.
func (g *Gate) decide(t core.Tool, args json.RawMessage, text string) core.Decision {
	switch normalizeReply(text) {
	case "y", "yes":
		return core.Granted
	case "always":
		if grant, ok := g.Grants().Add(t.Name(), args); ok {
			g.send(noticeMsg{g.redact("always: " + core.Sanitize(grant) + " (covers any arguments)")})
		} else {
			g.send(noticeMsg{g.redact(noGrantNotice)})
		}
		return core.Granted
	}
	return core.Denied
}

func normalizeReply(text string) string { return strings.ToLower(strings.TrimSpace(text)) }

// replySteers reports whether an answer is free text: a denial whose text is
// also queued for the model as steering.
func replySteers(text string) bool {
	switch normalizeReply(text) {
	case "", "y", "yes", "n", "no", "always":
		return false
	}
	return true
}

// ForPart returns the approval gate for one farm part at level. Its prompts
// go through the chat's prompt queue (one at a time, oldest first), labelled
// with the part; "always" grants cover that part, "farm" grants every part
// of this chat's farms.
func (g *Gate) ForPart(part string, level core.Level) core.Gate {
	g.mu.Lock()
	if g.farm == nil {
		g.farm = core.NewGrants(g.windows)
	}
	farm := g.farm
	g.mu.Unlock()
	return &partGate{g: g, part: part, level: level, grants: core.NewGrants(g.windows), farm: farm}
}

// partGate is a farm part's core.Gate.
type partGate struct {
	g      *Gate
	part   string
	level  core.Level
	grants *core.Grants // always for this part
	farm   *core.Grants // always for this farm
}

func (p *partGate) covered(t core.Tool, args json.RawMessage) bool {
	return !core.Gated(p.level, t) || p.grants.Covers(t.Name(), args) || p.farm.Covers(t.Name(), args)
}

// Allow implements core.Gate.
func (p *partGate) Allow(ctx context.Context, t core.Tool, args json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if p.covered(t, args) {
		return core.Granted, nil
	}
	g := p.g
	select {
	case g.ask <- struct{}{}:
	case <-ctx.Done():
		return core.Denied, ctx.Err()
	}
	defer func() { <-g.ask }()
	if p.covered(t, args) {
		return core.Granted, nil
	}
	var diff *core.Diff
	if preview != nil {
		diff = preview()
	}
	header := ""
	if diff != nil {
		header = g.redact("[" + p.part + "] " + core.Sanitize(t.Name()) + " " + core.Sanitize(diff.Path))
	}
	reply := make(chan string, 1)
	g.send(approvalMsg{
		part:   p.part,
		prompt: g.redact(fmt.Sprintf("[%s] approve %s: %s? [y/N/always/farm]", p.part, t.Name(), core.SummarizeCall(t.Name(), args))),
		header: header, diff: diff, reply: reply,
	})
	select {
	case <-ctx.Done():
		return core.Denied, ctx.Err()
	case text := <-reply:
		return p.decide(t, args, text), nil
	}
}

func (p *partGate) decide(t core.Tool, args json.RawMessage, text string) core.Decision {
	var grants *core.Grants
	switch normalizeReply(text) {
	case "y", "yes":
		return core.Granted
	case "always":
		grants = p.grants
	case "farm":
		grants = p.farm
	default:
		return core.Denied
	}
	if grant, ok := grants.Add(t.Name(), args); ok {
		p.g.send(noticeMsg{p.g.redact(fmt.Sprintf("[%s] always: %s (covers any arguments)", p.part, core.Sanitize(grant)))})
	} else {
		p.g.send(noticeMsg{p.g.redact(noGrantNotice)})
	}
	return core.Granted
}
