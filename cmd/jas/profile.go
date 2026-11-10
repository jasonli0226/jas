package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/settings"
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
