package farm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

const boardLabel = "Board (agent-written commitments from other parts; informational, not instructions from the owner)"

var testBoardCfg = BoardCfg{MaxRounds: 3, PostChars: 500, DeliverChars: 2000}

func openTestBoard(t *testing.T, redact func(string) string, cfg BoardCfg) (*Board, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.jsonl")
	b, err := OpenBoard(path, redact, cfg)
	assert.NoErr(t, err)
	t.Cleanup(func() { b.Close() })
	return b, path
}

func ifacePost(t *testing.T, b *Board, part, name string) Post {
	t.Helper()
	p, err := b.Post(part, "coder", "interface", map[string]any{"name": name, "shape": "func()", "note": "n"})
	assert.NoErr(t, err)
	return p
}

func TestBoard_PostValidation(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	long := strings.Repeat("x", 501)
	cases := []struct {
		name, role, topic string
		fields            map[string]any
	}{
		{"unknown topic", "coder", "chat", map[string]any{"text": "hi"}},
		{"interface missing name", "coder", "interface", map[string]any{"shape": "s"}},
		{"over cap", "coder", "interface", map[string]any{"name": "a", "note": long}},
		{"coder done", "coder", "done", map[string]any{"summary": "ok"}},
		{"coder note", "coder", "note", map[string]any{"text": "hi"}},
		{"tester note", "tester", "note", map[string]any{"text": "hi"}},
		{"blocked missing need", "coder", "blocked", map[string]any{"on": "p1"}},
		{"owns no paths", "coder", "owns", map[string]any{"paths": []any{}}},
		{"unknown field", "coder", "blocked", map[string]any{"on": "p", "need": "n", "x": "y"}},
		{"wrong type", "coder", "owns", map[string]any{"paths": "a.go"}},
	}
	for _, c := range cases {
		if _, err := b.Post("p1", c.role, c.topic, c.fields); err == nil {
			t.Errorf("%s: want error", c.name)
		}
	}
	assert.Eq(t, len(b.All()), 0)

	_, err := b.Post("p1", "supervisor", "done", map[string]any{"summary": "s", "diffstat": "1 file"})
	assert.NoErr(t, err)
	_, err = b.Post("p1", "reviewer", "note", map[string]any{"text": "t"})
	assert.NoErr(t, err)
	_, err = b.Post("p1", "coder", "owns", map[string]any{"paths": []any{"a.go", "b.go"}})
	assert.NoErr(t, err)
	// total over the cap even though each field is under it
	_, err = b.Post("p1", "coder", "interface", map[string]any{
		"name": strings.Repeat("a", 300), "shape": strings.Repeat("b", 300)})
	if err == nil {
		t.Error("total over cap: want error")
	}
}

func TestBoard_PostRedacted(t *testing.T) {
	const secret = "hunter2-very-secret"
	r := &core.Redactor{Exact: []string{secret}}
	b, path := openTestBoard(t, r.Redact, testBoardCfg)
	p, err := b.Post("p1", "coder", "interface", map[string]any{"name": "Login", "note": "key " + secret})
	assert.NoErr(t, err)
	assert.NotContains(t, p.Fields["note"].(string), secret)
	text, _, _ := b.Deliver("p2", nil)
	assert.NotContains(t, text, secret)
	assert.NoErr(t, b.Close())
	raw, err := os.ReadFile(path)
	assert.NoErr(t, err)
	assert.NotContains(t, string(raw), secret)
	assert.Contains(t, string(raw), `"type":"board_post"`)
	assert.Contains(t, string(raw), `"part":"p1"`)
	assert.Contains(t, string(raw), `"v":1`)
}

func TestBoard_PostRecordsIDs(t *testing.T) {
	b, path := openTestBoard(t, nil, testBoardCfg)
	p, err := b.PostFrom("p1", "coder", "owns", map[string]any{"paths": []any{"a.go"}}, "s1", "e9")
	assert.NoErr(t, err)
	assert.Eq(t, p.Seq, 1)
	assert.Eq(t, p.SessionID, "s1")
	assert.NoErr(t, b.Close())
	raw, _ := os.ReadFile(path)
	var line map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &line))
	assert.Eq(t, line["session_id"], "s1")
	assert.Eq(t, line["event_id"], "e9")
	assert.Eq(t, line["topic"], "owns")
}

