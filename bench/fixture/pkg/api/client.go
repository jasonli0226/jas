// Package api is a tiny HTTP client used by the fixture.
package api

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Client talks to a remote endpoint.
type Client struct {
	base string
	http *http.Client
}

// NewClient returns a Client for base.
func NewClient(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 5 * time.Second}}
}

// Ping checks that the endpoint answers.
func Ping(c *Client) error {
	resp, err := c.http.Get(c.base + "/ping")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ping: status %d", resp.StatusCode)
	}
	return nil
}

// JoinPath joins URL path segments with single slashes.
func JoinPath(parts ...string) string {
	return "/" + strings.Trim(strings.Join(parts, "/"), "/")
}

// ParseTimeout parses a duration, falling back to def when s is empty.
func ParseTimeout(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

// IsRetryable reports whether an HTTP status is worth retrying.
func IsRetryable(status int) bool {
	return status == 429 || status >= 500
}

func normalize(s string) string { return strings.TrimSpace(s) }
