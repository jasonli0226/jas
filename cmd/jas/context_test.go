package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func writeAgents(t *testing.T, dir, text string) {
	t.Helper()
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(text), 0o644))
}

func TestCLI_TrustProjectFlag(t *testing.T) {
	proj := t.TempDir()
	writeAgents(t, proj, "UNIQUE-RULE-7731")
	home := t.TempDir()
	env := []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_API_KEY=k"}

	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}, {Text: "b"}})
	_, errOut, code := runJasIn(t, proj, append(env, "JAS_BASE_URL="+url), "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "skipping untrusted AGENTS.md (pass --trust-project to load)")
	assert.NotContains(t, string(bodies()[0]), "UNIQUE-RULE-7731")

	_, _, code = runJasIn(t, proj, append(env, "JAS_BASE_URL="+url), "run", "--trust-project", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, string(bodies()[1]), "UNIQUE-RULE-7731")
	_, err := os.Stat(filepath.Join(home, "trust.json"))
	if !os.IsNotExist(err) {
		t.Fatalf("trust.json should not be written by --trust-project: %v", err)
	}
}

func TestRun_ContextFileInSystemPrompt(t *testing.T) {
	proj := t.TempDir()
	text := "TRUSTED-RULE-5521 token=sk-abcdefghijklmnopqrstuvwxyz0123456789"
	writeAgents(t, proj, text)
	real, err := filepath.EvalSymlinks(proj)
	assert.NoErr(t, err)
	home := t.TempDir()
	st := core.TrustStore{Path: filepath.Join(home, "trust.json")}
	assert.NoErr(t, st.Trust(real, []byte(text)))
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJasIn(t, proj, []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_API_KEY=k", "JAS_BASE_URL=" + url}, "run", "--json", "q")
	assert.Eq(t, code, 0)
	body := string(bodies()[0])
	assert.Contains(t, body, "TRUSTED-RULE-5521")
	assert.NotContains(t, body, "sk-abcdefghijklmnopqrstuvwxyz0123456789")
	var loaded bool
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(l), &m))
		if m["type"] == "session_started" {
			cf := m["context_files"].([]any)
			assert.Eq(t, len(cf), 1)
			e := cf[0].(map[string]any)
			loaded = e["loaded"] == true && len(e["sha256"].(string)) == 64 && strings.HasSuffix(e["path"].(string), "AGENTS.md")
		}
	}
	assert.Eq(t, loaded, true)
}

func TestRun_NoContextFileListsEmpty(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJas(t, []string{"JAS_MODEL=m", "JAS_API_KEY=k", "JAS_BASE_URL=" + url}, "run", "--json", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, `"context_files":[]`)
}
