package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/tools"
)

// jsonlTypes returns the "type" of every JSON line in s.
func jsonlTypes(t *testing.T, s string) []string {
	t.Helper()
	var types []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		typ, _ := m["type"].(string)
		types = append(types, typ)
	}
	return types
}

// Golden lists captured from `jas farm run --json` before the approval
// broker hosted the farm (base commit 073d7d2): one worker at a time, part a
// asks to run a shell command at approve shell with no TTY and no client.
var (
	goldenNoClientStdout = []string{"farm_started", "part_started", "part_ended", "owner_decision", "part_started", "part_check", "board_posted", "part_ended", "integrate_started", "integrate_check", "integrate_ended", "check_ran", "owner_decision", "farm_ended"}
	goldenNoClientLog    = goldenNoClientStdout // farm.jsonl is what --json tees
	goldenNoClientCode   = 6                    // part_dropped: a ended approval_needed
)

func TestFarmRun_JSONNoClientUnchanged(t *testing.T) {
	repo, home := farmRepo(t), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "farm.yaml"), []byte("check: \"true\"\nsplit:\n  max_workers: 1\n"), 0o644))
	plan := approvedPlan(t, repo)
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "shell", Args: `{"command":"echo hi"}`}}},
		{Text: "b done"},
	})
	out, errOut, code := runJasIn(t, repo, farmRunEnv(url, home, t), "farm", "run", plan, "--json", "--trust-project", "--approve", "shell")
	stdout := jsonlTypes(t, out)
	dirs := farmDirs(t, home)
	assert.Eq(t, len(dirs), 1)
	raw, err := os.ReadFile(filepath.Join(dirs[0], "farm.jsonl"))
	assert.NoErr(t, err)
	flog := jsonlTypes(t, string(raw))
	t.Logf("code %d\nstdout %q\nlog %q\nstderr %s", code, stdout, flog, errOut)
	assert.Contains(t, string(raw), `"part":"a","reason":"approval_needed"`)
	assert.Contains(t, errOut, "farm part_dropped (exit 6)")
	assert.Eq(t, code, goldenNoClientCode)
	assert.Eq(t, strings.Join(stdout, " "), strings.Join(goldenNoClientStdout, " "))
	assert.Eq(t, strings.Join(flog, " "), strings.Join(goldenNoClientLog, " "))
	// The intended difference: the part's log records the broker's refusal.
	part, err := os.ReadFile(filepath.Join(dirs[0], "parts", "a", "events.jsonl"))
	assert.NoErr(t, err)
	assert.Contains(t, string(part), `"type":"approval_requested"`)
	assert.Contains(t, string(part), `"by":"no_client"`)
}

func TestFarmRun_ApprovalWaitFlag(t *testing.T) {
	cfg := farm.Config{Budget: farm.Budget{ApprovalWait: 5 * time.Minute}}
	ra, err := parseFarmRunArgs([]string{"plan.yaml", "--approval-wait", "3m"})
	assert.NoErr(t, err)
	assert.Eq(t, brokerOptions(core.ShellOnly, nil, cfg, ra.approvalWait, nil).Wait, 3*time.Minute)
	if _, ok := ra.flags["approval_wait"]; ok {
		t.Fatal("--approval-wait passed on as a setting")
	}
	ra, err = parseFarmRunArgs([]string{"plan.yaml"})
	assert.NoErr(t, err)
	o := brokerOptions(core.ShellOnly, nil, cfg, ra.approvalWait, nil)
	assert.Eq(t, o.Wait, 5*time.Minute)
	assert.Eq(t, o.Level, core.ShellOnly)
	ra, err = parseFarmRunArgs([]string{"plan.yaml", "--approval-wait", "0"})
	assert.NoErr(t, err)
	assert.Eq(t, brokerOptions(core.ShellOnly, nil, cfg, ra.approvalWait, nil).Wait, time.Duration(0))
	if _, err := parseFarmRunArgs([]string{"plan.yaml", "--approval-wait", "soon"}); err == nil {
		t.Fatal("want a usage error for a bad duration")
	}
}

