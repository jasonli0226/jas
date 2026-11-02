package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

const (
	compactWindow  = 3000 // tokens; 12000 bytes, room for the rig's ~450-token system prompt and schemas
	summaryReply   = "SUMMARY-REPLY"
	compactSummary = "[jas] The earlier conversation was compacted. Summary:\n\n"
)

// tokens returns a text of about n estimated tokens (4 bytes each).
func tokens(n int) string { return strings.Repeat("x", n*4) }

// newCompactSession starts a session with a small window and cc as its
// compaction config (nil = off).
func newCompactSession(t *testing.T, r *rig, cc *core.CompactConfig) *core.Session {
	t.Helper()
	sc := r.sessionConfig()
	sc.Budget = &core.Budget{Window: compactWindow, ElideAt: 1, Keep: core.DefaultKeepToolResults}
	sc.Compact = cc
	s, err := core.NewSession(sc)
	assert.NoErr(t, err)
	return s
}

func eventsOf(evs []map[string]any, typ string) []map[string]any {
	var out []map[string]any
	for _, e := range evs {
		if e["type"] == typ {
			out = append(out, e)
		}
	}
	return out
}

func isSummaryMsg(m providers.Message) bool {
	return m.Role == providers.RoleUser && len(m.Content) == 1 && strings.HasPrefix(m.Content[0].Text, compactSummary)
}

func TestSession_CompactReplacesHistory(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Text: "A1" + tokens(100)}, {Text: "A2" + tokens(100)}, {Text: "A3" + tokens(100)},
		{Text: summaryReply, Usage: providers.Usage{PromptTokens: 11, CompletionTokens: 3}},
	})
	// Threshold is the whole window, so Send never auto-compacts here.
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 0, Keep: 150, Model: "small"})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		assert.Eq(t, send(t, s, ctx, u+tokens(100)).Status, "ok")
	}
	idsBefore := s.MessageIDs()

	res, err := s.Compact(ctx, "the tests")
	assert.NoErr(t, err)
	assert.Eq(t, res.Messages, 4)
	assert.Eq(t, res.Usage.PromptTokens, 11)

	summaryReq := r.prov.Requests[3]
	assert.Eq(t, summaryReq.Model, "small")
	assert.Eq(t, len(summaryReq.Tools), 0)
	assert.Contains(t, summaryReq.Messages[0].Content[0].Text, "Pay special attention to: the tests")

	msgs := s.Messages()
	assert.Eq(t, len(msgs), 3)
	assert.Eq(t, isSummaryMsg(msgs[0]), true)
	assert.Contains(t, msgs[0].Content[0].Text, summaryReply)
	assert.Eq(t, strings.HasPrefix(msgs[1].Content[0].Text, "u3"), true)
	assert.Eq(t, strings.HasPrefix(msgs[2].Content[0].Text, "A3"), true)

	evs := eventsOf(r.events(t), "compaction")
	assert.Eq(t, len(evs), 1)
	ev := evs[0]
	assert.Eq(t, ev["first_kept_id"].(string), idsBefore[4])
	assert.Eq(t, ev["summary"].(string), strings.TrimPrefix(msgs[0].Content[0].Text, compactSummary))
	before, after := ev["tokens_before"].(float64), ev["tokens_after"].(float64)
	assert.Eq(t, after < before, true)
	assert.Eq(t, int(before), res.TokensBefore)
	assert.Eq(t, int(after), res.TokensAfter)
	assert.Eq(t, ev["usage"].(map[string]any)["completion_tokens"].(float64), 3.0)

	ids := s.MessageIDs()
	assert.Eq(t, ids[0], ev["id"].(string))
	assert.Eq(t, strings.Join(ids[1:], ","), strings.Join(idsBefore[4:], ","))

	assert.NoErr(t, s.Close("ok", core.ExitOK))
	end := lastEnded(t, r.events(t))
	assert.Eq(t, end["prompt_tokens"].(float64), 11.0)
}

func TestSession_CompactNothingToCompact(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	s := newCompactSession(t, r, &core.CompactConfig{Keep: 150})
	ctx := context.Background()
	send(t, s, ctx, "u")
	_, err := s.Compact(ctx, "")
	assert.Eq(t, err, core.ErrNothingToCompact)
	assert.Eq(t, len(r.prov.Requests), 1)
}

func TestSession_CompactOffReturnsError(t *testing.T) {
	r := newRig(t, nil)
	s := newCompactSession(t, r, nil)
	_, err := s.Compact(context.Background(), "")
	if err == nil {
		t.Fatal("want error with compaction off")
	}
}

// autoRig scripts a big first exchange, so the second Send crosses the
// threshold (1500 tokens) with u1 and A1 to compact and u2 kept.
func autoRig(t *testing.T, summary fake.Turn, more ...fake.Turn) (*rig, *core.Session) {
	t.Helper()
	turns := append([]fake.Turn{{Text: "A1" + tokens(100)}, summary}, more...)
	r := newRig(t, turns)
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 1500, Keep: 50})
	// u1 alone is over the threshold but has nothing to cut: falls through.
	res := send(t, s, context.Background(), "u1"+tokens(1200))
	assert.Eq(t, res.Status, "ok")
	return r, s
}

