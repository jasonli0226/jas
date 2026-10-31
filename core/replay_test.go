package core_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// twoSendRig runs two Sends, the first using the read tool, and returns the rig
// and the live session's history. The log is closed so the file is replayable.
func twoSendRig(t *testing.T) (*rig, []providers.Message) {
	t.Helper()
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":"a.txt"}`}}},
		{Text: "A"},
		{Text: "B"},
	})
	assert.NoErr(t, os.WriteFile(filepath.Join(r.root, "a.txt"), []byte("hello"), 0o600))
	s := newSession(t, r)
	send(t, s, context.Background(), "a")
	send(t, s, context.Background(), "b")
	msgs := s.Messages()
	assert.NoErr(t, s.Close("ok", 0))
	assert.NoErr(t, r.cfg.Log.Close())
	return r, msgs
}

func TestReplay_RoundTripMatchesSession(t *testing.T) {
	r, want := twoSendRig(t)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	if !reflect.DeepEqual(st.Messages, want) {
		t.Fatalf("replayed messages differ:\n got %s\nwant %s", msgSummary(st.Messages), msgSummary(want))
	}
	assert.Eq(t, st.Model, "m")
	assert.Eq(t, st.Root, r.root)
	assert.Eq(t, len(st.Notices), 0)
}

// appendRaw adds text to the end of a closed log file.
func appendRaw(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	assert.NoErr(t, err)
	_, err = f.WriteString(text)
	assert.NoErr(t, err)
	assert.NoErr(t, f.Close())
}

func TestReplay_TruncatedLastLine(t *testing.T) {
	r, want := twoSendRig(t)
	appendRaw(t, r.logPath, `{"v":1,"id":"e000999","type":"user_message","content":[{"type":"te`)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	if !reflect.DeepEqual(st.Messages, want) {
		t.Fatalf("messages differ: %s", msgSummary(st.Messages))
	}
	assert.Eq(t, len(st.Notices), 1)
	assert.Contains(t, st.Notices[0], "unreadable")
}

func TestReplay_NoSessionEnded(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "A"}})
	s := newSession(t, r)
	send(t, s, context.Background(), "a") // never closed: crash after turn_ended
	assert.NoErr(t, r.cfg.Log.Close())
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, msgSummary(st.Messages), "user:a | assistant:A")
	assert.Eq(t, len(st.Notices), 0)
}

func TestReplay_OrphanToolUseGetsInterruptedResult(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "go"}}}))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{
		"text": "", "final": false,
		"content": []map[string]any{
			{"type": "tool_use", "tool_use_id": "c1", "name": "read", "input": map[string]any{"path": "x"}},
			{"type": "tool_use", "tool_use_id": "c2", "name": "read", "input": map[string]any{"path": "y"}},
		}}))
	assert.NoErr(t, log.Append(core.EvToolStarted, map[string]any{"call_id": "c1", "tool": "read"}))
	assert.NoErr(t, log.Append(core.EvToolResult, map[string]any{
		"call_id": "c2", "tool": "read", "status": "ok", "output": "yy"}))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.Messages), 3)
	last := st.Messages[2]
	assert.Eq(t, last.Role, providers.RoleUser)
	assert.Eq(t, len(last.Content), 2)
	// Results follow the tool_use order, not the log order.
	assert.Eq(t, last.Content[0].ToolUseID, "c1")
	assert.Eq(t, last.Content[0].Type, providers.BlockToolResult)
	assert.Eq(t, last.Content[0].IsError, true)
	assert.Eq(t, last.Content[0].Text, "interrupted: jas stopped while this call was running; it may have partly run. Inspect the current state before retrying.")
	assert.Eq(t, last.Content[1].ToolUseID, "c2")
	assert.Eq(t, last.Content[1].IsError, false)
	assert.Eq(t, last.Content[1].Text, "yy")
	assert.Eq(t, len(st.Interrupted), 1)
	assert.Eq(t, st.Interrupted[0], core.InterruptedCall{ID: "c1", Tool: "read", Status: "interrupted", Output: last.Content[0].Text})
}

