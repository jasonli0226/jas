package core

import (
	"bytes"
	"os"
	"path/filepath"
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
	assert.Eq(t, st.Mode().Perm(), os.FileMode(0o600))
	st, err = os.Stat(filepath.Dir(p))
	assert.NoErr(t, err)
	assert.Eq(t, st.Mode().Perm(), os.FileMode(0o700))
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
