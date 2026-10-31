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
	{Name: "provider", Validate: oneOf("openrouter", "ollama", "custom"), Flag: true},
	{Name: "model", Validate: nonEmpty, Flag: true},
	{Name: "base_url", Validate: validURL, Flag: true},
	{Name: "approve", Default: DefaultApprove, Validate: oneOf("never", "shell", "mutating"), Flag: true},
	{Name: "max_turns", Default: strconv.Itoa(DefaultMaxTurns), Validate: positiveInt, Flag: true},
	{Name: "max_tool_output", Default: strconv.Itoa(DefaultMaxToolOutput), Validate: positiveInt},
	{Name: "elide_at", Default: DefaultElideAt, Validate: fractionOpen},
	{Name: "keep_tool_results", Default: strconv.Itoa(DefaultKeepToolResults), Validate: nonNegativeInt},
	{Name: "redact_skip_paths", Validate: anyValue},
	{Name: "compact_reserve_tokens", Default: strconv.Itoa(DefaultCompactReserve), Validate: positiveInt},
	{Name: "compact_keep_tokens", Default: strconv.Itoa(DefaultCompactKeep), Validate: positiveInt},
	{Name: "compact_model", Validate: anyValue}, // "" = the session's model
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

// Resolve merges layers: flag > env > file > default. An empty string counts
// as unset in every layer.
func Resolve(flags map[string]string, env func(string) string, file File) Effective {
	out := Effective{}
	for _, k := range Keys {
		switch {
		case flags[k.Name] != "":
			out[k.Name] = Value{flags[k.Name], SourceFlag}
		case env(k.EnvName()) != "":
			out[k.Name] = Value{env(k.EnvName()), envSource(k.EnvName())}
		case file.Values[k.Name] != "":
			out[k.Name] = Value{file.Values[k.Name], SourceFile}
		default:
			out[k.Name] = Value{k.Default, SourceDefault}
		}
	}
	return out
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
