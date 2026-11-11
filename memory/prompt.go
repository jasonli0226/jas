package memory

import (
	"fmt"
	"strings"
)

const guidance = `Save a note only when a future session could not rediscover it by reading the repo, and would otherwise repeat a mistake or ask again. Kinds worth saving: about the owner (role, expertise, preferences); practices and feedback (corrections and confirmed approaches, with the reason); project management (goals, deadlines, ownership, decisions and why); references (where things live outside the repo). Write relative dates as absolute.
- Do not save code structure, file lists, git history, or anything in ` + "`AGENTS.md`" + `. Do not save secrets.
- Default to the profile store. Use the project folder only for a note that is meaningless outside this project.
- One fact per note file: frontmatter with ` + "`name`, `description`, `type`" + ` (user, feedback, project or reference); for ` + "`feedback` and `project`" + `, end with **Why:** and **How to apply:**. Link related notes as ` + "`[[name]]`" + `, freely, even to notes not written yet.
- After adding a note, add one line to the matching ` + "`MEMORY.md`" + `: ` + "`- [Title](file.md) — hook`" + `. Keep the index short; it is capped.
- Before adding a note, check for one that already covers it and update that instead. To retract a wrong note, rewrite it with the correct fact, or remove its line from ` + "`MEMORY.md`" + ` if the fact no longer applies; you cannot delete files.
- Change ` + "`MEMORY.md`" + ` with ` + "`edit`" + ` (add or remove one line), never a whole-file ` + "`write`" + `.`

const readOnlyNotice = "Memory writes are not available in this session; read the notes below, but do not try to save anything."

// Section renders the Memory block of the system prompt.
func Section(d Dirs, profile, project Index, writable bool) string {
	var b strings.Builder
	b.WriteString("## Memory\n\n")
	fmt.Fprintf(&b, "Profile memory (shared across projects): %s\n", d.Profile)
	fmt.Fprintf(&b, "Project memory (this project only): %s\n\n", d.Project)
	if writable {
		b.WriteString("- ")
		b.WriteString(guidance)
	} else {
		b.WriteString(readOnlyNotice)
	}
	b.WriteString("\n")
	writeIndex(&b, d.Profile, profile)
	writeIndex(&b, d.Project, project)
	return b.String()
}

func writeIndex(b *strings.Builder, dir string, idx Index) {
	fmt.Fprintf(b, "\n### %s/MEMORY.md\n\n", dir)
	if !idx.Exists || strings.TrimSpace(idx.Text) == "" {
		b.WriteString("(empty)\n")
		return
	}
	b.WriteString(idx.Text)
}
