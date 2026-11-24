package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
	"github.com/jasonli0226/jas/skills"
)

const profileUsage = `usage:
  jas profile list [--json]
  jas profile use <name>
  jas profile new <name>
  jas profile show [name]`

const profileDirMode = 0o700

// profileCmd runs jas profile list|use|new|show.
func profileCmd(args []string) error {
	if len(args) == 0 {
		return usageErr("%s", profileUsage)
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		asJSON := len(rest) == 1 && rest[0] == "--json"
		if len(rest) > 1 || (len(rest) == 1 && !asJSON) {
			return usageErr("usage: jas profile list [--json]")
		}
		return profileList(home, asJSON)
	case "use":
		if len(rest) != 1 {
			return usageErr("usage: jas profile use <name>")
		}
		return profileUse(home, rest[0])
	case "new":
		if len(rest) != 1 {
			return usageErr("usage: jas profile new <name>")
		}
		return profileNew(home, rest[0])
	case "show":
		if len(rest) > 1 {
			return usageErr("usage: jas profile show [name]")
		}
		return profileShow(home, rest)
	}
	return usageErr("unknown profile command %q\n%s", cmd, profileUsage)
}

// activeProfileName is the profile a session started now would use: env,
// then local, project and global config (the project counts as trusted, as
// for config get/list), else default.
func activeProfileName(home string) (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	layers, err := settings.LoadLayers(home, core.FindRoot(cwd))
	if err != nil {
		return "", err
	}
	eff, _ := layers.ForProject(true)
	name, _ := settings.ResolveProfile(nil, os.Getenv, eff)
	return name, nil
}

// profileSkillNames lists the skills in the named profile's own skills/
// folder; it never resolves the profile's chain.
func profileSkillNames(home, name string) []string {
	policy := profilePolicy(core.ExecPolicy{Deny: denyPatterns(home)}, home, profileState{name: name})
	srcs, _ := profileSkillSources(policy, home)
	found, _ := skills.Discover(slices.Concat(srcs...), policy.ProfileScope().Denied)
	names := make([]string, 0, len(found))
	for _, s := range found {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names
}

type profileRow struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Skills int    `json:"skills"`
}

func profileList(home string, asJSON bool) error {
	names, err := settings.ListProfiles(home)
	if err != nil {
		return err
	}
	active, err := activeProfileName(home)
	if err != nil {
		return err
	}
	rows := make([]profileRow, 0, len(names))
	for _, n := range names {
		rows = append(rows, profileRow{n, n == active, len(profileSkillNames(home, n))})
	}
	if asJSON {
		b, err := json.Marshal(rows)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	for _, r := range rows {
		mark := " "
		if r.Active {
			mark = "*"
		}
		fmt.Printf("%s %s  %d skills\n", mark, r.Name, r.Skills)
	}
	return nil
}

func profileUse(home, name string) error {
	if !profileExists(home, name) && name != settings.DefaultProfile {
		have, err := settings.ListProfiles(home)
		if err != nil {
			return err
		}
		return usageErr("unknown profile %q: have %s", name, strings.Join(have, ", "))
	}
	global, err := settings.LoadFile(settings.GlobalConfigPath(home))
	if err != nil {
		return err
	}
	next, err := global.Set("profile", name)
	if err != nil {
		return usageErr("%v", err)
	}
	if err := next.Save(settings.GlobalConfigPath(home)); err != nil {
		return err
	}
	fmt.Println("profile " + name)
	return nil
}

func profileNew(home, name string) error {
	if err := settings.ValidProfileName(name); err != nil {
		return usageErr("%v", err)
	}
	if profileExists(home, name) {
		return usageErr("profile %q exists", name)
	}
	if err := os.MkdirAll(settings.ProfileDir(home, name), profileDirMode); err != nil {
		return err
	}
	fmt.Println(settings.ProfileDir(home, name))
	return nil
}

func profileShow(home string, args []string) error {
	var name string
	if len(args) == 1 {
		name = args[0]
		if !profileExists(home, name) && name != settings.DefaultProfile {
			have, err := settings.ListProfiles(home)
			if err != nil {
				return err
			}
			return usageErr("unknown profile %q: have %s", name, strings.Join(have, ", "))
		}
	} else {
		var err error
		if name, err = activeProfileName(home); err != nil {
			return err
		}
	}
	file, err := settings.LoadFile(settings.ProfileConfigPath(home, name))
	if err != nil {
		return err
	}
	_, agents, err := readProfileAgents(home, name)
	if err != nil {
		return err
	}
	fmt.Printf("profile: %s\npath: %s\n", name, settings.ProfileDir(home, name))
	fmt.Println("settings (" + filepath.Join("profiles", name, "config.json") + "):")
	keys := make([]string, 0, len(file.Values))
	for k := range file.Values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		fmt.Printf("  %s  %s\n", k, file.Values[k])
	}
	if len(keys) == 0 {
		fmt.Println("  (none)")
	}
	has := "no"
	if agents != nil {
		has = "yes"
	}
	fmt.Printf("%s: %s\n", profileAgentsFile, has)
	names := profileSkillNames(home, name)
	fmt.Printf("skills (%d):\n", len(names))
	for _, n := range names {
		fmt.Println("  " + n)
	}
	return nil
}
