package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
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
	loc := writeSkill(t, filepath.Join(userHome, ".agents", "skills"), "demo", "Use when demoing", "SKILL-BODY-4410\n")
	args, err := json.Marshal(map[string]string{"path": loc})
	assert.NoErr(t, err)
	url, bodies := testutil.FakeOpenAI(t, []fake.Turn{
		{Calls: []fake.Call{{Name: "read", Args: string(args)}}},
		{Text: "done"},
	})
	_, errOut, code := runJas(t, []string{"HOME=" + userHome, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "q")
	assert.Eq(t, code, 0)
	assert.NotContains(t, errOut, "skill")
	first := systemPrompt(t, bodies()[0])
	assert.Contains(t, first, "<available_skills>")
	assert.Contains(t, first, "<name>demo</name>")
	assert.Contains(t, first, loc)
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
	assert.Contains(t, errOut, "skipping untrusted 1 project skill")
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
	assert.Contains(t, errOut, "skipping untrusted AGENTS.md and 1 project skill")
	assert.NotContains(t, string(bodies()[0]), "RULE-3310")
}

func TestRun_SessionStartedListsSkills(t *testing.T) {
	userHome := t.TempDir()
	loc := writeSkill(t, filepath.Join(userHome, ".claude", "skills"), "meta", "d", "b")
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a"}})
	out, _, code := runJas(t, []string{"HOME=" + userHome, "JAS_MODEL=m", "JAS_API_KEY=test-key-77", "JAS_BASE_URL=" + url}, "run", "--json", "q")
	assert.Eq(t, code, 0)
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		var m map[string]any
		assert.NoErr(t, json.Unmarshal([]byte(l), &m))
		if m["type"] != "session_started" {
			continue
		}
		sk := m["skills"].([]any)
		assert.Eq(t, len(sk), 1)
		e := sk[0].(map[string]any)
		assert.Eq(t, e["name"], any("meta"))
		assert.Eq(t, e["scope"], any("user"))
		assert.Eq(t, e["location"], any(loc))
		return
	}
	t.Fatal("no session_started event")
}
