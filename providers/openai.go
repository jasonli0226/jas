package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrContextLength is returned when the provider rejects the request because
// the prompt exceeds the model's context window.
var ErrContextLength = errors.New("context_length_exceeded")

// ErrStreamTruncated is returned when the stream ends without a finish_reason,
// e.g. because the connection dropped mid-answer.
var ErrStreamTruncated = errors.New("stream ended without finish_reason")

// HTTPError is a non-2xx response. It never includes request headers (and so
// never the API key).
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider returned HTTP %d: %s", e.Status, e.Body)
}

const (
	defaultRetries   = 3
	maxErrBodyBytes  = 4096
	baseRetryBackoff = time.Second
	maxRetryWait     = 60 * time.Second
)

// OpenAI talks to any OpenAI-compatible /chat/completions endpoint.
type OpenAI struct {
	BaseURL, APIKey string
	HTTP            *http.Client
	IdleTimeout     time.Duration
	Retries         int // retries after the first attempt; 0 means the default (3)
	// Sleep waits d or returns ctx.Err() if ctx ends first; nil uses a timer.
	Sleep func(ctx context.Context, d time.Duration) error
}

var _ Provider = (*OpenAI)(nil)

// Stream implements Provider.
func (o *OpenAI) Stream(ctx context.Context, req Request) (<-chan StreamEvent, <-chan error) {
	out := make(chan StreamEvent)
	errc := make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errc)
		if err := o.run(ctx, req, out); err != nil {
			errc <- err
		}
	}()
	return out, errc
}

