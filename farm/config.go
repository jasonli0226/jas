// Package farm splits one task into parts run in parallel worktrees.
package farm

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"go.yaml.in/yaml/v3"
)

// Source values for Var.Source.
const (
	SourceProject  = "project"
	SourceUser     = "user"
	SourceDetected = "detected"
	SourceUnset    = "unset"
)

const (
	onFailureDrop  = "drop"
	onFailureAbort = "abort"

	// warnMaxWorkers is the worker count above which a notice is raised.
	warnMaxWorkers = 4

	farmFile = "farm.yaml"
)

// Var is one resolved variable.
type Var struct {
	Value  []string
	Source string // project | user | detected | unset
}

// BuiltinVars are the fixed variable names every config knows.
var BuiltinVars = []string{"check", "test_globs", "src_globs", "build", "lint"}

// Split bounds how a task is divided.
type Split struct {
	MaxWorkers    int
	MinParts      int
	DisjointFiles bool
}

// Worktree controls where and how part worktrees are made.
type Worktree struct {
	Dir   string
	Copy  []string
	Setup string
}

// Budget caps tokens, time and request rate.
type Budget struct {
	PerWorkerTokens   int
	FarmTokens        int
	MaxOutputTokens   int
	IntegratorReserve float64
	PerWorkerTimeout  time.Duration
	FarmTimeout       time.Duration
	RequestsPerMinute int
}

// BoardCfg bounds the shared board.
type BoardCfg struct {
	MaxRounds    int
	PostChars    int
	DeliverChars int
}

// Models names optional per-stage model overrides.
type Models struct {
	Planner    string
	Integrator string
}

// RoleDef is the raw role definition from config; whole-role replacement
// across layers.
type RoleDef struct {
	Tools   []string `yaml:"tools"`
	Writes  []string `yaml:"writes"`
	Prompt  string   `yaml:"prompt"`
	Approve string   `yaml:"approve"`
	Model   string   `yaml:"model"`
	Budget  int      `yaml:"budget"`
}

// Config is the merged farm configuration.
type Config struct {
	Check     string
	PartCheck string // run on each part before it is done; "" = off; unset in files = Check
	Vars      map[string]Var
	Split     Split
	Worktree  Worktree
	Budget    Budget
	Board     BoardCfg
	OnFailure string // drop | abort
	Models    Models
	Roles     map[string]RoleDef

	partCheckSet bool // some layer set part_check (possibly to "")
}

// fileCfg mirrors farm.yaml; pointers distinguish "unset" from zero.
type fileCfg struct {
	Check     string               `yaml:"check"`
	PartCheck *string              `yaml:"part_check"`
	Vars      map[string]yaml.Node `yaml:"vars"`
	Split     struct {
		MaxWorkers    *int  `yaml:"max_workers"`
		MinParts      *int  `yaml:"min_parts"`
		DisjointFiles *bool `yaml:"disjoint_files"`
	} `yaml:"split"`
	Worktree struct {
		Dir   *string   `yaml:"dir"`
		Copy  *[]string `yaml:"copy"`
		Setup *string   `yaml:"setup"`
	} `yaml:"worktree"`
	Budget struct {
		PerWorkerTokens   *int     `yaml:"per_worker_tokens"`
		FarmTokens        *int     `yaml:"farm_tokens"`
		MaxOutputTokens   *int     `yaml:"max_output_tokens"`
		IntegratorReserve *float64 `yaml:"integrator_reserve"`
		PerWorkerTimeout  *string  `yaml:"per_worker_timeout"`
		FarmTimeout       *string  `yaml:"farm_timeout"`
		RequestsPerMinute *int     `yaml:"requests_per_minute"`
	} `yaml:"budget"`
	Board struct {
		MaxRounds    *int `yaml:"max_rounds"`
		PostChars    *int `yaml:"post_chars"`
		DeliverChars *int `yaml:"deliver_chars"`
	} `yaml:"board"`
	OnFailure *string `yaml:"on_failure"`
	Models    struct {
		Planner    *string `yaml:"planner"`
		Integrator *string `yaml:"integrator"`
	} `yaml:"models"`
	Roles map[string]RoleDef `yaml:"roles"`
}

func defaults() Config {
	return Config{
		Split:     Split{MaxWorkers: 3, MinParts: 2, DisjointFiles: true},
		Budget:    Budget{PerWorkerTokens: 400000, FarmTokens: 1500000, MaxOutputTokens: 16000, IntegratorReserve: 0.2, PerWorkerTimeout: 30 * time.Minute, FarmTimeout: 2 * time.Hour},
		Board:     BoardCfg{MaxRounds: 3, PostChars: 500, DeliverChars: 2000},
		OnFailure: onFailureDrop,
		Roles:     map[string]RoleDef{},
	}
}

// LoadConfig layers built-in defaults, <home>/farm.yaml and (when trusted)
// <root>/.jas/farm.yaml, merged key by key. Notices are non-fatal messages.
func LoadConfig(root, home string, projectTrusted bool) (Config, []string, error) {
	cfg := defaults()
	var notices []string
	userVars := map[string][]string{}
	projVars := map[string][]string{}

	userPath := filepath.Join(home, farmFile)
	projPath := filepath.Join(root, ".jas", farmFile)

	if err := loadLayer(userPath, &cfg, userVars); err != nil {
		return Config{}, nil, err
	}
	if projectTrusted {
		if err := loadLayer(projPath, &cfg, projVars); err != nil {
			return Config{}, nil, err
		}
	} else if _, err := os.Stat(projPath); err == nil {
		notices = append(notices, "ignoring untrusted project .jas/farm.yaml")
	}

	cfg.Vars = resolveVars(root, userVars, projVars)
	if v := cfg.Vars["check"]; len(v.Value) > 0 {
		cfg.Check = v.Value[0]
	}
	if !cfg.partCheckSet {
		cfg.PartCheck = cfg.Check
	}
	if err := validate(cfg); err != nil {
		return Config{}, nil, err
	}
	if cfg.Split.MaxWorkers > warnMaxWorkers {
		notices = append(notices, fmt.Sprintf("split.max_workers %d is >%d; success falls as agents are added", cfg.Split.MaxWorkers, warnMaxWorkers))
	}
	return cfg, notices, nil
}

