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
	"runtime"
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
	return &OpenAI{BaseURL: url, APIKey: testKey, Sleep: func(context.Context, time.Duration) error { return nil }}
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

func TestOpenAI_ReasoningEffort(t *testing.T) {
	// OpenRouter takes a reasoning object; absent when unset.
	r := encodeRequest(Request{Model: "m", ReasoningEffort: "low"}, true)
	if r.Reasoning == nil || r.Reasoning.Effort != "low" {
		t.Fatalf("reasoning = %+v, want effort low", r.Reasoning)
	}
	r = encodeRequest(Request{Model: "m"}, true)
	if r.Reasoning != nil {
		t.Fatalf("reasoning = %+v, want omitted", r.Reasoning)
	}
	// off disables thinking: "disable" on OpenRouter, "none" elsewhere.
	r = encodeRequest(Request{Model: "m", ReasoningEffort: "off"}, true)
	if r.Reasoning == nil || r.Reasoning.Effort != "disable" {
		t.Fatalf("reasoning = %+v, want effort disable", r.Reasoning)
	}
	r = encodeRequest(Request{Model: "m", ReasoningEffort: "off"}, false)
	if r.Reasoning != nil || r.ReasoningEffort != "none" {
		t.Fatalf("reasoning=%v reasoning_effort=%q, want none and no object", r.Reasoning, r.ReasoningEffort)
	}
	// Other endpoints take the standard reasoning_effort field.
	r = encodeRequest(Request{Model: "m", ReasoningEffort: "high"}, false)
	if r.Reasoning != nil || r.ReasoningEffort != "high" {
		t.Fatalf("reasoning=%v reasoning_effort=%q, want high field only", r.Reasoning, r.ReasoningEffort)
	}
	r = encodeRequest(Request{Model: "m"}, false)
	if r.Reasoning != nil || r.ReasoningEffort != "" {
		t.Fatalf("unset effort should omit both fields, got %+v %q", r.Reasoning, r.ReasoningEffort)
	}
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

var openAIFixtures = []string{"split_args.sse", "whole_call_one_chunk.sse", "two_parallel_calls.sse", "text_only.sse", "openrouter_glm.sse",
	"ollama.sse", "openrouter_deepseek.sse"}

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

func TestOpenAI_RecordedGLMFixture(t *testing.T) {
	srv := httptest.NewServer(sseHandler(readFixture(t, "openrouter_glm.sse")))
	defer srv.Close()
	evs, err := streamAll(t, newOpenAI(srv.URL))
	assert.NoErr(t, err)
	_, calls, err := accumulate(evs)
	assert.NoErr(t, err)
	if len(calls) < 1 {
		t.Fatal("expected at least one tool call")
	}
	for _, c := range calls {
		if c.Name == "" {
			t.Fatal("tool call without a name")
		}
		if !json.Valid(c.Input) {
			t.Fatalf("invalid JSON args: %q", c.Input)
		}
	}
	var usage *Usage
	for _, e := range evs {
		if e.Usage != nil {
			usage = e.Usage
		}
	}
	if usage == nil || usage.PromptTokens == 0 {
		t.Fatalf("usage not parsed: %+v", usage)
	}
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

func (s *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	s.mu.Lock()
	s.d = append(s.d, d)
	s.mu.Unlock()
	return nil
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

	// A provider echoing the key in its error body must be redacted.
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		fmt.Fprintf(w, `{"error":"bad key %s"}`, testKey)
	}))
	defer echo.Close()
	_, err = streamAll(t, newOpenAI(echo.URL))
	if !errors.As(err, &he) {
		t.Fatalf("want *HTTPError, got %v", err)
	}
	assert.NotContains(t, he.Body, testKey)
	assert.NotContains(t, err.Error(), testKey)
	assert.Contains(t, he.Body, "[REDACTED]")
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

func TestOpenAI_RetryAfterCapped(t *testing.T) {
	var n int
	body := readFixture(t, "text_only.sse")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("Retry-After", "3600")
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
	assert.Eq(t, rec.d[0], 60*time.Second)
}

func TestOpenAI_CancelDuringBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	o := &OpenAI{BaseURL: srv.URL, APIKey: testKey} // default Sleep
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	out, errc := o.Stream(ctx, Request{Model: "m"})
	_, err := drainEvents(out, errc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("cancellation did not interrupt backoff promptly")
	}
}

func TestOpenAI_ParseErrorReleasesReadSSE(t *testing.T) {
	// After a parse error, ReadSSE's goroutines must exit rather than block
	// on an unread channel.
	body := []byte("data: {not json\n\ndata: {\"choices\":[]}\n\ndata: {\"choices\":[]}\n\n")
	srv := httptest.NewServer(sseHandler(body))
	defer srv.Close()
	before := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		o := newOpenAI(srv.URL)
		// No keep-alive so idle HTTP connection goroutines don't muddy the count.
		o.HTTP = &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		_, err := streamAll(t, o)
		if err == nil || !strings.Contains(err.Error(), "parse chunk") {
			t.Fatalf("want parse error, got %v", err)
		}
	}
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if g := runtime.NumGoroutine(); g > before+2 {
		t.Fatalf("goroutines leaked: before=%d after=%d", before, g)
	}
}

