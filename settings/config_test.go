package settings

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestResolve_Precedence(t *testing.T) {
	file := File{Values: map[string]string{"model": "from-file"}}
	env := envOf(map[string]string{"JAS_MODEL": "from-env"})
	flags := map[string]string{"model": "from-flag"}

	got := Resolve(flags, env, Layers{Global: file})["model"]
	assert.Eq(t, got, Value{"from-flag", SourceFlag})
	got = Resolve(nil, env, Layers{Global: file})["model"]
	assert.Eq(t, got, Value{"from-env", Source("env JAS_MODEL")})
	got = Resolve(nil, envOf(nil), Layers{Global: file})["model"]
	assert.Eq(t, got, Value{"from-file", SourceFile})
	got = Resolve(nil, envOf(nil), Layers{})["max_turns"]
	assert.Eq(t, got, Value{"50", SourceDefault})
}

func TestConfig_SetValidates(t *testing.T) {
	_, err := File{}.Set("approve", "sometimes")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "never|shell|mutating")
	if _, err := (File{}).Set("max_turns", "-1"); err == nil {
		t.Fatal("max_turns -1 should fail")
	}
	if _, err := (File{}).Set("nope", "x"); err == nil {
		t.Fatal("unknown key should fail")
	}
}

func TestConfig_ModelOverride(t *testing.T) {
	f, err := File{}.Set("models.my-local.context_window", "32768")
	assert.NoErr(t, err)
	assert.Eq(t, f.Models["my-local"].ContextWindow, 32768)
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	assert.NoErr(t, f.Save(path))
	back, err := LoadFile(path)
	assert.NoErr(t, err)
	assert.Eq(t, back.ContextOverrides()["my-local"], 32768)
	f, err = f.Unset("models.my-local.context_window")
	assert.NoErr(t, err)
	assert.Eq(t, len(f.Models), 0)
}

func TestConfig_ValidatesAllKeys(t *testing.T) {
	cases := []struct {
		key, val string
		ok       bool
	}{
		{"elide_at", "0", false}, {"elide_at", "1.5", false}, {"elide_at", "0.6", true},
		{"cached_token_weight", "0", false}, {"cached_token_weight", "1.5", false},
		{"cached_token_weight", "0.25", true}, {"cached_token_weight", "1", true},
		{"keep_tool_results", "-1", false}, {"keep_tool_results", "0", true},
		{"max_tool_output", "0", false}, {"max_tool_output", "100", true},
		{"provider", "bogus", false}, {"provider", "openrouter", true},
		{"base_url", "not a url", false}, {"base_url", "http://localhost:1/v1", true},
		{"redact_skip_paths", "a/b,c", true},
	}
	for _, c := range cases {
		_, err := File{}.Set(c.key, c.val)
		if (err == nil) != c.ok {
			t.Errorf("%s=%s: err=%v want ok=%v", c.key, c.val, err, c.ok)
		}
	}
}

func TestBuild(t *testing.T) {
	_, err := Resolve(nil, envOf(nil), Layers{}).Build()
	if err == nil {
		t.Fatal("missing model should fail")
	}
	assert.Contains(t, err.Error(), "jas config set model")

	env := envOf(map[string]string{"JAS_MODEL": "m", "JAS_PROVIDER": "ollama", "JAS_REDACT_SKIP_PATHS": "a, b/c"})
	r, err := Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.BaseURL, Presets["ollama"])
	assert.Eq(t, len(r.RedactSkipPaths), 2)

	_, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "m"}), Layers{}).Build()
	if err == nil {
		t.Fatal("custom without base_url should fail")
	}
	assert.Contains(t, err.Error(), "jas config set base_url")

	_, err = Resolve(nil, envOf(map[string]string{"JAS_APPROVE": "bogus"}), Layers{}).Build()
	if err == nil {
		t.Fatal("bad env value should fail")
	}
	assert.Contains(t, err.Error(), "JAS_APPROVE")
}

