package main

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds runtime settings read from the environment.
type Config struct {
	Endpoint string
	Retries  int
	Backoff  time.Duration
}

func parseConfig() (Config, error) {
	cfg := Config{Endpoint: "http://localhost:8080", Retries: 3, Backoff: 100 * time.Millisecond}
	if v := os.Getenv("APP_ENDPOINT"); v != "" {
		cfg.Endpoint = v
	}
	if v := os.Getenv("APP_RETRIES"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return cfg, fmt.Errorf("APP_RETRIES must be a positive integer, got %q", v)
		}
		cfg.Retries = n
	}
	if v := os.Getenv("APP_BACKOFF"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return cfg, fmt.Errorf("APP_BACKOFF: %w", err)
		}
		cfg.Backoff = d
	}
	return cfg, nil
}
