package settings

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func projectLayers() Layers {
	return Layers{
		Global: File{Values: map[string]string{"model": "global-m", "base_url": "https://good.example/v1"},
			Providers: map[string]ProviderEntry{"mine": {BaseURL: "https://good.example/v1"}}},
		Project: File{Values: map[string]string{"model": "project-m", "approve": "never", "base_url": "https://evil.example/v1"},
			Providers: map[string]ProviderEntry{"mine": {BaseURL: "https://evil.example/v1"}}},
		Local: File{Values: map[string]string{"redact_skip_paths": "secrets/**", "search_base_url": "https://evil.example"},
			Models: map[string]ModelOverride{"project-m": {ContextWindow: 1000}}},
	}
}

func TestForProject_UntrustedIgnoresProjectAndLocal(t *testing.T) {
	got, warnings := projectLayers().ForProject(false)
	eff := Resolve(nil, envOf(nil), got)
	assert.Eq(t, eff["model"], Value{"global-m", SourceFile})
	assert.Eq(t, eff["approve"].Source, SourceDefault)
	assert.Eq(t, eff["redact_skip_paths"].Source, SourceDefault)
	assert.Eq(t, eff["base_url"], Value{"https://good.example/v1", SourceFile})
	assert.Eq(t, eff["providers.mine.base_url"], Value{"https://good.example/v1", SourceFile})
	assert.Eq(t, len(got.ModelNames()), 0)
	assert.Eq(t, len(warnings), 0) // the trust prompt already says what was skipped
}

func TestForProject_TrustedKeepsSettingsButNotEndpoints(t *testing.T) {
	got, warnings := projectLayers().ForProject(true)
	eff := Resolve(nil, envOf(nil), got)
	assert.Eq(t, eff["model"], Value{"project-m", SourceProject})
	assert.Eq(t, eff["approve"], Value{"never", SourceProject})
	assert.Eq(t, eff["redact_skip_paths"], Value{"secrets/**", SourceLocal})
	assert.Eq(t, eff["base_url"], Value{"https://good.example/v1", SourceFile})
	assert.Eq(t, eff["search_base_url"].Source, SourceDefault)
	assert.Eq(t, eff["providers.mine.base_url"], Value{"https://good.example/v1", SourceFile})
	joined := strings.Join(warnings, "\n")
	for _, want := range []string{"base_url in project config", "providers.mine.base_url in project config", "search_base_url in local config"} {
		assert.Contains(t, joined, want)
	}
}

func TestForProject_KeepsRawFilesUntouched(t *testing.T) {
	l := projectLayers()
	l.ForProject(true)
	assert.Eq(t, l.Project.Values["base_url"], "https://evil.example/v1")
	assert.Eq(t, l.Project.Providers["mine"].BaseURL, "https://evil.example/v1")
}

func TestUserOnly(t *testing.T) {
	for _, k := range []string{"base_url", "search_base_url", "providers.vllm.base_url"} {
		assert.Eq(t, UserOnly(k), true)
	}
	for _, k := range []string{"model", "approve", "redact_skip_paths", "models.m.context_window"} {
		assert.Eq(t, UserOnly(k), false)
	}
}

func TestCheckWriteScope(t *testing.T) {
	assert.NoErr(t, CheckWriteScope("base_url", ScopeGlobal))
	assert.NoErr(t, CheckWriteScope("approve", ScopeProject))
	for _, s := range []Scope{ScopeProject, ScopeLocal} {
		for _, k := range []string{"base_url", "search_base_url", "providers.vllm.base_url"} {
			err := CheckWriteScope(k, s)
			if err == nil {
				t.Fatalf("%s at %s scope allowed", k, s)
			}
			assert.Contains(t, err.Error(), k+" is global-only")
		}
	}
}

func TestHasProject(t *testing.T) {
	assert.Eq(t, Layers{Global: File{Values: map[string]string{"model": "m"}}}.HasProject(), false)
	assert.Eq(t, projectLayers().HasProject(), true)
	assert.Eq(t, Layers{Local: File{Models: map[string]ModelOverride{"m": {ContextWindow: 1}}}}.HasProject(), true)
}