func TestSession_AutoCompactBeforeRequest(t *testing.T) {
	r, s := autoRig(t, fake.Turn{Text: summaryReply}, fake.Turn{Text: "B"})
	res := send(t, s, context.Background(), "u2"+tokens(100))
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "B")

	assert.Eq(t, len(r.prov.Requests), 3)
	sum := r.prov.Requests[1]
	assert.Eq(t, len(sum.Tools), 0)
	assert.Contains(t, sum.Messages[0].Content[0].Text, "u1")
	next := r.prov.Requests[2].Messages
	assert.Eq(t, len(next), 2)
	assert.Eq(t, isSummaryMsg(next[0]), true)
	assert.Eq(t, strings.HasPrefix(next[1].Content[0].Text, "u2"), true)
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 1)
}

func TestSession_AutoCompactNothingToCutIsContextFull(t *testing.T) {
	r := newRig(t, nil)
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 1500, Keep: 50})
	res := send(t, s, context.Background(), tokens(compactWindow+100))
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Reason, "context window full")
	assert.Eq(t, len(r.prov.Requests), 0)
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 0)
}

func TestSession_CompactCancelledLeavesHistory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, s := autoRig(t, fake.Turn{Text: "partial", Block: true, OnEmit: cancel})
	before, beforeIDs := s.Messages(), s.MessageIDs()

	res := send(t, s, ctx, "u2"+tokens(100))
	assert.Eq(t, res.Status, "interrupted")
	assert.Eq(t, res.Code, core.ExitInterrupted)

	after := s.Messages()
	assert.Eq(t, len(after), len(before)+1) // only u2 was added
	assert.Eq(t, msgSummary(after[:len(before)]), msgSummary(before))
	assert.Eq(t, strings.Join(s.MessageIDs()[:len(before)], ","), strings.Join(beforeIDs, ","))
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 0)
}

func TestSession_CompactRejectsToolCall(t *testing.T) {
	r, s := autoRig(t, fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}})
	before := s.Messages()

	res := send(t, s, context.Background(), "u2"+tokens(100))
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Code, core.ExitContextFull)
	assert.Eq(t, res.Reason, "compaction failed: summary tried to call a tool")

	after := s.Messages()
	assert.Eq(t, len(after), len(before)+1)
	assert.Eq(t, msgSummary(after[:len(before)]), msgSummary(before))
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 0)
}

func TestSession_CompactEmptySummary(t *testing.T) {
	_, s := autoRig(t, fake.Turn{Text: "  "})
	res := send(t, s, context.Background(), "u2"+tokens(100))
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Reason, "compaction failed: empty summary")
}

func TestSession_CompactStreamError(t *testing.T) {
	_, s := autoRig(t, fake.Turn{Err: errBoom})
	res := send(t, s, context.Background(), "u2"+tokens(100))
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Reason, "compaction failed: "+errBoom.Error())
}

// overflowRig has one small exchange, then a second Send whose first request
// overflows; retry is what happens after the summary turn.
func overflowRig(t *testing.T, retry fake.Turn) (*rig, core.TurnResult) {
	t.Helper()
	r := newRig(t, []fake.Turn{
		{Text: "A1" + tokens(100)},
		{Err: providers.ErrContextLength},
		{Text: summaryReply},
		retry,
	})
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 0, Keep: 50})
	ctx := context.Background()
	send(t, s, ctx, "u1"+tokens(100))
	return r, send(t, s, ctx, "u2"+tokens(100))
}

func TestSession_OverflowRetriesOnce(t *testing.T) {
	r, res := overflowRig(t, fake.Turn{Text: "B"})
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "B")
	assert.Eq(t, res.Turns, 1)
	assert.Eq(t, len(r.prov.Requests), 4)
	assert.Eq(t, len(r.prov.Requests[2].Tools), 0)
	retried := r.prov.Requests[3].Messages
	assert.Eq(t, len(retried), 2)
	assert.Eq(t, isSummaryMsg(retried[0]), true)
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 1)
}

func TestSession_OverflowTwiceIsContextFull(t *testing.T) {
	r, res := overflowRig(t, fake.Turn{Err: providers.ErrContextLength})
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Code, core.ExitContextFull)
	assert.Eq(t, res.Reason, "context window full after compaction")
	assert.Eq(t, len(r.prov.Requests), 4)
}

func TestSession_CompactOffKeepsContextFull(t *testing.T) {
	r := newRig(t, []fake.Turn{{Err: providers.ErrContextLength}})
	s := newCompactSession(t, r, nil)
	ctx := context.Background()
	res := send(t, s, ctx, tokens(compactWindow+100))
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, len(r.prov.Requests), 0)

	r = newRig(t, []fake.Turn{{Text: "A1"}, {Err: providers.ErrContextLength}})
	s = newCompactSession(t, r, nil)
	send(t, s, ctx, "u1")
	res = send(t, s, ctx, "u2")
	assert.Eq(t, res.Status, "context_full")
	assert.Eq(t, res.Reason, "context window full")
	assert.Eq(t, len(r.prov.Requests), 2)
}