// loadLayer reads one file (missing is fine) and applies it onto cfg.
func loadLayer(path string, cfg *Config, vars map[string][]string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var f fileCfg
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w", path, err)
	}
	if err := apply(cfg, f, vars); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func set[T any](dst *T, src *T) {
	if src != nil {
		*dst = *src
	}
}

func apply(cfg *Config, f fileCfg, vars map[string][]string) error {
	for name, n := range f.Vars {
		vals, err := nodeStrings(n)
		if err != nil {
			return fmt.Errorf("vars.%s: %w", name, err)
		}
		vars[name] = vals
	}
	if f.Check != "" {
		vars["check"] = []string{f.Check}
	}
	if f.PartCheck != nil {
		cfg.PartCheck, cfg.partCheckSet = *f.PartCheck, true
	}
	set(&cfg.Split.MaxWorkers, f.Split.MaxWorkers)
	set(&cfg.Split.MinParts, f.Split.MinParts)
	set(&cfg.Split.DisjointFiles, f.Split.DisjointFiles)
	set(&cfg.Worktree.Dir, f.Worktree.Dir)
	set(&cfg.Worktree.Copy, f.Worktree.Copy)
	set(&cfg.Worktree.Setup, f.Worktree.Setup)
	set(&cfg.Budget.PerWorkerTokens, f.Budget.PerWorkerTokens)
	set(&cfg.Budget.FarmTokens, f.Budget.FarmTokens)
	set(&cfg.Budget.MaxOutputTokens, f.Budget.MaxOutputTokens)
	set(&cfg.Budget.IntegratorReserve, f.Budget.IntegratorReserve)
	set(&cfg.Budget.RequestsPerMinute, f.Budget.RequestsPerMinute)
	for _, d := range []struct {
		name string
		src  *string
		dst  *time.Duration
	}{
		{"per_worker_timeout", f.Budget.PerWorkerTimeout, &cfg.Budget.PerWorkerTimeout},
		{"farm_timeout", f.Budget.FarmTimeout, &cfg.Budget.FarmTimeout},
	} {
		if d.src == nil {
			continue
		}
		v, err := time.ParseDuration(*d.src)
		if err != nil {
			return fmt.Errorf("budget.%s: %w", d.name, err)
		}
		*d.dst = v
	}
	set(&cfg.Board.MaxRounds, f.Board.MaxRounds)
	set(&cfg.Board.PostChars, f.Board.PostChars)
	set(&cfg.Board.DeliverChars, f.Board.DeliverChars)
	set(&cfg.OnFailure, f.OnFailure)
	set(&cfg.Models.Planner, f.Models.Planner)
	set(&cfg.Models.Integrator, f.Models.Integrator)
	for name, r := range f.Roles {
		cfg.Roles[name] = r // whole-role replacement
	}
	return nil
}

// nodeStrings accepts a scalar or a list of scalars.
func nodeStrings(n yaml.Node) ([]string, error) {
	switch n.Kind {
	case yaml.ScalarNode:
		return []string{n.Value}, nil
	case yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			if c.Kind != yaml.ScalarNode {
				return nil, errors.New("list items must be strings")
			}
			out = append(out, c.Value)
		}
		return out, nil
	}
	return nil, errors.New("must be a string or a list of strings")
}

// resolveVars applies project -> user -> detected -> unset per variable.
func resolveVars(root string, user, proj map[string][]string) map[string]Var {
	detected := Detect(root)
	out := map[string]Var{}
	names := map[string]bool{}
	for _, m := range []map[string][]string{user, proj, detected} {
		for k := range m {
			names[k] = true
		}
	}
	for _, k := range BuiltinVars {
		names[k] = true
	}
	for name := range names {
		switch {
		case proj[name] != nil:
			out[name] = Var{proj[name], SourceProject}
		case user[name] != nil:
			out[name] = Var{user[name], SourceUser}
		case detected[name] != nil:
			out[name] = Var{detected[name], SourceDetected}
		default:
			out[name] = Var{nil, SourceUnset}
		}
	}
	return out
}

var varRef = regexp.MustCompile(`\$\{([^}]*)\}`)

func validate(cfg Config) error {
	if cfg.OnFailure != onFailureDrop && cfg.OnFailure != onFailureAbort {
		return fmt.Errorf("on_failure %q: want drop or abort", cfg.OnFailure)
	}
	roles := make([]string, 0, len(cfg.Roles))
	for name := range cfg.Roles {
		roles = append(roles, name)
	}
	sort.Strings(roles)
	for _, name := range roles {
		r := cfg.Roles[name]
		// Only writes take ${var} expansion; prompts and tools are literal.
		for _, s := range r.Writes {
			for _, m := range varRef.FindAllStringSubmatch(s, -1) {
				if _, ok := cfg.Vars[m[1]]; !ok {
					return fmt.Errorf("role %s: unknown variable ${%s}", name, m[1])
				}
			}
		}
	}
	return nil
}
