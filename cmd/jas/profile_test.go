package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

// writeProfile creates <jasHome>/profiles/<name>/ with the given files.
func writeProfile(t *testing.T, jasHome, name string, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(jasHome, "profiles", name)
	assert.NoErr(t, os.MkdirAll(dir, 0o700))
	for f, body := range files {
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, f), []byte(body), 0o600))
	}
	return dir
}

// sessionStarted returns the session_started line of a --json run.
func sessionStarted(t *testing.T, out string) map[string]any {
	t.Helper()
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		if m["type"] == "session_started" {
			return m
		}
	}
	t.Fatalf("no session_started in %q", out)
	return nil
}

func TestRun_ProfileFlagUnknown(t *testing.T) {
	userHome := t.TempDir()
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "--profile", "nope", "q")
	assert.Eq(t, code, core.ExitUsage)
	assert.Contains(t, errOut, `unknown profile "nope"`)
	assert.Contains(t, errOut, "default")
}

func TestRun_ProfileEnvUnknown(t *testing.T) {
	userHome := t.TempDir()
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, errOut, code := runJas(t, append(jasEnv(userHome, url), "JAS_PROFILE=nope"), "run", "q")
	assert.Eq(t, code, core.ExitUsage)
	assert.Contains(t, errOut, `unknown profile "nope"`)
	assert.Contains(t, errOut, "default")
}

func TestRun_ProfileFromConfigUnknownFallsBack(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	assert.NoErr(t, os.MkdirAll(jasHome, 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(jasHome, "config.json"), []byte(`{"profile":"ds"}`), 0o600))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, errOut, code := runJas(t, jasEnv(userHome, url), "run", "--json", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "not found; using default")
	assert.Eq(t, sessionStarted(t, out)["profile"], any("default"))
}

func TestRun_ProfileAgentsBeforeProject(t *testing.T) {
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	writeProfile(t, jasHome, "default", map[string]string{"AGENTS.md": "PROFILE-7781"})
	proj := t.TempDir()
	writeAgents(t, proj, "PROJECT-7782")
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJasIn(t, proj, jasEnv(userHome, url), "run", "--json", "--trust-project", "q")
	assert.Eq(t, code, 0)
	sys := systemPrompt(t, bodies()[0])
	pi, qi := strings.Index(sys, "PROFILE-7781"), strings.Index(sys, "PROJECT-7782")
	if pi < 0 || qi < 0 || pi > qi {
		t.Fatalf("profile at %d, project at %d in %q", pi, qi, sys)
	}
	assert.Contains(t, sys, "Profile instructions (AGENTS.md):")
	cf := sessionStarted(t, out)["context_files"].([]any)
	assert.Eq(t, len(cf), 2)
	first := cf[0].(map[string]any)
	assert.Eq(t, first["loaded"], any(true))
	assert.Contains(t, first["path"].(string), filepath.Join("profiles", "default", "AGENTS.md"))
}

func TestRun_ProfileSettingsApply(t *testing.T) {
	userHome := t.TempDir()
	writeProfile(t, filepath.Join(userHome, ".jas"), "ds", map[string]string{"config.json": `{"approve":"shell"}`})
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJas(t, jasEnv(userHome, url), "run", "--json", "--profile", "ds", "q")
	assert.Eq(t, code, 0)
	assert.Eq(t, sessionStarted(t, out)["approve"], any("shell"))
}

func TestRun_ProfileEndpointIgnored(t *testing.T) {
	userHome := t.TempDir()
	writeProfile(t, filepath.Join(userHome, ".jas"), "default", map[string]string{"config.json": `{"base_url":"http://127.0.0.1:1"}`})
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, errOut, code := runJas(t, jasEnv(userHome, url), "run", "--json", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "ignoring base_url in profile config (default)")
	assert.Eq(t, sessionStarted(t, out)["base_url"], any(url))
}

func TestRun_SessionStartedHasProfile(t *testing.T) {
	userHome := t.TempDir()
	writeProfile(t, filepath.Join(userHome, ".jas"), "ds", nil)
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJas(t, jasEnv(userHome, url), "run", "--json", "--profile", "ds", "q")
	assert.Eq(t, code, 0)
	assert.Eq(t, sessionStarted(t, out)["profile"], any("ds"))
}

func TestRun_ReadOnlyHomeNoDefault(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	userHome := t.TempDir()
	jasHome := filepath.Join(userHome, ".jas")
	assert.NoErr(t, os.MkdirAll(filepath.Join(jasHome, "sessions"), 0o700)) // the session log still needs a home
	assert.NoErr(t, os.Chmod(jasHome, 0o500))
	t.Cleanup(func() { _ = os.Chmod(jasHome, 0o700) })
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, errOut, code := runJas(t, jasEnv(userHome, url), "run", "--json", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, errOut, "profile")
	assert.Eq(t, sessionStarted(t, out)["profile"], any("default"))
	if _, err := os.Stat(filepath.Join(jasHome, "profiles")); err == nil {
		t.Fatal("profiles/ created in a read-only home")
	}
}

func TestRun_ProfileSkillsMovedHome(t *testing.T) {
	userHome, jasHome := t.TempDir(), t.TempDir() // JAS_HOME outside $HOME, as in CI
	loc := writeSkill(t, filepath.Join(jasHome, "profiles", "ds", "skills"), "demo", "Use when demoing", "MOVED-BODY-6610\n")
	args, err := json.Marshal(map[string]string{"path": loc})
	assert.NoErr(t, err)
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(args)}}},
		{Text: "done"},
	})
	env := append(jasEnv(userHome, url), "JAS_HOME="+jasHome)
	_, errOut, code := runJas(t, env, "run", "--profile", "ds", "q")
	assert.Eq(t, code, 0)
	first := systemPrompt(t, bodies()[0])
	assert.Contains(t, first, "<location>"+loc+"</location>")
	assert.Contains(t, string(bodies()[1]), "MOVED-BODY-6610")
	assert.NotContains(t, errOut, "denied")
}

func TestLoadProfile_Sources(t *testing.T) {
	home := t.TempDir()
	writeProfile(t, home, "ds", map[string]string{"config.json": `{"approve":"shell"}`, "AGENTS.md": "DS-RULES"})
	ps, w, err := loadProfile(home, "ds", "global config")
	assert.NoErr(t, err)
	assert.Eq(t, len(w), 0)
	assert.Eq(t, ps.name, "ds")
	assert.Eq(t, ps.layer.Values["approve"], "shell")
	assert.Eq(t, string(ps.agents), "DS-RULES")
	assert.Eq(t, ps.skillsDir, filepath.Join(home, "profiles", "ds", "skills"))

	ps, w, err = loadProfile(home, "nope", "global config")
	assert.NoErr(t, err)
	assert.Eq(t, ps.name, "default")
	assert.Eq(t, len(w), 1)
	assert.Contains(t, w[0], `profile "nope" (from global config) not found; using default`)

	_, _, err = loadProfile(home, "nope", "flag")
	assert.Contains(t, err.Error(), `unknown profile "nope" (from flag): have default, ds`)

	ps, w, err = loadProfile(home, "default", "default") // missing default: empty, no warning
	assert.NoErr(t, err)
	assert.Eq(t, ps.name, "default")
	assert.Eq(t, len(w), 0)
	assert.Eq(t, len(ps.agents), 0)
}
