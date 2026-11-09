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
	codeSpanRe = regexp.MustCompile("`([^`]+)`")
	headingRe  = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	bulletRe   = regexp.MustCompile(`^(\s*)[-*]\s+(.*)$`)
	orderedRe  = regexp.MustCompile(`^(\s*)(\d+)\.\s+(.*)$`)
	hangIndent = "  "
)

// zwj joins emoji sequences; it is a format rune but cannot move the cursor
// or reorder text.
const zwj = '\u200d'

// cleanText drops control and format runes (ESC, bidi overrides, ...) from
// model-controlled text, keeping newlines, tabs and zero-width joiners, so it
// cannot drive the terminal.
func cleanText(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r == zwj {
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

// isClosingFence reports a fence that can close a block: backticks only. A
// fence line with an info string (```go) inside a block is content.
func isClosingFence(line string) bool {
	t := strings.TrimSpace(line)
	return len(t) >= len(fence) && strings.Trim(t, "`") == ""
}

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
		if isClosingFence(line) {
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

// renderBlock styles one raw block; width (0 = no limit) bounds its tables.
func renderBlock(raw string, width int) string {
	lines := strings.Split(raw, "\n")
	if isFence(lines[0]) {
		return renderCode(lines)
	}
	out := make([]string, 0, len(lines))
	inItem := false
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if n := tableAt(lines, i); n > 0 {
			out = append(out, renderTable(lines[i:i+n], width))
			i += n - 1
			inItem = false
		} else if m := headingRe.FindStringSubmatch(l); m != nil {
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

// inline styles code spans, and bold only outside them, so "**" inside a
// code span (Python's **kwargs) stays literal.
func inline(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range codeSpanRe.FindAllStringSubmatchIndex(s, -1) {
		b.WriteString(bold(s[last:m[0]]))
		b.WriteString(codeSpanStyle.Render(s[m[2]:m[3]]))
		last = m[1]
	}
	b.WriteString(bold(s[last:]))
	return b.String()
}

func bold(s string) string {
	return boldRe.ReplaceAllStringFunc(s, func(m string) string {
		return boldStyle.Render(m[2 : len(m)-2])
	})
}

// renderCode shows a fenced block pi-style: dim fence lines around the code,
// which is indented and highlighted. A block cut off before its closing
// fence still gets one.
func renderCode(lines []string) string {
	body := lines[1:]
	if n := len(body); n > 0 && isClosingFence(body[n-1]) {
		body = body[:n-1]
	}
	out := make([]string, 0, len(body)+2)
	out = append(out, fenceStyle.Render(strings.TrimSpace(lines[0])))
	for _, l := range body {
		out = append(out, hangIndent+highlight(strings.ReplaceAll(l, "\t", "    ")))
	}
	return strings.Join(append(out, fenceStyle.Render(fence)), "\n")
}

// keywords is a union of common Go, JS/TS, Python, Rust and shell keywords;
// highlight does not know the block's language.
var keywords = map[string]bool{}

func init() {
	for _, k := range strings.Fields(`
		break case chan const continue default defer else fallthrough for func go
		goto if import interface map package range return select struct switch type
		var nil true false
		async await catch class delete export extends finally from function let new
		null this throw try typeof undefined void yield
		and as def del elif except in is lambda not or pass raise while with
		None True False self
		enum fn impl loop match mod mut pub trait use where
		do done esac fi then local`) {
		keywords[k] = true
	}
}

// highlight colours keywords, calls (an identifier followed by "("), string
// literals, // and # comments, and numbers; it is language-agnostic, so it
// only looks at lexical shape.
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
		case isWordRune(r):
			j := i
			for j < len(rs) && isWordRune(rs[j]) {
				j++
			}
			word := string(rs[i:j])
			switch {
			case keywords[word]:
				b.WriteString(keywordStyle.Render(word))
			case isCall(rs, j):
				b.WriteString(funcStyle.Render(word))
			default:
				b.WriteString(word)
			}
			i = j
		default:
			b.WriteRune(r)
			i++
		}
	}
	return b.String()
}

// isCall reports whether the word ending at rs[end] is followed by "(",
// allowing spaces in between.
func isCall(rs []rune, end int) bool {
	for end < len(rs) && rs[end] == ' ' {
		end++
	}
	return end < len(rs) && rs[end] == '('
}

func isWordRune(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }
