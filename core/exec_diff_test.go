package core_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

// recLog records events as JSON round-tripped maps, like the real log.
type recLog struct {
	mu  sync.Mutex
	evs []recEvent
}

type recEvent struct {
	typ  string
	data map[string]any
}

func roundTrip(data any) map[string]any {
	raw, _ := json.Marshal(data)
	m := map[string]any{}
	_ = json.Unmarshal(raw, &m)
	return m
}

func (l *recLog) add(typ string, data any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.evs = append(l.evs, recEvent{typ, roundTrip(data)})
}
func (l *recLog) Append(typ string, data any) error { l.add(typ, data); return nil }
func (l *recLog) AppendID(typ string, data any) (string, error) {
	l.add(typ, data)
	return "", nil
}
func (l *recLog) AppendSync(typ string, data any) error { l.add(typ, data); return nil }
func (l *recLog) Sync() error                           { return nil }
func (l *recLog) Close() error                          { return nil }

// recLog doubles as a UI: Event records like Append.
func (l *recLog) Text(string)                {}
func (l *recLog) Reasoning(string)           {}
func (l *recLog) Event(typ string, data any) { l.add(typ, data) }

func (l *recLog) result(t *testing.T) map[string]any {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range l.evs {
		if e.typ == core.EvToolResult {
			return e.data
		}
	}
	t.Fatal("no tool_result event")
	return nil
}

// diffRows returns "k t" for every line of an event's diff.
func diffRows(t *testing.T, ev map[string]any) []string {
	t.Helper()
	d, ok := ev["diff"].(map[string]any)
	if !ok {
		t.Fatalf("no diff in %v", ev)
	}
	var rows []string
	hunks, _ := d["hunks"].([]any)
	for _, h := range hunks {
		for _, l := range h.([]any) {
			m := l.(map[string]any)
			rows = append(rows, m["k"].(string)+" "+m["t"].(string))
		}
	}
	return rows
}

func countKind(rows []string, k string) int {
	n := 0
	for _, r := range rows {
		if strings.HasPrefix(r, k+" ") {
			n++
		}
	}
	return n
}

type diffRig struct {
	root string
	log  *recLog
	ex   *core.Executor
}

func newDiffRig(t *testing.T, gate core.Gate) *diffRig {
	t.Helper()
	root := t.TempDir()
	log := &recLog{}
	ex := &core.Executor{
		Reg: core.NewRegistry(tools.Mutating()...), Policy: core.ExecPolicy{Root: root},
		Gate: gate, Log: log,
	}
	return &diffRig{root: root, log: log, ex: ex}
}

func (r *diffRig) file(t *testing.T, name, content string) {
	t.Helper()
	assert.NoErr(t, os.WriteFile(filepath.Join(r.root, name), []byte(content), 0o644))
}

func (r *diffRig) run(t *testing.T, tool string, args any) []providers.ContentBlock {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := r.ex.Run(context.Background(), []providers.ContentBlock{{
		Type: providers.BlockToolUse, ToolUseID: "c1", Name: tool, Input: raw,
	}})
	assert.NoErr(t, err)
	return out
}

func TestExec_EditResultEventHasDiff(t *testing.T) {
	r := newDiffRig(t, nil)
	r.file(t, "a.txt", "one\ntwo\nthree\n")
	out := r.run(t, "edit", map[string]string{"path": "a.txt", "old_string": "two", "new_string": "TWO"})
	rows := diffRows(t, r.log.result(t))
	assert.Eq(t, countKind(rows, "-"), 1)
	assert.Eq(t, countKind(rows, "+"), 1)
	assert.Eq(t, out[0].Text, "edited a.txt")
}

func TestExec_WriteOverwriteLogDropsRemovedScreenKeeps(t *testing.T) {
	r := newDiffRig(t, nil)
	screen := &recLog{}
	r.ex.Log = core.NewUILog(r.log, screen)
	r.file(t, "a.txt", "old secret line\n")
	r.run(t, "write", map[string]string{"path": "a.txt", "content": "new line\n"})

	logged := diffRows(t, r.log.result(t))
	assert.Eq(t, countKind(logged, "-"), 0)
	assert.Eq(t, countKind(logged, "+"), 1)
	raw, _ := json.Marshal(r.log.result(t))
	assert.NotContains(t, string(raw), "old secret line")

	shown := diffRows(t, screen.result(t))
	assert.Eq(t, countKind(shown, "-"), 1)
	assert.Eq(t, countKind(shown, "+"), 1)
}

func TestExec_NoDiffOnError(t *testing.T) {
	r := newDiffRig(t, nil)
	r.file(t, "a.txt", "one\n")
	r.run(t, "edit", map[string]string{"path": "a.txt", "old_string": "missing", "new_string": "x"})
	ev := r.log.result(t)
	assert.Eq(t, ev["status"].(string), "error")
	_, has := ev["diff"]
	assert.Eq(t, has, false)
}

// previewGate grants every call after recording preview() and the file
// content at that moment.
type previewGate struct {
	path    string
	diff    *core.Diff
	content string
}

func (g *previewGate) Allow(_ context.Context, _ core.Tool, _ json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if preview != nil {
		g.diff = preview()
	}
	b, _ := os.ReadFile(g.path)
	g.content = string(b)
	return core.Granted, nil
}

func TestExec_PreviewPassedToGate(t *testing.T) {
	g := &previewGate{}
	r := newDiffRig(t, g)
	g.path = filepath.Join(r.root, "a.txt")
	r.file(t, "a.txt", "one\ntwo\n")
	r.run(t, "edit", map[string]string{"path": "a.txt", "old_string": "two", "new_string": "TWO"})
	if g.diff == nil {
		t.Fatal("gate got no preview diff")
	}
	rows := core.FormatDiffPlain(g.diff)
	joined := strings.Join(rows, "\n")
	assert.Contains(t, joined, "- two")
	assert.Contains(t, joined, "+ TWO")
	assert.Eq(t, g.content, "one\ntwo\n")
}

func TestExec_DiffRedacted(t *testing.T) {
	r := newDiffRig(t, nil)
	screen := &recLog{}
	r.ex.Log = core.NewUILog(r.log, screen)
	r.ex.Redact = func(s string) string { return strings.ReplaceAll(s, "sk-secret", "[REDACTED]") }
	r.file(t, "a.txt", "token=sk-secret-old\n")
	r.run(t, "write", map[string]string{"path": "a.txt", "content": "token=sk-secret\n"})
	for _, ev := range []map[string]any{r.log.result(t), screen.result(t)} {
		rows := diffRows(t, ev)
		if len(rows) == 0 {
			t.Fatal("empty diff")
		}
		for _, row := range rows {
			assert.NotContains(t, row, "sk-secret")
		}
	}
}
