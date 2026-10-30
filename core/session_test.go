package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// sessionConfig turns a Run rig into the equivalent SessionConfig.
func (r *rig) sessionConfig() core.SessionConfig {
	c := r.cfg
	return core.SessionConfig{
		Provider: c.Provider, Model: c.Model, System: c.System, Exec: c.Exec,
		Log: c.Log, UI: c.UI, MaxTurns: c.MaxTurns, Budget: c.Budget, Meta: c.SessionMeta,
	}
}

func newSession(t *testing.T, r *rig) *core.Session {
	t.Helper()
	s, err := core.NewSession(r.sessionConfig())
	assert.NoErr(t, err)
	return s
}

func send(t *testing.T, s *core.Session, ctx context.Context, text string) core.TurnResult {
	t.Helper()
	res, err := s.Send(ctx, text)
	assert.NoErr(t, err)
	return res
}

func count(evs []map[string]any, typ string) int {
	n := 0
	for _, e := range evs {
		if e["type"] == typ {
			n++
		}
	}
	return n
}

// msgSummary renders messages as "role:text" with tool blocks as their type.
func msgSummary(msgs []providers.Message) string {
	var out []string
	for _, m := range msgs {
		var parts []string
		for _, b := range m.Content {
			if b.Type == providers.BlockText {
				parts = append(parts, b.Text)
			} else {
				parts = append(parts, b.Type)
			}
		}
		out = append(out, string(m.Role)+":"+strings.Join(parts, "+"))
	}
	return strings.Join(out, " | ")
}

func TestSession_SecondSendSeesHistory(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}, {Text: "B"}})
	s := newSession(t, r)
	ctx := context.Background()
	assert.Eq(t, send(t, s, ctx, "a").Final, "A")
	res := send(t, s, ctx, "b")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "B")
	assert.Eq(t, len(r.prov.Requests), 2)
	assert.Eq(t, msgSummary(r.prov.Requests[1].Messages), "user:a | assistant:A | user:b")
	assert.NoErr(t, s.Close(res.Status, res.Code))

	evs := r.events(t)
	assert.Eq(t, count(evs, "session_started"), 1)
	assert.Eq(t, count(evs, "session_ended"), 1)
	var got []string
	for _, ty := range types(evs) {
		if ty != "usage" {
			got = append(got, ty)
		}
	}
	assert.Eq(t, strings.Join(got, ","), "session_started,"+
		"user_message,turn_started,assistant_message,turn_ended,"+
		"user_message,turn_started,assistant_message,turn_ended,session_ended")
}

func TestSession_TurnCapPerMessage(t *testing.T) {
	call := fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}
	r := newRig(t, []fake.Turn{call, call, {Text: "ok"}})
	r.cfg.MaxTurns = 2
	s := newSession(t, r)
	ctx := context.Background()
	res := send(t, s, ctx, "loop")
	assert.Eq(t, res.Status, "turn_cap")
	assert.Eq(t, res.Code, core.ExitTurnCap)
	assert.Eq(t, res.Turns, 2)
	res = send(t, s, ctx, "stop")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Turns, 1)
	assert.Eq(t, res.Final, "ok")
}

func TestSession_CancelThenSendAgain(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, Block: true},
		{Text: "fine"},
	})
	ctx, cancel := context.WithCancel(context.Background())
	r.prov.Turns[0].OnEmit = cancel
	s := newSession(t, r)
	res := send(t, s, ctx, "go")
	assert.Eq(t, res.Status, "interrupted")
	assert.Eq(t, res.Code, core.ExitInterrupted)

	res = send(t, s, context.Background(), "next")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "fine")
	assertWellFormed(t, r.prov.Requests[1].Messages, 1)
	assert.Eq(t, msgSummary(r.prov.Requests[1].Messages), "user:go | assistant:tool_use | user:tool_result | user:next")
}

func TestSession_SteerDeliveredAfterToolResults(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "done"},
	})
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("also X") }
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Final, "done")
	assert.Eq(t, res.Turns, 2)

	msgs := r.prov.Requests[1].Messages
	assert.Eq(t, msgSummary(msgs[len(msgs)-2:]), "user:tool_result | user:also X")

	var steers []map[string]any
	for _, e := range r.events(t) {
		if e["type"] == "user_message" && e["steer"] == true {
			steers = append(steers, e)
		}
	}
	assert.Eq(t, len(steers), 1)
	assert.Eq(t, steers[0]["content"].([]any)[0].(map[string]any)["text"].(string), "also X")
}

func TestSession_SteerJoinsQueuedTexts(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "done"},
	})
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("one"); s.Steer("two") }
	send(t, s, context.Background(), "go")
	msgs := r.prov.Requests[1].Messages
	assert.Eq(t, msgSummary(msgs[len(msgs)-1:]), "user:one\n\ntwo")
}

