package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/settings"
)

const configUsage = `usage:
  jas config set [--global|--in-profile|--project|--local] <key> <value>
  jas config get [--global|--in-profile|--project|--local] <key>
  jas config unset [--global|--in-profile|--project|--local] <key>
  jas config list
  jas config init

Scopes (Claude-style): global=~/.jas/config.json (default for set/unset),
in-profile=~/.jas/profiles/<active>/config.json (the active profile comes from
JAS_PROFILE or the profile key; no endpoint keys), project=<root>/.jas/config.json (shared), local=<root>/.jas/config.local.json
(personal). get without a scope prints the effective value. list always shows
effective values with sources. auth.json and trust.json stay global-only.`

func configCmd(args []string) error {
	if len(args) == 0 {
		return usageErr("%s", configUsage)
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	root := core.FindRoot(cwd)
	layers, err := settings.LoadLayers(home, root)
	if err != nil {
		return err
	}
	// get and list show what a trusted session reads: endpoints only from the
	// global file (trust itself is decided when a session starts).
	eff, _ := layers.ForProject(true)
	cmd := args[0]
	scope, rest, err := takeConfigScope(args[1:], cmd == "set" || cmd == "unset")
	if err != nil {
		return usageErr("%v", err)
	}
	if scope == settings.ScopeProfile {
		if layers, err = withActiveProfile(home, layers); err != nil {
			return err
		}
	}
	switch cmd {
	case "set":
		if len(rest) != 2 {
			return usageErr("usage: jas config set [--global|--in-profile|--project|--local] <key> <value>")
		}
		return configWrite(home, root, layers, scope, rest[0], rest[1], false)
	case "unset":
		if len(rest) != 1 {
			return usageErr("usage: jas config unset [--global|--in-profile|--project|--local] <key>")
		}
		return configWrite(home, root, layers, scope, rest[0], "", true)
	case "get":
		if len(rest) != 1 {
			return usageErr("usage: jas config get [--global|--in-profile|--project|--local] <key>")
		}
		if scope != "" {
			return configGet(layers, scope, rest[0], true) // the file as written
		}
		return configGet(eff, scope, rest[0], false)
	case "list":
		if len(rest) != 0 || scope != "" {
			return usageErr("usage: jas config list")
		}
		configList(eff)
		if layers.HasProject() {
			fmt.Println("# project and local config apply only when the project is trusted")
		}
		return nil
	case "init":
		if len(rest) != 0 || scope != "" {
			return usageErr("usage: jas config init")
		}
		return configInit(settings.GlobalConfigPath(home), home, layers.Global)
	}
	return usageErr("unknown config command %q\n%s", cmd, configUsage)
}

// withActiveProfile loads the active profile's config.json into the profile
// layer. The profile is chosen as a session would (env, then local, project
// and global config; the project counts as trusted, as for config get/list).
func withActiveProfile(home string, layers settings.Layers) (settings.Layers, error) {
	eff, _ := layers.ForProject(true)
	name, src := settings.ResolveProfile(nil, os.Getenv, eff)
	prof, warns, err := loadProfile(home, name, src)
	if err != nil {
		return layers, err
	}
	for _, w := range warns {
		fmt.Fprintln(os.Stderr, "jas: warning: "+w)
	}
	return layers.WithProfile(prof.name, prof.layer), nil
}

// takeConfigScope pulls --global|--in-profile|--project|--local out of args. When
// defaultGlobal is set and no flag appears, the write scope is global.
func takeConfigScope(args []string, defaultGlobal bool) (settings.Scope, []string, error) {
	var scope settings.Scope
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--global", "--in-profile", "--project", "--local":
			if scope != "" {
				return "", nil, fmt.Errorf("multiple scope flags")
			}
			s, err := settings.ParseScope(strings.TrimPrefix(strings.TrimPrefix(a, "--"), "in-"))
			if err != nil {
				return "", nil, err
			}
			scope = s
		default:
			rest = append(rest, a)
		}
	}
	if scope == "" && defaultGlobal {
		scope = settings.ScopeGlobal
	}
	return scope, rest, nil
}

// configWrite sets or unsets key in the chosen scope and saves that file.
func configWrite(home, root string, layers settings.Layers, scope settings.Scope, key, value string, unset bool) error {
	if !unset { // unset stays allowed, to clean an endpoint out of a project file
		if err := settings.CheckWriteScope(key, scope); err != nil {
			return usageErr("%v", err)
		}
	}
	path, err := scope.PathFor(home, root, layers.ProfileName)
	if err != nil {
		return usageErr("%v", err)
	}
	file := layers.File(scope)
	var next settings.File
	if unset {
		next, err = file.Unset(key)
	} else {
		next, err = file.Set(key, value)
	}
	if err != nil {
		return usageErr("%v", err)
	}
	if scope == settings.ScopeProfile {
		return next.Save(path) // outside the repository: no trust bundle to refresh
	}
	return saveSettingsFile(root, home, scope, path, next)
}

