package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/jasonli0226/jas/settings"
)

const authUsage = `usage:
  jas auth add <provider> [--env NAME]
  jas auth list
  jas auth rm <provider>
  jas auth rotate <provider>
providers: openrouter, ollama, custom`

func authCmd(args []string) error {
	if len(args) == 0 {
		return usageErr("%s", authUsage)
	}
	home, err := settingsHome()
	if err != nil {
		return err
	}
	path := filepath.Join(home, authFile)
	switch args[0] {
	case "list":
		m, err := settings.LoadAuth(path)
		if err != nil {
			return err
		}
		for _, l := range settings.AuthList(m) {
			fmt.Println(l)
		}
		return nil
	case "add", "rm", "rotate":
		provider, envName, err := parseAuthArgs(args)
		if err != nil {
			return err
		}
		return authMutate(args[0], path, provider, envName)
	}
	return usageErr("unknown auth command %q\n%s", args[0], authUsage)
}

// parseAuthArgs accepts `<provider> [--env NAME]` in either order.
func parseAuthArgs(args []string) (provider, envName string, err error) {
	fs := flag.NewFlagSet("auth "+args[0], flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&envName, "env", "", "env var holding the key")
	rest := args[1:]
	for len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		provider, rest = rest[0], rest[1:]
		break
	}
	if err := fs.Parse(rest); err != nil {
		return "", "", usageErr("%v", err)
	}
	if provider == "" || fs.NArg() != 0 {
		return "", "", usageErr("%s", authUsage)
	}
	if envName != "" && args[0] != "add" {
		return "", "", usageErr("--env is only valid for `jas auth add`")
	}
	switch provider {
	case settings.ProviderOpenRouter, settings.ProviderOllama, settings.ProviderCustom:
	default:
		return "", "", usageErr("unknown provider %q: want openrouter|ollama|custom", provider)
	}
	return provider, envName, nil
}

func authMutate(op, path, provider, envName string) error {
	switch op {
	case "rm":
		return settings.AuthRemove(path, provider)
	case "add":
		if envName != "" {
			return settings.AuthAdd(path, provider, settings.AuthEntry{Env: strings.TrimPrefix(envName, "$")}, os.Getenv)
		}
		if os.Getenv(settings.EnvNoPlaintext) == "1" {
			return usageErr("%s=1: refusing to store a literal key; use `jas auth add %s --env NAME`", settings.EnvNoPlaintext, provider)
		}
		key, err := readSecret(bufio.NewReader(os.Stdin), "API key for "+provider+" (input hidden)")
		if err != nil {
			return err
		}
		if key == "" {
			return usageErr("empty key")
		}
		return settings.AuthAdd(path, provider, settings.AuthEntry{Key: key}, os.Getenv)
	}
	// rotate: fail on an unknown provider before prompting for a key.
	m, err := settings.LoadAuth(path)
	if err != nil {
		return err
	}
	if _, ok := m[provider]; !ok {
		return fmt.Errorf("no stored key for %s; use `jas auth add %s`", provider, provider)
	}
	key, err := readSecret(bufio.NewReader(os.Stdin), "New API key for "+provider+" (input hidden)")
	if err != nil {
		return err
	}
	if key == "" {
		return usageErr("empty key")
	}
	return settings.AuthRotate(path, provider, key, os.Getenv)
}