func TestResumeSession_LogsInterruptedResults(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "go"}}}))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{
		"text": "", "final": false,
		"content": []map[string]any{
			{"type": "tool_use", "tool_use_id": "c1", "name": "bash", "input": map[string]any{"command": "x"}},
		}}))
	assert.NoErr(t, log.Append(core.EvToolStarted, map[string]any{"call_id": "c1", "tool": "bash"}))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	log2, err := core.OpenEventLog(r.logPath, nil, nil)
	assert.NoErr(t, err)
	c := r.sessionConfig()
	c.Log = log2
	c.Exec.Log = log2
	s, err := core.ResumeSession(c, st)
	assert.NoErr(t, err)
	assert.NoErr(t, s.Close("ok", 0))
	assert.NoErr(t, log2.Close())

	evs := r.events(t)
	var got []string
	for _, e := range evs {
		ty := e["type"].(string)
		if ty == "tool_result" {
			assert.Eq(t, e["call_id"].(string), "c1")
			assert.Eq(t, e["tool"].(string), "bash")
			assert.Eq(t, e["status"].(string), "interrupted")
			assert.Eq(t, e["output"].(string), st.Messages[2].Content[0].Text)
		}
		got = append(got, ty)
	}
	assert.Eq(t, strings.Join(got, ","), "session_started,user_message,assistant_message,tool_started,session_resumed,tool_result,session_ended")

	st2, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st2.Interrupted), 0)
	if !reflect.DeepEqual(st2.Messages, st.Messages) {
		t.Fatalf("messages changed: %s vs %s", msgSummary(st2.Messages), msgSummary(st.Messages))
	}
}

func TestReplay_NoSessionStartedIsError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	assert.NoErr(t, os.WriteFile(p, []byte("garbage\n"), 0o600))
	_, err := core.Replay(p)
	if err == nil {
		t.Fatal("want error")
	}
}

func TestReplay_OldAssistantMessageWithoutContentIsSkipped(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{"text": "old", "final": true}))
	assert.NoErr(t, log.Close())
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.Messages), 0)
	assert.Eq(t, len(st.Notices), 1)
}

func TestResumeSession_ContinuesFile(t *testing.T) {
	r, want := twoSendRig(t)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)

	log, err := core.OpenEventLog(r.logPath, nil, nil)
	assert.NoErr(t, err)
	prov := &fake.Provider{Turns: []fake.Turn{{Text: "C"}}}
	c := r.sessionConfig()
	c.Provider, c.Log, c.System, c.Model = prov, log, "fresh system", "m2"
	c.Exec.Log = log
	s, err := core.ResumeSession(c, st)
	assert.NoErr(t, err)
	res := send(t, s, context.Background(), "next")
	assert.Eq(t, res.Final, "C")
	assert.NoErr(t, s.Close(res.Status, res.Code))
	assert.NoErr(t, log.Close())

	req := prov.Requests[0]
	assert.Eq(t, req.System, "fresh system")
	assert.Eq(t, req.Model, "m2")
	if !reflect.DeepEqual(req.Messages[:len(want)], want) {
		t.Fatalf("history not replayed: %s", msgSummary(req.Messages))
	}
	assert.Eq(t, msgSummary(req.Messages[len(want):]), "user:next")

	evs := r.events(t)
	assert.Eq(t, count(evs, "session_started"), 1)
	assert.Eq(t, count(evs, "session_resumed"), 1)
	seen := map[string]bool{}
	for i, e := range evs {
		id := e["id"].(string)
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
		assert.Eq(t, e["seq"].(float64), float64(i+1))
	}
	for _, e := range evs {
		if e["type"] == "session_resumed" {
			assert.Eq(t, e["model"].(string), "m2")
			assert.Eq(t, e["root"].(string), r.root)
		}
	}
}

// writeSession writes a one-message session file under dir/id and sets its mtime.
func writeSession(t *testing.T, dir, id, root, first string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, id, "events.jsonl")
	log, err := core.OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": first}}}))
	assert.NoErr(t, log.Close())
	assert.NoErr(t, os.Chtimes(p, mtime, mtime))
}

func TestListSessions_FiltersByRootNewestFirst(t *testing.T) {
	dir := t.TempDir()
	rootA, rootB := t.TempDir(), t.TempDir()
	now := time.Now()
	writeSession(t, dir, "s1", rootA, "old one", now.Add(-3*time.Hour))
	writeSession(t, dir, "s2", rootB, "other project", now.Add(-2*time.Hour))
	writeSession(t, dir, "s3", rootA, "line one\nline two "+strings.Repeat("x", 200), now.Add(-time.Hour))

	got, err := core.ListSessions(dir, rootA, 10)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0].ID, "s3")
	assert.Eq(t, got[1].ID, "s1")
	assert.Eq(t, got[1].FirstMessage, "old one")
	assert.Eq(t, got[0].Path, filepath.Join(dir, "s3", "events.jsonl"))
	assert.Eq(t, strings.Contains(got[0].FirstMessage, "\n"), false)
	assert.Eq(t, len([]rune(got[0].FirstMessage)) <= 80, true)
	assert.Eq(t, got[0].Started.IsZero(), false)

	got, err = core.ListSessions(dir, rootA, 1)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0].ID, "s3")
}

