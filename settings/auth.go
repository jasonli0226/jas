package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Provider names with built-in meaning.
const (
	ProviderOpenRouter = "openrouter"
	ProviderOllama     = "ollama"
	ProviderCustom     = "custom"

	// EnvAPIKey beats every stored key.
	EnvAPIKey = "JAS_API_KEY"
	// EnvNoPlaintext set to 1 refuses literal keys in auth.json.
	EnvNoPlaintext = "JAS_NO_PLAINTEXT_KEYS"
)

// AuthEntry is one stored credential: a literal key or an env var name.
type AuthEntry struct {
	Key string `json:"key,omitempty"`
	Env string `json:"env,omitempty"`
}

// LoadAuth reads auth.json. A missing file is empty; a file readable by
// group or others is refused.
func LoadAuth(path string) (map[string]AuthEntry, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]AuthEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	if mode := info.Mode().Perm(); mode&^fileMode != 0 {
		return nil, fmt.Errorf("auth.json permissions are %04o; run chmod 600 %s", uint32(mode), path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m := map[string]AuthEntry{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// SaveAuth writes auth.json atomically with mode 0600.
func SaveAuth(path string, m map[string]AuthEntry) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}

// ResolveKey finds the API key: JAS_API_KEY, then the provider's env
// reference, then its literal key. ollama needs none.
func ResolveKey(provider string, env func(string) string, auth map[string]AuthEntry) (key string, source Source, err error) {
	if k := env(EnvAPIKey); k != "" {
		return k, envSource(EnvAPIKey), nil
	}
	e := auth[provider]
	if name := strings.TrimPrefix(e.Env, "$"); name != "" {
		if k := env(name); k != "" {
			return k, Source("auth.json $" + name), nil
		}
		if e.Key == "" {
			return "", "", fmt.Errorf("auth.json for %s refers to $%s, which is not set", provider, name)
		}
	}
	if e.Key != "" {
		return e.Key, "auth.json", nil
	}
	if KeyRequired(provider) {
		return "", "", fmt.Errorf("no API key for %s: run `jas auth add %s` or set %s", provider, provider, EnvAPIKey)
	}
	return "", "none", nil
}

// KeyRequired reports whether provider cannot run without an API key. Only
// openrouter needs one; ollama, custom and unset providers may be keyless.
func KeyRequired(provider string) bool { return provider == ProviderOpenRouter }

var envNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName checks an --env value without echoing it (a mistyped key
// must not be printed back).
func ValidEnvName(name string) error {
	if !envNameRE.MatchString(strings.TrimPrefix(name, "$")) {
		return errors.New("invalid environment variable name (want letters, digits and underscores, e.g. OPENROUTER_API_KEY); did you paste the key itself?")
	}
	return nil
}

// CheckPlaintext is the single JAS_NO_PLAINTEXT_KEYS gate for literal keys.
func CheckPlaintext(env func(string) string) error {
	if env(EnvNoPlaintext) == "1" {
		return fmt.Errorf("%s=1: refusing to store a literal key; use `jas auth add <provider> --env NAME`", EnvNoPlaintext)
	}
	return nil
}

// AuthAdd stores an entry for provider.
func AuthAdd(path, provider string, e AuthEntry, env func(string) string) error {
	if e.Env != "" {
		if err := ValidEnvName(e.Env); err != nil {
			return err
		}
	} else if err := CheckPlaintext(env); err != nil {
		return err
	}
	m, err := LoadAuth(path)
	if err != nil {
		return err
	}
	m[provider] = e
	return SaveAuth(path, m)
}

// AuthRotate replaces the literal key of an existing provider entry.
func AuthRotate(path, provider, key string, env func(string) string) error {
	m, err := LoadAuth(path)
	if err != nil {
		return err
	}
	old, ok := m[provider]
	if !ok {
		return fmt.Errorf("no stored key for %s; use `jas auth add %s`", provider, provider)
	}
	if old.Env != "" {
		return fmt.Errorf("%s uses env reference $%s; rotate the variable instead", provider, strings.TrimPrefix(old.Env, "$"))
	}
	if err := CheckPlaintext(env); err != nil {
		return err
	}
	m[provider] = AuthEntry{Key: key}
	return SaveAuth(path, m)
}

// AuthRemove deletes a provider entry.
func AuthRemove(path, provider string) error {
	m, err := LoadAuth(path)
	if err != nil {
		return err
	}
	if _, ok := m[provider]; !ok {
		return fmt.Errorf("no stored key for %s", provider)
	}
	delete(m, provider)
	return SaveAuth(path, m)
}

// AuthList returns "name  source" lines, never key values.
func AuthList(m map[string]AuthEntry) []string {
	var names []string
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	lines := make([]string, 0, len(names))
	for _, n := range names {
		src := "auth.json (stored key)"
		if env := strings.TrimPrefix(m[n].Env, "$"); env != "" {
			src = "env $" + env
		}
		lines = append(lines, n+"  "+src)
	}
	return lines
}
