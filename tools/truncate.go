// Package tools holds the built-in tools offered to the model.
package tools

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Truncate makes out valid UTF-8 and caps it at max bytes, cutting at the last
// newline before max (or at max when there is none) and appending a marker
// with the number of omitted lines.
func Truncate(out []byte, max int) string {
	s := strings.ToValidUTF8(string(out), "�")
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndexByte(s[:max], '\n')
	rest := 0
	if cut >= 0 {
		rest = cut + 1 // skip the newline we cut at
	} else {
		cut = max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		rest = cut
	}
	n := bytes.Count([]byte(s[rest:]), []byte("\n")) + 1
	return fmt.Sprintf("%s\n[truncated: %d more lines]", s[:cut], n)
}
