package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
)

// attachBy prefixes the broker's "by" for an answer from an attached client.
const attachBy = "attach:"

// farmTTY is a farm's terminal: one line reader on stdin for the farm's
// lifetime, which every farm question (approvals, failure choices, the
// final choice) reads through, one question on screen at a time.
type farmTTY struct {
	lr   *farm.LineReader
	turn chan struct{} // held while a question is on screen, its withdrawal line included

	mu    sync.Mutex
	notes []string // lines waiting for the screen (never printed into a prompt)
}

// newFarmTTY starts the farm's one reader of in; prompts and notes go to out.
func newFarmTTY(in io.Reader, out io.Writer) *farmTTY {
	return &farmTTY{lr: farm.NewLineReader(in, out), turn: make(chan struct{}, 1)}
}

// note prints line once no question is on screen.
func (t *farmTTY) note(line string) {
	t.mu.Lock()
	t.notes = append(t.notes, line)
	t.mu.Unlock()
	t.flushIfFree()
}

// flush prints the waiting notes; the caller holds the screen.
func (t *farmTTY) flush() {
	t.mu.Lock()
	notes := t.notes
	t.notes = nil
	t.mu.Unlock()
	for _, n := range notes {
		t.lr.Note(n)
	}
}

// flushIfFree prints waiting notes when the screen is free. Both note and
// give call it after their own step, so no note is left behind.
func (t *farmTTY) flushIfFree() {
	for {
		t.mu.Lock()
		empty := len(t.notes) == 0
		t.mu.Unlock()
		if empty {
			return
		}
		select {
		case t.turn <- struct{}{}:
			t.flush()
			<-t.turn
		default:
			return // the holder flushes when it gives the screen back
		}
	}
}

// take waits for the screen, or fails (not holding it) when ctx ends
// first, even if the screen came free at the same time.
func (t *farmTTY) take(ctx context.Context) error {
	select {
	case t.turn <- struct{}{}:
		if err := ctx.Err(); err != nil {
			t.give()
			return err
		}
		t.flush()
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// give prints waiting notes and frees the screen.
func (t *farmTTY) give() {
	t.flush()
	<-t.turn
	t.flushIfFree()
}

// ask puts a farm question (see farmAsk); the screen is held across a
// re-ask so no approval prompt lands between them.
func (t *farmTTY) ask(ctx context.Context, prompt string, choices []string) (string, bool) {
	if t.take(ctx) != nil {
		return "", false
	}
	defer t.give()
	for {
		line, err := t.lr.Ask(ctx, prompt)
		if err != nil {
			return "", false
		}
		ans := strings.TrimSpace(line)
		if len(choices) == 0 || ans == "" {
			return ans, true
		}
		if c, ok := farm.MatchChoice(ans, choices); ok {
			return c, true
		}
		t.lr.Note("answer one of: " + strings.Join(choices, ", "))
	}
}

// closedFunc reports how an approval closed (farm.Broker.Closed).
type closedFunc func(id string) (answer, by string, ok bool)

// approver is the broker's host asker on this terminal: the request's diff
// (if any) and `[part] approve [a3] shell: npm install zod? [y/N/always/farm] `.
// The broker passes the tool name and diff path raw, so they are redacted
// here (nil redact is identity). An empty or unknown answer is no. When
// another answer withdraws the prompt, the attach answer is printed before
// any later question; any other withdrawal just ends the prompt's line. A
// line typed for a request that something else closed first prints
// "already answered". A request that closed while it waited for the screen
// is never shown, and after an answer the screen is kept until ctx ends, so
// the broker records any grant (and releases the calls it covers) first.
func (t *farmTTY) approver(redact func(string) string, closed closedFunc) farm.HostAsker {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return func(ctx context.Context, req farm.ApprovalRequest) (farm.Answer, error) {
		if err := t.take(ctx); err != nil {
			return farm.AnswerNo, err
		}
		if closed != nil {
			if _, _, done := closed(req.ID); done { // answered or granted while it waited
				t.give()
				return farm.AnswerNo, context.Canceled
			}
		}
		line, err := t.lr.Ask(ctx, approvalPrompt(req, redact))
		if err != nil {
			if ctx.Err() != nil {
				t.withdrawn(req, redact, closed)
			}
			t.give()
			return farm.AnswerNo, err
		}
		t.lateAnswer(req, redact, closed)
		go func() { <-ctx.Done(); t.give() }()
		ans, _ := farm.ParseAnswer(line)
		return ans, nil
	}
}

// withdrawn ends a withdrawn prompt's line (whatever withdrew it) and, when
// an attached client answered it, says so.
func (t *farmTTY) withdrawn(req farm.ApprovalRequest, redact func(string) string, closed closedFunc) {
	t.lr.Note("")
	if closed == nil {
		return
	}
	ans, by, ok := closed(req.ID)
	if !ok || !strings.HasPrefix(by, attachBy) {
		return
	}
	t.lr.Note(core.Sanitize(redact(fmt.Sprintf("[%s] [%s] answered from attach: %s", req.Part, req.ID, ans))))
}

// lateAnswer says so when a line was typed for a request something else
// (an attached client, a grant) closed first: the typed answer changed
// nothing.
func (t *farmTTY) lateAnswer(req farm.ApprovalRequest, redact func(string) string, closed closedFunc) {
	if closed == nil {
		return
	}
	if ans, by, ok := closed(req.ID); ok {
		t.lr.Note(core.Sanitize(redact(fmt.Sprintf("[%s] [%s] already answered (%s, by %s)", req.Part, req.ID, ans, by))))
	}
}

// approvalPrompt is the TTY's approval text: the diff under
// "[part] <tool> <path>", every row labelled with the part, then the
// question.
func approvalPrompt(req farm.ApprovalRequest, redact func(string) string) string {
	label := "[" + core.Sanitize(redact(req.Part)) + "] "
	tool := core.Sanitize(redact(req.Tool))
	var b strings.Builder
	if d := req.Diff; d != nil {
		b.WriteString(label + tool + " " + core.Sanitize(redact(d.Path)) + "\n")
		for _, row := range core.FormatDiffPlain(d) {
			b.WriteString(label + row + "\n")
		}
	}
	fmt.Fprintf(&b, "%sapprove [%s] %s: %s? [y/N/always/farm] ", label, core.Sanitize(req.ID), tool, redact(req.Summary))
	return b.String()
}

// brokerOptions is a farm's approval broker: level for roles that set none,
// host (nil when unattended), and the approval wait — the --approval-wait
// flag when given, else budget.approval_wait. With a host there is no hold
// window (the host is asked), so the wait matters only unattended.
func brokerOptions(level core.Level, redact func(string) string, cfg farm.Config, flagWait *time.Duration, host farm.HostAsker) farm.BrokerOptions {
	wait := cfg.Budget.ApprovalWait
	if flagWait != nil {
		wait = *flagWait
	}
	return farm.BrokerOptions{Level: level, Host: host, Wait: wait, Windows: windowsGrants, Redact: redact}
}

// granted is the broker's OnGrant on this terminal: the chat's grant
// notices, printed once no question is on screen.
func (t *farmTTY) granted(redact func(string) string) func(farm.GrantNotice) {
	if redact == nil {
		redact = func(s string) string { return s }
	}
	return func(n farm.GrantNotice) { t.note(core.Sanitize(redact(n.Text()))) }
}
