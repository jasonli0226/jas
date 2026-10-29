package main

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestCLI_MaxTurnsFromStoredConfig(t *testing.T) {
	call := fake.Turn{Calls: []fake.Call{{Name: "ls", Args: `{}`}}}
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{call, call})
	home := t.TempDir()
	env := []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k"}
	_, errOut, code := runJas(t, env, "config", "set", "max_turns", "1")
	assert.Eq(t, code, 0)
	_, _, code = runJas(t, env, "run", "task")
	assert.Eq(t, code, 4)
	_ = errOut
}

func TestCLI_ConfigListShowsSources(t *testing.T) {
	home := t.TempDir()
	env := []string{"JAS_HOME=" + home, "JAS_APPROVE=shell"}
	_, _, code := runJas(t, env, "config", "set", "model", "x")
	assert.Eq(t, code, 0)
	out, _, code := runJas(t, env, "config", "list")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "approve  shell  (env JAS_APPROVE)")
	assert.Contains(t, out, "model  x  (global config)")
	assert.Contains(t, out, "max_turns  50  (default)")
}

func TestCLI_ConfigGetUnsetAndErrors(t *testing.T) {
	env := []string{"JAS_HOME=" + t.TempDir()}
	runJas(t, env, "config", "set", "models.my-local.context_window", "32768")
	out, _, code := runJas(t, env, "config", "get", "models.my-local.context_window")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "32768")
	_, _, code = runJas(t, env, "config", "unset", "models.my-local.context_window")
	assert.Eq(t, code, 0)
	_, _, code = runJas(t, env, "config", "get", "models.my-local.context_window")
	assert.Eq(t, code, 1)
	_, errOut, code := runJas(t, env, "config", "set", "approve", "sometimes")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "never|shell|mutating")
	_, _, code = runJas(t, env, "config", "bogus")
	assert.Eq(t, code, 2)
}

func TestCLI_RunUsesStoredConfig(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "stored ok"}})
	home := t.TempDir()
	env := []string{"JAS_HOME=" + home, "MY_PROVIDER_KEY=secret-from-env"}
	for _, args := range [][]string{
		{"config", "set", "model", "z-ai/glm-5.3"},
		{"config", "set", "base_url", url},
		{"auth", "add", "custom", "--env", "MY_PROVIDER_KEY"},
	} {
		_, errOut, code := runJas(t, env, args...)
		if code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut)
		}
	}
	out, errOut, code := runJas(t, env, "run", "task")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assert.Contains(t, out, "stored ok")
}

func TestCLI_RunMissingSettingsAreUsageErrors(t *testing.T) {
	env := []string{"JAS_HOME=" + t.TempDir()}
	_, errOut, code := runJas(t, env, "run", "task")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "jas config set model")
	_, errOut, code = runJas(t, append(env, "JAS_MODEL=m", "JAS_PROVIDER=openrouter"), "run", "task")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "jas auth add openrouter")
}