func TestListSessions_MissingDirIsEmpty(t *testing.T) {
	got, err := core.ListSessions(filepath.Join(t.TempDir(), "nope"), t.TempDir(), 5)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 0)
}

func TestResumeSession_ReElidesLongHistory(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	big := strings.Repeat("x", 1000)
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "go"}}}))
	var uses []map[string]any
	for _, id := range []string{"c1", "c2", "c3"} {
		uses = append(uses, map[string]any{"type": "tool_use", "tool_use_id": id, "name": "read", "input": map[string]any{"path": id}})
	}
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{"text": "", "final": false, "content": uses}))
	for _, id := range []string{"c1", "c2", "c3"} {
		assert.NoErr(t, log.Append(core.EvToolResult, map[string]any{"call_id": id, "tool": "read", "status": "ok", "output": big}))
	}
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{"text": "ok", "final": true,
		"content": []map[string]any{{"type": "text", "text": "ok"}}}))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	newBudget := func() *core.Budget { return &core.Budget{Window: 600, ElideAt: 0.6, Keep: 1} }
	assert.Eq(t, newBudget().WouldExceed(st.Messages), true) // unelided history does not fit

	log, err = core.OpenEventLog(r.logPath, nil, nil)
	assert.NoErr(t, err)
	prov := &fake.Provider{Turns: []fake.Turn{{Text: "C"}}}
	c := r.sessionConfig()
	c.Provider, c.Log, c.Budget = prov, log, newBudget()
	s, err := core.ResumeSession(c, st)
	assert.NoErr(t, err)
	res := send(t, s, context.Background(), "next")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, len(prov.Requests), 1)
	assert.NoErr(t, log.Close())

	evs := r.events(t)
	var order []string
	for _, e := range evs {
		if e["type"] == "session_resumed" || e["type"] == "elided" {
			order = append(order, e["type"].(string))
		}
	}
	assert.Eq(t, strings.Join(order, ","), "session_resumed,elided")
}

func TestSession_MalformedToolArgsRecoverAndReplay(t *testing.T) {
	r := newRig(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path": "x"`}}},
		{Text: "fixed"},
	})
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.Eq(t, res.Status, "ok")
	assert.Eq(t, res.Final, "fixed")
	want := s.Messages()
	assert.Contains(t, want[2].Content[0].Text, "invalid JSON arguments")
	assert.NoErr(t, s.Close("ok", 0))
	assert.NoErr(t, r.cfg.Log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	if !reflect.DeepEqual(st.Messages, want) {
		t.Fatalf("replayed messages differ:\n got %s\nwant %s", msgSummary(st.Messages), msgSummary(want))
	}
	assert.Eq(t, string(st.Messages[1].Content[0].Input), `{"path": "x"`)
}

func TestReplay_ToolUseNeverStartedIsNotRun(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "go"}}}))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{
		"text": "", "final": false,
		"content": []map[string]any{
			{"type": "tool_use", "tool_use_id": "c1", "name": "bash", "input": map[string]any{"command": "x"}},
			{"type": "tool_use", "tool_use_id": "c2", "name": "read", "input": map[string]any{"path": "y"}},
		}}))
	assert.NoErr(t, log.Append(core.EvToolStarted, map[string]any{"call_id": "c1", "tool": "bash"}))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	res := st.Messages[2].Content
	assert.Contains(t, res[0].Text, "interrupted:")
	assert.Eq(t, res[1].IsError, true)
	assert.Eq(t, res[1].Text, "not run: jas stopped before this call started; it had no effect.")
	assert.Eq(t, len(st.Interrupted), 2)
	assert.Eq(t, st.Interrupted[0].Status, "interrupted")
	assert.Eq(t, st.Interrupted[1], core.InterruptedCall{ID: "c2", Tool: "read", Status: "not_run", Output: res[1].Text})
}