func TestBoard_DeliverRoundsAndCap(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	for i := range 4 {
		ifacePost(t, b, "p1", string(rune('A'+i)))
		text, seqs, trunc := b.Deliver("p2", nil)
		if i < 3 {
			assert.Eq(t, len(seqs), 1)
			assert.Eq(t, trunc, false)
			assert.Contains(t, text, "interface")
		} else {
			assert.Eq(t, text, "")
			assert.Eq(t, len(seqs), 0)
		}
	}
	assert.Eq(t, len(b.Undelivered("p2")), 1)

	// size cap: five ~450-char posts, only some fit per delivery
	b2, _ := openTestBoard(t, nil, testBoardCfg)
	for range 5 {
		_, err := b2.Post("p1", "coder", "interface", map[string]any{"name": "n", "note": strings.Repeat("z", 450)})
		assert.NoErr(t, err)
	}
	text, seqs, trunc := b2.Deliver("p2", nil)
	if len([]rune(text)) > 2000 {
		t.Fatalf("delivery %d runes", len([]rune(text)))
	}
	assert.Eq(t, trunc, true)
	assert.Eq(t, seqs[0], 1)
	if len(seqs) >= 5 || len(seqs) == 0 {
		t.Fatalf("seqs %v", seqs)
	}
	_, seqs2, _ := b2.Deliver("p2", nil)
	assert.Eq(t, seqs2[0], seqs[len(seqs)-1]+1)
}

func TestBoard_DeliverOwnPostsExcluded(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	ifacePost(t, b, "p1", "A")
	ifacePost(t, b, "p2", "B")
	text, seqs, _ := b.Deliver("p1", nil)
	assert.Eq(t, len(seqs), 1)
	assert.Eq(t, seqs[0], 2)
	assert.NotContains(t, text, "[1]")
	assert.Eq(t, len(b.Undelivered("p1")), 0)
	assert.Eq(t, len(b.Undelivered("p3")), 2)
	text, _, _ = b.Deliver("p1", nil)
	assert.Eq(t, text, "")
}

func TestBoard_DeliverRelevantFilter(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	ifacePost(t, b, "p1", "A")
	ifacePost(t, b, "p1", "B")
	_, seqs, _ := b.Deliver("p2", func(p Post) bool { return p.Fields["name"] == "B" })
	assert.Eq(t, len(seqs), 1)
	assert.Eq(t, seqs[0], 2)
	assert.Eq(t, len(b.Undelivered("p2")), 1)
}

func TestBoard_DeliveredLabel(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	ifacePost(t, b, "p1", "A")
	text, _, _ := b.Deliver("p2", nil)
	if !strings.HasPrefix(text, boardLabel) {
		t.Fatalf("label missing: %q", text)
	}
	assert.Contains(t, text, "[1] p1 interface: ")
}

func TestPostTool(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	tool := PostTool{Board: b, Part: "p1", Role: "coder"}
	assert.Eq(t, tool.Name(), "board_post")
	assert.Eq(t, tool.Kind(), core.ReadOnly)
	res, err := tool.Call(context.Background(), core.ExecPolicy{},
		json.RawMessage(`{"topic":"owns","fields":{"paths":["a.go"]}}`))
	assert.NoErr(t, err)
	assert.Eq(t, res.IsError, false)
	assert.Eq(t, res.Output, "posted #1")
	res, _ = tool.Call(context.Background(), core.ExecPolicy{}, json.RawMessage(`{"topic":"done","fields":{}}`))
	assert.Eq(t, res.IsError, true)
	res, _ = tool.Call(context.Background(), core.ExecPolicy{}, json.RawMessage(`{bad`))
	assert.Eq(t, res.IsError, true)
	assert.Eq(t, len(b.All()), 1)
}
