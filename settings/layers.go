package settings

import (
	"fmt"
	"path/filepath"
)

// Scope names a config file layer the user can write to.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProfile Scope = "profile"
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

// Layers holds the config files: global, the active profile's, project and
// local. auth.json and trust.json stay global-only and are not part of Layers.
// ProfileName names the profile Profile was loaded from ("" when none).
// ProfileParents are the profile's ancestors, nearest parent first; they rank
// below Profile and above Global.
type Layers struct {
	Global, Profile, Project, Local File
	ProfileName                     string
	ProfileParents                  []ProfileFile
}

// ProfileFile is one ancestor profile's config.json with the profile's name,
// which labels where its values came from.
type ProfileFile struct {
	Name string
	File File
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

// ParseScope accepts global|profile|project|local.
func ParseScope(s string) (Scope, error) {
	switch Scope(s) {
	case ScopeGlobal, ScopeProfile, ScopeProject, ScopeLocal:
		return Scope(s), nil
	}
	return "", fmt.Errorf("unknown scope %q: want global|profile|project|local", s)
}

// Path returns the config file path for scope. Project and local need a root;
// the profile scope needs a name, so use PathFor.
func (s Scope) Path(home, root string) (string, error) {
	return s.PathFor(home, root, "")
}

// PathFor is Path with the active profile name for the profile scope.
func (s Scope) PathFor(home, root, profile string) (string, error) {
	switch s {
	case ScopeProfile:
		if home == "" {
			return "", fmt.Errorf("jas home not set")
		}
		if profile == "" {
			return "", fmt.Errorf("no active profile")
		}
		return ProfileConfigPath(home, profile), nil
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
	case ScopeProfile:
		return l.Profile
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
	case ScopeProfile:
		l.Profile = f
	case ScopeProject:
		l.Project = f
	case ScopeLocal:
		l.Local = f
	}
	return l
}

// Merged overlays models and providers: local > project > profile > parents
// (nearest first) > global. Plain Values are left empty — callers use Resolve
// for those.
func (l Layers) Merged() File {
	out := File{
		Models:    map[string]ModelOverride{},
		Providers: map[string]ProviderEntry{},
	}
	files := []File{l.Global}
	for i := len(l.ProfileParents) - 1; i >= 0; i-- {
		files = append(files, l.ProfileParents[i].File)
	}
	files = append(files, l.Profile, l.Project, l.Local)
	for _, f := range files {
		for k, v := range f.Models {
			out.Models[k] = v
		}
		for k, v := range f.Providers {
			out.Providers[k] = v
		}
	}
	return out
}

// ContextOverrides merges model context windows: local > project > profile > parents > global.
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
	if m, found := l.Profile.Models[name]; found {
		return m.ContextWindow, ProfileSource(l.ProfileName), true
	}
	for _, p := range l.ProfileParents {
		if m, found := p.File.Models[name]; found {
			return m.ContextWindow, ProfileSource(p.Name), true
		}
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

// candidate is a value with the layer that set it.
type candidate struct {
	value string
	src   Source
}

// profileValues lists key's value from the own profile file, then each parent
// nearest first, so pick can take the first non-empty one.
func (l Layers) profileValues(key string) []candidate {
	out := []candidate{{l.Profile.Values[key], ProfileSource(l.ProfileName)}}
	for _, p := range l.ProfileParents {
		out = append(out, candidate{p.File.Values[key], ProfileSource(p.Name)})
	}
	return out
}

// pick returns the first non-empty value among local, project, the profile
// candidates (own first, then parents nearest first) and global.
func pick(local, project string, profile []candidate, global string) (string, Source) {
	switch {
	case local != "":
		return local, SourceLocal
	case project != "":
		return project, SourceProject
	}
	for _, c := range profile {
		if c.value != "" {
			return c.value, c.src
		}
	}
	if global != "" {
		return global, SourceFile
	}
	return "", SourceDefault
}