// lockedBuf is a strings.Builder safe for the prompt writer and the test.
type lockedBuf struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitOut waits until out contains want.
func waitOut(t *testing.T, out *lockedBuf, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("output %q never contained %q", out.String(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func farmTool(t *testing.T, name string) core.Tool {
	t.Helper()
	for _, tl := range tools.Mutating() {
		if tl.Name() == name {
			return tl
		}
	}
	t.Fatalf("no tool %q", name)
	return nil
}

type gateResult struct {
	d   core.Decision
	err error
}

func allowIn(g core.Gate, tl core.Tool, args string, preview func() *core.Diff) <-chan gateResult {
	ch := make(chan gateResult, 1)
	go func() {
		d, err := g.Allow(context.Background(), tl, json.RawMessage(args), preview)
		ch <- gateResult{d, err}
	}()
	return ch
}

func gateDone(t *testing.T, ch <-chan gateResult) gateResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("gate never returned")
	}
	return gateResult{}
}

// ttyBroker is a broker whose host is a farm TTY reading in and writing out.
func ttyBroker(in io.Reader, out io.Writer, redact func(string) string) (*farmTTY, *farm.Broker) {
	tty := newFarmTTY(in, out)
	var b *farm.Broker
	b = farm.NewBroker(brokerOptions(core.ShellOnly, redact, farm.Config{}, nil,
		tty.approver(redact, func(id string) (string, string, bool) { return b.Closed(id) })))
	return tty, b
}

func TestFarmRun_TTYWithdrawnNextPrompts(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &lockedBuf{}
	_, b := ttyBroker(pr, out, nil)
	g, err := b.Gate("api", "", nil)
	assert.NoErr(t, err)
	shell := farmTool(t, "shell")
	first := allowIn(g, shell, `{"command":"npm install zod"}`, nil)
	waitOut(t, out, "[api] approve [a1] shell: npm install zod? [y/N/always/farm] ")
	ok, _ := b.Answer("a1", farm.AnswerYes, "attach:c1")
	assert.Eq(t, ok, true)
	assert.Eq(t, gateDone(t, first).d, core.Granted)

	second := allowIn(g, shell, `{"command":"go test ./..."}`, nil)
	waitOut(t, out, "[api] approve [a2] shell: go test ./...? [y/N/always/farm] ")
	text := out.String()
	note := "[api] [a1] answered from attach: yes\n"
	if i, j := strings.Index(text, note), strings.Index(text, "[a2]"); i < 0 || i > j {
		t.Fatalf("withdrawal line not printed before the next prompt:\n%s", text)
	}
	_, err = io.WriteString(pw, "n\n")
	assert.NoErr(t, err)
	assert.Eq(t, gateDone(t, second).d, core.Denied)
	if _, by, _ := b.Closed("a2"); by != "host" {
		t.Fatalf("a2 closed by %q, want host", by)
	}
}

// The host redacts the tool name and the diff's path before printing.
func TestFarmRun_TTYPromptRedacts(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &lockedBuf{}
	redact := func(s string) string { return strings.ReplaceAll(s, "sekret", "[REDACTED]") }
	_, b := ttyBroker(pr, out, redact)
	g, err := b.Gate("api", "mutating", nil)
	assert.NoErr(t, err)
	diff := &core.Diff{Path: "sekret/a.go", Hunks: [][]core.DiffLine{{{Kind: "+", New: 1, Text: "b"}}}}
	res := allowIn(g, farmTool(t, "write"), `{"path":"sekret/a.go","content":"b\n"}`, func() *core.Diff { return diff })
	waitOut(t, out, "[y/N/always/farm] ")
	_, err = io.WriteString(pw, "y\n")
	assert.NoErr(t, err)
	assert.Eq(t, gateDone(t, res).d, core.Granted)
	text := out.String()
	assert.Contains(t, text, "[api] write [REDACTED]/a.go\n")
	assert.Contains(t, text, "[api] approve [a1] write: [REDACTED]/a.go?")
	if strings.Contains(text, "sekret") {
		t.Fatalf("unredacted prompt:\n%s", text)
	}
}