// configGet prints the effective value, or the value stored in scope when
// scoped is true.
func configGet(layers settings.Layers, scope settings.Scope, key string, scoped bool) error {
	if scoped {
		v, found, err := layers.File(scope).Stored(key)
		if err != nil {
			return usageErr("%v", err)
		}
		if !found {
			return fmt.Errorf("%s is not set in %s config", key, scope)
		}
		fmt.Println(v)
		return nil
	}
	if _, plain := settings.LookupKey(key); plain {
		eff := settings.Resolve(nil, os.Getenv, layers)[key]
		if eff.Raw == "" && eff.Source == settings.SourceDefault {
			// defaults with empty raw (e.g. model) count as unset for get
			if eff.Raw == "" {
				return fmt.Errorf("%s is not set", key)
			}
		}
		if eff.Raw == "" {
			return fmt.Errorf("%s is not set", key)
		}
		fmt.Println(eff.Raw)
		return nil
	}
	// models.* / providers.*: prefer local > project > global stored value
	for _, s := range []settings.Scope{settings.ScopeLocal, settings.ScopeProject, settings.ScopeGlobal} {
		v, found, err := layers.File(s).Stored(key)
		if err != nil {
			return usageErr("%v", err)
		}
		if found {
			fmt.Println(v)
			return nil
		}
	}
	return fmt.Errorf("%s is not set", key)
}

func configList(layers settings.Layers) {
	eff := settings.Resolve(nil, os.Getenv, layers)
	for _, k := range settings.Keys {
		v := eff[k.Name]
		if v.Raw == "" {
			fmt.Printf("%s  (not set)\n", k.Name)
			continue
		}
		fmt.Printf("%s  %s  (%s)\n", k.Name, v.Raw, v.Source)
	}
	for _, n := range layers.ModelNames() {
		w, src, ok := layers.ModelSource(n)
		if !ok {
			continue
		}
		fmt.Printf("models.%s.context_window  %d  (%s)\n", n, w, src)
	}
	merged := layers.Merged()
	for _, n := range merged.ProviderNames() {
		if n == settings.ProviderOpenRouter || n == settings.ProviderOllama || n == settings.ProviderCustom {
			continue // presets without a stored entry
		}
		p, ok := merged.Providers[n]
		if !ok {
			continue
		}
		src := settings.SourceFile
		if _, ok := layers.Local.Providers[n]; ok {
			src = settings.SourceLocal
		} else if _, ok := layers.Project.Providers[n]; ok {
			src = settings.SourceProject
		}
		fmt.Printf("providers.%s.base_url  %s  (%s)\n", n, p.BaseURL, src)
	}
}

// configInit is the interactive onboarding: provider, key, default model.
func configInit(path, home string, file settings.File) error {
	in := bufio.NewReader(os.Stdin)
	provider, err := prompt(in, "Provider (openrouter|ollama|custom)", "openrouter")
	if err != nil {
		return err
	}
	if file, err = file.Set("provider", provider); err != nil {
		return usageErr("%v", err)
	}
	if provider == settings.ProviderCustom {
		u, err := prompt(in, "Base URL", "")
		if err != nil {
			return err
		}
		if file, err = file.Set("base_url", u); err != nil {
			return usageErr("%v", err)
		}
	}
	if provider != settings.ProviderOllama {
		key, err := readSecret(in, "API key (input hidden), or $ENV_NAME to reference an env var; empty to skip")
		if err != nil {
			return err
		}
		if key != "" {
			entry := settings.AuthEntry{Key: key}
			if name, ref := strings.CutPrefix(key, "$"); ref {
				entry = settings.AuthEntry{Env: name} // a reference, never expanded
			}
			if err := settings.AuthAdd(filepath.Join(home, authFile), provider, entry, os.Getenv); err != nil {
				return usageErr("%v", err)
			}
		}
	}
	model, err := prompt(in, "Default model (e.g. z-ai/glm-5.3)", "")
	if err != nil {
		return err
	}
	if model != "" {
		if file, err = file.Set("model", model); err != nil {
			return usageErr("%v", err)
		}
	}
	return file.Save(path)
}

func prompt(in *bufio.Reader, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(os.Stderr, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stderr, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return def, nil
	}
	if line = strings.TrimSpace(line); line != "" {
		return line, nil
	}
	return def, nil
}

// readSecret reads a key without echo on a terminal; otherwise one line from
// stdin (for piping).
func readSecret(in *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(os.Stderr, "%s: ", label)
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		return strings.TrimSpace(string(b)), err
	}
	line, _ := in.ReadString('\n')
	return strings.TrimSpace(line), nil
}
