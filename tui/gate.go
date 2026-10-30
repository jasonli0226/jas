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
		prompt string
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
	send    func(tea.Msg)
	ask     chan struct{} // holds a token while a prompt is open

	mu     sync.Mutex // guards level and grants
	level  core.Level
	grants *core.Grants
}

var _ core.Gate = (*Gate)(nil)

// NewGate returns a gate at level that posts prompts and notices with send.
func NewGate(level core.Level, windows bool, send func(tea.Msg)) *Gate {
	return &Gate{
		windows: windows, send: send, ask: make(chan struct{}, 1),
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
func (g *Gate) Allow(ctx context.Context, t core.Tool, args json.RawMessage) (core.Decision, error) {
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
	reply := make(chan string, 1)
	g.send(approvalMsg{
		prompt: fmt.Sprintf("approve %s: %s? [y/N/always]", t.Name(), core.SummarizeCall(t.Name(), args)),
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
			g.send(noticeMsg{"always: " + core.Sanitize(grant) + " (covers any arguments)"})
		} else {
			g.send(noticeMsg{noGrantNotice})
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
