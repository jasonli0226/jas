package settings

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Defaults (design §3 "Tool execution policy").
const (
	DefaultMaxTurns        = 50
	DefaultMaxToolOutput   = 32768
	DefaultElideAt         = "0.6"
	DefaultKeepToolResults = 6
	DefaultApprove         = "never"
	DefaultCompactReserve  = 16384 // matches core.DefaultCompactReserve
	DefaultCompactKeep     = 20000 // matches core.DefaultCompactKeep
	DefaultMaxOutputTokens = 16000

	homeDirName = ".jas"
)

// Home returns $JAS_HOME, else ~/.jas. It returns "" if neither can be found.
func Home() string {
	if h := os.Getenv("JAS_HOME"); h != "" {
		return h
	}
	uh, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(uh, homeDirName)
}

// KeySpec describes one setting.
type KeySpec struct {
	Name     string
	Default  string
	Options  []string // closed set for /settings picker; empty = free-text
	Validate func(string) error
	Flag     bool // a per-run --<name> flag exists
}

// EnvName is the JAS_<K> variable for the key.
func (k KeySpec) EnvName() string {
	return "JAS_" + strings.ToUpper(k.Name)
}

// Keys lists every plain setting. models.<name>.context_window is a pattern
// handled by File.Set.
var Keys = []KeySpec{
	{Name: "profile", Default: DefaultProfile, Validate: ValidProfileName, Flag: true},
	{Name: "provider", Options: []string{"openrouter", "ollama", "custom"}, Validate: oneOf("openrouter", "ollama", "custom"), Flag: true},
	{Name: "model", Validate: nonEmpty, Flag: true},
	{Name: "base_url", Validate: validURL, Flag: true},
	{Name: "approve", Default: DefaultApprove, Options: []string{"never", "shell", "mutating"}, Validate: oneOf("never", "shell", "mutating"), Flag: true},
	{Name: "max_turns", Default: strconv.Itoa(DefaultMaxTurns), Validate: positiveInt, Flag: true},
	{Name: "max_tool_output", Default: strconv.Itoa(DefaultMaxToolOutput), Validate: positiveInt},
	{Name: "elide_at", Default: DefaultElideAt, Validate: fractionOpen},
	{Name: "keep_tool_results", Default: strconv.Itoa(DefaultKeepToolResults), Validate: nonNegativeInt},
	{Name: "redact_skip_paths", Validate: anyValue},
	{Name: "compact_reserve_tokens", Default: strconv.Itoa(DefaultCompactReserve), Validate: positiveInt},
	{Name: "compact_keep_tokens", Default: strconv.Itoa(DefaultCompactKeep), Validate: positiveInt},
	{Name: "compact_model", Validate: anyValue},                                                                      // "" = the session's model
	{Name: "max_output_tokens", Default: strconv.Itoa(DefaultMaxOutputTokens), Validate: nonNegativeInt, Flag: true}, // 0 = omit max_tokens
	{Name: "farm_bell", Default: "true", Options: []string{"true", "false"}, Validate: oneOf("true", "false")},       // ring when a chat farm needs the owner
	{Name: "web_tools", Default: "false", Options: []string{"true", "false"}, Validate: oneOf("true", "false")},      // opt-in web_fetch + web_search (§12.1)
	{Name: "search_base_url", Validate: emptyOrURL},
	{Name: "hints", Default: "true", Options: []string{"true", "false"}, Validate: oneOf("true", "false")},        // one-time chat tips                                                                  // optional SearXNG root for web_search
	{Name: "memory", Default: "on", Options: []string{"on", "read", "off"}, Validate: oneOf("on", "read", "off")}, // profile memory (memory.ResolveMode)
}

// LookupKey finds a plain key spec by name.
func LookupKey(name string) (KeySpec, bool) {
	for _, k := range Keys {
		if k.Name == name {
			return k, true
		}
	}
	return KeySpec{}, false
}

// Source says which layer supplied a value.
type Source string

const (
	SourceFlag    Source = "flag"
	SourceFile    Source = "global config"
	SourceDefault Source = "default"
)

func envSource(name string) Source { return Source("env " + name) }

