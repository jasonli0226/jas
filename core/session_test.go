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

func TestSession_SetSystemLoggedAndUsed(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}, {Text: "B"}})
	s := newSession(t, r)
	ctx := context.Background()
	send(t, s, ctx, "a")
	assert.Eq(t, r.prov.Requests[0].System, r.cfg.System)
	next := "reloaded system prompt UNIQUE-7721"
	assert.NoErr(t, s.SetSystem(next, map[string]any{"context_files": []string{"AGENTS.md"}}))
	assert.Eq(t, s.System(), next)
	send(t, s, ctx, "b")
	assert.Eq(t, r.prov.Requests[1].System, next)
	var found bool
	for _, e := range r.events(t) {
		if e["type"] == "context_reloaded" {
			found = true
			assert.Eq(t, int(e["bytes"].(float64)), len(next))
			assert.Eq(t, e["context_files"].([]any)[0], "AGENTS.md")
		}
	}
	assert.Eq(t, found, true)
}

func TestSession_SetSystemReturnsLogError(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	r.cfg.Log = failLog{r.cfg.Log, map[string]bool{"context_reloaded": true}}
	s := newSession(t, r)
	err := s.SetSystem("x", nil)
	if !errors.Is(err, errBoom) {
		t.Fatalf("context_reloaded failure not returned: %v", err)
	}
	assert.Contains(t, err.Error(), "event log")
	assert.Eq(t, s.System(), "x") // takes effect even when logging fails
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

// hookLog runs onAppend after each Append of the given event type.
type hookLog struct {
	core.EventLog
	typ      string
	onAppend func()
}

func (h *hookLog) Append(typ string, data any) error {
	err := h.EventLog.Append(typ, data)
	if typ == h.typ {
		h.onAppend()
	}
	return err
}

func (h *hookLog) AppendID(typ string, data any) (string, error) {
	id, err := h.EventLog.AppendID(typ, data)
	if typ == h.typ {
		h.onAppend()
	}
	return id, err
}

func TestSession_SteerAfterEscAtFinalIsNotCommitted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := newRig(t, []fake.Turn{{Text: "done"}})
	var s *core.Session
	hl := &hookLog{EventLog: r.cfg.Log, typ: core.EvAssistantMessage, onAppend: func() { cancel(); s.Steer("late") }}
	r.cfg.Log, r.cfg.Exec.Log = hl, hl
	s = newSession(t, r)
	res := send(t, s, ctx, "go")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "done")
	if queuedSteer(s.Messages(), "late") {
		t.Fatalf("steer committed after Esc at final: %s", msgSummary(s.Messages()))
	}
	assert.Eq(t, strings.Join(s.TakeSteering(), ","), "late")
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

type recordingUI struct {
	core.UI
	reasoning, text strings.Builder
}

func (u *recordingUI) Reasoning(d string) { u.reasoning.WriteString(d) }
func (u *recordingUI) Text(d string)      { u.text.WriteString(d) }

func TestSession_ForwardsReasoningToUI(t *testing.T) {
	r := newRig(t, []fake.Turn{{Reasoning: "hmm", Text: "A"}})
	ui := &recordingUI{UI: core.NewQuietUI()}
	r.cfg.UI = ui
	s := newSession(t, r)
	send(t, s, context.Background(), "q")
	assert.Eq(t, ui.reasoning.String(), "hmm")
	assert.Eq(t, ui.text.String(), "A")
	msgs := s.Messages()
	last := msgs[len(msgs)-1]
	assert.Eq(t, last.Role, providers.Role("assistant"))
	assert.Eq(t, len(last.Content), 1)
	assert.Eq(t, last.Content[0].Type, providers.BlockText)
	assert.Eq(t, last.Content[0].Text, "A")
}

func eventIDs(evs []map[string]any, typ string) []string {
	var out []string
	for _, e := range evs {
		if e["type"] == typ {
			out = append(out, e["id"].(string))
		}
	}
	return out
}

func TestSession_TracksMessageEventIDs(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}, {Text: "done"}})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "ok")
	evs := r.events(t)
	user, asst := eventIDs(evs, core.EvUserMessage), eventIDs(evs, core.EvAssistantMessage)
	assert.Eq(t, len(user), 1)
	assert.Eq(t, len(asst), 2)
	want := []string{user[0], asst[0], "", asst[1]}
	assert.Eq(t, strings.Join(s.MessageIDs(), ","), strings.Join(want, ","))
	assert.Eq(t, len(s.MessageIDs()), len(s.Messages()))
}

