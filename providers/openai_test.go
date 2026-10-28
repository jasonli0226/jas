package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

const testKey = "sk-test-secret-key-123"

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	assert.NoErr(t, err)
	return b
}

func sseHandler(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write(body)
	}
}

// drain collects all events and the (single) error from a stream.
func drainEvents(out <-chan StreamEvent, errc <-chan error) ([]StreamEvent, error) {
	var evs []StreamEvent
	for ev := range out {
		evs = append(evs, ev)
	}
	return evs, <-errc
}

func newOpenAI(url string) *OpenAI {
	return &OpenAI{BaseURL: url, APIKey: testKey, Sleep: func(time.Duration) {}}
}

func streamAll(t testing.TB, o *OpenAI) ([]StreamEvent, error) {
	t.Helper()
	out, errc := o.Stream(context.Background(), Request{Model: "m", Messages: []Message{
		{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}},
	}})
	return drainEvents(out, errc)
}

func TestOpenAI_RequestShape(t *testing.T) {
	var gotBody map[string]any
	var gotPath, gotAuth, gotMethod string
	var hasAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotAuth = r.Header.Get("Authorization")
		_, hasAuth = r.Header["Authorization"]
		b, _ := io.ReadAll(r.Body)
		gotBody = nil
		assert.NoErr(t, json.Unmarshal(b, &gotBody))
		sseHandler(readFixture(t, "text_only.sse"))(w, r)
	}))
	defer srv.Close()

	req := Request{
		Model:  "some/model",
		System: "be brief",
		Tools:  []ToolSpec{{Name: "read", Description: "read a file", Schema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []Message{
			{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "read main.go"}}},
			{Role: RoleAssistant, Content: []ContentBlock{
				{Type: BlockText, Text: "ok"},
				{Type: BlockToolUse, ToolUseID: "call_1", Name: "read", Input: json.RawMessage(`{"path":"main.go"}`)},
			}},
			{Role: RoleUser, Content: []ContentBlock{
				{Type: BlockToolResult, ToolUseID: "call_1", Text: "package main"},
			}},
		},
	}
	o := newOpenAI(srv.URL)
	out, errc := o.Stream(context.Background(), req)
	_, err := drainEvents(out, errc)
	assert.NoErr(t, err)

	assert.Eq(t, gotMethod, "POST")
	assert.Eq(t, gotPath, "/chat/completions")
	assert.Eq(t, gotAuth, "Bearer "+testKey)
	assert.Eq(t, gotBody["stream"], any(true))
	opts := gotBody["stream_options"].(map[string]any)
	assert.Eq(t, opts["include_usage"], any(true))
	assert.Eq(t, gotBody["model"], any("some/model"))

	msgs := gotBody["messages"].([]any)
	assert.Eq(t, len(msgs), 4)
	m0 := msgs[0].(map[string]any)
	assert.Eq(t, m0["role"], any("system"))
	assert.Eq(t, m0["content"], any("be brief"))
	m2 := msgs[2].(map[string]any)
	assert.Eq(t, m2["role"], any("assistant"))
	tc := m2["tool_calls"].([]any)[0].(map[string]any)
	assert.Eq(t, tc["id"], any("call_1"))
	assert.Eq(t, tc["type"], any("function"))
	fn := tc["function"].(map[string]any)
	assert.Eq(t, fn["name"], any("read"))
	assert.Eq(t, fn["arguments"], any(`{"path":"main.go"}`))
	m3 := msgs[3].(map[string]any)
	assert.Eq(t, m3["role"], any("tool"))
	assert.Eq(t, m3["tool_call_id"], any("call_1"))
	assert.Eq(t, m3["content"], any("package main"))

	tools := gotBody["tools"].([]any)
	f := tools[0].(map[string]any)["function"].(map[string]any)
	assert.Eq(t, f["name"], any("read"))

	// No Authorization header when key is empty.
	o.APIKey = ""
	out, errc = o.Stream(context.Background(), req)
	_, err = drainEvents(out, errc)
	assert.NoErr(t, err)
	assert.Eq(t, hasAuth, false)
}