func TestConfig_RejectsNaNAndCredentialURLs(t *testing.T) {
	for _, v := range []string{"NaN", "nan", "Inf", "+Inf"} {
		if _, err := (File{}).Set("elide_at", v); err == nil {
			t.Errorf("elide_at=%s should fail", v)
		}
	}
	_, err := File{}.Set("base_url", "https://user:hunter2@host/v1")
	if err == nil {
		t.Fatal("userinfo in base_url should fail")
	}
	assert.NotContains(t, err.Error(), "hunter2")
}

func TestKeys_CachedTokenWeightDefault(t *testing.T) {
	env := envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1"})
	r, err := Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.CachedTokenWeight, 0.1)

	env = envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1", "JAS_CACHED_TOKEN_WEIGHT": "0.5"})
	r, err = Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.CachedTokenWeight, 0.5)
}

func TestKeys_CompactDefaults(t *testing.T) {
	env := envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1"})
	r, err := Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.CompactReserve, 16384)
	assert.Eq(t, r.CompactKeep, 20000)
	assert.Eq(t, r.CompactModel, "")

	env = envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1",
		"JAS_COMPACT_RESERVE_TOKENS": "8000", "JAS_COMPACT_KEEP_TOKENS": "4000", "JAS_COMPACT_MODEL": "z-ai/glm-x"})
	r, err = Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.CompactReserve, 8000)
	assert.Eq(t, r.CompactKeep, 4000)
	assert.Eq(t, r.CompactModel, "z-ai/glm-x")

	if _, err := (File{}).Set("compact_keep_tokens", "0"); err == nil {
		t.Fatal("compact_keep_tokens=0 should be rejected")
	}
	_, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1", "JAS_COMPACT_KEEP_TOKENS": "0"}), Layers{}).Build()
	if err == nil {
		t.Fatal("compact_keep_tokens=0 should be rejected")
	}
	assert.Contains(t, err.Error(), "compact_keep_tokens")
	for _, k := range []string{"compact_reserve_tokens", "compact_keep_tokens", "compact_model"} {
		spec, ok := LookupKey(k)
		assert.Eq(t, ok, true)
		assert.Eq(t, spec.Flag, false)
	}
}

func TestConfig_MaxOutputTokensDefault(t *testing.T) {
	r, err := Resolve(nil, envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1"}), Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.MaxOutputTokens, 16000)
	// 0 omits max_tokens, for endpoints that reject it (OpenAI o-series).
	if _, err := (File{}).Set("max_output_tokens", "0"); err != nil {
		t.Fatalf("max_output_tokens=0 should mean omit: %v", err)
	}
	if _, err := (File{}).Set("max_output_tokens", "-1"); err == nil {
		t.Fatal("max_output_tokens=-1 should be rejected")
	}
}

func TestProviders_SetStoredUnset(t *testing.T) {
	f, err := File{}.Set("providers.vllm.base_url", "http://localhost:8000/v1")
	assert.NoErr(t, err)
	v, ok, err := f.Stored("providers.vllm.base_url")
	assert.NoErr(t, err)
	assert.Eq(t, ok, true)
	assert.Eq(t, v, "http://localhost:8000/v1")

	// Round-trips through config.json.
	b, err := json.Marshal(f)
	assert.NoErr(t, err)
	var back File
	assert.NoErr(t, json.Unmarshal(b, &back))
	assert.Eq(t, back.Providers["vllm"], ProviderEntry{BaseURL: "http://localhost:8000/v1"})

	// Bad URL and bad names are rejected.
	if _, err := f.Set("providers.vllm.base_url", "not a url"); err == nil {
		t.Fatal("bad URL should be rejected")
	}
	for _, key := range []string{"providers.a.b.base_url", "providers..base_url", "providers.a/b.base_url", "providers.x.url"} {
		if _, err := f.Set(key, "http://x/v1"); err == nil {
			t.Errorf("%s should be rejected", key)
		}
	}

	// Unset removes the entry.
	f, err = f.Unset("providers.vllm.base_url")
	assert.NoErr(t, err)
	assert.Eq(t, len(f.Providers), 0)
	_, ok, err = f.Stored("providers.vllm.base_url")
	assert.NoErr(t, err)
	assert.Eq(t, ok, false)
}

func TestBuild_QualifiedModel(t *testing.T) {
	f := File{Providers: map[string]ProviderEntry{
		"vllm": {BaseURL: "http://localhost:8000/v1"},
	}}

	// A named provider's URL and its bare model id.
	r, err := Resolve(nil, envOf(map[string]string{"JAS_MODEL": "vllm/qwen3"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "vllm")
	assert.Eq(t, r.BaseURL, "http://localhost:8000/v1")
	assert.Eq(t, r.Model, "qwen3")

	// A preset provider is addressable without configuration; slash ids
	// after the first segment survive.
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "openrouter/z-ai/glm-5.3"}), Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "openrouter")
	assert.Eq(t, r.BaseURL, Presets["openrouter"])
	assert.Eq(t, r.Model, "z-ai/glm-5.3")

	// An unknown first segment stays a bare model id on the default provider.
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "z-ai/glm-5.3", "JAS_BASE_URL": "http://x/v1"}), Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "custom")
	assert.Eq(t, r.Model, "z-ai/glm-5.3")

	// A bare model keeps the flat provider and base_url.
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "qwen3", "JAS_BASE_URL": "http://x/v1"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "custom")
	assert.Eq(t, r.BaseURL, "http://x/v1")

	// A named provider whose stored entry has no URL fails loudly, naming
	// the fix (possible via a hand-edited config.json).
	f2 := File{Providers: map[string]ProviderEntry{"nope": {}}}
	_, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "nope/m"}), Layers{Global: f2}).Build()
	if err == nil {
		t.Fatal("provider without base_url should fail")
	}
	assert.Contains(t, err.Error(), "providers.nope.base_url")
}

