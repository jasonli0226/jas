package main

import (
	"bytes"
	"encoding/json"
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
	secretsJasKey = "sk-jas-test-0123456789abcdefghij"
	redactMarker  = "[REDACTED:"
)

func TestSecrets_ZeroLeakAcceptance(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; the layer-2 bypass case needs it")
	}
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
		call("grep", `{"pattern":"(?i)api_key","path":"."}`),
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

	assertPerLayer(t, string(events), secrets)
}

type toolResult struct {
	Output string `json:"output"`
	Status string `json:"status"`
	Tool   string `json:"tool"`
}

// Indexes of tool_result events, in script order.
const (
	resReadEnv = iota
	resReadConfig
	resCatEnv
	resPrintenv
	resEnvGrep
	resGrep
	resPython
	resCatConfig
	resCount
)

const (
	denyFloorMsg  = "path denied by policy"
	denyFilterMsg = "blocked by deny pattern"
)

func assertPerLayer(t *testing.T, events string, secrets []string) {
	t.Helper()
	var res []toolResult
	for _, line := range strings.Split(events, "\n") {
		var ev struct {
			Type string `json:"type"`
			toolResult
		}
		if line == "" || json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "tool_result" {
			continue
		}
		res = append(res, ev.toolResult)
	}
	if len(res) != resCount {
		t.Fatalf("want %d tool_result events, got %d", resCount, len(res))
	}
	blocked := func(i int, msg string) {
		if res[i].Status != "error" || !strings.Contains(res[i].Output, msg) {
			t.Errorf("result %d (%s): want error containing %q, got %s %q", i, res[i].Tool, msg, res[i].Status, res[i].Output)
		}
	}
	blocked(resReadEnv, denyFloorMsg)
	for _, i := range []int{resCatEnv, resPrintenv, resEnvGrep} {
		blocked(i, denyFilterMsg)
	}
	for _, i := range []int{resReadConfig, resGrep, resPython, resCatConfig} {
		r := res[i]
		if r.Status != "ok" || !strings.Contains(r.Output, redactMarker) {
			t.Errorf("result %d (%s): want ok with %s, got %s %q", i, r.Tool, redactMarker, r.Status, r.Output)
		}
		for _, s := range secrets {
			if strings.Contains(r.Output, s) {
				t.Errorf("result %d (%s) contains secret %q", i, r.Tool, s)
			}
		}
	}
}
