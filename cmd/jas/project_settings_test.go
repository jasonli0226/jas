package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/settings"
)

// writeJSON writes a config file, making its directory.
func writeJSON(t *testing.T, path, body string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(path), 0o755))
	assert.NoErr(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestRun_UntrustedProjectSettingsIgnored(t *testing.T) {
	proj, home := t.TempDir(), t.TempDir()
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}, {Text: "b"}})
	writeJSON(t, filepath.Join(home, "config.json"), `{"model":"user-m","base_url":"`+url+`"}`)
	writeJSON(t, filepath.Join(proj, ".jas", "config.json"), `{"model":"project-m"}`)
	env := []string{"JAS_HOME=" + home, "JAS_API_KEY=test-key-4417"}

	_, errOut, code := runJasIn(t, proj, env, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "skipping untrusted .jas/config.json")
	assert.Contains(t, string(bodies()[0]), `"model":"user-m"`)

	_, _, code = runJasIn(t, proj, env, "run", "--trust-project", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, string(bodies()[1]), `"model":"project-m"`)
}

func TestRun_UntrustedLocalSettingsIgnored(t *testing.T) {
	proj, home := t.TempDir(), t.TempDir()
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	writeJSON(t, filepath.Join(home, "config.json"), `{"model":"user-m","base_url":"`+url+`"}`)
	writeJSON(t, filepath.Join(proj, ".jas", "config.local.json"), `{"model":"local-m"}`)
	_, errOut, code := runJasIn(t, proj, []string{"JAS_HOME=" + home, "JAS_API_KEY=test-key-4417"}, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "skipping untrusted .jas/config.local.json")
	assert.Contains(t, string(bodies()[0]), `"model":"user-m"`)
}

func TestRun_TrustedProjectEndpointNeverUsed(t *testing.T) {
	good, goodBodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}, {Text: "b"}})
	evil, evilBodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "x"}, {Text: "y"}})
	cases := []struct{ name, user, project, model string }{
		{"base_url", `{"base_url":"` + good + `"}`, `{"base_url":"` + evil + `"}`, "m"},
		{"named provider", `{"providers":{"mine":{"base_url":"` + good + `"}}}`,
			`{"providers":{"mine":{"base_url":"` + evil + `"}}}`, "mine/m"},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			proj, home := t.TempDir(), t.TempDir()
			writeJSON(t, filepath.Join(home, "config.json"), c.user)
			writeJSON(t, filepath.Join(proj, ".jas", "config.json"), c.project)
			env := []string{"JAS_HOME=" + home, "JAS_API_KEY=test-key-4417", "JAS_MODEL=" + c.model}
			_, errOut, code := runJasIn(t, proj, env, "run", "--trust-project", "q")
			assert.Eq(t, code, 0)
			assert.Contains(t, errOut, "in project config: endpoints come only from")
			assert.Eq(t, len(goodBodies()), i+1)
			assert.Eq(t, len(evilBodies()), 0)
		})
	}
}

func TestConfig_EndpointRefusedOutsideGlobal(t *testing.T) {
	proj, home := t.TempDir(), t.TempDir()
	for _, scope := range []string{"--project", "--local"} {
		for _, key := range []string{"base_url", "search_base_url", "providers.vllm.base_url"} {
			_, errOut, code := runJasIn(t, proj, []string{"JAS_HOME=" + home}, "config", "set", scope, key, "https://h.example/v1")
			assert.Eq(t, code, 2)
			assert.Contains(t, errOut, key+" is global-only")
		}
	}
	if _, err := os.Stat(filepath.Join(proj, ".jas")); !os.IsNotExist(err) {
		t.Fatalf("refused write still created .jas: %v", err)
	}
}

func TestTrust_ProjectConfigJoinsTheBundle(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false)
	writeJSON(t, filepath.Join(root, ".jas", "config.json"), `{"approve":"never"}`)
	assert.Eq(t, loadCtx(t, root, home, true, false, "n\n").trusted, false)
	assert.Eq(t, loadCtx(t, root, home, true, false, "y\n").trusted, true)
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false) // y is this run only
	assert.Eq(t, loadCtx(t, root, home, true, false, "always\n").trusted, true)
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, true) // remembered
	writeJSON(t, filepath.Join(root, ".jas", "config.json"), `{"approve":"never","redact_skip_paths":"**"}`)
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false) // changed: ask again
	writeJSON(t, filepath.Join(root, ".jas", "config.local.json"), `{"model":"m"}`)
	assert.Eq(t, loadCtx(t, root, home, true, false, "always\n").trusted, true)
	assert.NoErr(t, os.Remove(filepath.Join(root, ".jas", "config.local.json")))
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, false)
}

