package settings

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

// mkProfile creates a profile directory, writing profile.yaml when yaml is set.
func mkProfile(t *testing.T, home, name, yaml string) {
	t.Helper()
	dir := ProfileDir(home, name)
	assert.NoErr(t, os.MkdirAll(dir, 0o700))
	if yaml != "" {
		assert.NoErr(t, os.WriteFile(filepath.Join(dir, ProfileMetaFile), []byte(yaml), 0o600))
	}
}

func chainNames(c []ChainLink) []string {
	var out []string
	for _, l := range c {
		out = append(out, l.Name)
	}
	return out
}

func TestLoadProfileMeta_Missing(t *testing.T) {
	m, err := LoadProfileMeta(t.TempDir(), "ds")
	assert.NoErr(t, err)
	assert.Eq(t, reflect.DeepEqual(m, ProfileMeta{}), true)
}

func TestLoadProfileMeta_EmptyFile(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "ds", "# just a comment\n")
	m, err := LoadProfileMeta(home, "ds")
	assert.NoErr(t, err)
	assert.Eq(t, reflect.DeepEqual(m, ProfileMeta{}), true)
}

func TestLoadProfileMeta_Fields(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "ds", "extends: programmer\nreplace_agents: true\nexclude: [tdd]\n")
	m, err := LoadProfileMeta(home, "ds")
	assert.NoErr(t, err)
	want := ProfileMeta{Extends: "programmer", ReplaceAgents: true, Exclude: []string{"tdd"}}
	assert.Eq(t, reflect.DeepEqual(m, want), true)
}

func TestLoadProfileMeta_UnknownKey(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "ds", "replace: true\n")
	_, err := LoadProfileMeta(home, "ds")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "profile.yaml")
	assert.Contains(t, err.Error(), "replace")
}

func TestLoadProfileMeta_BadParentName(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "ds", "extends: Programmer\n")
	_, err := LoadProfileMeta(home, "ds")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Contains(t, err.Error(), "invalid profile name")
}

func TestResolveChain_Flat(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "ds", "")
	c, err := ResolveChain(home, "ds")
	assert.NoErr(t, err)
	assert.Eq(t, reflect.DeepEqual(c, []ChainLink{{Name: "ds"}}), true)
}

func TestResolveChain_TwoAndThree(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "a", "")
	mkProfile(t, home, "b", "extends: a\n")
	mkProfile(t, home, "c", "extends: b\n")
	c, err := ResolveChain(home, "c")
	assert.NoErr(t, err)
	assert.Eq(t, reflect.DeepEqual(chainNames(c), []string{"a", "b", "c"}), true)
	assert.Eq(t, c[2].Meta.Extends, "b")
}

func TestResolveChain_MissingParent(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "b", "extends: nope\n")
	_, err := ResolveChain(home, "b")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Eq(t, err.Error(), `profile "b" extends "nope", which does not exist (`+ProfileDir(home, "nope")+`)`)
}

func TestResolveChain_Cycle(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "a", "extends: b\n")
	mkProfile(t, home, "b", "extends: a\n")
	_, err := ResolveChain(home, "a")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Eq(t, err.Error(), "profile chain loops: a → b → a")

	mkProfile(t, home, "s", "extends: s\n")
	_, err = ResolveChain(home, "s")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Eq(t, err.Error(), "profile chain loops: s → s")
}

func TestResolveChain_TooLong(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "a", "")
	mkProfile(t, home, "b", "extends: a\n")
	mkProfile(t, home, "c", "extends: b\n")
	mkProfile(t, home, "d", "extends: c\n")
	_, err := ResolveChain(home, "d")
	if err == nil {
		t.Fatal("want error")
	}
	assert.Eq(t, err.Error(), "profile chain a → b → c → d is longer than 3 profiles")
}

func TestResolveChain_VirtualDefault(t *testing.T) {
	home := t.TempDir()
	mkProfile(t, home, "b", "extends: default\n")
	c, err := ResolveChain(home, "b")
	assert.NoErr(t, err)
	assert.Eq(t, reflect.DeepEqual(chainNames(c), []string{"default", "b"}), true)
}
