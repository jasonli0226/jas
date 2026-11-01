package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// runJas runs the shared test binary with a clean JAS_* environment.
func runJas(t *testing.T, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runJasIn(t, t.TempDir(), env, args...)
}

// runJasIn is runJas with an explicit working directory.
func runJasIn(t *testing.T, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(testutil.JasBinary(t), args...)
	cmd.Dir = dir
	home := t.TempDir() // isolated from the real user skills on every OS
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "USERPROFILE=" + home, "JAS_HOME=" + testutil.TempHome(t)}, env...)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return so.String(), se.String(), code
}

func TestCLI_RunJSONEmitsEvents(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "hi there", Usage: providers.Usage{PromptTokens: 3, CompletionTokens: 2}}})
	out, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test"}, "run", "--json", "say hi")
	assert.Eq(t, code, 0)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var last map[string]any
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q (stderr %q)", l, errOut)
		}
		last = m
	}
	assert.Eq(t, last["type"].(string), "session_ended")
	assert.Eq(t, last["exit_code"].(float64), 0.0)
}

func TestCLI_PlainRunStreamsText(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "plain answer"}})
	out, _, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test"}, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "plain answer")
}

func TestCLI_PlainRunRedactsStreamedText(t *testing.T) {
	const key = "zq9plainrunkeyvalue42"
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789"
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "key " + key + " token " + token}})
	out, _, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=" + key}, "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, out, key)
	assert.NotContains(t, out, token)
	assert.Contains(t, out, "[REDACTED")
}

func TestCLI_MissingModelIsUsageError(t *testing.T) {
	_, errOut, code := runJas(t, []string{"JAS_BASE_URL=http://127.0.0.1:1", "JAS_API_KEY=k"}, "run", "task")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "JAS_MODEL")
}

func TestCLI_MaxTurnsFromEnv(t *testing.T) {
	call := fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{call, call})
	_, _, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k", "JAS_MAX_TURNS=1"}, "run", "task")
	assert.Eq(t, code, 4)
}

func TestCLI_SigintExits130(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Process.Signal cannot send SIGINT on windows")
	}
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "partial", Block: true}})
	cmd := exec.Command(testutil.JasBinary(t), "run", "--json", "task")
	cmd.Dir = t.TempDir()
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "JAS_HOME=" + testutil.TempHome(t), "JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k"}
	stdout, err := cmd.StdoutPipe()
	assert.NoErr(t, err)
	assert.NoErr(t, cmd.Start())
	// Wait until the run is under way (session_started is on stdout), then interrupt.
	buf := make([]byte, 1)
	if _, err := stdout.Read(buf); err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	assert.NoErr(t, cmd.Process.Signal(syscall.SIGINT))
	_, _ = io.Copy(io.Discard, stdout)
	var ee *exec.ExitError
	if !errors.As(cmd.Wait(), &ee) || ee.ExitCode() != 130 {
		t.Fatalf("want exit 130, got %v", ee)
	}
}

func TestCLI_Version(t *testing.T) {
	out, _, code := runJas(t, nil, "--version")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "jas dev")
}

func TestCLI_ApproveNoTTYExits5(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Calls: []fake.Call{{Name: "shell", Args: `{"command":"echo hi"}`}}}})
	out, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k"}, "run", "--json", "--approve=shell", "task")
	assert.Eq(t, code, 5)
	var sawUnavailable, startedApprove bool
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("bad line %q (stderr %q)", l, errOut)
		}
		if m["type"] == "approval" && m["decision"] == "unavailable" {
			sawUnavailable = true
		}
		if m["type"] == "session_started" && m["approve"] == "shell" {
			startedApprove = true
		}
	}
	if !sawUnavailable || !startedApprove {
		t.Fatalf("unavailable=%v approve-in-session_started=%v\n%s", sawUnavailable, startedApprove, out)
	}
}

func TestCLI_ApproveInvalidIsUsageError(t *testing.T) {
	_, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=http://127.0.0.1:1", "JAS_API_KEY=k", "JAS_APPROVE=bogus"}, "run", "task")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "never|shell|mutating")
}

// A prompt over the compaction threshold would compact in the chat; jas run
// has compaction off, so the run goes on with no compaction event.
func TestRunCmd_NoCompaction(t *testing.T) {
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "ls", Args: `{}`}}, Usage: providers.Usage{PromptTokens: 120000}},
		{Text: "done"},
	})
	out, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k", "JAS_COMPACT_KEEP_TOKENS=1"}, "run", "--json", "task")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, `"type":"session_ended"`)
	assert.NotContains(t, out, `"type":"compaction"`)
	assert.Eq(t, len(bodies()), 2)
	assert.NotContains(t, errOut, "compact")
}

func TestCLI_RunNameTitlesSession(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "ok"}})
	out, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k"}, "run", "--json", "--name", " nightly  lint ", "task")
	assert.Eq(t, code, 0)
	first, _, _ := strings.Cut(out, "\n")
	var m map[string]any
	if err := json.Unmarshal([]byte(first), &m); err != nil {
		t.Fatalf("bad line %q (stderr %q)", first, errOut)
	}
	assert.Eq(t, m["type"], any("session_started"))
	assert.Eq(t, m["title"], any("nightly lint"))
}

func TestCLI_RunPipedOutputHasNoDiff(t *testing.T) {
	dir := t.TempDir()
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "write", Args: `{"path":"new.txt","content":"one\ntwo\n"}`}}},
		{Text: "done"},
	})
	out, errOut, code := runJasIn(t, dir, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=test"}, "run", "--approve=never", "write a file")
	assert.Eq(t, code, 0)
	if strings.Contains(out, " + ") {
		t.Fatalf("piped stdout has a diff row: %q (stderr %q)", out, errOut)
	}
}
