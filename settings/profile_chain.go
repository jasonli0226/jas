package settings

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// ProfileMetaFile is the per-profile file that declares inheritance.
const ProfileMetaFile = "profile.yaml"

// MaxProfileChain is the most profiles in one inheritance chain, the active
// profile included.
const MaxProfileChain = 3

const chainArrow = " → "

// ProfileMeta is the parsed profile.yaml of one profile.
type ProfileMeta struct {
	Extends       string   `yaml:"extends"`
	ReplaceAgents bool     `yaml:"replace_agents"`
	Exclude       []string `yaml:"exclude"`
}

// ChainLink is one profile in a resolved chain with its profile.yaml.
type ChainLink struct {
	Name string
	Meta ProfileMeta
}

// ProfileMetaPath is <home>/profiles/<name>/profile.yaml.
func ProfileMetaPath(home, name string) string {
	return filepath.Join(ProfileDir(home, name), ProfileMetaFile)
}

// LoadProfileMeta reads a profile's profile.yaml. A missing file or an empty
// document is the zero value. Unknown keys are errors, so a typo is not
// silently ignored.
func LoadProfileMeta(home, name string) (ProfileMeta, error) {
	path := ProfileMetaPath(home, name)
	fail := func(err error) (ProfileMeta, error) {
		return ProfileMeta{}, fmt.Errorf("profile %s: %s: %w", name, path, err)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ProfileMeta{}, nil
	}
	if err != nil {
		return fail(err)
	}
	var m ProfileMeta
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil && !errors.Is(err, io.EOF) {
		return fail(err)
	}
	if m.Extends != "" {
		if err := ValidProfileName(m.Extends); err != nil {
			return fail(err)
		}
	}
	return m, nil
}

// ResolveChain returns the profiles name inherits from, root first and name
// last. A parent must have a profile directory, except "default", which
// always resolves. name itself is not checked.
func ResolveChain(home, name string) ([]ChainLink, error) {
	var up []ChainLink // active first
	names := []string{}
	for cur := name; ; {
		meta, err := LoadProfileMeta(home, cur)
		if err != nil {
			return nil, err
		}
		up = append(up, ChainLink{Name: cur, Meta: meta})
		names = append(names, cur)
		parent := meta.Extends
		if parent == "" {
			break
		}
		if slices.Contains(names, parent) {
			return nil, fmt.Errorf("profile chain loops: %s", strings.Join(append(names, parent), chainArrow))
		}
		if fi, err := os.Stat(ProfileDir(home, parent)); parent != DefaultProfile && (err != nil || !fi.IsDir()) {
			return nil, fmt.Errorf("profile %q extends %q, which does not exist (%s)", cur, parent, ProfileDir(home, parent))
		}
		cur = parent
	}
	slices.Reverse(up)
	if len(up) > MaxProfileChain {
		slices.Reverse(names)
		return nil, fmt.Errorf("profile chain %s is longer than %d profiles", strings.Join(names, chainArrow), MaxProfileChain)
	}
	return up, nil
}
