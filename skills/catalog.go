package skills

import (
	"html"
	"path/filepath"
	"strings"
)

const catalogIntro = `The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md) and use that full path in tool calls.
Pass a path that starts with ~/ to tools exactly as written: the tools expand ~/ to your home directory, so never replace ~ with a guessed home path.`

// Catalog renders the system-prompt section listing the skills the model may
// load, in pi's <available_skills> format. Skills with
// DisableModelInvocation are left out; with none left it returns "". The
// output depends only on its input, so the prompt prefix stays cacheable.
//
// Locations under home are written as ~/..., so the prompt does not carry
// the user's home path; the read tools expand ~/ back. The intro tells the
// model to pass ~/ through: a model that expands it itself guesses the home
// (on Windows, C:\Users\Public), and the read is refused (#175).
func Catalog(skills []Skill, home string) string {
	var b strings.Builder
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		b.WriteString(entry(s, home))
	}
	if b.Len() == 0 {
		return ""
	}
	return catalogIntro + "\n\n<available_skills>\n" + b.String() + "</available_skills>"
}

// MaxCatalogBytes bounds the catalog so a large skills collection cannot
// flood the system prompt; it matches the context file cap.
const MaxCatalogBytes = 64 * 1024

// Fit keeps skills while the catalog (rendered with home, as Catalog does)
// stays within budget bytes, and returns the names of model-invocable skills
// that did not fit. Project skills are about this repository, so they are
// kept first and user skills fill what is left; the result keeps skills'
// order. Hidden (DisableModelInvocation) skills cost nothing and are always
// kept.
func Fit(skills []Skill, budget int, home string) (kept []Skill, dropped []string) {
	used := len(catalogIntro) + len("\n\n<available_skills>\n</available_skills>")
	keep := make([]bool, len(skills))
	for _, projectPass := range []bool{true, false} {
		for i, s := range skills {
			if (s.Scope == Project) != projectPass {
				continue
			}
			if s.DisableModelInvocation {
				keep[i] = true
			} else if n := entrySize(s, home); used+n <= budget {
				used += n
				keep[i] = true
			}
		}
	}
	for i, s := range skills {
		if keep[i] {
			kept = append(kept, s)
		} else {
			dropped = append(dropped, s.Name)
		}
	}
	return kept, dropped
}

// entrySize is the bytes one skill adds to the catalog.
func entrySize(s Skill, home string) int { return len(entry(s, home)) }

// DisplayPath writes path with a leading home directory as ~/.
func DisplayPath(path, home string) string {
	if home == "" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, strings.TrimRight(home, string(filepath.Separator))+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}

func entry(s Skill, home string) string {
	return "  <skill>\n" +
		"    <name>" + html.EscapeString(s.Name) + "</name>\n" +
		"    <description>" + html.EscapeString(s.Description) + "</description>\n" +
		"    <location>" + html.EscapeString(DisplayPath(s.Location, home)) + "</location>\n" +
		"  </skill>\n"
}
