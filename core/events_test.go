package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
)

func TestEventLog_SeqMonotonicUnderConcurrency(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s", "e.jsonl")
	l, err := OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	var wg sync.WaitGroup
	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				if err := l.Append(EvUsage, map[string]any{"n": i}); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	assert.NoErr(t, l.Close())
	evs := testutil.ReadEvents(t, p)
	assert.Eq(t, len(evs), 1000)
	for i, e := range evs {
		assert.Eq(t, e["seq"].(float64), float64(i+1))
	}
}

func TestEventLog_HeaderShape(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "e.jsonl")
	l, err := OpenEventLog(p, nil, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, l.Append(EvAssistantMessage, struct {
		Text  string `json:"text"`
		Final bool   `json:"final"`
	}{"hi", true}))
	assert.NoErr(t, l.Close())
	e := testutil.ReadEvents(t, p)[0]
	assert.Eq(t, e["v"].(float64), 1.0)
	if v, ok := e["parent_id"]; !ok || v != nil {
		t.Fatalf("parent_id = %v, %v", v, ok)
	}
	_, err = time.Parse(time.RFC3339Nano, e["ts"].(string))
	assert.NoErr(t, err)
	assert.Eq(t, e["type"].(string), "assistant_message")
	assert.Eq(t, e["text"].(string), "hi")
	assert.Eq(t, e["final"].(bool), true)
	if e["id"].(string) == "" {
		t.Fatal("empty id")
	}
	st, err := os.Stat(p)
	assert.NoErr(t, err)
	assert.Perm(t, st.Mode(), 0o600)
	st, err = os.Stat(filepath.Dir(p))
	assert.NoErr(t, err)
	assert.Perm(t, st.Mode(), 0o700)
}

func TestEventLog_AppendSyncFsyncs(t *testing.T) {
	var n int
	var mu sync.Mutex
	l, err := openEventLog(filepath.Join(t.TempDir(), "e.jsonl"), nil, nil, func(*os.File) error {
		mu.Lock()
		n++
		mu.Unlock()
		return nil
	})
	assert.NoErr(t, err)
	count := func() int { mu.Lock(); defer mu.Unlock(); return n }
	assert.NoErr(t, l.Append(EvUsage, nil))
	assert.Eq(t, count(), 0)
	assert.NoErr(t, l.AppendSync(EvToolStarted, nil))
	assert.Eq(t, count(), 1)
	assert.NoErr(t, l.Sync())
	assert.Eq(t, count(), 2)
	assert.NoErr(t, l.Close())
}

func TestEventLog_TeeGetsIdenticalBytes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	var tee bytes.Buffer
	l, err := OpenEventLog(p, &tee, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, l.Append(EvUsage, map[string]any{"a": 1}))
	assert.NoErr(t, l.AppendSync(EvUsage, map[string]any{"b": "x"}))
	assert.NoErr(t, l.Close())
	b, err := os.ReadFile(p)
	assert.NoErr(t, err)
	assert.Eq(t, tee.String(), string(b))
}

func TestEventLog_RedactsNestedStrings(t *testing.T) {
	p := filepath.Join(t.TempDir(), "e.jsonl")
	l, err := OpenEventLog(p, nil, func(s string) string {
		if s == "SECRET" {
			return "[x]"
		}
		return s
	})
	assert.NoErr(t, err)
	assert.NoErr(t, l.Append("tool_started", map[string]any{"args": map[string]any{"l": []string{"SECRET"}}, "k": "SECRET", "type": "ignored"}))
	assert.NoErr(t, l.Close())
	e := testutil.ReadEvents(t, p)[0]
	assert.Eq(t, e["k"].(string), "[x]")
	assert.Eq(t, e["args"].(map[string]any)["l"].([]any)[0].(string), "[x]")
	assert.Eq(t, e["type"].(string), "tool_started")
}

