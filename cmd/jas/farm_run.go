package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/settings"
)

// farmRunOnlyFlags are jas farm run flags that are not stored settings.
var farmRunOnlyFlags = map[string]bool{"json": true, "allow-dirty": true, "trust-project": true}

type farmRunArgs struct {
	plan       string
	json       bool
	allowDirty bool
	trust      bool
	flags      map[string]string
}

// parseFarmRunArgs reads flags and the single plan path, accepting flags on
// either side of it.
func parseFarmRunArgs(args []string) (farmRunArgs, error) {
	fs := flag.NewFlagSet("farm run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var ra farmRunArgs
	fs.BoolVar(&ra.json, "json", false, "stream farm.jsonl events (only) to stdout")
	fs.BoolVar(&ra.allowDirty, "allow-dirty", false, "run on a dirty tree; uncommitted changes are not part of the farm")
	fs.BoolVar(&ra.trust, "trust-project", false, "load the project context file without asking")
	for _, k := range settings.Keys {
		if k.Flag {
			fs.String(strings.ReplaceAll(k.Name, "_", "-"), "", k.Name)
		}
	}
	var rest []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return ra, usageErr("%v", err)
		}
		args = nil
		if fs.NArg() > 0 {
			rest = append(rest, fs.Arg(0))
			args = fs.Args()[1:]
		}
	}
	if len(rest) != 1 || strings.TrimSpace(rest[0]) == "" {
		return ra, usageErr("expected exactly one plan: jas farm run [flags] plan.yaml")
	}
	ra.plan = rest[0]
	ra.flags = map[string]string{}
	fs.Visit(func(f *flag.Flag) {
		if !farmRunOnlyFlags[f.Name] {
			ra.flags[strings.ReplaceAll(f.Name, "-", "_")] = f.Value.String()
		}
	})
	return ra, nil
}

// farmRunCmd runs `jas farm run <plan.yaml>`.
func farmRunCmd(ctx context.Context, args []string) error {
	ra, err := parseFarmRunArgs(args)
	if err != nil {
		return err
	}
	e, err := prepare(ctx, ra.flags, ra.trust)
	if err != nil {
		return err
	}
	return runPlan(ctx, e, ra.plan, ra.json, ra.allowDirty)
}

// runPlan runs an approved plan with farm.RunFarm. ctx is the soft stop
// (first Ctrl-C); a second Ctrl-C cancels the hard context.
func runPlan(ctx context.Context, e *env, planPath string, jsonOut, allowDirty bool) error {
	cfg, notices, err := farm.LoadConfig(e.root, e.home, e.pc.trusted)
	if err != nil {
		return usageErr("farm config: %v", err)
	}
	for _, n := range notices {
		fmt.Fprintln(os.Stderr, core.Sanitize(e.red.Redact("jas: "+n)))
	}
	hard, release := secondInterrupt()
	defer release()
	var ask func(string, []string) (string, bool)
	if e.interactive {
		ask = farmAsk(ctx, e.stdin, os.Stderr)
	}
	code, reason, err := farm.RunFarm(ctx, farm.RunOptions{
		PlanPath: planPath, Home: e.home, Main: e.root, JSON: jsonOut, AllowDirty: allowDirty,
		Stdout: os.Stdout, Stderr: os.Stderr, Ask: ask,
		Cfg: cfg, Trusted: e.pc.trusted, NewSession: farmSession(e),
		Approvals: farm.NewApprovals(e.stdin, os.Stderr, e.interactive, e.level),
		Hard:      hard, Policy: e.policy, Redact: e.red.Redact, Width: stdoutWidth,
	})
	if err != nil {
		return err // a refusal is already an exit 2 ExitError
	}
	if code != core.ExitOK {
		return &core.ExitError{Code: code, Reason: "farm " + reason}
	}
	return nil
}

// farmSession builds a worker's or the integrator's session: the trusted
// project context (when the worktree's matches main's) before the farm's
// prompt, the role's model or the session model, the worker's UI (quiet when
// none), redaction. Memory is read-only: no one can approve a worker's write.
func farmSession(e *env) func(farm.WorkerSession) (*core.Session, error) {
	return func(ws farm.WorkerSession) (*core.Session, error) {
		system, trusted := workerContext(e.pc, e.root, ws.Root)
		ws.System = e.pc.readOnlyMemory(system) + "\n\n" + ws.System
		model := ws.Role.Model
		if model == "" {
			model = e.cfg.Model
		}
		c := ws.Config(e.provider, model) // wraps the provider in the farm's limiter
		c.Exec.Filter, c.Exec.Redact = e.red.Filter, e.red.Redact
		c.Exec.WriteGuard = memoryGuard(e.mem, true, e.red.Redact)
		if c.UI == nil { // the chat gives each worker its own UI
			c.UI = core.NewQuietUI()
		}
		c.MaxTurns = e.cfg.MaxTurns
		c.Budget = e.budget(model)
		meta := e.sessionMeta(model)
		for k, v := range c.Meta {
			meta[k] = v
		}
		meta["root"], meta["project_context"] = ws.Root, trusted
		c.Meta = meta
		return core.NewSession(c)
	}
}

// secondInterrupt returns a context cancelled by the second interrupt. The
// first one cancels main's signal context (the soft stop); keeping a channel
// registered here stops the second from killing the process outright.
func secondInterrupt() (context.Context, func()) {
	hard, cancel := context.WithCancel(context.Background())
	ch := make(chan os.Signal, 2)
	signal.Notify(ch, os.Interrupt)
	go func() {
		seen := 0
		for {
			select {
			case <-ch:
				if seen++; seen >= 2 {
					cancel()
					return
				}
			case <-hard.Done():
				return
			}
		}
	}()
	return hard, func() { signal.Stop(ch); cancel() }
}

// stdoutWidth is the terminal's width when stdout is one, else 0. It is read
// per line, so a resized terminal is followed.
func stdoutWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}
