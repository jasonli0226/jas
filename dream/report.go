package dream

import (
	"fmt"
	"sort"
	"strings"
)

const dateLayout = "2006-01-02"

var kindOrder = []string{"fix", "merge", "correct", "add", "remove", "prune"}

// RenderReport writes report.md: header, warnings, changes by kind, promotion
// candidates, lint, diff stat.
func RenderReport(m Meta, model string, tokens int, changes []Change, f Findings, logs []LogRef) string {
	var b strings.Builder
	refs := map[string]LogRef{}
	for _, l := range logs {
		refs[l.SessionID] = l
	}

	fmt.Fprintf(&b, "# Dream %s\n\n", m.ID)
	fmt.Fprintf(&b, "- Profile: %s\n", m.Profile)
	fmt.Fprintf(&b, "- Window: %s to %s (%d sessions)\n", m.Window.Since.Format(dateLayout), m.Window.Until.Format(dateLayout), len(m.Window.Sessions))
	fmt.Fprintf(&b, "- Model: %s\n- Tokens: %d\n", model, tokens)
	if m.Partial {
		b.WriteString("- Status: partial (stopped at the token cap)\n")
	}

	writeWarnings(&b, changes, f, refs)
	writeChanges(&b, changes, f, refs)
	writePromote(&b, changes, f, refs)
	writeLint(&b, f)
	c := f.Counts
	fmt.Fprintf(&b, "\n## Diff stat\n\n%d changed, %d added, %d removed. Run `jas dream diff` for the full diff.\n", c.Changed, c.Added, c.Removed)
	return b.String()
}

func writeWarnings(b *strings.Builder, changes []Change, f Findings, refs map[string]LogRef) {
	if len(f.Unexplained) == 0 && len(f.Unsupported) == 0 && !f.LargeRemoval {
		return
	}
	b.WriteString("\n## Warnings\n")
	if f.LargeRemoval {
		b.WriteString("\n**Large removal:** this proposal removes more than a third of the notes.\n")
	}
	if len(f.Unexplained) > 0 {
		b.WriteString("\n### Unexplained changes\n\n")
		if f.BestEffort {
			b.WriteString("Best-effort: the run was partial, so the model may have been stopped before reporting its edits.\n\n")
		}
		for _, p := range f.Unexplained {
			fmt.Fprintf(b, "- `%s`\n", p)
		}
	}
	if len(f.Unsupported) > 0 {
		b.WriteString("\n### Unsupported\n\nThese corrections or additions have no valid citation; `apply` asks again.\n\n")
		for _, i := range f.Unsupported {
			fmt.Fprintf(b, "- %s: %s (%s)\n", changes[i].Kind, joinPaths(changes[i].Paths), changes[i].Reason)
		}
	}
}

func writeChanges(b *strings.Builder, changes []Change, f Findings, refs map[string]LogRef) {
	for _, kind := range kindOrder {
		var idx []int
		for i, c := range changes {
			if c.Kind == kind {
				idx = append(idx, i)
			}
		}
		if len(idx) == 0 {
			continue
		}
		fmt.Fprintf(b, "\n## Changes: %s\n\n", kind)
		for _, i := range idx {
			writeChange(b, changes[i], f.Unverified[i], refs)
		}
	}
}

func writeChange(b *strings.Builder, c Change, bad []string, refs map[string]LogRef) {
	fmt.Fprintf(b, "- %s\n  %s\n", joinPaths(c.Paths), c.Reason)
	badSet := map[string]bool{}
	for _, x := range bad {
		badSet[x] = true
	}
	for _, e := range c.Evidence {
		fmt.Fprintf(b, "  - %s\n", renderCitation(e, refs, badSet[e]))
	}
}

func renderCitation(c string, refs map[string]LogRef, bad bool) string {
	id, _, ok := ParseCitation(c)
	ref, known := refs[id]
	if !ok || !known {
		return "`" + c + "` unverified"
	}
	s := fmt.Sprintf("`%s` (%s, %s)", c, ref.Root, ref.Started.Format(dateLayout))
	if bad {
		s += " unverified"
	}
	return s
}

func writePromote(b *strings.Builder, changes []Change, f Findings, refs map[string]LogRef) {
	var idx []int
	for i, c := range changes {
		if c.Kind == "promote" {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return
	}
	b.WriteString("\n## Promotion candidates\n\n")
	for _, i := range idx {
		writeChange(b, changes[i], f.Unverified[i], refs)
	}
}

func writeLint(b *strings.Builder, f Findings) {
	b.WriteString("\n## Lint\n")
	after := map[[2]string]bool{}
	for _, a := range f.LintAfter {
		after[[2]string{a.Kind, a.Path}] = true
	}
	var fixed []string
	for _, x := range f.LintBefore {
		if !after[[2]string{x.Kind, x.Path}] {
			fixed = append(fixed, fmt.Sprintf("- %s: `%s`", x.Kind, x.Path))
		}
	}
	var open []string
	for _, x := range f.LintAfter {
		if x.Kind != KindDanglingLink {
			open = append(open, fmt.Sprintf("- %s: `%s` %s", x.Kind, x.Path, x.Detail))
		}
	}
	sort.Strings(fixed)
	writeList(b, "Fixed", fixed)
	writeList(b, "Still open", open)
}

func writeList(b *strings.Builder, title string, items []string) {
	fmt.Fprintf(b, "\n### %s\n\n", title)
	if len(items) == 0 {
		b.WriteString("None.\n")
		return
	}
	b.WriteString(strings.Join(items, "\n") + "\n")
}

func joinPaths(ps []string) string {
	q := make([]string, len(ps))
	for i, p := range ps {
		q[i] = "`" + p + "`"
	}
	return strings.Join(q, ", ")
}