// Value is one resolved setting.
type Value struct {
	Raw    string
	Source Source
}

// Effective is the resolved value of every key.
type Effective map[string]Value

// Resolve merges layers: flag > env > local > project > profile > global > default.
// The profile key skips the profile layer (a profile cannot choose itself).
// An empty string counts as unset in every layer. Stored named providers are
// exposed as "providers.<name>.base_url" keys for Build (local > project > global).
func Resolve(flags map[string]string, env func(string) string, layers Layers) Effective {
	out := Effective{}
	for _, k := range Keys {
		switch {
		case flags[k.Name] != "":
			out[k.Name] = Value{flags[k.Name], SourceFlag}
		case env(k.EnvName()) != "":
			out[k.Name] = Value{env(k.EnvName()), envSource(k.EnvName())}
		default:
			profile := layers.Profile.Values[k.Name]
			if k.Name == "profile" {
				profile = ""
			}
			raw, src := pick(layers.Local.Values[k.Name], layers.Project.Values[k.Name], profile, layers.Global.Values[k.Name], layers.ProfileName)
			if raw == "" {
				out[k.Name] = Value{k.Default, SourceDefault}
			} else {
				out[k.Name] = Value{raw, src}
			}
		}
	}
	// Providers: overlay global → profile → project → local so the most specific wins.
	type pe struct {
		url string
		src Source
	}
	providers := map[string]pe{}
	for name, p := range layers.Global.Providers {
		providers[name] = pe{p.BaseURL, SourceFile}
	}
	for name, p := range layers.Profile.Providers {
		providers[name] = pe{p.BaseURL, ProfileSource(layers.ProfileName)}
	}
	for name, p := range layers.Project.Providers {
		providers[name] = pe{p.BaseURL, SourceProject}
	}
	for name, p := range layers.Local.Providers {
		providers[name] = pe{p.BaseURL, SourceLocal}
	}
	for name, p := range providers {
		out[providersPrefix+name+baseURLSuffix] = Value{p.url, p.src}
	}
	return out
}

// SessionApprove is the approve level for a chat / jas run session.
// When web_tools is enabled and approve is still the built-in default
// (never), it rises to shell so prompt injection from fetched pages cannot
// drive shell silently (design §11). An explicit never from flag, env, or
// any config layer is preserved.
func (e Effective) SessionApprove() string {
	approve := e["approve"].Raw
	if e["web_tools"].Raw == "true" && e["approve"].Source == SourceDefault {
		return "shell"
	}
	return approve
}

// Validate checks every non-default value, naming the key and its source.
func (e Effective) Validate() error {
	for _, k := range Keys {
		v := e[k.Name]
		if v.Source == SourceDefault || k.Validate == nil {
			continue
		}
		if err := k.Validate(v.Raw); err != nil {
			return fmt.Errorf("invalid %s (%s): %w", k.Name, v.Source, err)
		}
	}
	return nil
}

func anyValue(string) error { return nil }

func nonEmpty(s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("must not be empty")
	}
	return nil
}

func oneOf(opts ...string) func(string) error {
	return func(s string) error {
		for _, o := range opts {
			if s == o {
				return nil
			}
		}
		return fmt.Errorf("%q is not valid: want %s", s, strings.Join(opts, "|"))
	}
}

func validURL(s string) error {
	u, err := url.Parse(s)
	if err == nil && u.User != nil {
		return fmt.Errorf("must not contain credentials (user:password@); store keys with `jas auth add`")
	}
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not a valid http(s) URL", s)
	}
	return nil
}

// emptyOrURL accepts "" or a credential-free http(s) URL (search_base_url).
func emptyOrURL(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return validURL(s)
}

func positiveInt(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return fmt.Errorf("%q is not valid: want a positive integer", s)
	}
	return nil
}

func nonNegativeInt(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return fmt.Errorf("%q is not valid: want an integer >= 0", s)
	}
	return nil
}

// fractionOpen accepts 0 < x <= 1.
func fractionOpen(s string) error {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f > 1 {
		return fmt.Errorf("%q is not valid: want a number above 0 and at most 1", s)
	}
	return nil
}