func TestEventLog_AppendAfterClose(t *testing.T) {
	l, err := OpenEventLog(filepath.Join(t.TempDir(), "e.jsonl"), nil, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, l.Close())
	if l.Append(EvUsage, nil) == nil {
		t.Fatal("want error after Close")
	}
	assert.NoErr(t, l.Close())
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func TestEventLog_WriteErrorReturned(t *testing.T) {
	l, err := OpenEventLog(filepath.Join(t.TempDir(), "e.jsonl"), failWriter{}, nil)
	assert.NoErr(t, err)
	if l.Append(EvUsage, nil) == nil {
		t.Fatal("want tee write error")
	}
	_ = l.Close()
}

func TestExitError_Message(t *testing.T) {
	assert.Eq(t, (&ExitError{3, "context full"}).Error(), "context full (exit 3)")
}

func TestNewSessionID(t *testing.T) {
	id := NewSessionID(time.Date(2006, 10, 29, 15, 4, 5, 0, time.UTC))
	assert.Eq(t, len(id), len("20061029T150405-abcdef"))
	assert.Eq(t, id[:16], "20061029T150405-")
}

func TestEventLog_ArgsRedacted(t *testing.T) {
	const secret = "sk-or-v1-abcdef0123456789"
	r := &Redactor{Exact: []string{secret}}
	p := filepath.Join(t.TempDir(), "e.jsonl")
	l, err := OpenEventLog(p, nil, r.Redact)
	assert.NoErr(t, err)
	args := json.RawMessage(`{"command":"curl -H 'x: ` + secret + `'"}`)
	assert.NoErr(t, l.Append(EvToolStarted, map[string]any{"tool": "shell", "args": args}))
	assert.NoErr(t, l.Append("assistant_message", map[string]any{"text": "key is " + secret}))
	assert.NoErr(t, l.Close())
	b, err := os.ReadFile(p)
	assert.NoErr(t, err)
	assert.NotContains(t, string(b), secret)
	assert.Contains(t, string(b), "[REDACTED:exact]")
}

// tornWriter writes half of the first line it gets, then fails; later writes
// go through.
type tornWriter struct {
	w    io.Writer
	torn bool
}

func (t *tornWriter) Write(p []byte) (int, error) {
	if t.torn {
		return t.w.Write(p)
	}
	t.torn = true
	n, _ := t.w.Write(p[:len(p)/2])
	return n, errors.New("disk full")
}

func TestEventLog_WriteAfterTornLineStartsNewLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	l, err := OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	fl := l.(*fileLog)
	fl.w = &tornWriter{w: fl.f}
	if l.Append(EvUsage, map[string]any{"n": 1}) == nil {
		t.Fatal("want write error")
	}
	assert.NoErr(t, l.Append(EvUsage, map[string]any{"n": 2}))
	assert.NoErr(t, l.Close())

	data, err := os.ReadFile(path)
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	assert.Eq(t, len(lines), 2)
	var ev map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(lines[1]), &ev))
	assert.Eq(t, ev["n"].(float64), 2.0)
}

func TestEventLog_AppendIDReturnsWrittenID(t *testing.T) {
	for name, redact := range map[string]func(string) string{"plain": nil, "redacting": strings.ToUpper} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "e.jsonl")
			l, err := OpenEventLog(p, nil, redact)
			assert.NoErr(t, err)
			id1, err := l.AppendID(EvUsage, map[string]any{"n": 1})
			assert.NoErr(t, err)
			id2, err := l.AppendID(EvUsage, map[string]any{"n": 2})
			assert.NoErr(t, err)
			assert.NoErr(t, l.Close())
			assert.Eq(t, id1, "e000001")
			assert.Eq(t, id2, "e000002")
			evs := testutil.ReadEvents(t, p)
			assert.Eq(t, evs[0]["id"].(string), id1)
			assert.Eq(t, evs[1]["id"].(string), id2)
		})
	}
}

func TestEventLog_ResumeAfterEventBiggerThanTailChunk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	l, err := OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	assert.NoErr(t, l.Append(EvUsage, map[string]any{"n": 1}))
	assert.NoErr(t, l.Append(EvUsage, map[string]any{"big": strings.Repeat("x", 3*resumeTailChunk)}))
	assert.NoErr(t, l.Close())
	appendTo(t, path, `{"v":1,"id":"e000099","seq":99,"type":"us`) // torn by a crash

	l, err = OpenEventLog(path, nil, nil)
	assert.NoErr(t, err)
	id, err := l.AppendID(EvUsage, map[string]any{"n": 3})
	assert.NoErr(t, err)
	assert.Eq(t, id, "e000003")
	assert.NoErr(t, l.Close())
	data, err := os.ReadFile(path)
	assert.NoErr(t, err)
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var ev map[string]any
	assert.NoErr(t, json.Unmarshal([]byte(lines[len(lines)-1]), &ev))
	assert.Eq(t, ev["parent_id"], "e000002")
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	assert.NoErr(t, err)
	_, err = f.WriteString(text)
	assert.NoErr(t, err)
	assert.NoErr(t, f.Close())
}

// A non-empty log with no complete event must not restart seq at 1: the new
// events would reuse ids and put a null parent_id mid-file.
func TestEventLog_OpenRefusesNonEmptyLogWithNoEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.jsonl")
	assert.NoErr(t, os.WriteFile(path, []byte("not json\n{\"v\":1,\"id\":\"e0000"), 0o600))
	l, err := OpenEventLog(path, nil, nil)
	if err == nil {
		l.Close()
		t.Fatal("OpenEventLog succeeded on a log with no complete event")
	}
	assert.Contains(t, err.Error(), "no complete event")
}
