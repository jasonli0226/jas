package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

const (
	defaultHomeDir     = ".jas"
	sessionsDir        = "sessions"
	eventsFile         = "events.jsonl"
	sseIdleTimeout     = 60 * time.Second
	approveNever       = "never"
	defaultMaxTurnsEnv = core.DefaultMaxTurns
)

type runOpts struct {
	json           bool
	model, baseURL string
	apiKey, prompt string
	maxTurns       int
}

func runCmd(ctx context.Context, args []string) error {
	o, err := parseRun(args, os.Getenv)
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	root := core.FindRoot(cwd)

	home, err := jasHome(os.Getenv)
	if err != nil {
		return err
	}
	path := filepath.Join(home, sessionsDir, core.NewSessionID(time.Now()), eventsFile)
	var tee io.Writer
	var ui core.UI = core.NewPrinter(os.Stdout)
	if o.json {
		tee, ui = os.Stdout, core.NewQuietUI()
	}
	// TODO(Task 11): pass the secret redactor instead of nil.
	log, err := core.OpenEventLog(path, tee, nil)
	if err != nil {
		return err
	}
	defer log.Close()
	log = core.NewUILog(log, ui)

	exec := &core.Executor{
		Reg:    core.NewRegistry(tools.ReadOnly()...),
		Policy: core.ExecPolicy{Root: root},
		Gate:   core.AllowAll{},
		Log:    log,
	}
	_, err = core.Run(ctx, core.RunConfig{
		Provider: &providers.OpenAI{BaseURL: o.baseURL, APIKey: o.apiKey, IdleTimeout: sseIdleTimeout},
		Model:    o.model, Prompt: o.prompt, System: core.BuildSystem(core.BasePrompt, ""),
		Exec: exec, Log: log, UI: ui, MaxTurns: o.maxTurns,
		SessionMeta: map[string]any{
			"base_url": o.baseURL, "approve": approveNever, "root": root, "context_files": []string{},
		},
	})
	if err == nil && !o.json {
		fmt.Println()
	}
	return err
}

// parseRun reads flags and JAS_* settings. getenv is injected for testing.
// Task 13 replaces the env lookups with the settings package.
func parseRun(args []string, getenv func(string) string) (runOpts, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var o runOpts
	fs.BoolVar(&o.json, "json", false, "emit the event stream as JSONL on stdout")
	fs.StringVar(&o.model, "model", getenv("JAS_MODEL"), "model name (JAS_MODEL)")
	fs.StringVar(&o.baseURL, "base-url", getenv("JAS_BASE_URL"), "provider base URL (JAS_BASE_URL)")
	if err := fs.Parse(args); err != nil {
		return o, usageErr("%v", err)
	}
	if fs.NArg() != 1 || strings.TrimSpace(fs.Arg(0)) == "" {
		return o, usageErr(`expected exactly one task argument: jas run [flags] "task"`)
	}
	o.prompt = fs.Arg(0)
	o.apiKey = getenv("JAS_API_KEY")
	if o.model == "" {
		return o, usageErr("missing model: set JAS_MODEL or pass --model")
	}
	if o.baseURL == "" {
		return o, usageErr("missing base URL: set JAS_BASE_URL or pass --base-url")
	}
	o.maxTurns = defaultMaxTurnsEnv
	if v := getenv("JAS_MAX_TURNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return o, usageErr("invalid JAS_MAX_TURNS %q: want a positive integer", v)
		}
		o.maxTurns = n
	}
	return o, nil
}

func jasHome(getenv func(string) string) (string, error) {
	if h := getenv("JAS_HOME"); h != "" {
		return h, nil
	}
	uh, err := os.UserHomeDir()
	if err != nil {
		return "", errors.New("cannot locate home directory: set JAS_HOME")
	}
	return filepath.Join(uh, defaultHomeDir), nil
}
