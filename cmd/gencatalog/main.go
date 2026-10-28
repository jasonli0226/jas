// Command gencatalog fetches the OpenRouter model list and writes a
// {"<model id>": <context_length>} JSON catalog sorted by key.
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
	"sort"
	"time"
)

const (
	modelsURL    = "https://openrouter.ai/api/v1/models"
	fetchTimeout = 30 * time.Second
	maxBody      = 64 << 20
)

type modelList struct {
	Data []struct {
		ID            string `json:"id"`
		ContextLength int    `json:"context_length"`
	} `json:"data"`
}

func main() {
	out := flag.String("out", "catalog.json", "output file")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "gencatalog:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", modelsURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	data, err := render(body)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}

// render converts an OpenRouter response to the catalog JSON, one entry per
// line, sorted by key. Entries without a positive context length are skipped.
func render(body []byte) ([]byte, error) {
	var l modelList
	if err := json.Unmarshal(body, &l); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	m := map[string]int{}
	for _, e := range l.Data {
		if e.ID != "" && e.ContextLength > 0 {
			m[e.ID] = e.ContextLength
		}
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("no models in response")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		fmt.Fprintf(&b, "  %s: %d", kb, m[k])
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString("}\n")
	return b.Bytes(), nil
}
