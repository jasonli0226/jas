package skills

import (
	"html"
	"strings"
)

const catalogIntro = `The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md) and use that absolute path in tool calls.`

// Catalog renders the system-prompt section listing the skills the model may
// load, in pi's <available_skills> format. Skills with
// DisableModelInvocation are left out; with none left it returns "". The
// output depends only on its input, so the prompt prefix stays cacheable.
func Catalog(skills []Skill) string {
	var b strings.Builder
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		b.WriteString("  <skill>\n")
		b.WriteString("    <name>" + html.EscapeString(s.Name) + "</name>\n")
		b.WriteString("    <description>" + html.EscapeString(s.Description) + "</description>\n")
		b.WriteString("    <location>" + html.EscapeString(s.Location) + "</location>\n")
		b.WriteString("  </skill>\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return catalogIntro + "\n\n<available_skills>\n" + b.String() + "</available_skills>"
}
