package tui

import (
	"regexp"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

const fence = "```"

var (
	boldRe     = regexp.MustCompile(`\*\*(.+?)\*\*`)
	headingRe  = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	bulletRe   = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	orderedRe  = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)
	hangIndent = "  "
)

// cleanText drops control and format runes (ESC, bidi overrides, ...) from
// model-controlled text, keeping newlines and tabs, so it cannot drive the
// terminal.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// mdStream splits streamed text into blocks: a block ends at a blank line
// outside a code fence, and a fenced block ends only at its closing fence.
type mdStream struct {
	partial string   // current unterminated line
	lines   []string // complete lines of the open block
	inFence bool
}

func isFence(line string) bool { return strings.HasPrefix(strings.TrimSpace(line), fence) }

// Feed adds a delta and returns the raw text of every block it completed.
func (m *mdStream) Feed(delta string) []string {
	var done []string
	m.partial += delta
	for {
		i := strings.IndexByte(m.partial, '\n')
		if i < 0 {
			return done
		}
		line := strings.TrimRight(m.partial[:i], "\r")
		m.partial = m.partial[i+1:]
		done = append(done, m.line(line)...)
	}
}

func (m *mdStream) line(line string) []string {
	var done []string
	switch {
	case m.inFence:
		m.lines = append(m.lines, line)
		if isFence(line) {
			m.inFence = false
			done = append(done, m.take())
		}
	case isFence(line):
		if len(m.lines) > 0 {
			done = append(done, m.take())
		}
		m.inFence = true
		m.lines = append(m.lines, line)
	case strings.TrimSpace(line) == "":
		if len(m.lines) > 0 {
			done = append(done, m.take())
		}
	default:
		m.lines = append(m.lines, line)
	}
	return done
}

func (m *mdStream) take() string {
	s := strings.Join(m.lines, "\n")
	m.lines = nil
	return s
}

// Pending is the raw text of the open block including the partial line.
func (m *mdStream) Pending() string {
	lines := m.lines
	if m.partial != "" {
		lines = append(append([]string(nil), lines...), m.partial)
	}
	return strings.Join(lines, "\n")
}

// Flush returns the open block, if any, and resets the stream.
func (m *mdStream) Flush() (string, bool) {
	s := m.Pending()
	*m = mdStream{}
	return s, strings.TrimSpace(s) != ""
}

// renderBlock styles one raw block.
func renderBlock(raw string) string {
	lines := strings.Split(raw, "\n")
	if isFence(lines[0]) {
		return renderCode(lines)
	}
	out := make([]string, 0, len(lines))
	inItem := false
	for _, l := range lines {
		if m := headingRe.FindStringSubmatch(l); m != nil {
			out = append(out, boldStyle.Render(m[1]))
			inItem = false
		} else if m := bulletRe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1]+"• "+inline(m[2]))
			inItem = true
		} else if m := orderedRe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1]+m[2]+". "+inline(m[3]))
			inItem = true
		} else if inItem {
			out = append(out, hangIndent+inline(strings.TrimSpace(l)))
		} else {
			out = append(out, inline(l))
		}
	}
	return strings.Join(out, "\n")
}

func inline(s string) string {
	return boldRe.ReplaceAllStringFunc(s, func(m string) string {
		return boldStyle.Render(m[2 : len(m)-2])
	})
}

func renderCode(lines []string) string {
	body := lines[1:]
	if n := len(body); n > 0 && isFence(body[n-1]) {
		body = body[:n-1]
	}
	for i, l := range body {
		body[i] = highlight(strings.ReplaceAll(l, "\t", "    "))
	}
	return codeBox.Render(strings.Join(body, "\n"))
}

// highlight colours string literals, // and # comments, and numbers; it is
// language-agnostic, so it only looks at lexical shape.
func highlight(line string) string {
	rs := []rune(line)
	var b strings.Builder
	paint := func(st lipgloss.Style, from, to int) { b.WriteString(st.Render(string(rs[from:to]))) }
	for i := 0; i < len(rs); {
		r := rs[i]
		switch {
		case r == '"' || r == '\'' || r == '`':
			j := i + 1
			for j < len(rs) && rs[j] != r {
				if rs[j] == '\\' {
					j++
				}
				j++
			}
			j = min(j+1, len(rs))
			paint(stringStyle, i, j)
			i = j
		case r == '#' || (r == '/' && i+1 < len(rs) && rs[i+1] == '/'):
			paint(commentStyle, i, len(rs))
			i = len(rs)
		case unicode.IsDigit(r) && (i == 0 || !isWordRune(rs[i-1])):
			j := i
			for j < len(rs) && (isWordRune(rs[j]) || rs[j] == '.') {
				j++
			}
			paint(numberStyle, i, j)
			i = j
		default:
			j := i
			for j < len(rs) && isWordRune(rs[j]) {
				j++
			}
			j = max(j, i+1)
			b.WriteString(string(rs[i:j]))
			i = j
		}
	}
	return b.String()
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
