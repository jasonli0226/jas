package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func linkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	assert.NoErr(t, os.MkdirAll(filepath.Dir(link), 0o755))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
}

// runProfileRead runs jas with HOME=userHome and the default JAS_HOME, has
// the model read path, and returns the system prompt, the tool result
// request body and stderr.
func runProfileRead(t *testing.T, userHome, path string) (sys, result, errOut string) {
	t.Helper()
	args, err := json.Marshal(map[string]string{"path": path})
	assert.NoErr(t, err)
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(args)}}},
		{Text: "done"},
	})
	_, errOut, code := runJas(t, jasEnv(userHome, url), "run", "q")
	assert.Eq(t, code, 0)
	return systemPrompt(t, bodies()[0]), string(bodies()[1]), errOut
}

const shownDemo = "~/.jas/profiles/default/skills/demo/SKILL.md"

// assertDemoLoaded runs jas, reads the demo skill as the catalog shows it
// and checks it loaded without a denial or an ignored-skills warning.
func assertDemoLoaded(t *testing.T, userHome string) {
	t.Helper()
	sys, result, errOut := runProfileRead(t, userHome, shownDemo)
	assert.Contains(t, sys, "<name>demo</name>")
	assert.Contains(t, result, "LINK-BODY-7120")
	assert.NotContains(t, errOut, "denied")
	assert.NotContains(t, errOut, "ignored")
}

func TestRun_ProfileSkillSymlinkedJasHome(t *testing.T) {
	userHome := t.TempDir()
	real := filepath.Join(t.TempDir(), "jasdata")
	writeSkill(t, filepath.Join(real, "profiles", "default", "skills"), "demo", "Use when demoing", "LINK-BODY-7120\n")
	linkOrSkip(t, real, filepath.Join(userHome, ".jas"))
	assertDemoLoaded(t, userHome)
}

func TestRun_ProfileSkillSymlinkedProfileDir(t *testing.T) {
	userHome := t.TempDir()
	dotfiles := filepath.Join(t.TempDir(), "default")
	writeSkill(t, filepath.Join(dotfiles, "skills"), "demo", "Use when demoing", "LINK-BODY-7120\n")
	linkOrSkip(t, dotfiles, filepath.Join(userHome, ".jas", "profiles", "default"))
	assertDemoLoaded(t, userHome)
}

func TestRun_ProfileSkillsLinkOutside(t *testing.T) {
	userHome := t.TempDir()
	outside := filepath.Join(t.TempDir(), "dotskills")
	writeSkill(t, outside, "demo", "Use when demoing", "LINK-BODY-7120\n")
	linkOrSkip(t, outside, profileSkills(userHome))
	sys, result, errOut := runProfileRead(t, userHome, shownDemo)
	assert.Contains(t, sys, "<name>demo</name>")
	assert.Contains(t, result, "LINK-BODY-7120")
	assert.NotContains(t, errOut, "denied")
	assert.Contains(t, errOut, "resolves outside") // the existing outside-the-home warning
}

func TestRun_ProfileSkillsLinkToJasHomeIgnored(t *testing.T) {
	userHome := t.TempDir()
	jas := filepath.Join(userHome, ".jas")
	assert.NoErr(t, os.MkdirAll(filepath.Join(jas, "profiles", "default"), 0o700))
	assert.NoErr(t, os.WriteFile(filepath.Join(jas, "auth.json"), []byte(`{"x":{"key":"AUTH-SECRET-8812"}}`), 0o600))
	linkOrSkip(t, jas, profileSkills(userHome))
	sys, result, errOut := runProfileRead(t, userHome, "~/.jas/profiles/default/skills/auth.json")
	assertReadRefused(t, result)
	assert.NotContains(t, result, "AUTH-SECRET-8812")
	assert.NotContains(t, sys, "AUTH-SECRET-8812")
	assert.Contains(t, errOut, "profile skills "+profileSkills(userHome)+" ignored")
}

func TestRun_ProfileSkillsLinkToOtherProfileIgnored(t *testing.T) {
	userHome := t.TempDir()
	jas := filepath.Join(userHome, ".jas")
	other := filepath.Join(jas, "profiles", "other", "skills")
	loc := writeSkill(t, other, "theirs", "OTHER-SKILL-3301", "OTHER-BODY-3302\n")
	assert.NoErr(t, os.MkdirAll(filepath.Join(jas, "profiles", "default"), 0o700))
	linkOrSkip(t, other, profileSkills(userHome))
	sys, result, errOut := runProfileRead(t, userHome, loc)
	assert.NotContains(t, sys, "OTHER-SKILL-3301")
	assertReadRefused(t, result)
	assert.NotContains(t, result, "OTHER-BODY-3302")
	assert.Contains(t, errOut, "profile skills "+profileSkills(userHome)+" ignored")
}

// assertReadRefused checks the read tool refused the path: no read root
// covers it (outside the project) or the deny floor matched.
func assertReadRefused(t *testing.T, result string) {
	t.Helper()
	if !strings.Contains(result, "path outside project root") && !strings.Contains(result, "path denied by policy") {
		t.Fatalf("read was not refused: %s", result)
	}
}