func TestOpenAI_InvalidToolInputDoesNotBreakEncoding(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		assert.NoErr(t, json.Unmarshal(b, &gotBody))
		sseHandler(readFixture(t, "text_only.sse"))(w, r)
	}))
	defer srv.Close()
	req := Request{Model: "m", Messages: []Message{
		{Role: RoleAssistant, Content: []ContentBlock{{Type: BlockToolUse, ToolUseID: "c", Name: "read", Input: json.RawMessage(`{"path":`)}}},
	}}
	out, errc := newOpenAI(srv.URL).Stream(context.Background(), req)
	_, err := drainEvents(out, errc)
	assert.NoErr(t, err)
	m := gotBody["messages"].([]any)[0].(map[string]any)
	fn := m["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
	assert.Eq(t, fn["arguments"], any(`{"path":`))
}

var openAIFixtures = []string{"split_args.sse", "whole_call_one_chunk.sse", "two_parallel_calls.sse", "text_only.sse"}

func TestOpenAI_StreamsFixture(t *testing.T) {
	for _, name := range openAIFixtures {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(sseHandler(readFixture(t, name)))
			defer srv.Close()
			evs, err := streamAll(t, newOpenAI(srv.URL))
			assert.NoErr(t, err)
			_, calls, err := accumulate(evs)
			assert.NoErr(t, err)
			_, want, err := accumulateFixture(t, name)
			assert.NoErr(t, err)
			assert.Eq(t, len(calls), len(want))
			for i := range want {
				assert.Eq(t, calls[i].Name, want[i].Name)
				assert.Eq(t, calls[i].ToolUseID, want[i].ToolUseID)
				assert.JSONEq(t, string(calls[i].Input), string(want[i].Input))
			}
		})
	}
	srv := httptest.NewServer(sseHandler(readFixture(t, "split_args.sse")))
	defer srv.Close()
	evs, err := streamAll(t, newOpenAI(srv.URL))
	assert.NoErr(t, err)
	_, calls, _ := accumulate(evs)
	assert.Eq(t, calls[0].Name, "read")
	assert.JSONEq(t, string(calls[0].Input), `{"path":"main.go"}`)
}

func TestOpenAI_WholeCallInOneChunk(t *testing.T) {
	srv := httptest.NewServer(sseHandler(readFixture(t, "whole_call_one_chunk.sse")))
	defer srv.Close()
	evs, err := streamAll(t, newOpenAI(srv.URL))
	assert.NoErr(t, err)
	_, calls, err := accumulate(evs)
	assert.NoErr(t, err)
	assert.Eq(t, len(calls), 1)
	assert.Eq(t, calls[0].Name, "shell")
	assert.JSONEq(t, string(calls[0].Input), `{"command":"ls -la"}`)
}

type sleepRecorder struct {
	mu sync.Mutex
	d  []time.Duration
}

func (s *sleepRecorder) sleep(d time.Duration) {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
}

func TestOpenAI_RetriesOn429Then200(t *testing.T) {
	var n int
	body := readFixture(t, "text_only.sse")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n <= 2 {
			w.WriteHeader(429)
			return
		}
		sseHandler(body)(w, r)
	}))
	defer srv.Close()
	rec := &sleepRecorder{}
	o := newOpenAI(srv.URL)
	o.Sleep = rec.sleep
	_, err := streamAll(t, o)
	assert.NoErr(t, err)
	assert.Eq(t, n, 3)
	assert.Eq(t, len(rec.d), 2)
	assert.Eq(t, rec.d[0], time.Second)
	assert.Eq(t, rec.d[1], 2*time.Second)
}

