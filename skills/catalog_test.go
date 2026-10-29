package skills

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

func TestCatalog_ListsModelInvocableSkillsEscaped(t *testing.T) {
	got := Catalog([]Skill{
		{Name: "a", Description: "Use for <x> & y", Location: "/s/a/SKILL.md"},
		{Name: "hidden", Description: "d", Location: "/s/h/SKILL.md", DisableModelInvocation: true},
	})
	want := `The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md) and use that absolute path in tool calls.

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
	assert.Eq(t, Catalog(nil), "")
	assert.Eq(t, Catalog([]Skill{{Name: "h", Description: "d", DisableModelInvocation: true}}), "")
}

func TestFit_DropsSkillsPastBudget(t *testing.T) {
	ss := []Skill{
		{Name: "a", Description: "d", Location: "/a"},
		{Name: "hidden", Description: "d", Location: "/h", DisableModelInvocation: true},
		{Name: "b", Description: "d", Location: "/b"},
		{Name: "c", Description: "d", Location: "/c"},
	}
	one := len(Catalog(ss[:1]))
	kept, dropped := Fit(ss, one+entrySize(ss[2])) // room for a and b only
	assert.Eq(t, len(kept), 3)                     // a, hidden (costs nothing), b
	assert.Eq(t, kept[2].Name, "b")
	assert.Eq(t, strings.Join(dropped, ","), "c")
	if len(Catalog(kept)) > one+entrySize(ss[2]) {
		t.Fatalf("catalog %d bytes exceeds budget", len(Catalog(kept)))
	}
}