func TestSplitModelRef(t *testing.T) {
	known := func(n string) bool { return n == "vllm" }
	p, m, ok := SplitModelRef("vllm/qwen3", known)
	assert.Eq(t, ok, true)
	assert.Eq(t, p, "vllm")
	assert.Eq(t, m, "qwen3")

	// Unknown provider: the whole ref is the model.
	p, m, ok = SplitModelRef("z-ai/glm-5.3", known)
	assert.Eq(t, ok, false)
	assert.Eq(t, p, "")
	assert.Eq(t, m, "z-ai/glm-5.3")

	// Reserved and degenerate refs never split.
	for _, ref := range []string{"default/m", "vllm/", "bare"} {
		if _, _, ok := SplitModelRef(ref, known); ok {
			t.Errorf("%s should not split", ref)
		}
	}
}

func TestConfig_FarmBellKey(t *testing.T) {
	k, ok := LookupKey("farm_bell")
	if !ok || k.Default != "true" {
		t.Fatalf("farm_bell key %+v %v", k, ok)
	}
	if k.Validate("maybe") == nil || k.Validate("false") != nil {
		t.Fatal("farm_bell must accept true/false only")
	}
	assert.Eq(t, len(k.Options), 2)
}

func TestConfig_ClosedSetOptions(t *testing.T) {
	for _, name := range []string{"provider", "approve", "farm_bell", "web_tools"} {
		k, ok := LookupKey(name)
		if !ok || len(k.Options) == 0 {
			t.Fatalf("%s should expose Options for /settings picker", name)
		}
		for _, o := range k.Options {
			if err := k.Validate(o); err != nil {
				t.Fatalf("%s option %q: %v", name, o, err)
			}
		}
	}
	k, _ := LookupKey("model")
	if len(k.Options) != 0 {
		t.Fatal("model is free-text")
	}
}