func (o *OpenAI) run(ctx context.Context, req Request, out chan<- StreamEvent) error {
	// Cancelling on every return path releases the ReadSSE goroutines.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	body, err := json.Marshal(encodeRequest(req))
	if err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	resp, err := o.doWithRetry(ctx, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	payloads, sseErr := ReadSSE(ctx, resp.Body, o.IdleTimeout)
	finished := false
	for p := range payloads {
		evs, err := parseChunk(p)
		if err != nil {
			return err
		}
		for _, ev := range evs {
			if ev.FinishReason != "" {
				finished = true
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if err := <-sseErr; err != nil {
		return err
	}
	if !finished {
		return ErrStreamTruncated
	}
	return nil
}

func (o *OpenAI) doWithRetry(ctx context.Context, body []byte) (*http.Response, error) {
	retries := o.Retries
	if retries <= 0 {
		retries = defaultRetries
	}
	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	sleep := o.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	backoff := baseRetryBackoff
	for attempt := 0; ; attempt++ {
		resp, err := o.send(ctx, client, body)
		wait := backoff
		switch {
		case err != nil:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Transport errors before any body byte are retryable.
		case resp.StatusCode/100 == 2:
			return resp, nil
		default:
			herr := readHTTPError(resp, o.APIKey)
			if herr.Status == http.StatusBadRequest && strings.Contains(herr.Body, "context_length_exceeded") {
				return nil, fmt.Errorf("%w: %s", ErrContextLength, herr.Body)
			}
			if !retryableStatus(herr.Status) {
				return nil, herr
			}
			err = herr
			if ra := parseRetryAfter(resp.Header.Get("Retry-After")); ra > 0 {
				wait = ra
			}
		}
		if attempt >= retries {
			return nil, err
		}
		if wait > maxRetryWait {
			wait = maxRetryWait
		}
		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
		backoff *= 2
	}
}

func (o *OpenAI) send(ctx context.Context, client *http.Client, body []byte) (*http.Response, error) {
	url := strings.TrimRight(o.BaseURL, "/") + "/chat/completions"
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "text/event-stream")
	if o.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	return client.Do(hr)
}

func readHTTPError(resp *http.Response, apiKey string) *HTTPError {
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBodyBytes))
	body := string(b)
	if apiKey != "" {
		body = strings.ReplaceAll(body, apiKey, "[REDACTED]")
	}
	return &HTTPError{Status: resp.StatusCode, Body: body}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func retryableStatus(s int) bool {
	return s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || s >= 500
}

func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// --- request encoding ---

type oaiMessage struct {
	Role       string        `json:"role"`
	Content    *string       `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
}

type oaiToolCall struct {
	ID       string      `json:"id"`
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description,omitempty"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

type oaiRequest struct {
	Model         string       `json:"model"`
	Messages      []oaiMessage `json:"messages"`
	Tools         []oaiTool    `json:"tools,omitempty"`
	Stream        bool         `json:"stream"`
	StreamOptions struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

func strPtr(s string) *string { return &s }

func encodeRequest(req Request) oaiRequest {
	r := oaiRequest{Model: req.Model, Stream: true}
	r.StreamOptions.IncludeUsage = true
	if req.System != "" {
		r.Messages = append(r.Messages, oaiMessage{Role: "system", Content: strPtr(req.System)})
	}
	for _, m := range req.Messages {
		r.Messages = append(r.Messages, encodeMessage(m)...)
	}
	for _, t := range req.Tools {
		var ot oaiTool
		ot.Type = "function"
		ot.Function.Name, ot.Function.Description, ot.Function.Parameters = t.Name, t.Description, t.Schema
		r.Tools = append(r.Tools, ot)
	}
	return r
}

// encodeMessage maps one neutral message to one or more wire messages.
// Tool results become separate role "tool" messages, emitted first.
func encodeMessage(m Message) []oaiMessage {
	var text strings.Builder
	var calls []oaiToolCall
	var results []oaiMessage
	for _, b := range m.Content {
		switch b.Type {
		case BlockText:
			text.WriteString(b.Text)
		case BlockToolUse:
			// Arguments is a JSON string on the wire; pass the raw bytes so
			// invalid model-produced JSON can't break request encoding.
			calls = append(calls, oaiToolCall{ID: b.ToolUseID, Type: "function",
				Function: oaiFunction{Name: b.Name, Arguments: string(b.Input)}})
		case BlockToolResult:
			results = append(results, oaiMessage{Role: "tool", ToolCallID: b.ToolUseID, Content: strPtr(b.Text)})
		}
	}
	msgs := results
	if text.Len() > 0 || len(calls) > 0 {
		om := oaiMessage{Role: string(m.Role), ToolCalls: calls}
		if text.Len() > 0 {
			om.Content = strPtr(text.String())
		}
		msgs = append(msgs, om)
	}
	return msgs
}

// --- response parsing ---

type oaiChunk struct {
	Choices []struct {
		Delta struct {
			Content          string `json:"content"`
			Reasoning        string `json:"reasoning"`
			ReasoningContent string `json:"reasoning_content"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		Details          struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
}

// parseChunk converts one OpenAI chat-completions chunk into StreamEvents.
func parseChunk(payload []byte) ([]StreamEvent, error) {
	var c oaiChunk
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("parse chunk: %w", err)
	}
	var evs []StreamEvent
	for _, ch := range c.Choices {
		if r := firstNonEmpty(ch.Delta.Reasoning, ch.Delta.ReasoningContent); r != "" {
			evs = append(evs, StreamEvent{Reasoning: r})
		}
		if ch.Delta.Content != "" {
			evs = append(evs, StreamEvent{Text: ch.Delta.Content})
		}
		for _, tc := range ch.Delta.ToolCalls {
			evs = append(evs, StreamEvent{ToolCallDelta: &ToolCallDelta{
				Index: tc.Index, ID: tc.ID, Name: tc.Function.Name, ArgsFragment: tc.Function.Arguments,
			}})
		}
		if ch.FinishReason != "" {
			evs = append(evs, StreamEvent{FinishReason: ch.FinishReason})
		}
	}
	if c.Usage != nil {
		evs = append(evs, StreamEvent{Usage: &Usage{
			PromptTokens: c.Usage.PromptTokens, CompletionTokens: c.Usage.CompletionTokens,
			CachedTokens: c.Usage.Details.CachedTokens,
		}})
	}
	return evs, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
