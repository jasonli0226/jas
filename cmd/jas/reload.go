package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/skills"
)

// reloadContext rediscovers skills and rebuilds the project system prompt
// under the same trust rules as prepare. ask is used when trust must be
// re-confirmed (nil refuses like a non-interactive skip). On success env's
// project context, skill list, memory and policy read roots (skill and
// memory dirs, returned as roots) are replaced.
func (e *env) reloadContext(ask func(string) string) (notices []string, roots []string, err error) {
	var warnings []string
	warn := func(w string) { warnings = append(warnings, w) }
	if err := syncManualSkills(); err != nil {
		warn("manual skills: " + err.Error())
	}
	name := e.activeProfile()
	prof := e.prof
	prof.name = name
	e.policy = profilePolicy(e.policy, e.home, prof)
	prof, aw := prof.rereadAgents(e.home)
	projectSkills, pw := skills.Discover(skills.ProjectSources(e.root), e.policy.Denied)
	userSkills, uw := discoverUserSkills(e.policy, e.home, prof)
	for _, w := range slices.Concat(aw, pw, uw) {
		warn(w)
	}
	interactive := ask != nil
	pc, err := loadProjectContext(e.root, e.home, projectSkills, userSkills, prof, interactive, e.trust, ask, e.red.Redact, warn)
	if err != nil {
		return nil, nil, err
	}
	skillList, skillDirs := listedSkills(pc.skills)
	e.policy.ReadRoots = skillDirs
	var mem memoryState
	e.policy, mem = loadMemory(e.policy, e.home, name, e.root, e.layers, e.red.Redact, warn)
	pc = withMemory(pc, mem, !e.interactive)
	e.pc, e.skillList, e.prof, e.mem = pc, skillList, prof, mem
	notices = append(notices, formatReloadNotice(pc))
	if pc.trusted != e.settingsTrusted && len(readProjectSettings(e.root)) > 0 {
		notices = append(notices, "jas: project trust changed; project settings apply at the next start")
	}
	for _, w := range warnings {
		notices = append(notices, "jas: "+core.Sanitize(e.red.Redact(w)))
	}
	return notices, e.policy.ReadRoots, nil
}

// refreshMemory reloads memory alone from the current settings and files,
// as a new or resumed session starts on it: the Memory section, the
// indexes in context_files, the lift and the read root. It returns the
// warnings as notices.
func (e *env) refreshMemory() []string {
	var notices []string
	warn := func(w string) { notices = append(notices, "jas: "+core.Sanitize(e.red.Redact(w))) }
	policy := e.policy
	if e.mem.on() {
		policy.ReadRoots = slices.DeleteFunc(slices.Clone(policy.ReadRoots), func(r string) bool { return r == e.mem.dirs.Profile })
	}
	policy, mem := loadMemory(policy, e.home, e.activeProfile(), e.root, e.layers, e.red.Redact, warn)
	e.policy, e.mem, e.pc = policy, mem, withMemory(withoutMemory(e.pc), mem, !e.interactive)
	return notices
}

// formatReloadNotice is the one-line /reload result: what context loaded and
// how many skills the model can see.
func formatReloadNotice(pc projectContext) string {
	parts := []string{"reloaded"}
	sawCtx := false
	for _, f := range pc.files {
		if slices.Contains(pc.profileAgents, f.Path) {
			continue // a profile's file: the notice describes the project's
		}
		base := filepath.Base(f.Path)
		if base != "AGENTS.md" && base != "CLAUDE.md" {
			continue
		}
		sawCtx = true
		if f.Loaded {
			parts = append(parts, base+" loaded")
		} else {
			parts = append(parts, base+" not loaded")
		}
	}
	if !sawCtx {
		parts = append(parts, "no project context file")
	}
	n := 0
	for _, s := range pc.skills {
		if !s.DisableModelInvocation {
			n++
		}
	}
	switch n {
	case 0:
		parts = append(parts, "0 skills")
	case 1:
		parts = append(parts, "1 skill")
	default:
		parts = append(parts, fmt.Sprintf("%d skills", n))
	}
	return strings.Join(parts, " · ")
}
