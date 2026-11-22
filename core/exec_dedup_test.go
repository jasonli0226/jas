package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

const aGo = "package a\n\nfunc A() {}\n"

// dedupRig is an Executor over a temp root holding a.go, logging to a file.
type dedupRig struct {
	ex      *core.Executor
	root    string
	logPath string
}

func newDedupRig(t *testing.T) *dedupRig {
	t.Helper()
	root := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "a.go"), []byte(aGo), 0o644))
	logPath := filepath.Join(t.TempDir(), "events.jsonl")
	log, err := core.OpenEventLog(logPath, nil, nil)
	assert.NoErr(t, err)
	t.Cleanup(func() { _ = log.Close() })
	ex := &core.Executor{Reg: core.NewRegistry(tools.ReadOnly()...), Policy: core.ExecPolicy{Root: root}, Gate: core.AllowAll{}, Log: log}
	return &dedupRig{ex: ex, root: root, logPath: logPath}
}

func readUse(id, args string) providers.ContentBlock {
	return providers.ContentBlock{Type: providers.BlockToolUse, ToolUseID: id, Name: "read", Input: []byte(args)}
}

// readTurn is an assistant message calling read with args, then its result.
func readTurn(id, args, result string, isErr bool) []providers.Message {
	return []providers.Message{
		{Role: providers.RoleAssistant, Content: []providers.ContentBlock{readUse(id, args)}},
		{Role: providers.RoleUser, Content: []providers.ContentBlock{{Type: providers.BlockToolResult, ToolUseID: id, Text: result, IsError: isErr}}},
	}
}

// run runs calls after history, with the calls' assistant message appended
// as the session does before running tools.
func (r *dedupRig) run(t *testing.T, history []providers.Message, calls ...providers.ContentBlock) []providers.ContentBlock {
	t.Helper()
	h := append(append([]providers.Message(nil), history...), providers.Message{Role: providers.RoleAssistant, Content: calls})
	out, err := r.ex.RunWithHistory(context.Background(), h, calls)
	assert.NoErr(t, err)
	return out
}

func (r *dedupRig) lastResult(t *testing.T) map[string]any {
	t.Helper()
	res := eventsOf(testutil.ReadEvents(t, r.logPath), "tool_result")
	return res[len(res)-1]
}

func TestExec_ReadDedupSameRead(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go"}`, aGo, false), readUse("c2", `{"path":"a.go"}`))
	note := "unchanged since your read of a.go 1 turn ago (same range)"
	assert.Eq(t, out[0].Text, note)
	ev := r.lastResult(t)
	assert.Eq(t, ev["output"].(string), note)
	assert.Eq(t, ev["unchanged"].(bool), true)
}

func TestExec_ReadDedupTurnsAgo(t *testing.T) {
	r := newDedupRig(t)
	h := readTurn("c1", `{"path":"a.go"}`, aGo, false)
	h = append(h, readTurn("c2", `{"path":"b.go"}`, "missing", true)...)
	out := r.run(t, h, readUse("c3", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, "unchanged since your read of a.go 2 turns ago (same range)")
}

func TestExec_ReadDedupChangedFile(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go"}`, "package old\n", false), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
	_, has := r.lastResult(t)["unchanged"]
	assert.Eq(t, has, false)
}

func TestExec_ReadDedupDifferentRange(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go"}`, aGo, false), readUse("c2", `{"path":"a.go","offset":2}`))
	assert.Eq(t, out[0].Text, "\nfunc A() {}\n")
}

func TestExec_ReadDedupResolvedPath(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"./a.go"}`, aGo, false), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, "unchanged since your read of a.go 1 turn ago (same range)")
}

func TestExec_ReadDedupOffsetSpellings(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go","offset":1,"limit":0}`, aGo, false), readUse("c2", `{"path":"a.go"}`))
	assert.Contains(t, out[0].Text, "unchanged since your read of a.go")
}

func TestExec_ReadDedupElidedEarlier(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go"}`, "[elided: read a.go 3 lines]", false), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
}

func TestExec_ReadDedupAfterNote(t *testing.T) {
	r := newDedupRig(t)
	h := readTurn("c1", `{"path":"a.go"}`, aGo, false)
	h = append(h, readTurn("c2", `{"path":"a.go"}`, "unchanged since your read of a.go 1 turn ago (same range)", false)...)
	out := r.run(t, h, readUse("c3", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
}

func TestExec_ReadDedupErrorNotDeduped(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":"a.go"}`, "read a.go: no such file", true), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
	// A current error is never a note, even when it repeats an earlier one.
	h := readTurn("c3", `{"path":"gone.go"}`, "", true)
	first := r.run(t, nil, readUse("c3", `{"path":"gone.go"}`))
	h[1].Content[0].Text = first[0].Text
	again := r.run(t, h, readUse("c4", `{"path":"gone.go"}`))
	assert.Eq(t, again[0].Text, first[0].Text)
	assert.Eq(t, again[0].IsError, true)
}

func TestExec_ReadDedupIgnoresBadEarlierArgs(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, readTurn("c1", `{"path":`, aGo, false), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
}

func TestExec_ReadDedupParallelBatch(t *testing.T) {
	r := newDedupRig(t)
	out := r.run(t, nil, readUse("c1", `{"path":"a.go"}`), readUse("c2", `{"path":"a.go"}`))
	assert.Eq(t, out[0].Text, aGo)
	assert.Eq(t, out[1].Text, aGo)
}

func TestExec_RunHasNoDedup(t *testing.T) {
	r := newDedupRig(t)
	out, err := r.ex.Run(context.Background(), []providers.ContentBlock{readUse("c2", `{"path":"a.go"}`)})
	assert.NoErr(t, err)
	assert.Eq(t, out[0].Text, aGo)
}
