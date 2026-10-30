package core

import "strings"

// ShellWords splits cmd into words the way a POSIX shell would for quoting
// alone: space, tab and newline separate words, '…' is literal, "…" groups, and \ escapes
// the next rune outside single quotes. ok is false on an unterminated quote or
// a trailing backslash. It does no expansion.
func ShellWords(cmd string) (words []string, ok bool) {
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		case r == '\\':
			if i+1 >= len(rs) {
				return nil, false
			}
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case r == '\'':
			end := i + 1
			for end < len(rs) && rs[end] != '\'' {
				end++
			}
			if end >= len(rs) {
				return nil, false
			}
			cur.WriteString(string(rs[i+1 : end]))
			inWord = true
			i = end
		case r == '"':
			closed := false
			for i++; i < len(rs); i++ {
				if rs[i] == '"' {
					closed = true
					break
				}
				if rs[i] == '\\' {
					if i+1 >= len(rs) {
						return nil, false
					}
					// POSIX: inside "…" a backslash only escapes $ ` " \ and newline.
					if strings.ContainsRune("$`\"\\\n", rs[i+1]) {
						i++
					}
				}
				cur.WriteRune(rs[i])
			}
			if !closed {
				return nil, false
			}
			inWord = true
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return words, true
}
