package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/providers/fake"
)

// runJas runs the shared test binary with a clean JAS_* environment.
func runJas(t *testing.T, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(testutil.JasBinary(t), args...)
	cmd.Dir = t.TempDir()
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "JAS_HOME=" + testutil.TempHome(t)}, env...)
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

func TestCLI_Version(t *testing.T) {
	out, _, code := runJas(t, nil, "--version")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "jas dev")
}
