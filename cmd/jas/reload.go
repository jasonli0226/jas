package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/skills"
)

// reloadContext rediscovers skills and rebuilds the project system prompt
// under the same trust rules as prepare. ask is used when trust must be
// re-confirmed (nil refuses like a non-interactive skip). On success env's
// project context, skill list and policy read roots are replaced.
func (e *env) reloadContext(ask func(string) string) (notices []string, roots []string, err error) {
	var warnings []string
	warn := func(w string) { warnings = append(warnings, w) }
	if err := syncManualSkills(); err != nil {
		warn("manual skills: " + err.Error())
	}
	userHome, _ := os.UserHomeDir()
	userConfig, _ := os.UserConfigDir()
	projectSkills, pw := skills.Discover(skills.ProjectSources(e.root), e.policy.Denied)
	userSkills, uw := skills.Discover(skills.UserSources(userHome, userConfig), e.policy.Denied)
	for _, w := range append(pw, uw...) {
		warn(w)
	}
	interactive := ask != nil
	pc, err := loadProjectContext(e.root, e.home, projectSkills, userSkills, interactive, e.trust, ask, e.red.Redact, warn)
	if err != nil {
		return nil, nil, err
	}
	skillList, skillDirs := listedSkills(pc.skills)
	e.pc, e.skillList = pc, skillList
	e.policy.ReadRoots = skillDirs
	notices = append(notices, formatReloadNotice(pc))
	for _, w := range warnings {
		notices = append(notices, "jas: "+core.Sanitize(e.red.Redact(w)))
	}
	return notices, skillDirs, nil
}

// formatReloadNotice is the one-line /reload result: what context loaded and
// how many skills the model can see.
func formatReloadNotice(pc projectContext) string {
	parts := []string{"reloaded"}
	sawCtx := false
	for _, f := range pc.files {
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
