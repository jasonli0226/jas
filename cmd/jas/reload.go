package main

import (
	"fmt"
	"path/filepath"
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
	e.policy = profilePolicy(e.policy, e.home, name)
	prof := e.prof
	prof.name = name
	if prof.agentsPath, prof.agents, err = readProfileAgents(e.home, name); err != nil {
		warn(err.Error())
	}
	projectSkills, pw := skills.Discover(skills.ProjectSources(e.root), e.policy.Denied)
	userSkills, uw := discoverUserSkills(e.policy, e.home)
	for _, w := range append(pw, uw...) {
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

// formatReloadNotice is the one-line /reload result: what context loaded and
// how many skills the model can see.
func formatReloadNotice(pc projectContext) string {
	parts := []string{"reloaded"}
	sawCtx := false
	for _, f := range pc.files {
		if pc.profileAgents != "" && f.Path == pc.profileAgents {
			continue // the profile's file: the notice describes the project's
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