func TestSession_SteerAtFinalStartsNewTurn(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Text: "first", Usage: providers.Usage{PromptTokens: 5}},
		{Text: "second", Usage: providers.Usage{PromptTokens: 9}},
	})
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("more") }
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "second")
	assert.Eq(t, res.Turns, 1)
	assert.Eq(t, res.Usage.PromptTokens, 9)
	assert.Eq(t, len(r.prov.Requests), 2)
	assert.Eq(t, msgSummary(r.prov.Requests[1].Messages), "user:go | assistant:first | user:more")
	assert.NoErr(t, s.Close(res.Status, res.Code))

	evs := r.events(t)
	var got []string
	for _, e := range evs {
		ty := e["type"].(string)
		switch {
		case ty == "usage":
			continue
		case ty == "user_message" && e["steer"] == true:
			ty = "user_message(steer)"
		case ty == "turn_ended":
			ty += "(" + e["status"].(string) + ")"
		}
		got = append(got, ty)
	}
	assert.Eq(t, strings.Join(got, ","), "session_started,user_message,turn_started,assistant_message,turn_ended(ok),"+
		"user_message(steer),turn_started,assistant_message,turn_ended(ok),session_ended")
	end := lastEnded(t, evs)
	assert.Eq(t, end["turns"].(float64), 2.0)
	assert.Eq(t, end["prompt_tokens"].(float64), 14.0)
}

func TestSession_CloseWritesSessionEndedOnce(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "hi"}})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.NoErr(t, s.Close(res.Status, res.Code))
	assert.NoErr(t, s.Close(res.Status, res.Code))
	assert.Eq(t, count(r.events(t), "session_ended"), 1)
}

func TestSession_SetModelLoggedAndUsed(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}, {Text: "B"}})
	s := newSession(t, r)
	ctx := context.Background()
	send(t, s, ctx, "a")
	assert.NoErr(t, s.SetModel("m2"))
	send(t, s, ctx, "b")
	assert.Eq(t, r.prov.Requests[0].Model, "m")
	assert.Eq(t, r.prov.Requests[1].Model, "m2")
	var changed []string
	for _, e := range r.events(t) {
		if e["type"] == "model_changed" {
			changed = append(changed, e["model"].(string))
		}
	}
	assert.Eq(t, strings.Join(changed, ","), "m2")
}

func TestSession_SetModelReturnsLogError(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	r.cfg.Log = failLog{r.cfg.Log, map[string]bool{"model_changed": true}}
	s := newSession(t, r)
	err := s.SetModel("m2")
	if !errors.Is(err, errBoom) {
		t.Fatalf("model_changed failure not returned: %v", err)
	}
	assert.Contains(t, err.Error(), "event log")
	send(t, s, context.Background(), "a")
	assert.Eq(t, r.prov.Requests[0].Model, "m2")
}

func TestSession_MessagesIsCopy(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	s := newSession(t, r)
	send(t, s, context.Background(), "a")
	m := s.Messages()
	m[0].Content = nil
	assert.Eq(t, msgSummary(s.Messages()), "user:a | assistant:A")
}

// cancelTool cancels the run's context and still returns a result.
type cancelTool struct{ cancel func() }

func (cancelTool) Name() string            { return "cancel" }
func (cancelTool) Description() string     { return "cancels" }
func (cancelTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (cancelTool) Kind() core.Kind         { return core.ReadOnly }
func (c cancelTool) Call(context.Context, core.ExecPolicy, json.RawMessage) (core.Result, error) {
	c.cancel()
	return core.Result{Output: "done"}, nil
}

func queuedSteer(msgs []providers.Message, text string) bool {
	return strings.Contains(msgSummary(msgs), text)
}

func TestSession_SteerNotCommittedAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "cancel", Args: `{}`}}},
		{Text: "unused"},
	}, cancelTool{cancel})
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("late") }
	res := send(t, s, ctx, "go")
	assert.Eq(t, res.Status, "interrupted")
	if queuedSteer(s.Messages(), "late") {
		t.Fatalf("steer committed after cancel: %s", msgSummary(s.Messages()))
	}
}

func TestSession_SteerNotCommittedAfterCancelAtFinal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newRig(t, []fake.Turn{{Text: "first"}, {Text: "unused"}})
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("late"); cancel() }
	res := send(t, s, ctx, "go")
	t.Logf("status %s", res.Status)
	if queuedSteer(s.Messages(), "late") || len(r.prov.Requests) != 1 {
		t.Fatalf("steer committed after cancel: %s", msgSummary(s.Messages()))
	}
}

func TestSession_SteerKeptWhenTurnCapHit(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}})
	r.cfg.MaxTurns = 1
	s := newSession(t, r)
	r.prov.Turns[0].OnEmit = func() { s.Steer("late") }
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "turn_cap")
	if queuedSteer(s.Messages(), "late") {
		t.Fatalf("steer committed on last turn: %s", msgSummary(s.Messages()))
	}
}

func TestSession_TakeSteeringEmptiesQueue(t *testing.T) {
	r := newRig(t, nil)
	s := newSession(t, r)
	s.Steer("one")
	s.Steer("two")
	assert.Eq(t, strings.Join(s.TakeSteering(), ","), "one,two")
	assert.Eq(t, len(s.TakeSteering()), 0)
}
