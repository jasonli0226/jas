package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

// profileShowResolved prints the flattened profile name: its chain, merged
// settings, the instructions text the prompt gets and the effective skills,
// each with the profile it comes from. A chain that does not resolve is a
// usage error.
func profileShowResolved(home, name string, src settings.Source) error {
	prof, warnings, err := loadProfile(home, name, src)
	if err != nil {
		return err
	}
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "jas: "+w)
	}
	fmt.Printf("profile: %s\npath: %s\n", prof.name, settings.ProfileDir(home, prof.name))
	fmt.Println("chain: " + strings.Join(prof.names(), " → "))
	var replacing []string
	for _, p := range prof.parts() {
		if p.meta.ReplaceAgents {
			replacing = append(replacing, p.name)
		}
	}
	if len(replacing) > 0 {
		fmt.Println("replace_agents: " + strings.Join(replacing, ", "))
	}
	printResolvedSettings(prof)
	fmt.Println(profileAgentsFile + ":")
	auth, err := settings.LoadAuth(filepath.Join(home, authFile))
	if err != nil {
		return err
	}
	redact := sessionRedactor(os.Getenv(settings.EnvAPIKey), auth).Redact
	if text := profileAgentsText(prof, redact); text != "" {
		fmt.Println(text)
	} else {
		fmt.Println("  (none)")
	}
	policy := profilePolicy(core.ExecPolicy{Deny: denyPatterns(home)}, home, prof)
	folders, fw := chainSkillFolders(policy, home, prof)
	found, cw := discoverChainSkills(folders, home, policy.ProfileScope().Denied)
	for _, w := range slices.Concat(fw, cw) {
		fmt.Fprintln(os.Stderr, "jas: "+w)
	}
	slices.SortFunc(found, func(a, b skills.Skill) int { return strings.Compare(a.Name, b.Name) })
	fmt.Printf("skills (%d):\n", len(found))
	for _, s := range found {
		fmt.Printf("  %s  (%s)\n", s.Name, skillOwner(prof.parts(), s))
	}
	printExcluded(prof.parts())
	return nil
}

// printResolvedSettings prints the chain's merged plain settings, each with
// the profile that sets it (the nearest one to the active profile wins).
// Keys a profile may not set are left out, as the session ignores them.
func printResolvedSettings(prof profileState) {
	value, owner := map[string]string{}, map[string]string{}
	for _, p := range prof.parts() { // root first, so a child overrides
		for k, v := range p.layer.Values {
			if settings.CheckWriteScope(k, settings.ScopeProfile) != nil {
				continue
			}
			value[k], owner[k] = v, p.name
		}
		for id, m := range p.layer.Models { // a child's model entry wins whole
			k := "models." + id + ".context_window"
			value[k], owner[k] = strconv.Itoa(m.ContextWindow), p.name
		}
	}
	keys := make([]string, 0, len(value))
	for k := range value {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	fmt.Println("settings:")
	for _, k := range keys {
		fmt.Printf("  %s  %s  (%s)\n", k, value[k], owner[k])
	}
	if len(keys) == 0 {
		fmt.Println("  (none)")
	}
}

// skillOwner is the profile whose skills folder holds s.
func skillOwner(parts []profilePart, s skills.Skill) string {
	for _, p := range slices.Backward(parts) {
		for _, root := range []string{p.skillsDir, evalOr(p.skillsDir)} {
			if strings.HasPrefix(s.Dir, root+string(filepath.Separator)) {
				return p.name
			}
		}
	}
	return "?"
}

// evalOr is path with symlinks resolved, or path when it cannot be.
func evalOr(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return path
}

// printExcluded prints each skill name a profile of the chain excludes.
func printExcluded(parts []profilePart) {
	header := false
	for _, p := range slices.Backward(parts) {
		for _, n := range p.meta.Exclude {
			if !header {
				fmt.Println("excluded:")
				header = true
			}
			fmt.Printf("  %s  (by %s)\n", n, p.name)
		}
	}
}
