package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"golang.org/x/term"
)

// jas attach (#156, phase 1): watch a farm from another terminal. Everything
// is read from the farm's files; a farm is running while its process holds
// the liveness lock (farm.LockFarm).

const (
	attachUsage       = "usage: jas attach [<farm-id> [<part>]]"
	attachBacklog     = 3 // tool calls replayed per part on attach
	attachSaysRunes   = 300
	attachResultRunes = 160
)

func attachCmd(ctx context.Context, args []string) error {
	home, err := settingsHome()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runAttach(ctx, home, args, os.Stdout, 0)
}

// runAttach is jas attach against home, printing to out; poll 0 uses the
// follower's default.
func runAttach(ctx context.Context, home string, args []string, out io.Writer, poll time.Duration) error {
	if len(args) > 2 || (len(args) > 0 && strings.HasPrefix(args[0], "-")) {
		return usageErr(attachUsage)
	}
	var id string
	var err error
	if len(args) == 0 {
		id, err = onlyRunningFarm(home, out)
		if err != nil || id == "" {
			return err
		}
	} else if id, err = matchFarm(home, args[0]); err != nil {
		return err
	}
	dir := filepath.Join(home, farmsDir, id)
	m, merr := farm.LoadManifest(filepath.Join(dir, farmManifestFile))
	part := ""
	if len(args) == 2 {
		part = args[1]
		if part != farm.IntegratePart && (merr != nil || m.Parts[part] == nil) {
			return usageErr("farm %s has no part %q", id, part)
		}
	}
	running := func() bool { return farm.FarmRunning(home, id) }
	red := newRedactor("")
	prog := farm.NewProgress(out, red.Redact)
	if fd := int(os.Stdout.Fd()); out == os.Stdout && term.IsTerminal(fd) {
		prog.Width = func() int { w, _, _ := term.GetSize(fd); return w }
	}
	var watch farm.Watcher = prog
	if part != "" {
		watch = &partDetail{Watcher: prog, part: part, out: out, redact: red.Redact}
	}
	f := &farm.Follow{Dir: dir, Watch: watch, Backlog: attachBacklog, Poll: poll, Running: running}
	f.Start() // before the table: what is written after it is shown as new
	if merr != nil {
		fmt.Fprintf(out, "farm %s is starting\n", id)
	} else {
		printFarmStatus(out, m)
	}
	if !running() {
		printFarmEnd(out, dir)
		return nil
	}
	outcome, err := f.Run(ctx)
	if err != nil {
		return err
	}
	switch outcome {
	case farm.FollowEnded:
		printFarmEnd(out, dir)
	case farm.FollowDied:
		// farm_ended may predate Start, written just before the lock dropped.
		if _, ok := farmEndReason(filepath.Join(dir, "farm.jsonl")); ok {
			printFarmEnd(out, dir)
		} else {
			fmt.Fprintln(out, "farm process ended")
		}
	}
	return nil
}

// onlyRunningFarm returns the one running farm; with none it says so and
// returns ""; with several it is a usage error listing them.
func onlyRunningFarm(home string, out io.Writer) (string, error) {
	var running []string
	for _, id := range farmIDs(home) {
		if farm.FarmRunning(home, id) {
			running = append(running, id)
		}
	}
	switch len(running) {
	case 0:
		fmt.Fprintln(out, "no farm is running (see jas farm status)")
		return "", nil
	case 1:
		return running[0], nil
	}
	return "", usageErr("several farms are running; name one: jas attach <farm-id>\n%s", strings.Join(running, "\n"))
}

// matchFarm resolves an exact farm id or a unique prefix of one.
func matchFarm(home, arg string) (string, error) {
	if _, err := farmIDArg(arg); err != nil {
		return "", err
	}
	var matches []string
	for _, id := range farmIDs(home) {
		if id == arg {
			return id, nil
		}
		if strings.HasPrefix(id, arg) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", usageErr("no farm %q under %s", arg, filepath.Join(home, farmsDir))
	case 1:
		return matches[0], nil
	}
	return "", usageErr("farm id %q matches several farms:\n%s", arg, strings.Join(matches, "\n"))
}

// farmIDs lists the farm dirs under home, oldest first.
func farmIDs(home string) []string {
	entries, err := os.ReadDir(filepath.Join(home, farmsDir))
	if err != nil {
		return nil
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() {
			ids = append(ids, e.Name())
		}
	}
	sort.Strings(ids)
	return ids
}

// printFarmEnd says how a farm that is not running ended.
func printFarmEnd(out io.Writer, dir string) {
	if reason, ok := farmEndReason(filepath.Join(dir, "farm.jsonl")); ok {
		fmt.Fprintf(out, "farm ended: %s\n", reason)
		return
	}
	fmt.Fprintln(out, "farm process ended without finishing")
}

// farmEndReason is the reason of the last farm_ended in a farm.jsonl.
func farmEndReason(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	reason, found := "", false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type   string `json:"type"`
			Reason string `json:"reason"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == farm.EvFarmEnded {
			reason, found = ev.Reason, true
		}
	}
	return reason, found
}

// partDetail adds one part's replies and tool results to the progress lines.
type partDetail struct {
	farm.Watcher
	part   string
	out    io.Writer
	redact func(string) string
}

func (d *partDetail) WorkerEvent(part, typ string, m map[string]any) {
	d.Watcher.WorkerEvent(part, typ, m)
	if part != d.part {
		return
	}
	switch typ {
	case core.EvAssistantMessage:
		if text, _ := m["text"].(string); strings.TrimSpace(text) != "" {
			d.print(fmt.Sprintf("[%s] says: %s", part, clipLine(text, attachSaysRunes)))
		}
	case core.EvToolResult:
		out, _ := m["output"].(string)
		d.print(fmt.Sprintf("[%s] -> %v: %s", part, m["status"], clipLine(out, attachResultRunes)))
	}
}

func (d *partDetail) print(s string) {
	fmt.Fprintln(d.out, core.Sanitize(d.redact(s)))
}

// clipLine is the first line of s, at most n runes.
func clipLine(s string, n int) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
