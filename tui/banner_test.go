package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// bannerLines renders a banner and returns its lines with styling stripped.
func bannerLines(version string, files []ContextFile, skills []string, width int) []string {
	return strings.Split(strings.Join(plain(banner(version, files, skills, width)), "\n"), "\n")
}

const testHints = "esc stop run · ctrl+l clear · ctrl+c clear/twice-quit · / commands · /help"

func TestBanner(t *testing.T) {
	lines := bannerLines("1.2.3", []ContextFile{{Path: "/p/AGENTS.md", Loaded: true}}, []string{"a", "b", "c"}, 80)
	i := 0
	for ; lines[i] == ""; i++ { // first non-empty line is the wordmark + version
	}
	if !strings.Contains(lines[i], "jas") || !strings.Contains(lines[i], "v1.2.3") {
		t.Fatalf("first line %q lacks jas or v1.2.3", lines[i])
	}
	var ctx, file, skills, hints bool
	for _, l := range lines {
		if l == testHints {
			hints = true
		}
		if l == "[Context]" {
			ctx = true
		}
		if strings.Contains(l, "/p/AGENTS.md") {
			if strings.Contains(l, "not loaded") {
				t.Fatalf("loaded file marked not loaded: %q", l)
			}
			file = true
		}
		if l == "[Skills]" {
			skills = true
		}
	}
	if !hints {
		t.Fatalf("hints line missing")
	}
	if !ctx {
		t.Fatalf("[Context] header missing")
	}
	if !file {
		t.Fatalf("context file missing")
	}
	if !skills {
		t.Fatalf("[Skills] header missing")
	}
	skillLine := strings.Join(lines, "\n")
	if !strings.Contains(skillLine, "a, b, c") {
		t.Fatalf("skills not listed: %q", skillLine)
	}
}

func TestBanner_NotLoadedDimmed(t *testing.T) {
	lines := bannerLines("1", []ContextFile{{Path: "/p/AGENTS.md", Loaded: false}}, nil, 80)
	found := false
	for _, l := range lines {
		if strings.Contains(l, "/p/AGENTS.md") {
			if !strings.Contains(l, "not loaded") {
				t.Fatalf("declined file lacks not loaded: %q", l)
			}
			found = true
		}
	}
	if !found {
		t.Fatalf("declined file missing")
	}
}

func TestBanner_EmptySections(t *testing.T) {
	for _, l := range bannerLines("1", nil, nil, 80) {
		if l == "[Context]" || l == "[Skills]" {
			t.Fatalf("empty section shown: %q", l)
		}
	}
}

func TestBanner_Wraps(t *testing.T) {
	skills := []string{"one-long-skill", "two-long-skill", "three-long-skill", "four-long-skill", "five-long-skill"}
	files := []ContextFile{{Path: "/a/really/long/context/path/AGENTS.md", Loaded: true}}
	for _, l := range bannerLines("1", files, skills, 30) {
		if w := lipgloss.Width(l); w > 30 {
			t.Fatalf("line %d cells, %q", w, l)
		}
		if strings.HasPrefix(l, " ") && strings.TrimSpace(l) != "" {
			if !strings.HasPrefix(l, "  ") {
				t.Fatalf("continuation not indented: %q", l)
			}
		}
	}
}

func TestBanner_TrailingBlankLine(t *testing.T) {
	// One blank line follows the banner, separating it from the session's
	// first block.
	raw := strings.Join(banner("1", nil, nil, 80), "\n")
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("no trailing blank line: %q", raw)
	}
}
