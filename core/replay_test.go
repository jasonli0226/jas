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
	assert.Eq(t, last.Content[0].Text, "interrupted")
	assert.Eq(t, last.Content[1].ToolUseID, "c2")
	assert.Eq(t, last.Content[1].IsError, false)
	assert.Eq(t, last.Content[1].Text, "yy")
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
