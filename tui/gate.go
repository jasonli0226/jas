package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
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
	// approvalWithdrawnMsg removes the farm prompt whose reply channel is
	// reply, if it is still open: something else answered it.
	approvalWithdrawnMsg struct{ reply chan<- string }
	// noticeMsg is a dim line for scrollback.
	noticeMsg struct{ s string }
)

const noGrantNotice = farm.NoGrantNotice

// mustAskNotice answers "always" on a must-ask call (core.MustAsk).
const mustAskNotice = farm.MustAskNotice

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

// covered reports whether t needs no prompt: its level does not gate it, or
// a session grant covers it (never for a must-ask call).
func (g *Gate) covered(t core.Tool, args json.RawMessage) bool {
	return !core.Gated(g.Level(), t) || (!core.MustAsk(t) && g.Grants().Covers(t.Name(), args))
}

// Allow implements core.Gate. It returns on ctx done even while another
// call's prompt is open.
func (g *Gate) Allow(ctx context.Context, t core.Tool, args json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if g.covered(t, args) {
		return core.AutoGranted, nil
	}
	select {
	case g.ask <- struct{}{}:
	case <-ctx.Done():
		return core.Denied, ctx.Err()
	}
	defer func() { <-g.ask }()
	// An answer to the prompt we waited behind may have granted this call.
	if g.covered(t, args) {
		return core.AutoGranted, nil
	}
	var (
		diff   *core.Diff
		header string
	)
	if preview != nil {
		diff = preview()
	}
	if diff != nil {
		header = core.Sanitize(g.redact(t.Name())) + " " + core.Sanitize(g.redact(diff.Path))
	}
	reply := make(chan string, 1)
	g.send(approvalMsg{
		prompt: fmt.Sprintf("approve %s: %s? [y/N/always]", core.Sanitize(g.redact(t.Name())), core.SummarizeCall(t.Name(), g.redactArgs(args))),
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
	switch classifyReply(text) {
	case replyYes:
		return core.Granted
	case replyAlways:
		if core.MustAsk(t) {
			g.send(noticeMsg{mustAskNotice})
		} else if grant, ok := g.Grants().Add(t.Name(), args); ok {
			g.send(noticeMsg{g.redact("always: " + core.Sanitize(grant) + " (covers any arguments)")})
		} else {
			g.send(noticeMsg{g.redact(noGrantNotice)})
		}
		return core.Granted
	}
	return core.Denied
}

// reply is what an answer to an approval prompt means.
type reply int

const (
	replyNo     reply = iota // n, no or empty: deny
	replyYes                 // y or yes: allow once
	replyAlways              // a or always: allow and add a standing grant
	replyFarm                // farm: a farm part's grant for every part
	replyFree                // anything else: deny, and the chat steers with it
)

// classifyReply is the one place approval answers are read.
func classifyReply(text string) reply {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "", "n", "no":
		return replyNo
	case "y", "yes":
		return replyYes
	case "a", "always":
		return replyAlways
	case "farm":
		return replyFarm
	}
	return replyFree
}

// replySteers reports whether an answer to the main chat's prompt is free
// text: a denial whose text is also queued for the model as steering. The
// main prompt offers no farm grant, so "farm" there is free text too.
func replySteers(text string) bool {
	r := classifyReply(text)
	return r == replyFree || r == replyFarm
}

// redactArgs redacts a call's raw arguments before they are summarized (and
// sanitized) for a prompt: redact first, then sanitize, as the chat does. A
// redaction marker holds no quotes or backslashes, so the JSON stays valid.
func (g *Gate) redactArgs(args json.RawMessage) json.RawMessage {
	return json.RawMessage(g.redact(string(args)))
}

// AskFarm puts a farm part's gated call to the user through the chat's
// prompt queue (one at a time, oldest first), labelled with the part, and
// returns the reply text (farm.ParseAnswer reads it). It is the prompting
// half of a farm gate: the broker keeps the grants. When ctx ends first it
// returns ctx.Err(); an open prompt is then removed from the model (a key
// press for it is ignored) and a queued one is never shown.
func (g *Gate) AskFarm(ctx context.Context, part string, t core.Tool, args json.RawMessage, preview func() *core.Diff) (string, error) {
	var diff *core.Diff
	if preview != nil {
		diff = preview()
	}
	return g.askFarm(ctx, farm.ApprovalRequest{Part: part, Tool: t.Name(),
		Summary: core.SummarizeCall(t.Name(), g.redactArgs(args)), Diff: diff}, nil, nil)
}

// FarmHost is a farm broker's host asker on this chat: AskFarm's prompt for
// req, its reply read by farm.ParseAnswer (unknown text is no). closed is the
// broker's record of how an approval closed (farm.Broker.Closed): when an
// attached client's answer withdraws the open prompt, the chat says so.
func (g *Gate) FarmHost(closed func(id string) (answer, by string, ok bool)) farm.HostAsker {
	return func(ctx context.Context, req farm.ApprovalRequest) (farm.Answer, error) {
		text, err := g.askFarm(ctx, req, closed, func() {
			if ans, by, ok := closed(req.ID); ok && strings.HasPrefix(by, attachBy) {
				g.send(noticeMsg{g.redact(fmt.Sprintf("[%s] [%s] answered from attach: %s",
					core.Sanitize(req.Part), core.Sanitize(req.ID), ans))})
			}
		})
		if err != nil {
			return farm.AnswerNo, err
		}
		a, _ := farm.ParseAnswer(text)
		return a, nil
	}
}

// FarmGranted prints what a farm part's always or farm answer granted (the
// broker's OnGrant), as the chat's own grants are reported.
func (g *Gate) FarmGranted(n farm.GrantNotice) {
	g.send(noticeMsg{g.redact(n.Text())})
}

// attachBy prefixes the broker's "by" for an answer from an attached client.
const attachBy = "attach:"

// askFarm shows req's prompt once it holds the prompt slot and waits for the
// reply. The broker passes the tool name and diff path raw; they are
// redacted here (the summary already is). ctx done with the prompt open
// withdraws it, then runs withdrawn (nil: nothing) before the slot frees,
// so anything it posts lands before the next prompt.
//
// With closed (a broker host), a request that closed while queued is never
// shown, and after a reply the slot is kept until ctx ends: the broker
// records the answer's grant (and releases the calls it covers) first.
func (g *Gate) askFarm(ctx context.Context, req farm.ApprovalRequest, closed func(string) (string, string, bool), withdrawn func()) (string, error) {
	select {
	case g.ask <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	release := func() { <-g.ask }
	if err := ctx.Err(); err != nil { // ended while queued: never shown
		release()
		return "", err
	}
	if closed != nil {
		if _, _, done := closed(req.ID); done { // answered or granted while queued
			release()
			return "", context.Canceled
		}
	}
	label := "[" + core.Sanitize(g.redact(req.Part)) + "]"
	tool := core.Sanitize(g.redact(req.Tool))
	header := ""
	if req.Diff != nil {
		header = label + " " + tool + " " + core.Sanitize(g.redact(req.Diff.Path))
	}
	id := ""
	if req.ID != "" {
		id = "[" + core.Sanitize(req.ID) + "] "
	}
	reply := make(chan string, 1)
	g.send(approvalMsg{
		part:   req.Part,
		prompt: fmt.Sprintf("%s approve %s%s: %s? [y/N/always/farm]", label, id, tool, g.redact(req.Summary)),
		header: header, diff: req.Diff, reply: reply,
	})
	select {
	case <-ctx.Done():
		g.send(approvalWithdrawnMsg{reply: reply})
		if withdrawn != nil {
			withdrawn()
		}
		release()
		return "", ctx.Err()
	case text := <-reply:
		if closed != nil {
			go func() { <-ctx.Done(); release() }()
		} else {
			release()
		}
		return text, nil
	}
}
