package skills

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// keyLine matches a top-level "key: value" (or "key:") frontmatter line.
var keyLine = regexp.MustCompile(`^([A-Za-z0-9_-]+):(?:\s+(.*))?$`)

var errNoFrontmatter = errors.New("no frontmatter: SKILL.md must start with a --- line")

// parseFrontmatter reads the top-level string fields of a SKILL.md YAML
// frontmatter block. It is deliberately lenient rather than a YAML parser:
// plain values run to the end of the line, so an unquoted colon (common in
// "Use when: ..." descriptions) is kept; quoted, block (| and >) and
// indented-continuation values are folded to one string; nested maps are
// skipped. Anything else at the top level is an error, and the skill is skipped.
func parseFrontmatter(src []byte) (map[string]string, error) {
	src = bytes.TrimPrefix(src, []byte("\xef\xbb\xbf"))
	lines := strings.Split(strings.ReplaceAll(string(src), "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return nil, errNoFrontmatter
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if l := strings.TrimRight(lines[i], " \t"); l == "---" || l == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return nil, errors.New("frontmatter has no closing --- line")
	}
	body := lines[1:end]
	fm := map[string]string{}
	for i := 0; i < len(body); {
		line := body[i]
		if isBlankOrComment(line) {
			i++
			continue
		}
		m := keyLine.FindStringSubmatch(line)
		if m == nil {
			return nil, fmt.Errorf("frontmatter line %d: expected key: value", i+2)
		}
		cont, next := continuation(body, i+1)
		v, err := scalar(strings.TrimSpace(m[2]), cont)
		if err != nil {
			return nil, fmt.Errorf("frontmatter %s: %w", m[1], err)
		}
		fm[m[1]] = v
		i = next
	}
	return fm, nil
}

func isBlankOrComment(line string) bool {
	t := strings.TrimSpace(line)
	return t == "" || strings.HasPrefix(t, "#")
}

// continuation returns the indented (or blank) lines that belong to the key
// before index start, and the index of the next top-level line.
func continuation(body []string, start int) ([]string, int) {
	i := start
	for i < len(body) && (strings.TrimSpace(body[i]) == "" || body[i][0] == ' ' || body[i][0] == '\t') {
		i++
	}
	return body[start:i], i
}

// scalar turns a value's first line plus its continuation lines into a string.
func scalar(first string, cont []string) (string, error) {
	switch {
	case strings.HasPrefix(first, "|"), strings.HasPrefix(first, ">"):
		return block(first[0] == '>', cont), nil
	case strings.HasPrefix(first, `"`), strings.HasPrefix(first, "'"):
		return quoted(first, cont)
	}
	parts := []string{stripComment(first)}
	for _, l := range cont {
		if !isBlankOrComment(l) {
			parts = append(parts, strings.TrimSpace(l))
		}
	}
	return strings.TrimSpace(strings.Join(parts, " ")), nil
}

// stripComment drops a " #" trailing comment from a plain value.
func stripComment(s string) string {
	if i := strings.Index(s, " #"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// block renders a | (literal) or > (folded) block scalar, trimmed.
func block(folded bool, cont []string) string {
	indent := -1
	for _, l := range cont {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if n := len(l) - len(strings.TrimLeft(l, " \t")); indent < 0 || n < indent {
			indent = n
		}
	}
	out := make([]string, len(cont))
	for i, l := range cont {
		if len(l) >= indent && indent > 0 {
			l = l[indent:]
		}
		out[i] = strings.TrimRight(l, " \t")
	}
	if !folded {
		return strings.TrimSpace(strings.Join(out, "\n"))
	}
	var b strings.Builder
	for i, l := range out {
		switch {
		case l == "":
			b.WriteString("\n")
		case i > 0 && out[i-1] != "":
			b.WriteString(" " + l)
		default:
			b.WriteString(l)
		}
	}
	return strings.TrimSpace(b.String())
}

// quoted renders a single- or double-quoted value, which may span lines.
func quoted(first string, cont []string) (string, error) {
	q := first[0]
	text := first
	for _, l := range cont {
		if closed(text, q) {
			break
		}
		text += " " + strings.TrimSpace(l)
	}
	if !closed(text, q) {
		return "", errors.New("unterminated quoted value")
	}
	end := closingIndex(text, q)
	inner := text[1:end]
	if q == '\'' {
		return strings.ReplaceAll(inner, "''", "'"), nil
	}
	if s, err := strconv.Unquote(`"` + inner + `"`); err == nil {
		return s, nil
	}
	return inner, nil
}

func closed(text string, q byte) bool { return closingIndex(text, q) > 0 }

// closingIndex finds the quote that closes text[0], honouring \" in double
// quotes and ” in single quotes; -1 when there is none.
func closingIndex(text string, q byte) int {
	for i := 1; i < len(text); i++ {
		switch {
		case q == '"' && text[i] == '\\':
			i++
		case text[i] == q && q == '\'' && i+1 < len(text) && text[i+1] == '\'':
			i++
		case text[i] == q:
			return i
		}
	}
	return -1
}
