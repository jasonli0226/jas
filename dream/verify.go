package dream

import (
	"path"
	"strconv"
	"strings"
)

// Findings is the deterministic check of a proposal. Unverified and
// Unsupported are keyed by index into the change list.
type Findings struct {
	Unexplained  []string
	Unverified   map[int][]string
	Unsupported  []int
	LargeRemoval bool
	LintBefore   []Issue
	LintAfter    []Issue
	Counts       Counts
	BestEffort   bool
}

const (
	proposedPrefix = "proposed/"
	// largeRemovalDivisor: removing more than 1/3 of the notes is large.
	largeRemovalDivisor = 3
)

// Verify checks a proposal against the snapshot, the reported changes and the
// window's event seqs. base and proposed map slash-separated store-relative
// paths to hashes.
func Verify(base, proposed map[string]string, changes []Change, seqs Seqs, before, after []Issue, partial bool) Findings {
	f := Findings{
		Unverified: map[int][]string{},
		LintBefore: before,
		LintAfter:  after,
		BestEffort: partial,
	}
	reported := map[string]bool{}
	for i, ch := range changes {
		for _, p := range ch.Paths {
			reported[normPath(p)] = true
		}
		valid := 0
		for _, c := range ch.Evidence {
			if validCitation(c, seqs) {
				valid++
			} else {
				f.Unverified[i] = append(f.Unverified[i], c)
			}
		}
		if (ch.Kind == "correct" || ch.Kind == "add") && valid == 0 {
			f.Unsupported = append(f.Unsupported, i)
		}
		if ch.Kind == "promote" {
			f.Counts.Promote++
		}
	}
	for _, p := range ChangedPaths(base, proposed) {
		if !reported[p] {
			f.Unexplained = append(f.Unexplained, p)
		}
	}
	f.Counts.Changed, f.Counts.Added, f.Counts.Removed = diffCounts(base, proposed)
	f.LargeRemoval = largeRemoval(base, proposed)
	f.Counts.LintFixed, f.Counts.LintOpen = lintCounts(before, after)
	return f
}

func normPath(p string) string {
	p = strings.TrimPrefix(path.Clean(strings.ReplaceAll(p, "\\", "/")), proposedPrefix)
	return path.Clean(p)
}

// ParseCitation splits "<session-id>#<seq>"; ok is false for anything else.
func ParseCitation(c string) (id string, seq int64, ok bool) {
	i := strings.LastIndex(c, "#")
	if i <= 0 {
		return "", 0, false
	}
	n, err := strconv.ParseInt(c[i+1:], 10, 64)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return c[:i], n, true
}

func validCitation(c string, seqs Seqs) bool {
	id, seq, ok := ParseCitation(c)
	return ok && seqs[id][seq]
}

func diffCounts(base, proposed map[string]string) (changed, added, removed int) {
	for p, h := range proposed {
		bh, ok := base[p]
		switch {
		case !ok:
			added++
		case bh != h:
			changed++
		}
	}
	for p := range base {
		if _, ok := proposed[p]; !ok {
			removed++
		}
	}
	return
}

func isNote(p string) bool {
	return strings.HasSuffix(p, noteExt) && path.Base(p) != indexName
}

func largeRemoval(base, proposed map[string]string) bool {
	notes, removed := 0, 0
	for p := range base {
		if !isNote(p) {
			continue
		}
		notes++
		if _, ok := proposed[p]; !ok {
			removed++
		}
	}
	return removed*largeRemovalDivisor > notes
}

func lintCounts(before, after []Issue) (fixed, open int) {
	still := map[[2]string]bool{}
	for _, a := range after {
		still[[2]string{a.Kind, a.Path}] = true
		if a.Kind != KindDanglingLink {
			open++
		}
	}
	for _, b := range before {
		if !still[[2]string{b.Kind, b.Path}] {
			fixed++
		}
	}
	return
}

// Warnings is what apply shows before asking, kept in dream.json so the
// apply screen never parses the report. Strings are as reported (redacted
// by the caller); printing them is the printer's job to sanitize.
type Warnings struct {
	Unexplained  []string      `json:"unexplained,omitempty"`
	Unsupported  []Unsupported `json:"unsupported,omitempty"`
	LargeRemoval bool          `json:"large_removal,omitempty"`
	BestEffort   bool          `json:"best_effort,omitempty"` // Unexplained may be incomplete: the run was partial
}

// Unsupported is a correct or add change with no valid citation.
type Unsupported struct {
	Kind   string   `json:"kind"`
	Paths  []string `json:"paths"`
	Reason string   `json:"reason"`
}

// Any reports whether there is anything to warn about.
func (w Warnings) Any() bool {
	return len(w.Unexplained) > 0 || len(w.Unsupported) > 0 || w.LargeRemoval
}

// NewWarnings extracts the apply-time warnings from f; changes is the list
// f was verified against.
func NewWarnings(f Findings, changes []Change) Warnings {
	w := Warnings{Unexplained: f.Unexplained, LargeRemoval: f.LargeRemoval, BestEffort: f.BestEffort}
	for _, i := range f.Unsupported {
		c := changes[i]
		w.Unsupported = append(w.Unsupported, Unsupported{Kind: c.Kind, Paths: c.Paths, Reason: c.Reason})
	}
	return w
}