func TestSession_SetTitleNormalizesAndLogs(t *testing.T) {
	r := newRig(t, nil)
	s := newSession(t, r)
	assert.NoErr(t, s.SetTitle("  fix  login\n bug "))
	assert.Eq(t, s.Title(), "fix login bug")
	assert.NoErr(t, s.SetTitle(""))
	assert.Eq(t, s.Title(), "")
	var titles []string
	for _, e := range r.events(t) {
		if e["type"] == "session_titled" {
			titles = append(titles, e["title"].(string))
		}
	}
	assert.Eq(t, strings.Join(titles, "|"), "fix login bug|")
}

func TestSession_SetTitleCapsLength(t *testing.T) {
	s := newSession(t, newRig(t, nil))
	assert.NoErr(t, s.SetTitle(strings.Repeat("x", 200)))
	assert.Eq(t, len([]rune(s.Title())), 80)
}

func TestSession_RequestCarriesMaxOutput(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "ok"}})
	sc := r.sessionConfig()
	sc.MaxOutput = 123
	s, err := core.NewSession(sc)
	assert.NoErr(t, err)
	send(t, s, context.Background(), "hi")
	assert.Eq(t, r.prov.Requests[0].MaxTokens, 123)
}

// A small known window caps max_tokens to the room left after the prompt, so
// a 16k-context local model is not asked for more than it can return.
func TestSession_MaxOutputClampedToWindowRoom(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "ok"}})
	sc := r.sessionConfig()
	sc.MaxOutput = 16000
	sc.Budget = &core.Budget{Window: 4000, ElideAt: core.DefaultElideAt, Keep: core.DefaultKeepToolResults}
	s, err := core.NewSession(sc)
	assert.NoErr(t, err)
	send(t, s, context.Background(), "hi")
	got := r.prov.Requests[0].MaxTokens
	if got <= 0 || got >= 4000 {
		t.Fatalf("MaxTokens = %d, want clamped into (0, 4000)", got)
	}
}

// model_changed names where the new model's requests go: the session's
// provider and base_url, never a key (#97).
func TestSession_SetModelLogsProviderAndBaseURL(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	s := newSession(t, r)
	assert.NoErr(t, s.SetModel("m2"))
	var got map[string]any
	for _, e := range r.events(t) {
		if e["type"] == "model_changed" {
			got = e
		}
	}
	assert.Eq(t, got["model"], "m2")
	assert.Eq(t, got["provider"], "vllm")
	assert.Eq(t, got["base_url"], "http://x")
}

// TestSession_ReplyCutOffAtOutputCap: a reply with no tool calls that ends on
// finish_reason "length" is not an answer; the turn ends output_cap, exit 9,
// and the log records the finish reason (#194).
func TestSession_ReplyCutOffAtOutputCap(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "half an ans", Finish: "length"}})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "output_cap")
	assert.Eq(t, res.Code, core.ExitOutputCap)
	assert.Eq(t, res.Final, "half an ans")
	assert.NoErr(t, s.Close(res.Status, res.Code))

	evs := r.events(t)
	for _, e := range evs {
		if e["type"] == "assistant_message" {
			assert.Eq(t, e["final"], false)
			assert.Eq(t, e["finish_reason"], "length")
		}
	}
	end := lastEnded(t, evs)
	assert.Eq(t, end["status"], "output_cap")
	assert.Eq(t, end["exit_code"].(float64), 9.0)
}

// TestSession_FinishReasonLoggedOnAnswer: a normal answer logs its finish
// reason too.
func TestSession_FinishReasonLoggedOnAnswer(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "done"}})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "ok")
	assert.NoErr(t, s.Close(res.Status, res.Code))
	for _, e := range r.events(t) {
		if e["type"] == "assistant_message" {
			assert.Eq(t, e["final"], true)
			assert.Eq(t, e["finish_reason"], "stop")
		}
	}
}

// TestSession_OutputCapReasonWithoutMaxOutput: with no max_output_tokens the
// provider's own limit cut the reply; the reason does not claim a cap of 0.
func TestSession_OutputCapReasonWithoutMaxOutput(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "half", Finish: "length"}})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "output_cap")
	assert.Eq(t, res.Reason, "reply cut off at the provider's output limit")
	assert.NoErr(t, s.Close(res.Status, res.Code))
}
