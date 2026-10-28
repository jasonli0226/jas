package main

import (
	"fmt"
	"os"
	"time"

	"fixture/pkg/api"
)

func main() {
	cfg, err := parseConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(2)
	}
	var lastErr error
	for attempt := 0; attempt < cfg.Retries; attempt++ {
		if lastErr = fetch(cfg); lastErr == nil {
			fmt.Println("ok")
			return
		}
		time.Sleep(cfg.Backoff << attempt)
	}
	fmt.Fprintln(os.Stderr, "giving up:", lastErr)
	os.Exit(1)
}

func fetch(cfg Config) error {
	return api.Ping(api.NewClient(cfg.Endpoint))
}
