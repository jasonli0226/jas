package settings

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestResolve_LayerPrecedence(t *testing.T) {
	layers := Layers{
		Global:  File{Values: map[string]string{"model": "global-m", "approve": "never", "max_turns": "10"}},
		Project: File{Values: map[string]string{"model": "project-m", "approve": "shell"}},
		Local:   File{Values: map[string]string{"model": "local-m"}},
	}
	env := envOf(map[string]string{"JAS_APPROVE": "mutating"})
	flags := map[string]string{"max_turns": "3"}

	got := Resolve(flags, env, layers)
	assert.Eq(t, got["model"], Value{"local-m", SourceLocal})
	assert.Eq(t, got["approve"], Value{"mutating", Source("env JAS_APPROVE")})
	assert.Eq(t, got["max_turns"], Value{"3", SourceFlag})

	// Without flag/env: local > project > global.
	got = Resolve(nil, envOf(nil), layers)
	assert.Eq(t, got["model"], Value{"local-m", SourceLocal})
	assert.Eq(t, got["approve"], Value{"shell", SourceProject})
	assert.Eq(t, got["max_turns"], Value{"10", SourceFile})

	// Drop local: project wins for model.
	layers.Local = File{}
	got = Resolve(nil, envOf(nil), layers)
	assert.Eq(t, got["model"], Value{"project-m", SourceProject})
}

func TestResolve_ProviderOverlay(t *testing.T) {
	layers := Layers{
		Global:  File{Providers: map[string]ProviderEntry{"vllm": {BaseURL: "http://global/v1"}}},
		Project: File{Providers: map[string]ProviderEntry{"vllm": {BaseURL: "http://project/v1"}}},
		Local:   File{Providers: map[string]ProviderEntry{"vllm": {BaseURL: "http://local/v1"}, "other": {BaseURL: "http://other/v1"}}},
	}
	got := Resolve(nil, envOf(nil), layers)
	assert.Eq(t, got["providers.vllm.base_url"], Value{"http://local/v1", SourceLocal})
	assert.Eq(t, got["providers.other.base_url"], Value{"http://other/v1", SourceLocal})

	layers.Local = File{}
	got = Resolve(nil, envOf(nil), layers)
	assert.Eq(t, got["providers.vllm.base_url"], Value{"http://project/v1", SourceProject})
}

func TestLoadLayers_RoundTrip(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	g, err := File{}.Set("model", "g")
	assert.NoErr(t, err)
	assert.NoErr(t, g.Save(GlobalConfigPath(home)))
	p, err := File{}.Set("approve", "shell")
	assert.NoErr(t, err)
	assert.NoErr(t, p.Save(ProjectConfigPath(root)))
	l, err := File{}.Set("max_turns", "7")
	assert.NoErr(t, err)
	assert.NoErr(t, l.Save(LocalConfigPath(root)))

	layers, err := LoadLayers(home, root)
	assert.NoErr(t, err)
	eff := Resolve(nil, envOf(nil), layers)
	assert.Eq(t, eff["model"].Raw, "g")
	assert.Eq(t, eff["model"].Source, SourceFile)
	assert.Eq(t, eff["approve"].Raw, "shell")
	assert.Eq(t, eff["approve"].Source, SourceProject)
	assert.Eq(t, eff["max_turns"].Raw, "7")
	assert.Eq(t, eff["max_turns"].Source, SourceLocal)
}

func TestScope_Path(t *testing.T) {
	home, root := "/h", "/r"
	p, err := ScopeGlobal.Path(home, root)
	assert.NoErr(t, err)
	assert.Eq(t, p, filepath.Join(home, "config.json"))
	p, err = ScopeProject.Path(home, root)
	assert.NoErr(t, err)
	assert.Eq(t, p, filepath.Join(root, ".jas", "config.json"))
	p, err = ScopeLocal.Path(home, root)
	assert.NoErr(t, err)
	assert.Eq(t, p, filepath.Join(root, ".jas", "config.local.json"))
	_, err = ScopeProject.Path(home, "")
	if err == nil {
		t.Fatal("project scope without root should fail")
	}
}

func TestLayers_ContextOverrides(t *testing.T) {
	layers := Layers{
		Global:  File{Models: map[string]ModelOverride{"a": {ContextWindow: 1}, "b": {ContextWindow: 2}}},
		Project: File{Models: map[string]ModelOverride{"b": {ContextWindow: 20}}},
		Local:   File{Models: map[string]ModelOverride{"c": {ContextWindow: 3}}},
	}
	got := layers.ContextOverrides()
	assert.Eq(t, got["a"], 1)
	assert.Eq(t, got["b"], 20)
	assert.Eq(t, got["c"], 3)
	w, src, ok := layers.ModelSource("b")
	assert.Eq(t, ok, true)
	assert.Eq(t, w, 20)
	assert.Eq(t, src, SourceProject)
}

func TestParseScope(t *testing.T) {
	s, err := ParseScope("local")
	assert.NoErr(t, err)
	assert.Eq(t, s, ScopeLocal)
	_, err = ParseScope("team")
	if err == nil {
		t.Fatal("want error")
	}
}

func TestLoadLayers_MissingRootIsGlobalOnly(t *testing.T) {
	home := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "config.json"), []byte(`{"model":"m"}`+"\n"), 0o600))
	layers, err := LoadLayers(home, "")
	assert.NoErr(t, err)
	assert.Eq(t, layers.Global.Values["model"], "m")
	assert.Eq(t, len(layers.Project.Values)+len(layers.Local.Values), 0)
}
