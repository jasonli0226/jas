package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

// profileAgentsFile is the profile's instructions file, loaded before the
// project's context file.
const profileAgentsFile = "AGENTS.md"

// envProfileSource is the source settings gives a profile chosen by JAS_PROFILE.
const envProfileSource = settings.Source("env JAS_PROFILE")

// profileState is what the active profile adds to a session.
type profileState struct {
	name       string
	layer      settings.File
	agents     []byte
	agentsPath string // set when the profile AGENTS.md exists
	skillsDir  string
}

// loadProfile reads the named profile's config.json and AGENTS.md (each
// missing = empty). A name with no profile directory is a usage error when
// the owner asked for it on this run (flag or env), naming the profiles
// there are; one from a config file warns and falls back to default, so a
// repository that commits a profile still runs where it does not exist.
// default never fails: a missing default/ is an empty profile.
func loadProfile(home, name string, src settings.Source) (profileState, []string, error) {
	var warnings []string
	if name != settings.DefaultProfile && !profileExists(home, name) {
		if src == settings.SourceFlag || src == envProfileSource {
			have, err := settings.ListProfiles(home)
			if err != nil {
				return profileState{}, nil, err
			}
			return profileState{}, nil, usageErr("unknown profile %q (from %s): have %s", name, src, strings.Join(have, ", "))
		}
		warnings = append(warnings, fmt.Sprintf("profile %q (from %s) not found; using %s", name, src, settings.DefaultProfile))
		name = settings.DefaultProfile
	}
	layer, err := settings.LoadFile(settings.ProfileConfigPath(home, name))
	if err != nil {
		return profileState{}, nil, err
	}
	ps := profileState{name: name, layer: layer, skillsDir: filepath.Join(settings.ProfileDir(home, name), "skills")}
	ps.agentsPath, ps.agents, err = readProfileAgents(home, name)
	if err != nil {
		warnings = append(warnings, err.Error())
	}
	return ps, warnings, nil
}

// profileExists reports whether name is a valid profile with a directory.
func profileExists(home, name string) bool {
	if settings.ValidProfileName(name) != nil {
		return false
	}
	fi, err := os.Stat(settings.ProfileDir(home, name))
	return err == nil && fi.IsDir()
}

// readProfileAgents reads the profile's AGENTS.md. A missing file returns
// "" and no error; an unreadable one is reported so the session can warn.
func readProfileAgents(home, name string) (string, []byte, error) {
	p := filepath.Join(settings.ProfileDir(home, name), profileAgentsFile)
	b, err := os.ReadFile(p)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("profile %s: %w", profileAgentsFile, err)
	}
	return p, b, nil
}

// activeProfile is the session's profile name, default when unset.
func (e *env) activeProfile() string {
	if e.profile == "" {
		return settings.DefaultProfile
	}
	return e.profile
}

// skillsNoticeMarker records, in the profile directory, that the one-time
// skills migration notice was shown.
const skillsNoticeMarker = ".skills-notice"

// oldSkillDirs are the user skill folders earlier jas versions read, relative
// to the user home and the user config dir. The last one also holds the old
// manual copy, jas/SKILL.md, which every earlier jas installed.
func oldSkillDirs(userHome, userConfig string) []string {
	var dirs []string
	if userHome != "" {
		dirs = append(dirs, filepath.Join(userHome, ".agents", "skills"), filepath.Join(userHome, ".claude", "skills"))
	}
	if userConfig != "" {
		dirs = append(dirs, filepath.Join(userConfig, "jas", "skills"))
	}
	return dirs
}

// hasOldSkills reports whether dir holds a */SKILL.md. The old manual copy
// (jas/SKILL.md under the user config dir) does not count: it is not the
// user's own skill.
func hasOldSkills(dir string, isConfigDir bool) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*", "SKILL.md"))
	for _, m := range matches {
		if isConfigDir && filepath.Base(filepath.Dir(m)) == "jas" {
			continue
		}
		return true
	}
	return false
}

// migrationNotice is the one-time note for users upgrading from a jas that
// read skills from ~/.agents/skills and friends: "" unless the profile has no
// skills and an old folder holds some, or the notice was already shown.
func migrationNotice(home, profile, userHome, userConfig string, profileSkills int) string {
	if profileSkills > 0 {
		return ""
	}
	if _, err := os.Stat(filepath.Join(settings.ProfileDir(home, profile), skillsNoticeMarker)); err == nil {
		return ""
	}
	var found []string
	for _, d := range oldSkillDirs(userHome, userConfig) {
		if hasOldSkills(d, userConfig != "" && d == filepath.Join(userConfig, "jas", "skills")) {
			found = append(found, d)
		}
	}
	if len(found) == 0 {
		return ""
	}
	return fmt.Sprintf("skills are now read from the profile's skills folder, %s; none are there yet. "+
		"Move or copy your skills from %s. The old manual copy in %s can be deleted. This notice is shown once.",
		filepath.Join(settings.ProfileDir(home, profile), "skills"), strings.Join(found, ", "),
		filepath.Join(userConfig, "jas", "skills", "jas"))
}

// markSkillsNotice remembers that the notice was shown. Best effort: the
// profile directory is not created, and a failure only repeats the notice.
func markSkillsNotice(home, profile string) {
	dir := settings.ProfileDir(home, profile)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, skillsNoticeMarker), nil, 0o600)
}

// countProfileSkills counts the discovered skills that live in the profile's
// skills directory (not the built-in manual).
func countProfileSkills(found []skills.Skill, profileSkillsDir string) int {
	roots := []string{profileSkillsDir}
	if r, err := filepath.EvalSymlinks(profileSkillsDir); err == nil {
		roots = append(roots, r)
	}
	n := 0
	for _, s := range found {
		for _, r := range roots {
			if strings.HasPrefix(s.Dir, r+string(filepath.Separator)) {
				n++
				break
			}
		}
	}
	return n
}