func TestCLI_AuthOnlyKeyIsRedacted(t *testing.T) {
	const key = "zq9plainstoredvalue77"
	repo := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(repo, "note.txt"), []byte("the key is "+key+"\n"), 0o644))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "shell", Args: `{"command":"cat note.txt"}`}}},
		{Text: "done"},
	})
	home := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"custom":{"key":"`+key+`"}}`), 0o600))
	cmdEnv := []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_BASE_URL=" + url}
	out, errOut, code := runJasIn(t, repo, cmdEnv, "run", "--json", "show note")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assert.NotContains(t, out, key)
	assert.NotContains(t, errOut, key)
	assert.Contains(t, out, "[REDACTED")
	logs, _ := filepath.Glob(filepath.Join(home, "sessions", "*", "events.jsonl"))
	assert.Eq(t, len(logs), 1)
	b, _ := os.ReadFile(logs[0])
	assert.NotContains(t, string(b), key)
}

func TestCLI_AuthCommands(t *testing.T) {
	home := t.TempDir()
	env := []string{"JAS_HOME=" + home, "OR_KEY=x"}
	_, _, code := runJas(t, env, "auth", "add", "openrouter", "--env", "OR_KEY")
	assert.Eq(t, code, 0)
	out, _, code := runJas(t, env, "auth", "list")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "openrouter  env $OR_KEY")
	_, _, code = runJas(t, append(env, "JAS_NO_PLAINTEXT_KEYS=1"), "auth", "add", "ollama")
	assert.Eq(t, code, 2)
	_, _, code = runJas(t, env, "auth", "rotate", "custom")
	assert.Eq(t, code, 1)
	_, _, code = runJas(t, env, "auth", "rm", "openrouter")
	assert.Eq(t, code, 0)
	_, _, code = runJas(t, env, "auth", "add", "bogus", "--env", "X")
	assert.Eq(t, code, 2)
}

func TestCLI_KeylessCustomRunSendsNoAuthorization(t *testing.T) {
	backend, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "keyless ok"}})
	target, _ := url.Parse(backend)
	var sawAuth bool
	proxy := httputil.NewSingleHostReverseProxy(target)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["Authorization"]; ok {
			sawAuth = true
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()
	out, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_BASE_URL=" + srv.URL}, "run", "task")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assert.Contains(t, out, "keyless ok")
	if sawAuth {
		t.Fatal("Authorization header sent without a key")
	}
}

func TestCLI_OpenRouterWithoutKeyExits2(t *testing.T) {
	_, errOut, code := runJas(t, []string{"JAS_MODEL=m", "JAS_PROVIDER=openrouter"}, "run", "task")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "jas auth add openrouter")
}

func TestCLI_BadAuthFileDoesNotBlockEnvKey(t *testing.T) {
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "fine"}})
	home := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{}`), 0o644))
	out, errOut, code := runJas(t, []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_BASE_URL=" + url, "JAS_API_KEY=k"}, "run", "task")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assert.Contains(t, out, "fine")
}

func TestCLI_AuthEnvRejectsSecretAndDoesNotEcho(t *testing.T) {
	const secret = "sk-or-v1-abcdef123456"
	home := t.TempDir()
	_, errOut, code := runJas(t, []string{"JAS_HOME=" + home}, "auth", "add", "openrouter", "--env", secret)
	assert.Eq(t, code, 2)
	assert.NotContains(t, errOut, secret)
	out, _, _ := runJas(t, []string{"JAS_HOME=" + home}, "auth", "list")
	assert.NotContains(t, out, secret)
}

func TestCLI_RotateChecksPlaintextPolicyFirst(t *testing.T) {
	_, errOut, code := runJas(t, []string{"JAS_HOME=" + t.TempDir(), "JAS_NO_PLAINTEXT_KEYS=1"}, "auth", "rotate", "openrouter")
	assert.Eq(t, code, 2)
	assert.Contains(t, errOut, "JAS_NO_PLAINTEXT_KEYS")
}

func TestCLI_ConfigSetRejectsAPIKey(t *testing.T) {
	home := t.TempDir()
	_, _, code := runJas(t, []string{"JAS_HOME=" + home}, "config", "set", "api_key", "sk-secret")
	assert.Eq(t, code, 2)
	if _, err := os.Stat(filepath.Join(home, "config.json")); err == nil {
		t.Fatal("config.json must not be written")
	}
}

func TestCLI_ConfigSetRejectsCredentialURL(t *testing.T) {
	_, errOut, code := runJas(t, []string{"JAS_HOME=" + t.TempDir()}, "config", "set", "base_url", "https://u:pw123@h/v1")
	assert.Eq(t, code, 2)
	assert.NotContains(t, errOut, "pw123")
}

func TestCLI_JasHomeInsideRootIsDenied(t *testing.T) {
	const key = "zq9homeinrootvalue55"
	repo := t.TempDir()
	home := filepath.Join(repo, "state")
	assert.NoErr(t, os.MkdirAll(home, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{"other":{"key":"`+key+`"}}`), 0o600))
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: `{"path":"state/auth.json"}`}}},
		{Text: "done"},
	})
	out, errOut, code := runJasIn(t, repo, []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_BASE_URL=" + url}, "run", "--json", "read auth")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errOut)
	}
	assert.Contains(t, out, "path denied by policy")
	for _, b := range bodies() {
		assert.NotContains(t, string(b), key)
	}
}
