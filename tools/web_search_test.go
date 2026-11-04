package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func TestWebSearch_SearXNG(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Eq(t, r.URL.Path, "/search")
		assert.Eq(t, r.URL.Query().Get("format"), "json")
		assert.Eq(t, r.URL.Query().Get("q"), "golang")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"Go","url":"https://go.dev","content":"The Go programming language"},
			{"title":"Specs","url":"https://go.dev/ref/spec","content":"Language spec"}
		]}`))
	}))
	defer srv.Close()

	tl := WebSearch{BaseURL: srv.URL, Client: srv.Client()}
	r := call(t, tl, core.ExecPolicy{}, `{"query":"golang","limit":5}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "provider: searxng")
	assert.Contains(t, r.Output, "https://go.dev")
	assert.Contains(t, r.Output, "The Go programming language")
}

func TestWebSearch_DuckDuckGoLiteHTML(t *testing.T) {
	htmlBody := `<!DOCTYPE html><html><body>
<table>
<tr><td><a rel="nofollow" href="https://example.com/a">Alpha Result</a></td></tr>
<tr><td><a rel="nofollow" href="//duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fb">Beta Result</a></td></tr>
<tr><td><a href="/lite/">Home</a></td></tr>
</table></body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(htmlBody))
	}))
	defer srv.Close()

	tl := WebSearch{Searchers: []Searcher{liteStub{url: srv.URL, client: srv.Client()}}}
	r := call(t, tl, core.ExecPolicy{}, `{"query":"alpha"}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "provider: duckduckgo")
	assert.Contains(t, r.Output, "https://example.com/a")
	assert.Contains(t, r.Output, "Alpha Result")
	assert.Contains(t, r.Output, "https://example.com/b")
}

// liteStub GETs a fixture URL and parses it with the real DDG lite parser.
type liteStub struct {
	url    string
	client *http.Client
}

func (liteStub) Name() string { return "duckduckgo" }
func (s liteStub) Search(ctx context.Context, _ string, limit int) ([]SearchResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseDuckDuckGoLite(body, limit)
}

func TestWebSearch_FallsBack(t *testing.T) {
	fail := stubSearcher{name: "a", err: fmt.Errorf("down")}
	ok := stubSearcher{name: "b", res: []SearchResult{{Title: "T", URL: "https://x.test", Snippet: "s"}}}
	tl := WebSearch{Searchers: []Searcher{fail, ok}}
	r := call(t, tl, core.ExecPolicy{}, `{"query":"q"}`)
	assert.Eq(t, r.IsError, false)
	assert.Contains(t, r.Output, "provider: b")
	assert.Contains(t, r.Output, "https://x.test")
}

func TestWebSearch_AllFail(t *testing.T) {
	tl := WebSearch{Searchers: []Searcher{
		stubSearcher{name: "a", err: fmt.Errorf("no")},
		stubSearcher{name: "b", err: fmt.Errorf("nope")},
	}}
	r := call(t, tl, core.ExecPolicy{}, `{"query":"q"}`)
	assert.Eq(t, r.IsError, true)
	assert.Contains(t, r.Output, "all providers failed")
}

func TestWebSearch_EmptyQuery(t *testing.T) {
	r := call(t, WebSearch{}, core.ExecPolicy{}, `{"query":"  "}`)
	assert.Eq(t, r.IsError, true)
}

func TestWebSearch_Kind(t *testing.T) {
	tl := WebSearch{}
	assert.Eq(t, tl.Kind(), core.ReadOnly)
	assert.Eq(t, tl.Name(), "web_search")
	var s map[string]any
	assert.NoErr(t, json.Unmarshal(tl.Schema(), &s))
}

func TestWeb_AndSessionTools(t *testing.T) {
	names := func(tls []core.Tool) string {
		var b strings.Builder
		for i, tl := range tls {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(tl.Name())
		}
		return b.String()
	}
	assert.Eq(t, names(SessionTools(false, "")), "read,ls,grep,write,edit,shell")
	assert.Contains(t, names(SessionTools(true, "http://searx.example")), "web_fetch")
	assert.Contains(t, names(SessionTools(true, "")), "web_search")
	assert.Eq(t, len(Web("")), 2)
}

func TestParseDuckDuckGoLite(t *testing.T) {
	body := []byte(`<a href="https://a.test">A</a><a href="/rel">R</a><a href="https://b.test">B</a>`)
	res, err := parseDuckDuckGoLite(body, 10)
	assert.NoErr(t, err)
	assert.Eq(t, len(res), 2)
	assert.Eq(t, res[0].URL, "https://a.test")
	assert.Eq(t, res[1].URL, "https://b.test")
}

func TestDefaultChain_Order(t *testing.T) {
	chain := (WebSearch{BaseURL: "http://searx.test"}).defaultChain()
	assert.Eq(t, len(chain), 2)
	assert.Eq(t, chain[0].Name(), "searxng")
	assert.Eq(t, chain[1].Name(), "duckduckgo")
	chain = (WebSearch{}).defaultChain()
	assert.Eq(t, len(chain), 1)
	assert.Eq(t, chain[0].Name(), "duckduckgo")
}

type stubSearcher struct {
	name string
	res  []SearchResult
	err  error
}

func (s stubSearcher) Name() string { return s.name }
func (s stubSearcher) Search(context.Context, string, int) ([]SearchResult, error) {
	return s.res, s.err
}
