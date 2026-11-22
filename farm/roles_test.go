package farm

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func writable(rules []core.WriteRule, path string) bool {
	for _, r := range rules {
		ok := len(r.All) > 0
		for _, g := range r.All {
			if m, _ := doublestar.Match(g, path); !m {
				ok = false
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func TestRoles_TesterUnavailableWithoutTestGlobs(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	avail, unavail := Available(cfg)
	if _, ok := avail["tester"]; ok {
		t.Fatal("tester should be unavailable")
	}
	assert.Contains(t, unavail["tester"], "test_globs")
	if _, ok := avail["coder"]; !ok {
		t.Fatal("coder should be available")
	}
}

func TestRoles_TesterWritesIntersect(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module x\n")
	cfg, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	r, err := ResolveRole(cfg, "tester", []string{"core/**"})
	assert.NoErr(t, err)
	assert.Eq(t, writable(r.Writes, "core/a_test.go"), true)
	assert.Eq(t, writable(r.Writes, "core/a.go"), false)
	assert.Eq(t, writable(r.Writes, "cmd/a_test.go"), false)
}

func TestRoles_CoderWritesOwns(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	r, err := ResolveRole(cfg, "coder", []string{"core/**", "docs/*.md"})
	assert.NoErr(t, err)
	assert.Eq(t, len(r.Writes), 2)
	assert.Eq(t, writable(r.Writes, "docs/a.md"), true)
	assert.Eq(t, writable(r.Writes, "cmd/a.go"), false)
	assert.Eq(t, r.Budget, cfg.Budget.PerWorkerTokens)
}

func TestRoles_ProjectReplacesBuiltin(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "roles:\n  coder:\n    tools: [read]\n")
	cfg, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	r, err := ResolveRole(cfg, "coder", []string{"a/**"})
	assert.NoErr(t, err)
	assert.Eq(t, len(r.Tools), 1)
	assert.Eq(t, r.Tools[0], "read")
}

func TestRoles_PromptKeepsDollarBraces(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "roles:\n  coder:\n    prompt: 'echo ${HOME}'\n")
	cfg, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	r, err := ResolveRole(cfg, "coder", []string{"a/**"})
	assert.NoErr(t, err)
	assert.Eq(t, r.Prompt, "echo ${HOME}")
}

func TestRoles_UnknownRoleAndUnavailable(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	if _, err := ResolveRole(cfg, "nope", nil); err == nil {
		t.Fatal("want error for unknown role")
	}
	if _, err := ResolveRole(cfg, "tester", []string{"a/**"}); err == nil {
		t.Fatal("want error for unavailable tester")
	}
}

func TestRoles_DigestStable(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "go.mod"), "module x\n")
	a, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	b, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	assert.Eq(t, RolesDigest(a), RolesDigest(b))
	write(t, filepath.Join(root, ".jas", "farm.yaml"), "roles:\n  coder:\n    prompt: hi\n")
	c, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	if RolesDigest(a) == RolesDigest(c) {
		t.Fatal("digest should change with prompt")
	}
}

func TestRoles_UnknownToolMakesRoleUnavailable(t *testing.T) {
	cfg, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	cfg.Roles = map[string]RoleDef{"gated": {Tools: []string{"read", "find"}, Approve: "shell"}}
	_, unavail := Available(cfg)
	assert.Eq(t, unavail["gated"], `unknown tool "find"`)
	_, err = ResolveRole(cfg, "gated", []string{"a/**"})
	if err == nil || !strings.Contains(err.Error(), `unknown tool "find"`) {
		t.Fatalf("ResolveRole: %v", err)
	}
}

// The manual lists the tools a role may name; a new built-in tool must be
// added there (docs/manual.md, roles.<name>).
func TestRoles_BuiltinToolsMatchManual(t *testing.T) {
	var names []string
	for n := range builtinByName() {
		names = append(names, n)
	}
	sort.Strings(names)
	assert.Eq(t, strings.Join(names, ","), "edit,grep,ls,read,shell,write")
}
