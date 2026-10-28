package settings

import (
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

	got := Resolve(flags, env, file)["model"]
	assert.Eq(t, got, Value{"from-flag", SourceFlag})
	got = Resolve(nil, env, file)["model"]
	assert.Eq(t, got, Value{"from-env", Source("env JAS_MODEL")})
	got = Resolve(nil, envOf(nil), file)["model"]
	assert.Eq(t, got, Value{"from-file", SourceFile})
	got = Resolve(nil, envOf(nil), File{})["max_turns"]
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
	_, err := Resolve(nil, envOf(nil), File{}).Build()
	if err == nil {
		t.Fatal("missing model should fail")
	}
	assert.Contains(t, err.Error(), "jas config set model")

	env := envOf(map[string]string{"JAS_MODEL": "m", "JAS_PROVIDER": "ollama", "JAS_REDACT_SKIP_PATHS": "a, b/c"})
	r, err := Resolve(nil, env, File{}).Build()
	assert.NoErr(t, err)
	assert.Eq(t, r.BaseURL, Presets["ollama"])
	assert.Eq(t, len(r.RedactSkipPaths), 2)

	_, err = Resolve(nil, envOf(map[string]string{"JAS_MODEL": "m"}), File{}).Build()
	if err == nil {
		t.Fatal("custom without base_url should fail")
	}
	assert.Contains(t, err.Error(), "jas config set base_url")

	_, err = Resolve(nil, envOf(map[string]string{"JAS_APPROVE": "bogus"}), File{}).Build()
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
