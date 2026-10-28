package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

const (
	secretsJasKey      = "sk-jas-test-0123456789abcdefghij"
	redactMarker       = "[REDACTED:"
	minRedactedResults = 2 // the two layer-2 bypass commands
)

func TestSecrets_ZeroLeakAcceptance(t *testing.T) {
	repo := testutil.NewFixtureRepo(t)
	call := func(name, args string) fake.Turn {
		return fake.Turn{Calls: []fake.Call{{Name: name, Args: args}}}
	}
	turns := []fake.Turn{
		call("read", `{"path":".env"}`),
		call("read", `{"path":"config.yaml"}`),
		call("shell", `{"command":"cat .env"}`),
		call("shell", `{"command":"printenv"}`),
		call("shell", `{"command":"env | grep KEY"}`),
		call("grep", `{"pattern":"API_KEY","path":"."}`),
		call("shell", `{"command":"python3 -c \"print(open('.e'+'nv').read())\""}`),
		call("shell", `{"command":"cat config.yaml"}`),
		{Text: "I saw everything: .env, config.yaml, printenv, grep output."},
	}
	url, bodies := testutil.FakeOpenAI(t, turns)

	home := t.TempDir()
	cmd := exec.Command(testutil.JasBinary(t), "run", "--json", "inspect all the secrets")
	cmd.Dir = repo
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "JAS_HOME=" + home,
		"JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=" + secretsJasKey,
	}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	if err := cmd.Run(); err != nil {
		t.Fatalf("jas run: %v\nstderr: %s", err, se.String())
	}

	logs, err := filepath.Glob(filepath.Join(home, "sessions", "*", "events.jsonl"))
	if err != nil || len(logs) != 1 {
		t.Fatalf("want one events.jsonl, got %v (%v)", logs, err)
	}
	events, err := os.ReadFile(logs[0])
	if err != nil {
		t.Fatal(err)
	}

	reqs := bodies()
	if len(reqs) != len(turns) {
		t.Fatalf("fake received %d requests, want %d", len(reqs), len(turns))
	}
	surfaces := map[string]string{"stdout": so.String(), "stderr": se.String(), "events.jsonl": string(events)}
	for i, b := range reqs {
		surfaces[fmt.Sprintf("request body %d", i)] = string(b)
	}
	secrets := []string{secretsJasKey, testutil.FixtureOpenAIKey, testutil.FixtureDBPassword, testutil.FixtureConfigKey}
	for surface, text := range surfaces {
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("secret %q leaked into %s", s, surface)
			}
		}
	}

	// Layer 2 proof: the bypass results reached the tool and were redacted.
	if n := strings.Count(string(reqs[len(reqs)-1]), redactMarker); n < minRedactedResults {
		t.Errorf("want >= %d %s markers in the final request, got %d", minRedactedResults, redactMarker, n)
	}
	redacted := 0
	for _, line := range strings.Split(string(events), "\n") {
		if strings.Contains(line, `"tool_result"`) && strings.Contains(line, redactMarker) {
			redacted++
		}
	}
	if redacted < minRedactedResults {
		t.Errorf("want >= %d tool_result events with %s, got %d", minRedactedResults, redactMarker, redacted)
	}
}
