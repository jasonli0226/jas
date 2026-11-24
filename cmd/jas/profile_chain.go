package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

// profilePart is one profile of the active profile's chain: its
// profile.yaml, config.json, AGENTS.md and skills directory.
type profilePart struct {
	name       string
	meta       settings.ProfileMeta
	layer      settings.File
	agents     []byte
	agentsPath string // set when the profile AGENTS.md exists
	skillsDir  string
}

// loadChain resolves name's parent chain and loads each profile in it, root
// first. A chain that does not resolve is a usage error. An unreadable
// AGENTS.md is a warning.
func loadChain(home, name string) ([]profilePart, []string, error) {
	links, err := settings.ResolveChain(home, name)
	if err != nil {
		return nil, nil, usageErr("%v", err)
	}
	var warnings []string
	chain := make([]profilePart, 0, len(links))
	for _, l := range links {
		layer, err := settings.LoadFile(settings.ProfileConfigPath(home, l.Name))
		if err != nil {
			return nil, nil, err
		}
		part := profilePart{name: l.Name, meta: l.Meta, layer: layer, skillsDir: filepath.Join(settings.ProfileDir(home, l.Name), "skills")}
		if part.agentsPath, part.agents, err = readProfileAgents(home, l.Name); err != nil {
			warnings = append(warnings, err.Error())
		}
		chain = append(chain, part)
	}
	return chain, warnings, nil
}

// parts is the chain, or the active profile alone for a state built without
// loadProfile.
func (ps profileState) parts() []profilePart {
	if len(ps.chain) > 0 {
		return ps.chain
	}
	return []profilePart{{name: ps.name, layer: ps.layer, agents: ps.agents, agentsPath: ps.agentsPath, skillsDir: ps.skillsDir}}
}

// names are the chain's profile names, root first.
func (ps profileState) names() []string {
	parts := ps.parts()
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = p.name
	}
	return out
}

// ancestors are the chain without the active profile, nearest parent first.
func (ps profileState) ancestors() []profilePart {
	parts := ps.parts()
	up := slices.Clone(parts[:len(parts)-1])
	slices.Reverse(up)
	return up
}

// parentFiles are the ancestors' config.json files, nearest parent first,
// for Layers.WithProfileChain.
func (ps profileState) parentFiles() []settings.ProfileFile {
	var out []settings.ProfileFile
	for _, p := range ps.ancestors() {
		out = append(out, settings.ProfileFile{Name: p.name, File: p.layer})
	}
	return out
}

// inheritedSkillDirs are the ancestors' skills directories, nearest parent
// first, for ExecPolicy.InheritedSkills.
func (ps profileState) inheritedSkillDirs() []string {
	var out []string
	for _, p := range ps.ancestors() {
		out = append(out, p.skillsDir)
	}
	return out
}

// agentParts are the profiles whose AGENTS.md the prompt carries, root
// first: from the last one with replace_agents down (the whole chain when
// none has it), skipping those with no AGENTS.md.
func (ps profileState) agentParts() []profilePart {
	parts := ps.parts()
	start := 0
	for i, p := range parts {
		if p.meta.ReplaceAgents {
			start = i
		}
	}
	var out []profilePart
	for _, p := range parts[start:] {
		if p.agentsPath != "" {
			out = append(out, p)
		}
	}
	return out
}

// profileAgentsText is the prompt's profile section: a flat profile's
// AGENTS.md under today's heading, or one block per agentParts entry under
// a heading naming the profile. Each part is redacted and capped on its own,
// with the heading outside the cap.
func profileAgentsText(ps profileState, redact func(string) string) string {
	flat := len(ps.parts()) == 1
	var blocks []string
	for _, p := range ps.agentParts() {
		heading := "Profile instructions (" + profileAgentsFile + "):\n"
		if !flat {
			heading = "Profile instructions (" + p.name + " " + profileAgentsFile + "):\n"
		}
		blocks = append(blocks, heading+core.CapContext(redact(string(p.agents))))
	}
	return strings.Join(blocks, "\n\n")
}

// discoverChainSkills discovers each chain skills folder on its own
// (folders run child first, see chainSkillFolders) and combines them first
// wins, child first, with no shadow warning between chain folders. It then
// drops what each profile excludes from its ancestors and warns about
// exclude names no ancestor has.
func discoverChainSkills(folders []chainFolder, home string, denied func(string) bool) ([]skills.Skill, []string) {
	var warns []string
	byFolder := make([][]skills.Skill, len(folders))
	for i, f := range folders {
		var w []string
		byFolder[i], w = skills.Discover(f.srcs, denied)
		warns = append(warns, w...)
	}
	var out []skills.Skill
	taken := map[string]bool{}
	for i, found := range byFolder {
		for _, s := range found {
			if taken[s.Name] || excludedBelow(folders, i, s.Name) {
				continue
			}
			taken[s.Name] = true
			out = append(out, s)
		}
	}
	return out, append(warns, unknownExcludes(byFolder, folders, home)...)
}

// excludedBelow reports whether a profile below folder i (a smaller index:
// folders run child first) excludes name.
func excludedBelow(folders []chainFolder, i int, name string) bool {
	for _, f := range folders[:i] {
		if slices.Contains(f.part.meta.Exclude, name) {
			return true
		}
	}
	return false
}

// unknownExcludes warns about each exclude name that matches no skill in
// the excluding profile's ancestors' folders.
func unknownExcludes(byFolder [][]skills.Skill, folders []chainFolder, home string) []string {
	var warns []string
	for i, f := range folders {
		for _, name := range f.part.meta.Exclude {
			if !ancestorHas(byFolder, i, name) {
				warns = append(warns, fmt.Sprintf("profile %s (%s): exclude names %q, which no parent profile has",
					f.part.name, settings.ProfileMetaPath(home, f.part.name), name))
			}
		}
	}
	return warns
}

// ancestorHas reports whether a folder above i holds a skill named name.
func ancestorHas(byFolder [][]skills.Skill, i int, name string) bool {
	for _, found := range byFolder[i+1:] {
		for _, s := range found {
			if s.Name == name {
				return true
			}
		}
	}
	return false
}

// inheritedNote is what the confirmation of a --in-profile write of key
// adds when a parent profile also sets it: the nearest such parent, which
// a set now overrides and an unset reveals. "" for any other scope, a key
// a profile may not set, or a key no parent sets.
func inheritedNote(l settings.Layers, scope settings.Scope, key string, unset bool) string {
	if scope != settings.ScopeProfile || settings.CheckWriteScope(key, scope) != nil {
		return ""
	}
	for _, p := range l.ProfileParents {
		if _, found, err := p.File.Stored(key); err != nil || !found {
			continue
		}
		if unset {
			return " (now inherited from profile " + p.Name + ")"
		}
		return " (overrides profile " + p.Name + ")"
	}
	return ""
}
