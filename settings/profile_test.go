package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func vals(kv ...string) File {
	f := File{Values: map[string]string{}}
	for i := 0; i < len(kv); i += 2 {
		f.Values[kv[i]] = kv[i+1]
	}
	return f
}

func TestResolve_ProfileLayerOrder(t *testing.T) {
	l := Layers{Global: vals("approve", "never"), Profile: vals("approve", "shell"), ProfileName: "ds"}
	got := Resolve(nil, envOf(nil), l)
	assert.Eq(t, got["approve"], Value{"shell", ProfileSource("ds")})

	l.Project = vals("approve", "mutating")
	got = Resolve(nil, envOf(nil), l)
	assert.Eq(t, got["approve"], Value{"mutating", SourceProject})
}

func TestResolve_ProfileModelsMap(t *testing.T) {
	mo := func(n int) map[string]ModelOverride { return map[string]ModelOverride{"m": {ContextWindow: n}} }
	l := Layers{
		Global:      File{Models: mo(1000)},
		Profile:     File{Models: mo(2000)},
		ProfileName: "ds",
	}
	assert.Eq(t, l.ContextOverrides()["m"], 2000)
	w, src, ok := l.ModelSource("m")
	assert.Eq(t, ok, true)
	assert.Eq(t, w, 2000)
	assert.Eq(t, src, ProfileSource("ds"))

	l.Project = File{Models: mo(3000)}
	assert.Eq(t, l.ContextOverrides()["m"], 3000)
}

func TestResolve_ProfileProvidersOverlay(t *testing.T) {
	pe := func(u string) map[string]ProviderEntry { return map[string]ProviderEntry{"x": {BaseURL: u}} }
	l := Layers{Global: File{Providers: pe("http://g/v1")}, Profile: File{Providers: pe("http://p/v1")}, ProfileName: "ds"}
	got := Resolve(nil, envOf(nil), l)
	assert.Eq(t, got["providers.x.base_url"], Value{"http://p/v1", ProfileSource("ds")})
}

func TestForProject_ProfileDropsEndpointKeys(t *testing.T) {
	prof := vals("provider", "custom", "base_url", "http://e/v1", "search_base_url", "http://s", "profile", "other", "model", "m")
	prof.Providers = map[string]ProviderEntry{"x": {BaseURL: "http://x/v1"}}
	l := Layers{Profile: prof, ProfileName: "ds"}
	for _, trusted := range []bool{true, false} {
		got, warns := l.ForProject(trusted)
		assert.Eq(t, got.ProfileName, "ds")
		assert.Eq(t, len(got.Profile.Values), 1)
		assert.Eq(t, got.Profile.Values["model"], "m")
		assert.Eq(t, got.Profile.Providers == nil, true)
		assert.Eq(t, len(warns), 5)
		for _, w := range warns {
			assert.Contains(t, w, "profile config (ds)")
		}
	}
	// the receiver is untouched
	assert.Eq(t, len(l.Profile.Values), 5)
}

func TestResolveProfile_Order(t *testing.T) {
	l := Layers{
		Global:  vals("profile", "g"),
		Profile: vals("profile", "ignored"),
		Project: vals("profile", "p"),
		Local:   vals("profile", "l"),
	}
	name, src := ResolveProfile(map[string]string{"profile": "f"}, envOf(map[string]string{"JAS_PROFILE": "e"}), l)
	assert.Eq(t, name, "f")
	assert.Eq(t, src, SourceFlag)
	name, src = ResolveProfile(nil, envOf(map[string]string{"JAS_PROFILE": "e"}), l)
	assert.Eq(t, name, "e")
	assert.Eq(t, src, envSource("JAS_PROFILE"))
	name, src = ResolveProfile(nil, envOf(nil), l)
	assert.Eq(t, name, "l")
	assert.Eq(t, src, SourceLocal)
	l.Local = File{}
	name, src = ResolveProfile(nil, envOf(nil), l)
	assert.Eq(t, name, "p")
	assert.Eq(t, src, SourceProject)
	l.Project = File{}
	name, src = ResolveProfile(nil, envOf(nil), l)
	assert.Eq(t, name, "g")
	assert.Eq(t, src, SourceFile)
	l.Global = File{}
	name, src = ResolveProfile(nil, envOf(nil), l)
	assert.Eq(t, name, DefaultProfile)
	assert.Eq(t, src, SourceDefault)
}

func TestResolveProfile_UntrustedProjectIgnored(t *testing.T) {
	l := Layers{Project: vals("profile", "ds")}
	l, _ = l.ForProject(false)
	name, src := ResolveProfile(nil, envOf(nil), l)
	assert.Eq(t, name, DefaultProfile)
	assert.Eq(t, src, SourceDefault)
}

func TestValidProfileName(t *testing.T) {
	for _, ok := range []string{"ds", "data-sci", "a1"} {
		assert.NoErr(t, ValidProfileName(ok))
	}
	for _, bad := range []string{"DS", "-x", "x-", "a_b", "", strings.Repeat("a", 65)} {
		if ValidProfileName(bad) == nil {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestListProfiles(t *testing.T) {
	home := t.TempDir()
	got, err := ListProfiles(home)
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[default]")

	dir := ProfilesDir(home)
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "ds"), 0o700))
	assert.NoErr(t, os.MkdirAll(filepath.Join(dir, "Bad_Name"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "notes"), nil, 0o600))
	got, err = ListProfiles(home)
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[default ds]")
}

func TestEnsureDefaultProfile_ReadOnlyHome(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics")
	}
	home := t.TempDir()
	assert.NoErr(t, os.Chmod(home, 0o500))
	t.Cleanup(func() { os.Chmod(home, 0o700) })
	EnsureDefaultProfile(home)
	got, err := ListProfiles(home)
	assert.NoErr(t, err)
	assert.Eq(t, fmt.Sprint(got), "[default]")
}

func TestScopeProfilePath(t *testing.T) {
	s, err := ParseScope("profile")
	assert.NoErr(t, err)
	p, err := s.PathFor("/h", "/r", "ds")
	assert.NoErr(t, err)
	assert.Eq(t, p, ProfileConfigPath("/h", "ds"))
	if _, err := s.PathFor("/h", "/r", ""); err == nil || !strings.Contains(err.Error(), "no active profile") {
		t.Errorf("want no active profile error, got %v", err)
	}
	if _, err := s.Path("/h", "/r"); err == nil {
		t.Error("Path with profile scope should fail")
	}
}
