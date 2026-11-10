package settings

import (
	"fmt"
	"slices"
)

// userOnlyKeys name endpoints: the model endpoint receives the API key and the
// search endpoint receives the owner's queries, so only the owner's own
// config, flags and env choose them, never a file inside the repository.
var userOnlyKeys = []string{"base_url", "search_base_url"}

// UserOnly reports whether key can be set only in the global config, by
// flag or by env: base_url, search_base_url and providers.<name>.base_url.
func UserOnly(key string) bool {
	if _, ok := providerKey(key); ok {
		return true
	}
	return slices.Contains(userOnlyKeys, key)
}

// ForProject returns the layers a session resolves from, given the project
// trust decision. Untrusted, the project and local files (both live in the
// repository) are ignored. Trusted, they apply except for user-only keys,
// which are dropped with one warning each. l itself is not changed, so the
// files can still be written back as they are.
func (l Layers) ForProject(trusted bool) (Layers, []string) {
	profile, warnings := withoutProfileForbidden(l.Profile, l.ProfileName)
	if !trusted {
		return Layers{Global: l.Global, Profile: profile, ProfileName: l.ProfileName}, warnings
	}
	project, w := withoutUserOnly(l.Project, SourceProject)
	warnings = append(warnings, w...)
	local, w := withoutUserOnly(l.Local, SourceLocal)
	warnings = append(warnings, w...)
	return Layers{Global: l.Global, Profile: profile, ProfileName: l.ProfileName, Project: project, Local: local}, warnings
}

// HasProject reports whether the project or local file sets anything.
func (l Layers) HasProject() bool {
	for _, f := range []File{l.Project, l.Local} {
		if len(f.Values)+len(f.Models)+len(f.Providers) > 0 {
			return true
		}
	}
	return false
}

// withoutUserOnly copies f without its user-only keys, naming each dropped one.
func withoutUserOnly(f File, src Source) (File, []string) {
	out := f.clone()
	var dropped []string
	for _, k := range userOnlyKeys {
		if out.Values[k] != "" {
			dropped = append(dropped, k)
		}
		delete(out.Values, k)
	}
	for name := range out.Providers {
		dropped = append(dropped, providersPrefix+name+baseURLSuffix)
	}
	out.Providers = nil
	slices.Sort(dropped)
	warnings := make([]string, len(dropped))
	for i, k := range dropped {
		warnings[i] = fmt.Sprintf("ignoring %s in %s: endpoints come only from ~/.jas/config.json, flags or env (jas config set %s <url>)", k, src, k)
	}
	return out, warnings
}

// CheckWriteScope refuses a user-only key outside the global config, where
// a session would never read it.
func CheckWriteScope(key string, scope Scope) error {
	if scope == ScopeGlobal || !UserOnly(key) {
		return nil
	}
	return fmt.Errorf("%s is global-only: sessions never read an endpoint from %s config (drop --%s)", key, scope, scope)
}
