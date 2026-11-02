package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

func injectSession(t *testing.T, r *rig, inject func() (string, map[string]any)) *core.Session {
	t.Helper()
	c := r.sessionConfig()
	c.Inject = inject
	s, err := core.NewSession(c)
	assert.NoErr(t, err)
	return s
}

func TestSession_InjectAfterToolResults(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "done"},
	})
	calls := 0
	s := injectSession(t, r, func() (string, map[string]any) {
		calls++
		if calls > 1 {
			return "", nil
		}
		return "BOARD", map[string]any{"board_seqs": []int{3, 4}}
	})
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Final, "done")

	for _, m := range r.prov.Requests[0].Messages {
		assert.Eq(t, strings.Contains(msgSummary([]providers.Message{m}), "BOARD"), false)
	}
	msgs := r.prov.Requests[1].Messages
	assert.Eq(t, msgSummary(msgs[len(msgs)-1:]), "user:tool_result+BOARD")

	evs := eventsOf(r.events(t), core.EvInjected)
	assert.Eq(t, len(evs), 1)
	assert.Eq(t, evs[0]["text"], "BOARD")
	assert.Eq(t, len(evs[0]["board_seqs"].([]any)), 2)
}

func TestSession_InjectNotAfterFinal(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "done"}})
	called := false
	s := injectSession(t, r, func() (string, map[string]any) { called = true; return "X", nil })
	send(t, s, context.Background(), "go")
	assert.Eq(t, called, false)
}

func TestSession_InjectLeavesSteeringQueue(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "done"},
	})
	s := injectSession(t, r, func() (string, map[string]any) { return "BOARD", nil })
	r.prov.Turns[0].OnEmit = func() { s.Steer("also X") }
	send(t, s, context.Background(), "go")
	msgs := r.prov.Requests[1].Messages
	assert.Eq(t, msgSummary(msgs[len(msgs)-2:]), "user:tool_result+BOARD | user:also X")
}

func TestReplay_InjectedAppendsTextToToolResults(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}},
		{Text: "done"},
	})
	s := injectSession(t, r, func() (string, map[string]any) { return "BOARD", nil })
	send(t, s, context.Background(), "go")
	want := msgSummary(s.Messages())
	assert.NoErr(t, r.cfg.Log.Close())
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, msgSummary(st.Messages), want)
	assert.Contains(t, want, "tool_result+BOARD")
}
