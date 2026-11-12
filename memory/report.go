package memory

import (
	"fmt"
	"os"
	"strings"
)

const bytesPerKB = 1024

// Report describes both stores for /memory and jas memory: the mode, then
// per store its dir, index size against the cap (before truncation), a
// truncated marker and the note count. A zero project Index means the
// project store is hidden by the project's own memory=off. A non-empty
// pendingDream is the id of the newest pending dream and adds a last line
// pointing at `jas dream diff`.
func Report(d Dirs, profile, project Index, mode Mode, pendingDream string) string {
	if mode == ModeOff {
		return fmt.Sprintf("memory is off; it would live in %s", d.Profile)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "memory: %s\n", mode)
	writeStore(&b, "profile", d.Profile, profile)
	if project.Path == "" {
		fmt.Fprintf(&b, "project: hidden (project memory is off)\n  %s", d.Project)
	} else {
		writeStore(&b, "project", d.Project, project)
	}
	if pendingDream != "" {
		fmt.Fprintf(&b, "\ndream %s pending: jas dream diff", pendingDream)
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeStore(b *strings.Builder, name, dir string, idx Index) {
	fmt.Fprintf(b, "%s: %s\n  %d/%d lines, %.1f/%d KB", name, dir,
		idx.Lines, MaxIndexLines, float64(idx.Bytes)/bytesPerKB, MaxIndexBytes/bytesPerKB)
	if idx.Truncated {
		b.WriteString(", truncated")
	}
	fmt.Fprintf(b, "\n  %d notes\n", countNotes(dir))
}

// countNotes counts the .md files directly in dir other than the index.
// A missing or unreadable dir has none.
func countNotes(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".md") && !IsIndex(e.Name()) {
			n++
		}
	}
	return n
}

// String is the setting value: on, read or off.
func (m Mode) String() string {
	switch m {
	case ModeRead:
		return "read"
	case ModeOff:
		return "off"
	}
	return "on"
}
