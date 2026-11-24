package skills

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestCatalog_ListsModelInvocableSkillsEscaped(t *testing.T) {
	got := Catalog([]Skill{
		{Name: "a", Description: "Use for <x> & y", Location: "/s/a/SKILL.md"},
		{Name: "hidden", Description: "d", Location: "/s/h/SKILL.md", DisableModelInvocation: true},
	}, "")
	want := `The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md) and use that full path in tool calls.
Pass a path that starts with ~/ to tools exactly as written: the tools expand ~/ to your home directory, so never replace ~ with a guessed home path.

<available_skills>
  <skill>
    <name>a</name>
    <description>Use for &lt;x&gt; &amp; y</description>
    <location>/s/a/SKILL.md</location>
  </skill>
</available_skills>`
	assert.Eq(t, got, want)
}

func TestCatalog_EmptyWhenNothingToList(t *testing.T) {
	assert.Eq(t, Catalog(nil, ""), "")
	assert.Eq(t, Catalog([]Skill{{Name: "h", Description: "d", DisableModelInvocation: true}}, ""), "")
}

func TestFit_DropsSkillsPastBudget(t *testing.T) {
	ss := []Skill{
		{Name: "a", Description: "d", Location: "/a"},
		{Name: "hidden", Description: "d", Location: "/h", DisableModelInvocation: true},
		{Name: "b", Description: "d", Location: "/b"},
		{Name: "c", Description: "d", Location: "/c"},
	}
	one := len(Catalog(ss[:1], ""))
	kept, dropped := Fit(ss, one+entrySize(ss[2], ""), "") // room for a and b only
	assert.Eq(t, len(kept), 3)                             // a, hidden (costs nothing), b
	assert.Eq(t, kept[2].Name, "b")
	assert.Eq(t, strings.Join(dropped, ","), "c")
	if len(Catalog(kept, "")) > one+entrySize(ss[2], "") {
		t.Fatalf("catalog %d bytes exceeds budget", len(Catalog(kept, "")))
	}
}

func TestFit_DropsUserSkillsBeforeProject(t *testing.T) {
	ss := []Skill{
		{Name: "a", Description: "d", Location: "/u/a", Scope: User},
		{Name: "b", Description: "d", Location: "/p/b", Scope: Project},
		{Name: "c", Description: "d", Location: "/u/c", Scope: User},
	}
	one := len(Catalog(ss[:1], ""))
	kept, dropped := Fit(ss, one+entrySize(ss[2], ""), "") // room for two
	assert.Eq(t, strings.Join(names(kept), ","), "a,b")    // name order kept
	assert.Eq(t, strings.Join(dropped, ","), "c")
	kept, dropped = Fit(ss, one, "") // room for one: the project skill
	assert.Eq(t, strings.Join(names(kept), ","), "b")
	assert.Eq(t, strings.Join(dropped, ","), "a,c")
}

func TestCatalog_WritesHomeAsTilde(t *testing.T) {
	other := filepath.FromSlash("/home/u2/b/SKILL.md")
	got := Catalog([]Skill{
		{Name: "a", Description: "d", Location: filepath.FromSlash("/home/u/.agents/skills/a/SKILL.md")},
		{Name: "b", Description: "d", Location: other},
	}, filepath.FromSlash("/home/u"))
	assert.Contains(t, got, "<location>~/.agents/skills/a/SKILL.md</location>")
	assert.Contains(t, got, "<location>"+other+"</location>") // only a real path prefix
}
