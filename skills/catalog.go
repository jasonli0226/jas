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
		b.WriteString(entry(s))
	}
	if b.Len() == 0 {
		return ""
	}
	return catalogIntro + "\n\n<available_skills>\n" + b.String() + "</available_skills>"
}

// MaxCatalogBytes bounds the catalog so a large skills collection cannot
// flood the system prompt; it matches the context file cap.
const MaxCatalogBytes = 64 * 1024

// Fit keeps skills, in order, while the catalog stays within budget bytes,
// and returns the names of model-invocable skills that did not fit. Hidden
// (DisableModelInvocation) skills cost nothing and are always kept.
func Fit(skills []Skill, budget int) (kept []Skill, dropped []string) {
	used := len(catalogIntro) + len("\n\n<available_skills>\n</available_skills>")
	for _, s := range skills {
		if s.DisableModelInvocation {
			kept = append(kept, s)
			continue
		}
		if n := entrySize(s); used+n <= budget {
			used += n
			kept = append(kept, s)
		} else {
			dropped = append(dropped, s.Name)
		}
	}
	return kept, dropped
}

// entrySize is the bytes one skill adds to the catalog.
func entrySize(s Skill) int { return len(entry(s)) }

func entry(s Skill) string {
	return "  <skill>\n" +
		"    <name>" + html.EscapeString(s.Name) + "</name>\n" +
		"    <description>" + html.EscapeString(s.Description) + "</description>\n" +
		"    <location>" + html.EscapeString(s.Location) + "</location>\n" +
		"  </skill>\n"
}