func TestReplay_NewerSchemaVersionIsRefused(t *testing.T) {
	r, _ := twoSendRig(t)
	appendRaw(t, r.logPath, `{"v":9,"id":"e000999","seq":999,"type":"user_message","content":[{"type":"text","text":"future"}]}`+"\n")
	_, err := core.Replay(r.logPath)
	if err == nil || !strings.Contains(err.Error(), "newer jas") {
		t.Fatalf("want newer-version error, got %v", err)
	}
}

func TestReplay_MessageIDsMatchEvents(t *testing.T) {
	r, _ := twoSendRig(t)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	byType := map[string][]string{}
	for _, e := range r.events(t) {
		ty := e["type"].(string)
		byType[ty] = append(byType[ty], e["id"].(string))
	}
	users, asst, results := byType["user_message"], byType["assistant_message"], byType["tool_result"]
	// user a, assistant tool_use, tool results, assistant A, user b, assistant B
	want := []string{users[0], asst[0], results[0], asst[1], users[1], asst[2]}
	assert.Eq(t, strings.Join(st.MessageIDs, ","), strings.Join(want, ","))
}

func TestReplay_SynthesizedResultsHaveNoID(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "go"}}}))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, map[string]any{
		"content": []map[string]any{{"type": "tool_use", "tool_use_id": "c1", "name": "read", "input": map[string]any{}}}}))
	assert.NoErr(t, log.Close())
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.MessageIDs), 3)
	assert.Eq(t, st.MessageIDs[2], "")
}

func TestResumeModel(t *testing.T) {
	st := core.ReplayState{Model: "glm-4.6", BaseURL: "https://openrouter.ai/api/v1/"}
	m, notice := core.ResumeModel(st, "https://openrouter.ai/api/v1", "deepseek/x")
	assert.Eq(t, m, "glm-4.6")
	assert.Eq(t, notice, "")

	m, notice = core.ResumeModel(st, "http://localhost:11434/v1", "qwen3")
	assert.Eq(t, m, "qwen3")
	assert.Contains(t, notice, "glm-4.6")
	assert.Contains(t, notice, "qwen3")

	m, notice = core.ResumeModel(core.ReplayState{}, "http://x", "qwen3")
	assert.Eq(t, m, "qwen3")
	assert.Eq(t, notice, "")
}

func TestReplay_RecordsBaseURL(t *testing.T) {
	r, _ := twoSendRig(t)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, st.BaseURL, "http://x")
}

func TestReplay_OnlyTrailingOrphansAreInterrupted(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	use := func(id string) map[string]any {
		return map[string]any{"text": "", "final": false, "content": []map[string]any{
			{"type": "tool_use", "tool_use_id": id, "name": "read", "input": map[string]any{"path": "x"}}}}
	}
	user := map[string]any{"content": []map[string]any{{"type": "text", "text": "go"}}}
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, user))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, use("c1")))
	assert.NoErr(t, log.Append(core.EvUserMessage, user))
	assert.NoErr(t, log.Append(core.EvAssistantMessage, use("c2")))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.Interrupted), 1)
	assert.Eq(t, st.Interrupted[0].ID, "c2")
	var got []string
	for _, m := range st.Messages {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolResult {
				got = append(got, b.ToolUseID)
			}
		}
	}
	assert.Eq(t, strings.Join(got, ","), "c1,c2")
}

