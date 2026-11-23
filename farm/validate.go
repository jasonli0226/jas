package farm

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Problem is one validation failure; Part is empty for plan-level problems.
type Problem struct{ Part, Message string }

// writingParts returns the parts whose resolved role can write.
func writingParts(p Plan, cfg Config) []Part {
	avail, _ := Available(cfg)
	var out []Part
	for _, pt := range p.Parts {
		def, ok := avail[pt.Role]
		if !ok {
			continue
		}
		for _, tool := range def.Tools {
			if tool == "write" || tool == "edit" {
				out = append(out, pt)
				break
			}
		}
	}
	return out
}

// Validate checks roles, after references, cycles, owns and ownership
// overlap of concurrently runnable writing parts.
func Validate(p Plan, cfg Config, filesAtBase []string) []Problem {
	probs, cyclic := validateStructure(p, cfg)
	if cyclic {
		return probs
	}
	return append(probs, overlaps(p, cfg, filesAtBase)...)
}

// validateStructure checks everything Validate does except ownership
// overlap; cyclic reports a dependency cycle.
func validateStructure(p Plan, cfg Config) (probs []Problem, cyclic bool) {
	add := func(part, msg string) { probs = append(probs, Problem{part, msg}) }
	avail, bad := Available(cfg)
	ids := map[string]bool{}
	for _, pt := range p.Parts {
		if ids[pt.ID] {
			add(pt.ID, "duplicate part id")
		}
		ids[pt.ID] = true
	}
	for _, pt := range p.Parts {
		if pt.Role == reviewerRole {
			add(pt.ID, "role reviewer runs after integration; it cannot be a plan part")
			continue
		}
		if _, ok := avail[pt.Role]; !ok {
			if why, un := bad[pt.Role]; un {
				add(pt.ID, fmt.Sprintf("role %s unavailable: %s", pt.Role, why))
			} else {
				add(pt.ID, fmt.Sprintf("unknown role %q", pt.Role))
			}
		}
		for _, a := range pt.After {
			if !ids[a] {
				add(pt.ID, fmt.Sprintf("after: unknown part %q", a))
			}
		}
	}
	for _, pt := range writingParts(p, cfg) {
		if len(pt.Owns) == 0 {
			add(pt.ID, "owns must be non-empty for a writing role")
		}
	}
	if id, cyclic := findCycle(p); cyclic {
		add(id, "dependency cycle through after")
		return probs, true
	}
	return probs, false
}

// findCycle reports the first part (plan order) that lies on a cycle.
func findCycle(p Plan) (string, bool) {
	for _, pt := range p.Parts {
		for _, a := range pt.After {
			if reaches(p, a, pt.ID) {
				return pt.ID, true
			}
		}
	}
	return "", false
}

// reaches reports whether "from" depends (transitively, or is) on "to".
func reaches(p Plan, from, to string) bool {
	byID := map[string]Part{}
	for _, pt := range p.Parts {
		byID[pt.ID] = pt
	}
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(id string) bool {
		if id == to {
			return true
		}
		if seen[id] {
			return false
		}
		seen[id] = true
		for _, a := range byID[id].After {
			if walk(a) {
				return true
			}
		}
		return false
	}
	return walk(from)
}

func ordered(p Plan, a, b string) bool { return reaches(p, a, b) || reaches(p, b, a) }

func overlaps(p Plan, cfg Config, files []string) []Problem {
	var probs []Problem
	wp := writingParts(p, cfg)
	for i := range wp {
		for j := i + 1; j < len(wp); j++ {
			a, b := wp[i], wp[j]
			if ordered(p, a.ID, b.ID) {
				continue
			}
			if why := ownsOverlap(a.Owns, b.Owns, files); why != "" {
				probs = append(probs, Problem{a.ID, fmt.Sprintf("owns overlaps concurrent part %s (%s)", b.ID, why)})
			}
		}
	}
	return probs
}

