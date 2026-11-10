package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

// DefaultProfile is the profile used when none is chosen. It always exists.
const DefaultProfile = "default"

const maxProfileName = 64

var profileNameRE = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// profileForbiddenKeys are plain keys a profile config may not set: endpoints
// stay global-only (a trusted project can pick a profile), and the profile
// cannot choose itself.
var profileForbiddenKeys = []string{"base_url", "profile", "provider", "search_base_url"}

// ProfilesDir is <home>/profiles.
func ProfilesDir(home string) string { return filepath.Join(home, "profiles") }

// ProfileDir is <home>/profiles/<name>.
func ProfileDir(home, name string) string { return filepath.Join(ProfilesDir(home), name) }

// ProfileConfigPath is <home>/profiles/<name>/config.json.
func ProfileConfigPath(home, name string) string {
	return filepath.Join(ProfileDir(home, name), globalConfigFile)
}

// ProfileSource labels values that come from a profile's config.json.
func ProfileSource(name string) Source { return Source("profile config (" + name + ")") }

// ValidProfileName accepts 1-64 characters of a-z, 0-9 and "-", starting and
// ending with a letter or digit.
func ValidProfileName(name string) error {
	if len(name) == 0 || len(name) > maxProfileName || !profileNameRE.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: want 1-%d characters of a-z, 0-9 and -, starting and ending with a letter or digit", name, maxProfileName)
	}
	return nil
}

// ListProfiles returns the sorted names of the valid profile directories under
// home, always including "default". A missing profiles directory is not an error.
func ListProfiles(home string) ([]string, error) {
	names := []string{DefaultProfile}
	entries, err := os.ReadDir(ProfilesDir(home))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() && ValidProfileName(e.Name()) == nil && e.Name() != DefaultProfile {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	return names, nil
}

// EnsureDefaultProfile creates the default profile directory, best effort: a
// read-only home just means the default profile stays virtual.
func EnsureDefaultProfile(home string) {
	_ = os.MkdirAll(ProfileDir(home, DefaultProfile), 0o700)
}

// ResolveProfile picks the active profile name: flag > env > local > project >
// global > default. The profile layer never chooses itself. Call it on layers
// already passed through ForProject so an untrusted project is ignored.
func ResolveProfile(flags map[string]string, env func(string) string, l Layers) (string, Source) {
	v := Resolve(flags, env, l)["profile"]
	return v.Raw, v.Source
}

// WithProfile returns a copy of l with the profile layer set to f.
func (l Layers) WithProfile(name string, f File) Layers {
	l.Profile = f
	l.ProfileName = name
	return l
}

// withoutProfileForbidden copies f without the keys a profile may not set,
// returning one warning per dropped key.
func withoutProfileForbidden(f File, name string) (File, []string) {
	out := f.clone()
	var dropped []string
	for _, k := range profileForbiddenKeys {
		if out.Values[k] != "" {
			dropped = append(dropped, k)
		}
		delete(out.Values, k)
	}
	for p := range out.Providers {
		dropped = append(dropped, providersPrefix+p+baseURLSuffix)
	}
	out.Providers = nil
	slices.Sort(dropped)
	warnings := make([]string, len(dropped))
	for i, k := range dropped {
		warnings[i] = fmt.Sprintf("ignoring %s in %s: a profile cannot set endpoints or choose a profile (use ~/.jas/config.json)", k, ProfileSource(name))
	}
	return out, warnings
}
