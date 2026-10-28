package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

const toolArgFragments = 3

// FakeOpenAI serves an OpenAI-compatible chat-completions endpoint that answers
// each POST with the next turn as SSE chunks (tool-call arguments split into
// three fragments). It returns the base URL and an accessor for the raw request
// bodies received so far.
func FakeOpenAI(t *testing.T, turns []fake.Turn) (string, func() [][]byte) {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies [][]byte
		next   int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		i := next
		next++
		mu.Unlock()
		if i >= len(turns) {
			http.Error(w, "no scripted turn", http.StatusInternalServerError)
			return
		}
		writeTurn(w, r, turns[i])
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() [][]byte {
		mu.Lock()
		defer mu.Unlock()
		return append([][]byte(nil), bodies...)
	}
}

func writeTurn(w http.ResponseWriter, r *http.Request, turn fake.Turn) {
	if turn.Err != nil {
		status := http.StatusInternalServerError
		if he, ok := turn.Err.(*providers.HTTPError); ok {
			status = he.Status
		}
		http.Error(w, turn.Err.Error(), status)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	chunk := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if fl != nil {
			fl.Flush()
		}
	}
	delta := func(d map[string]any, finish any) map[string]any {
		return map[string]any{"choices": []any{map[string]any{"index": 0, "delta": d, "finish_reason": finish}}}
	}
	if turn.Text != "" {
		chunk(delta(map[string]any{"role": "assistant", "content": turn.Text}, nil))
	}
	for n, c := range turn.Calls {
		for k, frag := range splitN(c.Args, toolArgFragments) {
			fn := map[string]any{"arguments": frag}
			tc := map[string]any{"index": n, "function": fn}
			if k == 0 {
				tc["id"] = fmt.Sprintf("call_%d", n)
				tc["type"] = "function"
				fn["name"] = c.Name
			}
			chunk(delta(map[string]any{"tool_calls": []any{tc}}, nil))
		}
	}
	reason := "stop"
	if len(turn.Calls) > 0 {
		reason = "tool_calls"
	}
	chunk(delta(map[string]any{}, reason))
	chunk(map[string]any{"choices": []any{}, "usage": map[string]any{
		"prompt_tokens": turn.Usage.PromptTokens, "completion_tokens": turn.Usage.CompletionTokens,
		"prompt_tokens_details": map[string]any{"cached_tokens": turn.Usage.CachedTokens},
	}})
	fmt.Fprint(w, "data: [DONE]\n\n")
	if fl != nil {
		fl.Flush()
	}
	if turn.Block {
		<-r.Context().Done()
	}
}

// splitN cuts s into n contiguous fragments on rune boundaries (some may be
// empty when s is short).
func splitN(s string, n int) []string {
	out := make([]string, n)
	size := (len(s) + n - 1) / n
	pos := 0
	for i := range out {
		hi := min(pos+size, len(s))
		for hi < len(s) && !utf8.RuneStart(s[hi]) {
			hi++
		}
		out[i] = s[pos:hi]
		pos = hi
	}
	return out
}
