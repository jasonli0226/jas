package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/jasonli0226/jas/settings"
)

const configUsage = `usage:
  jas config set <key> <value>
  jas config get <key>
  jas config unset <key>
  jas config list
  jas config init`

func configCmd(args []string) error {
	if len(args) == 0 {
		return usageErr("%s", configUsage)
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, configFile)
	file, err := settings.LoadFile(path)
	if err != nil {
		return err
	}
	rest := args[1:]
	switch args[0] {
	case "set":
		if len(rest) != 2 {
			return usageErr("usage: jas config set <key> <value>")
		}
		next, err := file.Set(rest[0], rest[1])
		if err != nil {
			return usageErr("%v", err)
		}
		return next.Save(path)
	case "unset":
		if len(rest) != 1 {
			return usageErr("usage: jas config unset <key>")
		}
		next, err := file.Unset(rest[0])
		if err != nil {
			return usageErr("%v", err)
		}
		return next.Save(path)
	case "get":
		if len(rest) != 1 {
			return usageErr("usage: jas config get <key>")
		}
		return configGet(file, rest[0])
	case "list":
		configList(file)
		return nil
	case "init":
		return configInit(path, home, file)
	}
	return usageErr("unknown config command %q\n%s", args[0], configUsage)
}

// configGet prints the effective value of a plain key, or the stored
// override of a models.* key.
func configGet(file settings.File, key string) error {
	v, found, err := file.Stored(key)
	if err != nil {
		return usageErr("%v", err)
	}
	if _, plain := settings.LookupKey(key); plain {
		eff := settings.Resolve(nil, os.Getenv, file)[key]
		v, found = eff.Raw, eff.Raw != ""
	}
	if !found {
		return fmt.Errorf("%s is not set", key)
	}
	fmt.Println(v)
	return nil
}

func configList(file settings.File) {
	eff := settings.Resolve(nil, os.Getenv, file)
	for _, k := range settings.Keys {
		v := eff[k.Name]
		if v.Raw == "" {
			fmt.Printf("%s  (not set)\n", k.Name)
			continue
		}
		fmt.Printf("%s  %s  (%s)\n", k.Name, v.Raw, v.Source)
	}
	for _, n := range file.ModelNames() {
		fmt.Printf("models.%s.context_window  %d  (%s)\n", n, file.Models[n].ContextWindow, settings.SourceFile)
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
		key, err := readSecret(in, "API key (input hidden; empty to skip, then use JAS_API_KEY or `jas auth add`)")
		if err != nil {
			return err
		}
		if key != "" {
			if err := settings.AuthAdd(filepath.Join(home, authFile), provider, settings.AuthEntry{Key: key}, os.Getenv); err != nil {
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
