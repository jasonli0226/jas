package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/jasonli0226/jas/core"
)

const (
	defaultSearchTimeout = 30 * time.Second
	defaultSearchMaxBytes = 1 << 20
	defaultSearchLimit   = 8
)

// SearchResult is one hit from a search provider.
type SearchResult struct {
	Title   string
	URL     string
	Snippet string
}

// Searcher finds results for a query. Providers are tried in chain order.
type Searcher interface {
	Name() string
	Search(ctx context.Context, query string, limit int) ([]SearchResult, error)
}

// WebSearch runs a web search via a pluggable provider chain (design §12.1).
// When BaseURL is set it tries SearXNG first; otherwise DuckDuckGo lite HTML.
type WebSearch struct {
	BaseURL string // SearXNG instance root, optional
	Client  *http.Client
	// Searchers overrides the default chain (tests).
	Searchers []Searcher
}

func (WebSearch) Name() string { return "web_search" }
func (WebSearch) Description() string {
	return "Search the public web and return a short list of title/URL/snippet results. Prefer this over shell curl for discovery; follow up with web_fetch for page bodies."
}
func (WebSearch) Kind() core.Kind { return core.ReadOnly }
func (WebSearch) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"query":{"type":"string","description":"Search query."},` +
		`"limit":{"type":"integer","description":"Max results to return (default 8, max 20)."}},` +
		`"required":["query"]}`)
}

func (t WebSearch) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	q := strings.TrimSpace(a.Query)
	if q == "" {
		return errResult("web_search: query must not be empty")
	}
	limit := a.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > 20 {
		limit = 20
	}
	chain := t.Searchers
	if len(chain) == 0 {
		chain = t.defaultChain()
	}
	var errs []string
	for _, s := range chain {
		res, err := s.Search(ctx, q, limit)
		if err != nil {
			if ok, cerr := cancelled(ctx, "web_search"); ok {
				return core.Result{}, cerr
			}
			errs = append(errs, fmt.Sprintf("%s: %v", s.Name(), err))
			continue
		}
		if len(res) == 0 {
			errs = append(errs, fmt.Sprintf("%s: no results", s.Name()))
			continue
		}
		return core.Result{Output: Truncate([]byte(formatSearchResults(s.Name(), res)), p.OutputLimit())}, nil
	}
	if len(errs) == 0 {
		return errResult("web_search: no providers configured")
	}
	return errResult("web_search: all providers failed: %s", strings.Join(errs, "; "))
}

func (t WebSearch) defaultChain() []Searcher {
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: defaultSearchTimeout}
	}
	var chain []Searcher
	if base := strings.TrimRight(strings.TrimSpace(t.BaseURL), "/"); base != "" {
		chain = append(chain, searxngSearcher{BaseURL: base, Client: client})
	}
	chain = append(chain, duckDuckGoSearcher{Client: client})
	return chain
}

func formatSearchResults(provider string, res []SearchResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "provider: %s\n", provider)
	for i, r := range res {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, strings.TrimSpace(r.Title), strings.TrimSpace(r.URL))
		if s := strings.TrimSpace(r.Snippet); s != "" {
			fmt.Fprintf(&b, "   %s\n", s)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// --- SearXNG ---

type searxngSearcher struct {
	BaseURL string
	Client  *http.Client
}

func (s searxngSearcher) Name() string { return "searxng" }

func (s searxngSearcher) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u, err := url.Parse(s.BaseURL + "/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jas-web-search/1")
	req.Header.Set("Accept", "application/json")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultSearchMaxBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var parsed struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}
	out := make([]SearchResult, 0, limit)
	for _, r := range parsed.Results {
		if r.URL == "" {
			continue
		}
		out = append(out, SearchResult{Title: r.Title, URL: r.URL, Snippet: r.Content})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// --- DuckDuckGo lite (keyless HTML) ---

type duckDuckGoSearcher struct {
	Client *http.Client
}

func (duckDuckGoSearcher) Name() string { return "duckduckgo" }

func (s duckDuckGoSearcher) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	u := "https://lite.duckduckgo.com/lite/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "jas-web-search/1")
	req.Header.Set("Accept", "text/html")
	resp, err := s.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, defaultSearchMaxBytes))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return parseDuckDuckGoLite(body, limit)
}

// parseDuckDuckGoLite extracts results from DDG lite HTML. Links whose href
// is an absolute http(s) URL and whose text is not empty are treated as hits;
// uddg= redirect wrappers are unwrapped when present.
func parseDuckDuckGoLite(body []byte, limit int) ([]SearchResult, error) {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return nil, err
	}
	var out []SearchResult
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if len(out) >= limit {
			return
		}
		if n.Type == html.ElementNode && strings.EqualFold(n.Data, "a") {
			href := attr(n, "href")
			title := strings.TrimSpace(textContent(n))
			link := unwrapDDG(href)
			if title != "" && looksAbsoluteHTTP(link) {
				out = append(out, SearchResult{Title: title, URL: link})
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return out, nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

func textContent(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func unwrapDDG(href string) string {
	href = strings.TrimSpace(href)
	u, err := url.Parse(href)
	if err != nil {
		return href
	}
	if v := u.Query().Get("uddg"); v != "" {
		if decoded, err := url.QueryUnescape(v); err == nil {
			return decoded
		}
		return v
	}
	return href
}

func looksAbsoluteHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}