func TestResumeSession_SeedsIDs(t *testing.T) {
	r := newRig(t, []fake.Turn{{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}, {Text: "done"}})
	s := newSession(t, r)
	send(t, s, context.Background(), "go")
	assert.NoErr(t, s.Close("ok", 0))
	assert.NoErr(t, r.cfg.Log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	log2, err := core.OpenEventLog(r.logPath, nil, nil)
	assert.NoErr(t, err)
	c := r.sessionConfig()
	c.Log = log2
	c.Exec.Log = log2
	s2, err := core.ResumeSession(c, st)
	assert.NoErr(t, err)
	assert.NoErr(t, s2.Close("ok", 0))
	assert.NoErr(t, log2.Close())

	assert.Eq(t, len(st.MessageIDs), 4)
	assert.Eq(t, strings.Join(s2.MessageIDs(), ","), strings.Join(st.MessageIDs, ","))
	assert.Eq(t, len(s2.MessageIDs()), len(s2.Messages()))
}

// compactedRig runs three sends, a Compact, then keeps going; turns holds the
// provider script after the three initial replies. It returns the live session.
func compactedRig(t *testing.T, rest ...fake.Turn) (*rig, *core.Session) {
	t.Helper()
	turns := append([]fake.Turn{
		{Text: "A1" + tokens(100)}, {Text: "A2" + tokens(100)}, {Text: "A3" + tokens(100)},
		{Text: summaryReply},
	}, rest...)
	r := newRig(t, turns)
	s := newCompactSession(t, r, &core.CompactConfig{Keep: 150})
	for _, u := range []string{"u1", "u2", "u3"} {
		send(t, s, context.Background(), u+tokens(100))
	}
	_, err := s.Compact(context.Background(), "")
	assert.NoErr(t, err)
	return r, s
}

func closeAll(t *testing.T, r *rig, s *core.Session) {
	t.Helper()
	assert.NoErr(t, s.Close("ok", 0))
	assert.NoErr(t, r.cfg.Log.Close())
}

func TestReplay_CompactionRoundTrip(t *testing.T) {
	r, s := compactedRig(t, fake.Turn{Text: "A4"})
	send(t, s, context.Background(), "u4")
	want, wantIDs := s.Messages(), s.MessageIDs()
	closeAll(t, r, s)

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	if !reflect.DeepEqual(st.Messages, want) {
		t.Fatalf("replayed messages differ:\n got %s\nwant %s", msgSummary(st.Messages), msgSummary(want))
	}
	assert.Eq(t, strings.Join(st.MessageIDs, ","), strings.Join(wantIDs, ","))
	assert.Eq(t, len(st.Notices), 0)
}

func TestReplay_UsesLatestCompaction(t *testing.T) {
	r, s := compactedRig(t, fake.Turn{Text: "A4" + tokens(100)}, fake.Turn{Text: "A5" + tokens(100)}, fake.Turn{Text: "SUMMARY-TWO"})
	send(t, s, context.Background(), "u4"+tokens(100))
	send(t, s, context.Background(), "u5"+tokens(100))
	_, err := s.Compact(context.Background(), "")
	assert.NoErr(t, err)
	want, wantIDs := s.Messages(), s.MessageIDs()
	closeAll(t, r, s)

	evs := eventsOf(r.events(t), "compaction")
	assert.Eq(t, len(evs), 2)
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Contains(t, st.Messages[0].Content[0].Text, "SUMMARY-TWO")
	assert.Eq(t, st.MessageIDs[0], evs[1]["id"].(string))
	if !reflect.DeepEqual(st.Messages, want) {
		t.Fatalf("replayed messages differ:\n got %s\nwant %s", msgSummary(st.Messages), msgSummary(want))
	}
	assert.Eq(t, strings.Join(st.MessageIDs, ","), strings.Join(wantIDs, ","))
}

func TestReplay_CompactionUnknownKeptID(t *testing.T) {
	r := newRig(t, nil)
	log := r.cfg.Log
	assert.NoErr(t, log.Append(core.EvSessionStarted, map[string]any{"model": "m", "root": r.root}))
	assert.NoErr(t, log.Append(core.EvUserMessage, map[string]any{
		"content": []map[string]any{{"type": "text", "text": "hi"}}}))
	assert.NoErr(t, log.Append(core.EvCompaction, map[string]any{"summary": "S", "first_kept_id": "e999999"}))
	assert.NoErr(t, log.Close())

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	assert.Eq(t, len(st.Messages), 1)
	assert.Eq(t, st.Messages[0].Content[0].Text, "hi")
	assert.Eq(t, strings.Join(st.Notices, "|"), "ignored compaction at line 3: first kept message not found")
}

func TestResumeSession_AfterCompactionCanCompactAgain(t *testing.T) {
	r, s := compactedRig(t, fake.Turn{Text: "A4" + tokens(100)}, fake.Turn{Text: "SUMMARY-TWO"})
	send(t, s, context.Background(), "u4"+tokens(100))
	closeAll(t, r, s)

	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	log2, err := core.OpenEventLog(r.logPath, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log2.Close() })
	c := r.sessionConfig()
	c.Log = log2
	c.Exec.Log = log2
	c.Budget = &core.Budget{Window: compactWindow, ElideAt: 1, Keep: core.DefaultKeepToolResults}
	c.Compact = &core.CompactConfig{Keep: 150}
	s2, err := core.ResumeSession(c, st)
	assert.NoErr(t, err)
	_, err = s2.Compact(context.Background(), "")
	assert.NoErr(t, err)

	req := r.prov.Requests[len(r.prov.Requests)-1]
	assert.Contains(t, req.Messages[0].Content[0].Text, "<previous-summary>")
	assert.Contains(t, s2.Messages()[0].Content[0].Text, "SUMMARY-TWO")
}
