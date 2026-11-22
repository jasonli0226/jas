package core_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

var readA = fake.Turn{Calls: []fake.Call{{Name: "read", Args: `{"path":"a.go"}`}}}

// lastResultText is the text of the last tool_result in req.
func lastResultText(req providers.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		c := req.Messages[i].Content
		for j := len(c) - 1; j >= 0; j-- {
			if c[j].Type == providers.BlockToolResult {
				return c[j].Text
			}
		}
	}
	return ""
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0o644))
}

func TestSession_ReadDedupSecondRead(t *testing.T) {
	r := newRig(t, []fake.Turn{readA, readA, {Text: "done"}})
	writeFile(t, r.root, "a.go", aGo)
	s := newSession(t, r)
	assert.Eq(t, send(t, s, context.Background(), "go").Status, "ok")
	assert.Eq(t, lastResultText(r.prov.Requests[1]), aGo)
	assert.Eq(t, lastResultText(r.prov.Requests[2]), "unchanged since your read of a.go 1 turn ago (same range)")
}

func TestSession_ReadDedupAfterCompact(t *testing.T) {
	r := newRig(t, []fake.Turn{
		readA, {Text: "A1" + tokens(100)}, {Text: "A2" + tokens(100)}, {Text: "A3" + tokens(100)},
		{Text: summaryReply},
		readA, {Text: "done"},
	})
	writeFile(t, r.root, "a.go", aGo)
	s := newCompactSession(t, r, &core.CompactConfig{Reserve: 0, Keep: 150, Model: "small"})
	ctx := context.Background()
	for _, u := range []string{"u1", "u2", "u3"} {
		assert.Eq(t, send(t, s, ctx, u+tokens(100)).Status, "ok")
	}
	_, err := s.Compact(ctx, "")
	assert.NoErr(t, err)
	assert.Eq(t, send(t, s, ctx, "u4").Status, "ok")
	assert.Eq(t, lastResultText(r.prov.Requests[len(r.prov.Requests)-1]), aGo)
}

func TestSession_ReadDedupNoteThenElided(t *testing.T) {
	big := strings.Repeat("0123456789\n", 100)
	readB := fake.Turn{Calls: []fake.Call{{Name: "read", Args: `{"path":"b.go"}`}}}
	r := newRig(t, []fake.Turn{readA, readA, readB, readA, {Text: "done"}})
	writeFile(t, r.root, "a.go", big)
	writeFile(t, r.root, "b.go", big+"b")
	r.cfg.Budget = &core.Budget{Window: 1000, ElideAt: 0.3, Keep: 1} // trigger 300 tokens: after b.go
	s := newSession(t, r)
	assert.Eq(t, send(t, s, context.Background(), "go").Status, "ok")
	assert.Contains(t, lastResultText(r.prov.Requests[2]), "unchanged since your read of a.go")
	assert.Eq(t, lastResultText(r.prov.Requests[4]), big)
	if count(r.events(t), "elided") == 0 {
		t.Fatal("no elision: the test does not cover a note followed by elision")
	}
}

func TestSession_ReadDedupResume(t *testing.T) {
	r := newRig(t, []fake.Turn{readA, readA, {Text: "done"}})
	writeFile(t, r.root, "a.go", aGo)
	s := newSession(t, r)
	res := send(t, s, context.Background(), "go")
	assert.NoErr(t, s.Close(res.Status, res.Code))
	st, err := core.Replay(r.logPath)
	assert.NoErr(t, err)
	var notes int
	for _, m := range st.Messages {
		for _, b := range m.Content {
			if b.Type == providers.BlockToolResult && strings.HasPrefix(b.Text, "unchanged since your read of a.go") {
				notes++
			}
		}
	}
	assert.Eq(t, notes, 1)
}
