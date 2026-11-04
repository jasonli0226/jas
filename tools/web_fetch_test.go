package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestWebFetch_HTMLToText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>T</title>
<script>evil()</script><style>.x{}</style></head>
<body><h1>Hello</h1><p>World <b>bold</b></p><p>Second</p></body></html>`))
	}))
	defer srv.Close()

	tl := WebFetch{Client: srv.Client()}
	r := call(t, tl, core.ExecPolicy{}, `{"url":`+mustJSON(srv.URL)+`}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "Hello")
	assert.Contains(t, r.Output, "World bold")
	assert.Contains(t, r.Output, "Second")
	if strings.Contains(r.Output, "evil") || strings.Contains(r.Output, ".x{}") {
		t.Fatalf("script/style leaked: %q", r.Output)
	}
}

func TestWebFetch_RejectsBadURL(t *testing.T) {
	r := call(t, WebFetch{}, core.ExecPolicy{}, `{"url":"ftp://x"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "http(s)")
	r = call(t, WebFetch{}, core.ExecPolicy{}, `{"url":"/relative"}`)
	assert.Eq(t, r.IsError, true)
}

func TestWebFetch_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()
	r := call(t, WebFetch{Client: srv.Client()}, core.ExecPolicy{}, `{"url":`+mustJSON(srv.URL)+`}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "HTTP 404")
}

func TestWebFetch_CapsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(strings.Repeat("a", 200)))
	}))
	defer srv.Close()
	tl := WebFetch{Client: srv.Client(), MaxBytes: 50}
	r := call(t, tl, core.ExecPolicy{}, `{"url":`+mustJSON(srv.URL)+`}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "truncated")
}

func TestWebFetch_PlainText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("just text"))
	}))
	defer srv.Close()
	r := call(t, WebFetch{Client: srv.Client()}, core.ExecPolicy{}, `{"url":`+mustJSON(srv.URL)+`}`)
	assert.Eq(t, r.IsError, false)
	assert.Eq(t, r.Output, "just text")
}

func TestWebFetch_KindAndSchema(t *testing.T) {
	tl := WebFetch{}
	assert.Eq(t, tl.Kind(), core.ReadOnly)
	assert.Eq(t, tl.Name(), "web_fetch")
	var s map[string]any
	assert.NoErr(t, json.Unmarshal(tl.Schema(), &s))
}

func TestWebFetch_Cancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hi"))
	}))
	defer srv.Close()
	_, err := WebFetch{Client: srv.Client()}.Call(ctx, core.ExecPolicy{}, json.RawMessage(`{"url":`+mustJSON(srv.URL)+`}`))
	if err == nil {
		t.Fatal("want cancel error")
	}
	assert.Contains(t, err.Error(), "cancelled")
}

func mustJSON(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
