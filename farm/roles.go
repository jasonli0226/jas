package farm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jasonli0226/jas/core"
)

const testerPrompt = "Write and fix tests only. If non-test code must change, post a blocked board entry instead of changing it."

// reviewerRole is the built-in read-only role that reviews the integrated
// result; it is not a plan part role.
const reviewerRole = "reviewer"

// Role is a RoleDef resolved for one part.
type Role struct {
	Name    string
	Tools   []string
	Writes  []core.WriteRule // after ${var} expansion and intersection with the part's owns
	Prompt  string
	Approve string
	Model   string
	Budget  int
}

func builtinTools() []string {
	return []string{"read", "grep", "ls", "write", "edit", "shell"}
}

// BuiltinRoles returns the MVP roles. A coder with no writes entries may
// write the part's owns; the tester additionally requires ${test_globs}.
func BuiltinRoles() map[string]RoleDef {
	return map[string]RoleDef{
		"coder":      {Tools: builtinTools()},
		"tester":     {Tools: builtinTools(), Writes: []string{"${test_globs}"}, Prompt: testerPrompt},
		reviewerRole: {Tools: []string{"read", "grep", "ls"}, Prompt: reviewerPrompt},
	}
}

// merged returns built-ins overlaid by config roles (whole-role replacement).
func merged(cfg Config) map[string]RoleDef {
	out := BuiltinRoles()
	for name, def := range cfg.Roles {
		out[name] = def
	}
	return out
}

// expandGlob expands every ${var} in s; a multi-valued variable yields one
// result per value. An unset variable is an error.
func expandGlob(s string, vars map[string]Var) ([]string, error) {
	out := []string{s}
	for _, m := range varRef.FindAllStringSubmatch(s, -1) {
		v, ok := vars[m[1]]
		if !ok {
			return nil, fmt.Errorf("unknown variable ${%s}", m[1])
		}
		if len(v.Value) == 0 {
			return nil, fmt.Errorf("variable %s is unset", m[1])
		}
		var next []string
		for _, cur := range out {
			for _, val := range v.Value {
				next = append(next, strings.ReplaceAll(cur, m[0], val))
			}
		}
		out = next
	}
	return out, nil
}

func expandWrites(def RoleDef, vars map[string]Var) ([]string, error) {
	var out []string
	for _, w := range def.Writes {
		g, err := expandGlob(w, vars)
		if err != nil {
			return nil, err
		}
		out = append(out, g...)
	}
	return out, nil
}

// Available splits roles into usable ones and unavailable name -> reason.
func Available(cfg Config) (map[string]RoleDef, map[string]string) {
	ok, bad := map[string]RoleDef{}, map[string]string{}
	known := builtinByName()
	for name, def := range merged(cfg) {
		if _, err := expandWrites(def, cfg.Vars); err != nil {
			bad[name] = err.Error()
			continue
		}
		if t := unknownTool(known, def.Tools); t != "" {
			bad[name] = fmt.Sprintf("unknown tool %q", t)
			continue
		}
		ok[name] = def
	}
	return ok, bad
}

// unknownTool returns the first name roleTools would reject, or "".
func unknownTool(known map[string]core.Tool, names []string) string {
	for _, n := range names {
		if _, ok := known[n]; !ok {
			return n
		}
	}
	return ""
}

// ResolveRole resolves a role for a part that owns the given globs.
func ResolveRole(cfg Config, name string, owns []string) (Role, error) {
	avail, bad := Available(cfg)
	def, found := avail[name]
	if !found {
		if reason, un := bad[name]; un {
			return Role{}, fmt.Errorf("role %s unavailable: %s", name, reason)
		}
		return Role{}, fmt.Errorf("unknown role %q", name)
	}
	writes, err := expandWrites(def, cfg.Vars)
	if err != nil {
		return Role{}, err
	}
	var rules []core.WriteRule
	for _, o := range owns {
		if len(writes) == 0 {
			rules = append(rules, core.WriteRule{All: []string{o}})
			continue
		}
		for _, w := range writes {
			rules = append(rules, core.WriteRule{All: []string{o, w}})
		}
	}
	budget := def.Budget
	if budget == 0 {
		budget = cfg.Budget.PerWorkerTokens
	}
	return Role{
		Name: name, Tools: append([]string(nil), def.Tools...), Writes: rules,
		Prompt: def.Prompt, Approve: def.Approve, Model: def.Model, Budget: budget,
	}, nil
}

// RolesDigest hashes the sorted resolved role definitions (writes expanded)
// plus unavailability reasons.
func RolesDigest(cfg Config) string {
	avail, bad := Available(cfg)
	type entry struct {
		Name        string
		Def         RoleDef
		Unavailable string
	}
	names := map[string]bool{}
	for n := range avail {
		names[n] = true
	}
	for n := range bad {
		names[n] = true
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	all := merged(cfg)
	entries := make([]entry, 0, len(sorted))
	for _, n := range sorted {
		def := all[n]
		if w, err := expandWrites(def, cfg.Vars); err == nil {
			def.Writes = w
		}
		entries = append(entries, entry{n, def, bad[n]})
	}
	b, _ := json.Marshal(entries) // plain structs: cannot fail
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