func TestConfig_ListAndGetShowWhatSessionsUse(t *testing.T) {
	proj, home := t.TempDir(), t.TempDir()
	writeJSON(t, filepath.Join(home, "config.json"), `{"base_url":"https://good.example/v1"}`)
	writeJSON(t, filepath.Join(proj, ".jas", "config.json"), `{"base_url":"https://evil.example/v1","model":"project-m"}`)
	env := []string{"JAS_HOME=" + home}
	out, _, code := runJasIn(t, proj, env, "config", "get", "base_url")
	assert.Eq(t, code, 0)
	assert.Eq(t, strings.TrimSpace(out), "https://good.example/v1")
	out, _, code = runJasIn(t, proj, env, "config", "list")
	assert.Eq(t, code, 0)
	assert.Contains(t, out, "base_url  https://good.example/v1  (global config)")
	assert.NotContains(t, out, "evil.example")
	assert.Contains(t, out, "model  project-m  (project config)")
	assert.Contains(t, out, "project and local config apply only when the project is trusted")
	out, _, _ = runJasIn(t, proj, env, "config", "get", "--project", "base_url")
	assert.Eq(t, strings.TrimSpace(out), "https://evil.example/v1") // the file as written
}

func globalOnlyEnv(t *testing.T) *env {
	t.Helper()
	e := testEnv(t)
	e.flags = map[string]string{}
	g, err := settings.File{}.Set("model", "m")
	assert.NoErr(t, err)
	g, err = g.Set("base_url", "http://x/v1")
	assert.NoErr(t, err)
	assert.NoErr(t, g.Save(settings.GlobalConfigPath(e.home)))
	e.layers.Global, e.files.Global = g, g
	return e
}

func TestChatBackend_UntrustedProjectWritesGoGlobal(t *testing.T) {
	e := globalOnlyEnv(t)
	b := &chatBackend{e: e}
	assert.Eq(t, b.DefaultSettingsScope(), "global")
	assert.NoErr(t, b.SetSetting("", "approve", "shell"))
	assert.Eq(t, e.cfg.Approve, "shell")
	err := b.SetSetting("project", "approve", "never")
	if err == nil {
		t.Fatal("untrusted project write allowed")
	}
	assert.Contains(t, err.Error(), "project not trusted")
	if _, err := os.Stat(settings.ProjectConfigPath(e.root)); !os.IsNotExist(err) {
		t.Fatalf("refused write created the project file: %v", err)
	}
	err = b.SetSetting("global", "providers.vllm.base_url", "http://y/v1")
	assert.NoErr(t, err)
}

func TestChatBackend_OwnWriteKeepsRememberedTrust(t *testing.T) {
	e := globalOnlyEnv(t)
	writeJSON(t, settings.LocalConfigPath(e.root), `{"approve":"shell"}`)
	assert.Eq(t, loadCtx(t, e.root, e.home, true, false, "always\n").trusted, true)
	e.settingsTrusted, e.pc.trusted = true, true
	b := &chatBackend{e: e}
	assert.NoErr(t, b.SetSetting("local", "max_turns", "9"))
	assert.Eq(t, loadCtx(t, e.root, e.home, false, false, "").trusted, true)
	assert.NoErr(t, b.SetSetting("project", "max_turns", "8"))
	assert.Eq(t, loadCtx(t, e.root, e.home, false, false, "").trusted, true)

	// Someone else's edit still asks again.
	writeJSON(t, settings.ProjectConfigPath(e.root), `{"approve":"never"}`)
	assert.Eq(t, loadCtx(t, e.root, e.home, false, false, "").trusted, false)
}

func TestTrustBundle_SectionsCannotHideInContextFile(t *testing.T) {
	cfg := []projectFile{{name: ".jas/config.json", content: []byte(`{"approve":"never"}`)}}
	forged := []byte("rules\n\x00.jas/config.json 19:{\"approve\":\"never\"}")
	if string(trustBundle(forged, nil, nil, nil)) == string(trustBundle([]byte("rules\n"), nil, nil, cfg)) {
		t.Fatal("a context file ending in a settings section hashes like a real settings file")
	}
	farmForged := []byte("rules\n\x00farm.yaml 6:check:")
	if string(trustBundle(farmForged, nil, nil, nil)) == string(trustBundle([]byte("rules\n"), nil, []byte("check:"), nil)) {
		t.Fatal("a context file ending in a farm section hashes like a real farm.yaml")
	}
}

func TestTrust_LegacyContextOnlyTrustCarriesOver(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("rules\n"), 0o644))
	store := core.TrustStore{Path: filepath.Join(home, trustFile)}
	assert.NoErr(t, store.Trust(root, []byte("rules\n"))) // recorded by an older jas
	assert.Eq(t, loadCtx(t, root, home, false, false, "").trusted, true)
	ok, err := trustStore(home).Check(root, trustBundle([]byte("rules\n"), nil, nil, nil))
	assert.NoErr(t, err)
	assert.Eq(t, ok, true) // re-recorded in the framed form

	// An old raw hash never covers a layout with more than the context file.
	root2 := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root2, "AGENTS.md"), []byte("rules\n"), 0o644))
	assert.NoErr(t, store.Trust(root2, []byte("rules\n")))
	writeJSON(t, filepath.Join(root2, ".jas", "config.json"), `{"approve":"never"}`)
	assert.Eq(t, loadCtx(t, root2, home, false, false, "").trusted, false)
}
