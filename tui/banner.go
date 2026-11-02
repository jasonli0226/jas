package tui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// bannerHints is the key-hints line the start banner shows, verbatim.
const bannerHints = "esc stop run · ctrl+c clear/twice-quit · / commands · /help full list"

// bannerIndent prefixes every section row and its continuation lines.
const bannerIndent = "  "

// banner renders the start banner: wordmark and version, the key hints,
// then the [Context] and [Skills] sections, each header hidden when its
// list is empty. Lines are wrapped to width so the banner never overflows
// a narrow terminal.
func banner(version string, files []ContextFile, skills []string, width int) []string {
	head := boldStyle.Render("jas")
	if version != "" {
		head += "  " + dimStyle.Render("v"+version)
	}
	lines := []string{head}
	for _, l := range wrapWords(bannerHints, width) {
		lines = append(lines, dimStyle.Render(l))
	}
	if len(files) > 0 {
		lines = append(lines, "", dimStyle.Render("[Context]"))
		for _, f := range files {
			row := f.Path
			mark := ""
			if !f.Loaded {
				mark = " · not loaded"
			}
			for i, l := range wrapWords(row+mark, max(width-len(bannerIndent), len(bannerIndent))) {
				if i > 0 {
					l = bannerIndent + l
				}
				if !f.Loaded {
					l = strings.Replace(l, mark, dimStyle.Render(mark), 1)
				}
				lines = append(lines, bannerIndent+l)
			}
		}
	}
	if len(skills) > 0 {
		lines = append(lines, "", dimStyle.Render("[Skills]"))
		for i, l := range wrapWords(strings.Join(skills, ", "), max(width-len(bannerIndent), len(bannerIndent))) {
			if i > 0 {
				l = bannerIndent + l
			}
			lines = append(lines, bannerIndent+l)
		}
	}
	// A trailing blank line separates the banner from the session's first
	// block.
	return []string{strings.Join(append(lines, ""), "\n")}
}

// wrapWords word-wraps s at limit, hard-splitting words longer than limit.
func wrapWords(s string, limit int) []string {
	if limit <= 0 || ansi.StringWidth(s) <= limit {
		return []string{s}
	}
	words := strings.Split(s, " ")
	var out []string
	cur := ""
	for _, w := range words {
		for ansi.StringWidth(w) > limit { // a word alone breaks the limit: hard-split it
			cut := limit
			for cut > 0 && ansi.StringWidth(w[:cut]) > limit {
				cut--
			}
			if cut == 0 {
				cut = 1 // a single cell wider than the limit: give up on the wrap
			}
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			out = append(out, w[:cut])
			w = w[cut:]
		}
		if cur == "" {
			cur = w
			continue
		}
		if ansi.StringWidth(cur)+1+ansi.StringWidth(w) <= limit {
			cur += " " + w
		} else {
			out = append(out, cur)
			cur = w
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