func TestWebTools_DefaultsAndBuild(t *testing.T) {
	r, err := Resolve(nil, envOf(map[string]string{"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1"}), Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.WebTools, false)
	assert.Eq(t, r.SearchBaseURL, "")
	assert.Eq(t, r.Approve, "never")

	env := envOf(map[string]string{
		"JAS_MODEL": "m", "JAS_BASE_URL": "http://x/v1",
		"JAS_WEB_TOOLS": "true", "JAS_SEARCH_BASE_URL": "http://searx.local",
	})
	r, err = Resolve(nil, env, Layers{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.WebTools, true)
	assert.Eq(t, r.SearchBaseURL, "http://searx.local")
	// web_tools on + default approve → shell (§11)
	assert.Eq(t, r.Approve, "shell")

	if _, err := (File{}).Set("web_tools", "maybe"); err == nil {
		t.Fatal("web_tools=maybe should fail")
	}
	if _, err := (File{}).Set("search_base_url", "not a url"); err == nil {
		t.Fatal("bad search_base_url should fail")
	}
	if _, err := (File{}).Set("search_base_url", ""); err != nil {
		t.Fatalf("empty search_base_url should be ok: %v", err)
	}
}

func TestSessionApprove_WebTools(t *testing.T) {
	// Default never + web off → never
	e := Resolve(nil, envOf(nil), Layers{})
	assert.Eq(t, e.SessionApprove(), "never")

	// Default never + web on → shell
	e = Resolve(nil, envOf(map[string]string{"JAS_WEB_TOOLS": "true"}), Layers{})
	assert.Eq(t, e.SessionApprove(), "shell")

	// Explicit never (env) + web on → keep never
	e = Resolve(nil, envOf(map[string]string{"JAS_WEB_TOOLS": "true", "JAS_APPROVE": "never"}), Layers{})
	assert.Eq(t, e.SessionApprove(), "never")

	// Explicit never (global file) + web on → keep never
	e = Resolve(nil, envOf(map[string]string{"JAS_WEB_TOOLS": "true"}), Layers{
		Global: File{Values: map[string]string{"approve": "never"}},
	})
	assert.Eq(t, e.SessionApprove(), "never")

	// Explicit mutating + web on → keep mutating
	e = Resolve(nil, envOf(map[string]string{"JAS_WEB_TOOLS": "true", "JAS_APPROVE": "mutating"}), Layers{})
	assert.Eq(t, e.SessionApprove(), "mutating")

	// Flag never wins and stays never
	e = Resolve(map[string]string{"approve": "never"}, envOf(map[string]string{"JAS_WEB_TOOLS": "true"}), Layers{})
	assert.Eq(t, e.SessionApprove(), "never")
}

// A configured provider named like an OpenRouter vendor (qwen) still wins
// the first "/" split, but never silently: Build warns with both readings
// and the qualified forms that disambiguate (#96).
func TestBuild_VendorNamedProviderWarns(t *testing.T) {
	f := File{
		Values:    map[string]string{"provider": ProviderOpenRouter},
		Providers: map[string]ProviderEntry{"qwen": {BaseURL: "http://localhost:8000/v1"}},
	}
	r, err := Resolve(nil, envOf(map[string]string{"JAS_MODEL": "qwen/qwen3-coder"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "qwen")
	assert.Eq(t, r.Model, "qwen3-coder")
	assert.Eq(t, len(r.Warnings), 1)
	assert.Contains(t, r.Warnings[0], `providers.qwen`)
	assert.Contains(t, r.Warnings[0], `openrouter/qwen/qwen3-coder`)

	// The qualified form stays on OpenRouter and does not warn.
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "openrouter/qwen/qwen3-coder"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, ProviderOpenRouter)
	assert.Eq(t, r.Model, "qwen/qwen3-coder")
	assert.Eq(t, len(r.Warnings), 0)

	// A provider name that is not an OpenRouter vendor is unambiguous.
	f.Providers = map[string]ProviderEntry{"vllm": {BaseURL: "http://localhost:8000/v1"}}
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "vllm/qwen3"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.Provider, "vllm")
	assert.Eq(t, len(r.Warnings), 0)

	// Off OpenRouter, vendor segments mean nothing: no warning.
	f.Values = map[string]string{"provider": ProviderCustom, "base_url": "http://x/v1"}
	f.Providers = map[string]ProviderEntry{"qwen": {BaseURL: "http://localhost:8000/v1"}}
	r, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "qwen/qwen3-coder"}), Layers{Global: f}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, len(r.Warnings), 0)
}
