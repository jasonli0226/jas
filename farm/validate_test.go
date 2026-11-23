package farm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func planCfg(t *testing.T) Config {
	t.Helper()
	root := t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644))
	cfg, _, err := LoadConfig(root, t.TempDir(), true)
	assert.NoErr(t, err)
	cfg.Check = "go test ./..."
	return cfg
}

func mustPlan(t *testing.T, y string) Plan {
	t.Helper()
	p, err := ParsePlan([]byte(y))
	assert.NoErr(t, err)
	return p
}

func part(id, role, owns, after string) string {
	s := "  - id: " + id + "\n    role: " + role + "\n    goal: g\n"
	if owns != "" {
		s += "    owns: [" + owns + "]\n"
	}
	if after != "" {
		s += "    after: [" + after + "]\n"
	}
	return s
}

func plan(parts ...string) string { return "task: t\nparts:\n" + strings.Join(parts, "") }

var baseFiles = []string{"core/a.go", "core/store/s.go", "cmd/main.go"}

func TestValidate_Problems(t *testing.T) {
	cfg := planCfg(t)
	cfg.Roles = map[string]RoleDef{"broken": {Tools: []string{"write"}, Writes: []string{"${nope}"}}}
	cases := []struct{ name, y, part string }{
		{"unknown role", plan(part("a", "wizard", `"a/**"`, "")), "a"},
		{"unavailable role", plan(part("a", "broken", `"a/**"`, "")), "a"},
		{"missing after", plan(part("a", "coder", `"a/**"`, "nope")), "a"},
		{"cycle", plan(part("a", "coder", `"a/**"`, "b"), part("b", "coder", `"b/**"`, "a")), "a"},
		{"empty owns", plan(part("a", "coder", "", "")), "a"},
		{"duplicate id", plan(part("a", "coder", `"a/**"`, ""), part("a", "coder", `"b/**"`, "")), "a"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			probs := Validate(mustPlan(t, c.y), cfg, baseFiles)
			assert.Eq(t, len(probs), 1)
			assert.Eq(t, probs[0].Part, c.part)
		})
	}
	// Tester unavailable without test globs.
	bare, _, err := LoadConfig(t.TempDir(), t.TempDir(), true)
	assert.NoErr(t, err)
	probs := Validate(mustPlan(t, plan(part("t", "tester", `"a/**"`, ""))), bare, nil)
	assert.Eq(t, len(probs), 1)
	assert.Eq(t, probs[0].Part, "t")
}

func TestValidate_OverlapOnlyWhenConcurrent(t *testing.T) {
	cfg := planCfg(t)
	ok := plan(part("core", "coder", `"core/**"`, ""), part("tests-core", "tester", `"core/**"`, "core"))
	assert.Eq(t, len(Validate(mustPlan(t, ok), cfg, baseFiles)), 0)
	bad := plan(part("core", "coder", `"core/**"`, ""), part("other", "coder", `"core/**"`, ""))
	probs := Validate(mustPlan(t, bad), cfg, baseFiles)
	assert.Eq(t, len(probs), 1)
	// New-file prefix overlap with no existing file at base.
	prefix := plan(part("a", "coder", `"pkg/**"`, ""), part("b", "coder", `"pkg/sub/**"`, ""))
	assert.Eq(t, len(Validate(mustPlan(t, prefix), cfg, nil)), 1)
	disjoint := plan(part("a", "coder", `"pkg/**"`, ""), part("b", "coder", `"cmd/**"`, ""))
	assert.Eq(t, len(Validate(mustPlan(t, disjoint), cfg, baseFiles)), 0)
}

func TestValidate_NewFileLongestPrefix(t *testing.T) {
	p := mustPlan(t, plan(
		part("core", "coder", `"core/**"`, ""),
		part("store", "coder", `"core/store/**"`, "core"),
	))
	assert.Eq(t, OwnerOf(p, "core/store/x.go"), "store")
	assert.Eq(t, OwnerOf(p, "core/y.go"), "core")
	assert.Eq(t, OwnerOf(p, "elsewhere/z.go"), "")
	assert.Eq(t, len(Validate(p, planCfg(t), baseFiles)), 0)
}

func TestCodeRefusal(t *testing.T) {
	cfg := planCfg(t)
	reason := func(y string, c Config) string {
		r := CodeRefusal(mustPlan(t, y), c, baseFiles)
		if r == nil {
			return ""
		}
		return r.Reason
	}
	assert.Eq(t, reason(plan(part("a", "coder", `"a/**"`, "")), cfg), "single_package")
	assert.Eq(t, reason(plan(part("a", "coder", `"core/a/**"`, ""), part("b", "coder", `"core/b/**"`, "")), cfg), "single_package")
	assert.Eq(t, reason(plan(part("a", "coder", `"core/**"`, ""), part("b", "coder", `"cmd/**", "core/x/**"`, "")), cfg), "overlapping_ownership")
	assert.Eq(t, reason(plan(part("a", "coder", `"a/**"`, ""), part("b", "coder", `"b/**"`, "a"), part("c", "coder", `"c/**"`, "b")), cfg), "serial_dependencies")
	two := plan(part("a", "coder", `"a/**"`, ""), part("b", "coder", `"b/**"`, ""))
	noCheck := cfg
	noCheck.Check = ""
	assert.Eq(t, reason(two, noCheck), "no_check")
	assert.Eq(t, reason(two, cfg), "")
	// A planner-written refusal passes through untouched.
	r := CodeRefusal(mustPlan(t, "task: t\nrefuse: {reason: small_change, detail: d}\n"), cfg, nil)
	assert.Eq(t, r.Reason, "small_change")
}

func TestValidate_ReviewerRoleInPartsRejected(t *testing.T) {
	cfg := planCfg(t)
	probs := Validate(mustPlan(t, plan(part("a", "reviewer", `"a/**"`, ""))), cfg, baseFiles)
	assert.Eq(t, len(probs), 1)
	assert.Eq(t, probs[0].Message, "role reviewer runs after integration; it cannot be a plan part")
}