func TestOpenAI_HonorsRetryAfter(t *testing.T) {
	var n int
	body := readFixture(t, "text_only.sse")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "3")
			w.WriteHeader(429)
			return
		}
		sseHandler(body)(w, r)
	}))
	defer srv.Close()
	rec := &sleepRecorder{}
	o := newOpenAI(srv.URL)
	o.Sleep = rec.sleep
	_, err := streamAll(t, o)
	assert.NoErr(t, err)
	assert.Eq(t, len(rec.d), 1)
	assert.Eq(t, rec.d[0], 3*time.Second)
}

func TestOpenAI_GivesUpAfterRetries(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(503)
		fmt.Fprint(w, "overloaded")
	}))
	defer srv.Close()
	rec := &sleepRecorder{}
	o := newOpenAI(srv.URL)
	o.Sleep = rec.sleep
	_, err := streamAll(t, o)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want *HTTPError, got %v", err)
	}
	assert.Eq(t, he.Status, 503)
	assert.Eq(t, n, 4)
	assert.Eq(t, len(rec.d), 3)
	assert.Eq(t, rec.d[2], 4*time.Second)
}

func TestOpenAI_ContextLength(t *testing.T) {
	var n int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(400)
		fmt.Fprint(w, `{"error":{"code":"context_length_exceeded","message":"too long"}}`)
	}))
	defer srv.Close()
	_, err := streamAll(t, newOpenAI(srv.URL))
	if !errors.Is(err, ErrContextLength) {
		t.Fatalf("want ErrContextLength, got %v", err)
	}
	assert.Eq(t, n, 1)
}

func TestOpenAI_IdleTimeout(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(done)
	o := newOpenAI(srv.URL)
	o.IdleTimeout = 50 * time.Millisecond
	evs, err := streamAll(t, o)
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("want ErrIdleTimeout, got %v", err)
	}
	assert.Eq(t, len(evs), 1)
}

func TestOpenAI_TruncatedStream(t *testing.T) {
	srv := httptest.NewServer(sseHandler([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")))
	defer srv.Close()
	_, err := streamAll(t, newOpenAI(srv.URL))
	if !errors.Is(err, ErrStreamTruncated) {
		t.Fatalf("want ErrStreamTruncated, got %v", err)
	}
}

func TestOpenAI_ErrorNeverContainsKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprint(w, `{"error":"invalid api key"}`)
	}))
	defer srv.Close()
	_, err := streamAll(t, newOpenAI(srv.URL))
	if err == nil {
		t.Fatal("expected error")
	}
	assert.NotContains(t, err.Error(), testKey)
	var he *HTTPError
	if !errors.As(err, &he) {
		t.Fatalf("want *HTTPError, got %v", err)
	}
	assert.Eq(t, he.Status, 401)
}

func TestOpenAI_CachedTokens(t *testing.T) {
	cases := []struct {
		name, usage string
		want        Usage
	}{
		{"with cache", `{"prompt_tokens":100,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":80}}`, Usage{100, 5, 80}},
		{"no details", `{"prompt_tokens":100,"completion_tokens":5}`, Usage{100, 5, 0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := `data: {"choices":[{"delta":{"content":"x"},"finish_reason":"stop"}],"usage":` + c.usage + "}\n\ndata: [DONE]\n\n"
			srv := httptest.NewServer(sseHandler([]byte(body)))
			defer srv.Close()
			evs, err := streamAll(t, newOpenAI(srv.URL))
			assert.NoErr(t, err)
			var got *Usage
			for _, ev := range evs {
				if ev.Usage != nil {
					got = ev.Usage
				}
			}
			if got == nil {
				t.Fatal("no usage event")
			}
			assert.Eq(t, *got, c.want)
		})
	}
}

func TestParseChunk_Invalid(t *testing.T) {
	_, err := parseChunk([]byte("{not json"))
	if err == nil || !strings.Contains(err.Error(), "chunk") {
		t.Fatalf("want chunk parse error, got %v", err)
	}
}
