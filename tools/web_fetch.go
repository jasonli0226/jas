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
	"unicode"

	"golang.org/x/net/html"

	"github.com/jasonli0226/jas/core"
)

const (
	defaultFetchTimeout = 30 * time.Second
	defaultFetchMaxBytes = 1 << 20 // 1 MiB
)

// WebFetch GETs a URL and returns the page as plain text (HTML stripped).
// It is ReadOnly: the only side effect is the HTTP GET (design §12.1).
type WebFetch struct {
	Client  *http.Client // nil → default with Timeout
	Timeout time.Duration
	MaxBytes int
}

func (WebFetch) Name() string { return "web_fetch" }
func (WebFetch) Description() string {
	return "Fetch a URL over HTTP(S) and return the page body as plain text (HTML tags stripped). Use for documentation and public pages; not for authenticated or binary content."
}
func (WebFetch) Kind() core.Kind { return core.ReadOnly }
func (WebFetch) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"url":{"type":"string","description":"Absolute http(s) URL to fetch."}},` +
		`"required":["url"]}`)
}

func (t WebFetch) Call(ctx context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	u, err := url.Parse(strings.TrimSpace(a.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errResult("web_fetch: url must be an absolute http(s) URL")
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = defaultFetchTimeout
	}
	max := t.MaxBytes
	if max <= 0 {
		max = defaultFetchMaxBytes
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errResult("web_fetch: %v", err)
	}
	req.Header.Set("User-Agent", "jas-web-fetch/1")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		if ok, cerr := cancelled(ctx, "web_fetch"); ok {
			return core.Result{}, cerr
		}
		return errResult("web_fetch: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)+1))
	if err != nil {
		if ok, cerr := cancelled(ctx, "web_fetch"); ok {
			return core.Result{}, cerr
		}
		return errResult("web_fetch: read body: %v", err)
	}
	truncated := ""
	if len(body) > max {
		body = body[:max]
		truncated = fmt.Sprintf("\n[truncated: response capped at %d bytes]", max)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview := string(body)
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		return errResult("web_fetch: HTTP %d from %s: %s", resp.StatusCode, u.Host, strings.TrimSpace(preview))
	}
	ct := resp.Header.Get("Content-Type")
	text := string(body)
	if looksLikeHTML(ct, body) {
		text = htmlToText(body)
	}
	out := Truncate([]byte(strings.TrimSpace(text)+truncated), p.OutputLimit())
	return core.Result{Output: out}, nil
}

func looksLikeHTML(ct string, body []byte) bool {
	ct = strings.ToLower(ct)
	if strings.Contains(ct, "html") {
		return true
	}
	if strings.Contains(ct, "text/plain") || strings.Contains(ct, "application/json") {
		return false
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 256 {
		s = s[:256]
	}
	lower := strings.ToLower(s)
	return strings.Contains(lower, "<html") || strings.Contains(lower, "<!doctype html") || strings.Contains(lower, "<body")
}

// htmlToText strips tags to readable plain text, dropping script/style/noscript.
func htmlToText(body []byte) string {
	doc, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return string(body)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "noscript", "svg", "template":
				return
			case "br", "hr":
				b.WriteByte('\n')
			case "p", "div", "section", "article", "header", "footer", "main",
				"li", "tr", "h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
			}
		}
		if n.Type == html.TextNode {
			t := strings.Map(func(r rune) rune {
				if unicode.IsSpace(r) {
					return ' '
				}
				return r
			}, n.Data)
			t = strings.TrimSpace(t)
			if t != "" {
				if b.Len() > 0 {
					last := b.String()[b.Len()-1]
					if last != '\n' && last != ' ' {
						b.WriteByte(' ')
					}
				}
				b.WriteString(t)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "p", "div", "section", "article", "li", "tr",
				"h1", "h2", "h3", "h4", "h5", "h6", "blockquote", "pre":
				b.WriteByte('\n')
			}
		}
	}
	walk(doc)
	lines := strings.Split(b.String(), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