func TestSession_CompactKeptMessageWithoutID(t *testing.T) {
	r := newRig(t, nil)
	sc := r.sessionConfig()
	sc.Budget = &core.Budget{Window: compactWindow}
	sc.Compact = &core.CompactConfig{Keep: 50}
	text := func(role providers.Role, s string) providers.Message {
		return providers.Message{Role: role, Content: []providers.ContentBlock{{Type: providers.BlockText, Text: s}}}
	}
	old := []providers.Message{
		text(providers.RoleUser, "u1"+tokens(100)), text(providers.RoleAssistant, "A1"+tokens(100)),
		text(providers.RoleUser, "u2"+tokens(100)),
	}
	s, err := core.ResumeSession(sc, core.ReplayState{Messages: old}) // no ids, as in an old file
	assert.NoErr(t, err)
	_, err = s.Compact(context.Background(), "")
	if err == nil || err.Error() != "compaction failed: kept message has no event id" {
		t.Fatalf("err = %v", err)
	}
	assert.Eq(t, len(r.prov.Requests), 0)
	assert.Eq(t, msgSummary(s.Messages()), msgSummary(old))
}

// smallWindowRig runs a session on the compactWindow with a Reserve far over
// the window (so the trigger is half the window, 1500 tokens) and every model
// turn, summaries included, answering with about 100 tokens.
func smallWindowRig(t *testing.T, keep int) (*rig, *core.Session) {
	t.Helper()
	turns := make([]fake.Turn, 40)
	for i := range turns {
		turns[i] = fake.Turn{Text: "A" + tokens(100)}
	}
	r := newRig(t, turns)
	return r, newCompactSession(t, r, &core.CompactConfig{Reserve: 16384, Keep: keep})
}

// summaryRequests counts the tool-less requests, which are summaries.
func summaryRequests(r *rig) int {
	n := 0
	for _, req := range r.prov.Requests {
		if len(req.Tools) == 0 {
			n++
		}
	}
	return n
}

func TestSession_SmallWindowKeepIsClamped(t *testing.T) {
	// Keep 1800 is above the 1500-token trigger: unclamped, the kept tail
	// alone is over the threshold and every Send compacts again.
	r, s := smallWindowRig(t, 1800)
	ctx := context.Background()
	const sends = 10
	compacted := false
	for i := 0; i < sends; i++ {
		reqs, sums := len(r.prov.Requests), summaryRequests(r)
		res := send(t, s, ctx, "u"+tokens(100))
		assert.Eq(t, res.Status, "ok")
		if compacted && summaryRequests(r) == sums {
			assert.Eq(t, len(r.prov.Requests)-reqs, 1)
		}
		if summaryRequests(r) > sums {
			if compacted {
				t.Fatalf("send %d compacted straight after a compaction", i)
			}
			compacted = true
			continue
		}
		compacted = false
	}
	n := len(eventsOf(r.events(t), "compaction"))
	if n == 0 || n >= sends/2 {
		t.Fatalf("compactions = %d over %d sends", n, sends)
	}
}

func TestSession_KeepLargerThanWindowStillCompacts(t *testing.T) {
	r, s := smallWindowRig(t, 3500)
	ctx := context.Background()
	for i := 0; i < 6; i++ {
		res := send(t, s, ctx, "u"+tokens(100))
		assert.Eq(t, res.Status, "ok")
	}
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")) > 0, true)
}

// compactFailLog fails the compaction event's write.
type compactFailLog struct{ core.EventLog }

func (l *compactFailLog) AppendID(typ string, data any) (string, error) {
	if typ == core.EvCompaction {
		return "", errBoom
	}
	return l.EventLog.AppendID(typ, data)
}

func TestSession_CompactLogFailureLeavesSession(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Text: "A1" + tokens(100)}, {Text: "A2" + tokens(100)}, {Text: "A3" + tokens(100)},
		{Text: summaryReply, Usage: providers.Usage{PromptTokens: 11, CompletionTokens: 3}},
	})
	r.cfg.Log = &compactFailLog{EventLog: r.cfg.Log}
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 0, Keep: 150})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		assert.Eq(t, send(t, s, ctx, u+tokens(100)).Status, "ok")
	}
	msgs, ids := s.Messages(), s.MessageIDs()

	_, err := s.Compact(ctx, "")
	if err == nil || !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want wrapped errBoom", err)
	}
	assert.Eq(t, msgSummary(s.Messages()), msgSummary(msgs))
	assert.Eq(t, strings.Join(s.MessageIDs(), ","), strings.Join(ids, ","))
	assert.Eq(t, len(eventsOf(r.events(t), "compaction")), 0)

	// The summary's usage was not added to the session total.
	assert.NoErr(t, s.Close("ok", core.ExitOK))
	assert.Eq(t, lastEnded(t, r.events(t))["prompt_tokens"].(float64), 0.0)
}
