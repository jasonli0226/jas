package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
	"github.com/jasonli0226/jas/skills"
)

// writeSkill creates <base>/<name>/SKILL.md and returns its resolved path.
func writeSkill(t *testing.T, base, name, desc, body string) string {
	t.Helper()
	dir := filepath.Join(base, name)
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	p := filepath.Join(dir, "SKILL.md")
	assert.NoErr(t, os.WriteFile(p, []byte("---\nname: "+name+"\ndescription: "+desc+"\n---\n"+body), 0o644))
	r, err := filepath.EvalSymlinks(p)
	assert.NoErr(t, err)
	return r
}

func TestRun_UserSkillListedAndReadable(t *testing.T) {
	userHome := t.TempDir()
	writeSkill(t, filepath.Join(userHome, ".agents", "skills"), "demo", "Use when demoing", "SKILL-BODY-4410\n")
	shown := "~/.agents/skills/demo/SKILL.md" // the catalog writes home as ~/; read expands it
	args, err := json.Marshal(map[string]string{"path": shown})
	assert.NoErr(t, err)
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(args)}}},
		{Text: "done"},
	})
	_, errOut, code := runJas(t, []string{"HOME=" + userHome, "USERPROFILE=" + userHome, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, errOut, "skill")
	first := systemPrompt(t, bodies()[0])
	assert.Contains(t, first, "<available_skills>")
	assert.Contains(t, first, "<name>demo</name>")
	assert.Contains(t, first, "<location>"+shown+"</location>")
	assert.NotContains(t, first, userHome) // no home path in the prompt
	assert.NotContains(t, first, "SKILL-BODY-4410") // body loads on demand only
	assert.Contains(t, string(bodies()[1]), "SKILL-BODY-4410")
}

// systemPrompt decodes the system message from a recorded request body.
func systemPrompt(t *testing.T, body []byte) string {
	t.Helper()
	var req struct {
		Messages []struct{ Role, Content string } `json:"messages"`
	}
	assert.NoErr(t, json.Unmarshal(body, &req))
	assert.Eq(t, req.Messages[0].Role, "system")
	return req.Messages[0].Content
}

func TestRun_ProjectSkillsNeedTrust(t *testing.T) {
	proj := t.TempDir()
	writeSkill(t, filepath.Join(proj, ".agents", "skills"), "proj", "PROJECT-SKILL-9921", "body")
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}, {Text: "b"}})
	env := []string{"JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}

	_, errOut, code := runJasIn(t, proj, env, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "skipping untrusted 1 project skill (proj)")
	assert.NotContains(t, string(bodies()[0]), "PROJECT-SKILL-9921")

	_, _, code = runJasIn(t, proj, env, "run", "--trust-project", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, string(bodies()[1]), "PROJECT-SKILL-9921")
}

func TestRun_AddingProjectSkillInvalidatesTrust(t *testing.T) {
	proj := t.TempDir()
	writeAgents(t, proj, "RULE-3310")
	real, err := filepath.EvalSymlinks(proj)
	assert.NoErr(t, err)
	home := t.TempDir()
	assert.NoErr(t, core.TrustStore{Path: filepath.Join(home, "trust.json")}.Trust(real, []byte("RULE-3310")))
	writeSkill(t, filepath.Join(proj, ".claude", "skills"), "late", "added after trust", "body")
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, errOut, code := runJasIn(t, proj, []string{"JAS_HOME=" + home, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, "skipping untrusted AGENTS.md and 1 project skill (late)")
	assert.NotContains(t, string(bodies()[0]), "RULE-3310")
}

func TestRun_SessionStartedListsSkills(t *testing.T) {
	userHome := t.TempDir()
	loc := writeSkill(t, filepath.Join(userHome, ".claude", "skills"), "meta", "d", "b")
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJas(t, []string{"HOME=" + userHome, "USERPROFILE=" + userHome, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "--json", "q")
	assert.Eq(t, code, 0)
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(l), &m))
		if m["type"] != "session_started" {
			continue
		}
		sk := m["skills"].([]any)
		// meta plus jas's own manual, installed under the config dir.
		assert.Eq(t, len(sk), 2)
		var meta map[string]any
		for _, e := range sk {
			e := e.(map[string]any)
			if e["name"] == "meta" {
				meta = e
			}
		}
		if meta == nil {
			t.Fatal("meta skill missing from session_started")
		}
		assert.Eq(t, meta["scope"], any("user"))
		assert.Eq(t, meta["location"], any(loc))
		return
	}
	t.Fatal("no session_started event")
}

func TestRun_UntrustedProjectSkillDoesNotHideUserSkill(t *testing.T) {
	proj, userHome := t.TempDir(), t.TempDir()
	writeSkill(t, filepath.Join(proj, ".agents", "skills"), "foo", "PROJECT-FOO", "body")
	writeSkill(t, filepath.Join(userHome, ".agents", "skills"), "foo", "USER-FOO-6612", "body")
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, errOut, code := runJasIn(t, proj, []string{"HOME=" + userHome, "USERPROFILE=" + userHome, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, errOut, "shadowed")
	sys := systemPrompt(t, bodies()[0])
	assert.Contains(t, sys, "USER-FOO-6612")
	assert.NotContains(t, sys, "PROJECT-FOO")
}

func TestRun_SkillWarningsEscapeControlBytes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows file names cannot hold control characters")
	}
	proj := t.TempDir()
	dir := filepath.Join(proj, ".agents", "skills", "x\x1b]0;pwned\x07")
	assert.NoErr(t, os.MkdirAll(dir, 0o755))
	assert.NoErr(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644))
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	_, errOut, code := runJasIn(t, proj, []string{"JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "q")
	assert.Eq(t, code, 0)
	assert.Contains(t, errOut, `\x1b]0;pwned\x07`)
	assert.NotContains(t, errOut, "\x1b")
}

func TestTrustLabel_NamesProjectSkills(t *testing.T) {
	named := func(names ...string) []skills.Skill {
		out := make([]skills.Skill, len(names))
		for i, n := range names {
			out[i] = skills.Skill{Name: n}
		}
		return out
	}
	assert.Eq(t, trustLabel("/p/AGENTS.md", named("deploy"), false, nil), "AGENTS.md and 1 project skill (deploy)")
	assert.Eq(t, trustLabel("", named("b", "a"), true, nil), "2 project skills (a, b) and .jas/farm.yaml")
	assert.Eq(t, trustLabel("", named("a", "b", "c", "d", "e", "f", "g"), false, nil),
		"7 project skills (a, b, c, d, e and 2 more)")
}
