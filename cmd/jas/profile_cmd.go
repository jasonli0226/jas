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
  jas profile new <name> [--extends <parent>]
  jas profile show [name] [--resolved]`

const (
	profileDirMode  = 0o700
	profileFileMode = 0o600
)

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
		name, parent, ok := parseProfileNew(rest)
		if !ok {
			return usageErr("usage: jas profile new <name> [--extends <parent>]")
		}
		return profileNew(home, name, parent)
	case "show":
		names, resolved := splitFlag(rest, "--resolved")
		if len(names) > 1 {
			return usageErr("usage: jas profile show [name] [--resolved]")
		}
		return profileShow(home, names, resolved)
	}
	return usageErr("unknown profile command %q\n%s", cmd, profileUsage)
}

// parseProfileNew reads jas profile new's arguments: the name and the
// optional --extends parent.
func parseProfileNew(args []string) (name, parent string, ok bool) {
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--extends" && i+1 < len(args) && parent == "":
			i++
			parent = args[i]
		case strings.HasPrefix(args[i], "-") || name != "":
			return "", "", false
		default:
			name = args[i]
		}
	}
	return name, parent, name != ""
}

// splitFlag removes every occurrence of flag from args and reports whether
// it was there.
func splitFlag(args []string, flag string) ([]string, bool) {
	rest := slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == flag })
	return rest, len(rest) != len(args)
}

// activeProfileName is the profile a session started now would use: env,
// then local, project and global config (the project counts as trusted, as
// for config get/list), else default.
func activeProfileName(home string) (string, error) {
	name, _, err := activeProfileSource(home)
	return name, err
}

// activeProfileSource is activeProfileName with where the name came from.
func activeProfileSource(home string) (string, settings.Source, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	layers, err := settings.LoadLayers(home, core.FindRoot(cwd))
	if err != nil {
		return "", "", err
	}
	eff, _ := layers.ForProject(true)
	name, src := settings.ResolveProfile(nil, os.Getenv, eff)
	return name, src, nil
}

// profileSkillNames lists the skills in the named profile's own skills/
// folder; it never resolves the profile's chain.
func profileSkillNames(home, name string) []string {
	policy := profilePolicy(core.ExecPolicy{Deny: denyPatterns(home)}, home, profileState{name: name})
	folders, _ := chainSkillFolders(policy, home, profileState{name: name})
	found, _ := skills.Discover(folders[0].srcs, policy.ProfileScope().Denied)
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
	// Extends and Error come from the profile's own profile.yaml; list never
	// resolves a chain.
	Extends string `json:"extends,omitempty"`
	Error   string `json:"error,omitempty"`
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
		row := profileRow{Name: n, Active: n == active, Skills: len(profileSkillNames(home, n))}
		meta, err := settings.LoadProfileMeta(home, n)
		row.Extends = meta.Extends
		if err != nil {
			row.Error = err.Error()
		}
		rows = append(rows, row)
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
		note := ""
		if r.Error != "" {
			note = "  (bad " + settings.ProfileMetaFile + ")"
		} else if r.Extends != "" {
			note = "  (extends " + r.Extends + ")"
		}
		fmt.Printf("%s %s  %d skills%s\n", mark, r.Name, r.Skills, note)
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

// profileNew creates the profile directory; with a parent it also writes a
// profile.yaml naming it. The parent must exist (default always does).
func profileNew(home, name, parent string) error {
	if err := settings.ValidProfileName(name); err != nil {
		return usageErr("%v", err)
	}
	if profileExists(home, name) {
		return usageErr("profile %q exists", name)
	}
	if parent != "" && parent != settings.DefaultProfile && !profileExists(home, parent) {
		have, err := settings.ListProfiles(home)
		if err != nil {
			return err
		}
		return usageErr("unknown parent profile %q: have %s", parent, strings.Join(have, ", "))
	}
	if err := os.MkdirAll(settings.ProfileDir(home, name), profileDirMode); err != nil {
		return err
	}
	if parent != "" {
		meta := filepath.Join(settings.ProfileDir(home, name), settings.ProfileMetaFile)
		if err := os.WriteFile(meta, []byte("extends: "+parent+"\n"), profileFileMode); err != nil {
			return err
		}
	}
	fmt.Println(settings.ProfileDir(home, name))
	return nil
}

func profileShow(home string, args []string, resolved bool) error {
	var name string
	src := settings.SourceFlag
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
		if name, src, err = activeProfileSource(home); err != nil {
			return err
		}
	}
	if resolved {
		return profileShowResolved(home, name, src)
	}
	meta, err := settings.LoadProfileMeta(home, name)
	if err != nil {
		return err
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
	if meta.Extends != "" {
		fmt.Println("extends: " + meta.Extends)
	}
	if meta.ReplaceAgents {
		fmt.Println("replace_agents: true")
	}
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
