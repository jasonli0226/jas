package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func authPath(t *testing.T) string { return filepath.Join(t.TempDir(), "auth.json") }

func TestAuth_FileMode0600(t *testing.T) {
	p := authPath(t)
	assert.NoErr(t, AuthAdd(p, "openrouter", AuthEntry{Key: "k1"}, envOf(nil)))
	info, err := os.Stat(p)
	assert.NoErr(t, err)
	assert.Eq(t, info.Mode().Perm(), os.FileMode(0o600))
	assert.NoErr(t, os.Chmod(p, 0o644))
	_, err = LoadAuth(p)
	if err == nil {
		t.Fatal("want permission error")
	}
	assert.Contains(t, err.Error(), "chmod 600 "+p)
}

func TestAuth_ResolveOrder(t *testing.T) {
	auth := map[string]AuthEntry{
		"openrouter": {Key: "stored"},
		"custom":     {Env: "MY_KEY"},
	}
	k, src, err := ResolveKey("openrouter", envOf(map[string]string{"JAS_API_KEY": "envkey"}), auth)
	assert.NoErr(t, err)
	assert.Eq(t, k, "envkey")
	assert.Eq(t, src, Source("env JAS_API_KEY"))

	k, _, err = ResolveKey("custom", envOf(map[string]string{"MY_KEY": "viaref"}), auth)
	assert.NoErr(t, err)
	assert.Eq(t, k, "viaref")

	k, _, err = ResolveKey("openrouter", envOf(nil), auth)
	assert.NoErr(t, err)
	assert.Eq(t, k, "stored")

	k, _, err = ResolveKey("ollama", envOf(nil), nil)
	assert.NoErr(t, err)
	assert.Eq(t, k, "")

	_, _, err = ResolveKey("openrouter", envOf(nil), nil)
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "jas auth add openrouter")
}

func TestAuth_ListNeverPrintsValues(t *testing.T) {
	p := authPath(t)
	assert.NoErr(t, AuthAdd(p, "openrouter", AuthEntry{Key: "sk-super-secret"}, envOf(nil)))
	assert.NoErr(t, AuthAdd(p, "custom", AuthEntry{Env: "FOO_KEY"}, envOf(nil)))
	m, err := LoadAuth(p)
	assert.NoErr(t, err)
	out := strings.Join(AuthList(m), "\n")
	assert.NotContains(t, out, "sk-super-secret")
	assert.Contains(t, out, "openrouter")
	assert.Contains(t, out, "FOO_KEY")
}

func TestAuth_NoPlaintextKeys(t *testing.T) {
	p := authPath(t)
	env := envOf(map[string]string{"JAS_NO_PLAINTEXT_KEYS": "1"})
	if err := AuthAdd(p, "openrouter", AuthEntry{Key: "k"}, env); err == nil {
		t.Fatal("literal key should be refused")
	}
	assert.NoErr(t, AuthAdd(p, "openrouter", AuthEntry{Env: "OR_KEY"}, env))
}

func TestAuth_RotateReplacesKey(t *testing.T) {
	p := authPath(t)
	assert.NoErr(t, AuthAdd(p, "openrouter", AuthEntry{Key: "old-key"}, envOf(nil)))
	assert.NoErr(t, AuthRotate(p, "openrouter", "new-key", envOf(nil)))
	m, err := LoadAuth(p)
	assert.NoErr(t, err)
	k, _, err := ResolveKey("openrouter", envOf(nil), m)
	assert.NoErr(t, err)
	assert.Eq(t, k, "new-key")
	b, _ := os.ReadFile(p)
	assert.NotContains(t, string(b), "old-key")
	info, _ := os.Stat(p)
	assert.Eq(t, info.Mode().Perm(), os.FileMode(0o600))
	if err := AuthRotate(p, "nobody", "x", envOf(nil)); err == nil {
		t.Fatal("rotate unknown provider should fail")
	}
}