func TestParseChunk_Reasoning(t *testing.T) {
	for _, tc := range []struct{ payload, want string }{
		{`{"choices":[{"delta":{"reasoning":"a"}}]}`, "a"},
		{`{"choices":[{"delta":{"reasoning_content":"b"}}]}`, "b"},
		{`{"choices":[{"delta":{"reasoning":"a","reasoning_content":"b"}}]}`, "a"},
	} {
		evs, err := parseChunk([]byte(tc.payload))
		assert.NoErr(t, err)
		assert.Eq(t, len(evs), 1)
		assert.Eq(t, evs[0], StreamEvent{Reasoning: tc.want})
	}
}

func TestParseChunk_ReasoningWithContentAndEmptyDeltas(t *testing.T) {
	evs, err := parseChunk([]byte(`{"choices":[{"delta":{"reasoning":"think","content":"say"}}]}`))
	assert.NoErr(t, err)
	assert.Eq(t, len(evs), 2) // reasoning first, then the text, as the stream orders them
	assert.Eq(t, evs[0], StreamEvent{Reasoning: "think"})
	assert.Eq(t, evs[1], StreamEvent{Text: "say"})
	for _, p := range []string{`{"choices":[{"delta":{}}]}`, `{"choices":[{"delta":{"reasoning":"","content":""}}]}`} {
		evs, err := parseChunk([]byte(p))
		assert.NoErr(t, err)
		assert.Eq(t, len(evs), 0)
	}
}

// TestOpenAI_RecordedProviderFixtures pins what the adapter reads from real
// recorded streams: ollama sends the whole call in one chunk and usage in an
// empty-choices chunk; OpenRouter (DeepSeek upstream) interleaves
// ": OPENROUTER PROCESSING" comments and splits the arguments.
func TestOpenAI_RecordedProviderFixtures(t *testing.T) {
	tests := []struct {
		fixture, id, input string
		prompt             int
	}{
		{"ollama.sse", "call_l886bbfm", `{"file":"go.mod"}`, 162},
		{"openrouter_deepseek.sse", "call_xOaaLTCbW7nFBFmHBxe4tukd", `{"path":"go.mod"}`, 302},
	}
	for _, tc := range tests {
		t.Run(tc.fixture, func(t *testing.T) {
			srv := httptest.NewServer(sseHandler(readFixture(t, tc.fixture)))
			defer srv.Close()
			evs, err := streamAll(t, newOpenAI(srv.URL))
			assert.NoErr(t, err)
			_, calls, err := accumulate(evs)
			assert.NoErr(t, err)
			assert.Eq(t, len(calls), 1)
			assert.Eq(t, calls[0].Name, "read")
			assert.Eq(t, calls[0].ToolUseID, tc.id)
			assert.JSONEq(t, string(calls[0].Input), tc.input)
			prompt := 0
			for _, ev := range evs {
				if ev.Usage != nil {
					prompt = ev.Usage.PromptTokens
				}
			}
			assert.Eq(t, prompt, tc.prompt)
		})
	}
}

// captureBody serves text_only.sse and returns the decoded request body.
func captureBody(t *testing.T, req Request) map[string]any {
	t.Helper()
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		assert.NoErr(t, json.Unmarshal(b, &got))
		sseHandler(readFixture(t, "text_only.sse"))(w, r)
	}))
	defer srv.Close()
	out, errc := newOpenAI(srv.URL).Stream(context.Background(), req)
	_, err := drainEvents(out, errc)
	assert.NoErr(t, err)
	return got
}

func TestOpenAI_RequestSendsMaxTokens(t *testing.T) {
	body := captureBody(t, Request{Model: "m", MaxTokens: 16000, Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}}}})
	assert.Eq(t, body["max_tokens"], any(16000.0))
}

func TestOpenAI_RequestOmitsZeroMaxTokens(t *testing.T) {
	body := captureBody(t, Request{Model: "m", Messages: []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}}}})
	if _, ok := body["max_tokens"]; ok {
		t.Fatalf("max_tokens sent: %v", body["max_tokens"])
	}
}

func TestOpenAI_RequestSendsToolChoiceWithTools(t *testing.T) {
	hi := []Message{{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: "hi"}}}}
	tools := []ToolSpec{{Name: "ls", Description: "list", Schema: json.RawMessage(`{"type":"object"}`)}}
	body := captureBody(t, Request{Model: "m", Messages: hi, Tools: tools, ToolChoice: "none"})
	assert.Eq(t, body["tool_choice"], any("none"))
	// Without tools there is nothing to choose; some providers reject it.
	body = captureBody(t, Request{Model: "m", Messages: hi, ToolChoice: "none"})
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("tool_choice sent without tools: %v", body["tool_choice"])
	}
}