func isGlob(s string) bool { return strings.ContainsAny(s, "*?[{") }

// literalPrefix is the directory part of g before its first glob character.
func literalPrefix(g string) string {
	i := strings.IndexAny(g, "*?[{")
	if i < 0 {
		return g
	}
	pre := g[:i]
	if j := strings.LastIndex(pre, "/"); j >= 0 {
		return pre[:j+1]
	}
	return ""
}

func matchAny(globs []string, f string) bool {
	for _, g := range globs {
		if m, _ := doublestar.Match(g, f); m {
			return true
		}
	}
	return false
}

// ownsOverlap returns a reason if the two glob sets can claim the same file:
// a file at base matching both, or overlapping literal prefixes (new files).
func ownsOverlap(a, b, files []string) string {
	for _, f := range files {
		if matchAny(a, f) && matchAny(b, f) {
			return "both match " + f
		}
	}
	for _, ga := range a {
		for _, gb := range b {
			switch {
			case !isGlob(ga) && !isGlob(gb):
				if ga == gb {
					return "both own " + ga
				}
			case !isGlob(ga):
				if m, _ := doublestar.Match(gb, ga); m {
					return gb + " matches " + ga
				}
			case !isGlob(gb):
				if m, _ := doublestar.Match(ga, gb); m {
					return ga + " matches " + gb
				}
			default:
				pa, pb := literalPrefix(ga), literalPrefix(gb)
				if strings.HasPrefix(pa, pb) || strings.HasPrefix(pb, pa) {
					return fmt.Sprintf("prefix of %s overlaps %s", ga, gb)
				}
			}
		}
	}
	return ""
}

// OwnerOf returns the id of the part a (possibly new) file belongs to: among
// parts whose owns match it, the one with the longest literal prefix.
// Returns "" if no part owns it.
func OwnerOf(p Plan, path string) string {
	best, bestLen := "", -1
	for _, pt := range p.Parts {
		for _, g := range pt.Owns {
			if m, _ := doublestar.Match(g, path); m && len(literalPrefix(g)) > bestLen {
				best, bestLen = pt.ID, len(literalPrefix(g))
			}
		}
	}
	return best
}

func refusal(reason, detail string) *Refusal { return &Refusal{Reason: reason, Detail: detail} }

// CodeRefusal applies the code-enforced refusal rules; nil means the split is
// allowed. A planner-written refusal is returned unchanged. small_change is a
// planner estimate and is never computed here.
func CodeRefusal(p Plan, cfg Config, filesAtBase []string) *Refusal {
	if p.Refuse != nil {
		return p.Refuse
	}
	wp := writingParts(p, cfg)
	if len(wp) < cfg.Split.MinParts {
		return refusal("single_package", fmt.Sprintf("%d writing parts, need at least %d", len(wp), cfg.Split.MinParts))
	}
	if singleRoot(wp) {
		return refusal("single_package", "all writing parts own paths under one directory root")
	}
	if probs := overlaps(p, cfg, filesAtBase); len(probs) > 0 {
		return refusal("overlapping_ownership", probs[0].Message)
	}
	if allOrdered(p, wp) {
		return refusal("serial_dependencies", "every writing part depends on another; none can run at once")
	}
	if strings.TrimSpace(cfg.Check) == "" {
		return refusal("no_check", "no check command resolved")
	}
	return nil
}

func singleRoot(wp []Part) bool {
	roots := map[string]bool{}
	for _, pt := range wp {
		for _, g := range pt.Owns {
			roots[strings.SplitN(g, "/", 2)[0]] = true
		}
	}
	return len(roots) <= 1
}

func allOrdered(p Plan, wp []Part) bool {
	ids := make([]string, len(wp))
	for i, pt := range wp {
		ids[i] = pt.ID
	}
	sort.Strings(ids)
	for i := range ids {
		for j := i + 1; j < len(ids); j++ {
			if !ordered(p, ids[i], ids[j]) {
				return false
			}
		}
	}
	return true
}
