package settings

import (
	"fmt"
	"path/filepath"
)

// Scope names a config file layer the user can write to.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
	ScopeLocal   Scope = "local"
)

// Source labels for the project and local config files. SourceFile remains
// "global config" for the ~/.jas/config.json layer.
const (
	SourceProject Source = "project config"
	SourceLocal   Source = "local config"
)

const (
	projectDirName   = ".jas"
	localConfigFile  = "config.local.json"
	globalConfigFile = "config.json"
)

// Layers holds the three Claude-style config files. auth.json and trust.json
// stay global-only and are not part of Layers.
type Layers struct {
	Global, Project, Local File
}

// ProjectDir is <root>/.jas.
func ProjectDir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, projectDirName)
}

// GlobalConfigPath is ~/.jas/config.json (or $JAS_HOME/config.json).
func GlobalConfigPath(home string) string {
	return filepath.Join(home, globalConfigFile)
}

// ProjectConfigPath is <root>/.jas/config.json (shared, commit).
func ProjectConfigPath(root string) string {
	return filepath.Join(ProjectDir(root), globalConfigFile)
}

// LocalConfigPath is <root>/.jas/config.local.json (personal, gitignore).
func LocalConfigPath(root string) string {
	return filepath.Join(ProjectDir(root), localConfigFile)
}

// LoadLayers reads global, project and local config files. A missing file is
// empty. root may be "" to load only the global file.
func LoadLayers(home, root string) (Layers, error) {
	global, err := LoadFile(GlobalConfigPath(home))
	if err != nil {
		return Layers{}, err
	}
	var project, local File
	if root != "" {
		if project, err = LoadFile(ProjectConfigPath(root)); err != nil {
			return Layers{}, err
		}
		if local, err = LoadFile(LocalConfigPath(root)); err != nil {
			return Layers{}, err
		}
	}
	return Layers{Global: global, Project: project, Local: local}, nil
}

// ParseScope accepts global|project|local.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeGlobal, ScopeProject, ScopeLocal:
		return Scope(s), nil
	}
	return "", fmt.Errorf("unknown scope %q: want global|project|local", s)
}

// Path returns the config file path for scope. Project and local need a root.
func (s Scope) Path(home, root string) (string, error) {
	switch s {
	case ScopeGlobal:
		if home == "" {
			return "", fmt.Errorf("jas home not set")
		}
		return GlobalConfigPath(home), nil
	case ScopeProject, ScopeLocal:
		if root == "" {
			return "", fmt.Errorf("no project directory for %s config", s)
		}
		if s == ScopeProject {
			return ProjectConfigPath(root), nil
		}
		return LocalConfigPath(root), nil
	}
	return "", fmt.Errorf("unknown scope %q", s)
}

// File returns the layer for scope.
func (l Layers) File(scope Scope) File {
	switch scope {
	case ScopeGlobal:
		return l.Global
	case ScopeProject:
		return l.Project
	case ScopeLocal:
		return l.Local
	}
	return File{}
}

// WithFile returns a copy of l with scope replaced by f.
func (l Layers) WithFile(scope Scope, f File) Layers {
	switch scope {
	case ScopeGlobal:
		l.Global = f
	case ScopeProject:
		l.Project = f
	case ScopeLocal:
		l.Local = f
	}
	return l
}

// Merged overlays models and providers: local > project > global. Plain
// Values are left empty — callers use Resolve for those.
func (l Layers) Merged() File {
	out := File{
		Models:    map[string]ModelOverride{},
		Providers: map[string]ProviderEntry{},
	}
	for _, f := range []File{l.Global, l.Project, l.Local} {
		for k, v := range f.Models {
			out.Models[k] = v
		}
		for k, v := range f.Providers {
			out.Providers[k] = v
		}
	}
	return out
}

// ContextOverrides merges model context windows: local > project > global.
func (l Layers) ContextOverrides() map[string]int {
	return l.Merged().ContextOverrides()
}

// ModelSource returns the context window and which layer set it.
func (l Layers) ModelSource(name string) (window int, source Source, ok bool) {
	if m, found := l.Local.Models[name]; found {
		return m.ContextWindow, SourceLocal, true
	}
	if m, found := l.Project.Models[name]; found {
		return m.ContextWindow, SourceProject, true
	}
	if m, found := l.Global.Models[name]; found {
		return m.ContextWindow, SourceFile, true
	}
	return 0, "", false
}

// ModelNames returns every model override name across layers, sorted.
func (l Layers) ModelNames() []string {
	return l.Merged().ModelNames()
}

// pick returns the first non-empty value among local, project, global.
func pick(local, project, global string) (string, Source) {
	switch {
	case local != "":
		return local, SourceLocal
	case project != "":
		return project, SourceProject
	case global != "":
		return global, SourceFile
	}
	return "", SourceDefault
}
