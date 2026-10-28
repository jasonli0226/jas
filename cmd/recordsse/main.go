// Command recordsse captures the raw SSE body of one chat-completions call so
// it can be used as a test fixture. Config comes from JAS_BASE_URL and
// JAS_API_KEY; the key is sent only as an Authorization header and is never
// written to the output file.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const readToolSchema = `{"type":"object","properties":{"path":{"type":"string","description":"File path to read"}},"required":["path"]}`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "recordsse:", err)
		os.Exit(1)
	}
}

func run() error {
	model := flag.String("model", "", "model id (required)")
	prompt := flag.String("prompt", "read main.go", "user prompt")
	out := flag.String("out", "", "output .sse file (required)")
	flag.Parse()
	if *model == "" || *out == "" {
		flag.Usage()
		return fmt.Errorf("-model and -out are required")
	}
	base := os.Getenv("JAS_BASE_URL")
	if base == "" {
		return fmt.Errorf("JAS_BASE_URL is not set")
	}

	body, err := json.Marshal(map[string]any{
		"model":          *model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"max_tokens":     200,
		"messages":       []map[string]any{{"role": "user", "content": *prompt}},
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "read",
				"description": "Read a file from disk",
				"parameters":  json.RawMessage(readToolSchema),
			},
		}},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("JAS_API_KEY"); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(raw, 500))
	}
	if err := os.WriteFile(*out, raw, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(raw), *out)
	return nil
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
