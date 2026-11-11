package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// A paste over pasteMaxLines lines or pasteMaxChars characters goes into the
// input as one marker, expanded back to the pasted text on submit (as pi
// does), so a long paste does not bury the cursor.
const (
	pasteMaxLines = 10
	pasteMaxChars = 1000
)

// pastes holds the large pastes collapsed into markers in the input. It is
// kept for the whole chat, so a marker still expands from a stashed draft
// or a history entry.
type pastes struct {
	n    int
	text map[string]string // marker → pasted text
}

// collapse returns the marker for a large paste, recording its text, or
// ok false when s is small enough to insert as it is.
func (p *pastes) collapse(s string) (marker string, ok bool) {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
	lines := strings.Count(strings.TrimRight(s, "\n"), "\n") + 1
	chars := utf8.RuneCountInString(s)
	if lines <= pasteMaxLines && chars <= pasteMaxChars {
		return "", false
	}
	p.n++
	if lines > pasteMaxLines {
		marker = fmt.Sprintf("[paste #%d +%d lines]", p.n, lines)
	} else {
		marker = fmt.Sprintf("[paste #%d %d chars]", p.n, chars)
	}
	if p.text == nil {
		p.text = map[string]string{}
	}
	p.text[marker] = s
	return marker, true
}

// expand replaces every known marker in s with its pasted text.
func (p *pastes) expand(s string) string {
	for marker, text := range p.text {
		s = strings.ReplaceAll(s, marker, text)
	}
	return s
}

// edge returns the known marker that s ends with (suffix) or starts with.
func (p *pastes) edge(s string, suffix bool) (string, bool) {
	for marker := range p.text {
		if (suffix && strings.HasSuffix(s, marker)) || (!suffix && strings.HasPrefix(s, marker)) {
			return marker, true
		}
	}
	return "", false
}

// onPaste inserts a paste into the input, collapsed when it is large.
func (m *Model) onPaste(msg tea.PasteMsg) tea.Cmd {
	if marker, ok := m.pastes.collapse(msg.Content); ok {
		m.input.InsertString(marker)
		return nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return cmd
}

// submitText is the trimmed input with paste markers expanded: what a submit sends.
func (m *Model) submitText() string {
	return m.pastes.expand(strings.TrimSpace(m.input.Value()))
}

// deletePasteMarker deletes a whole marker next to the cursor, before it on
// Backspace or after it on Delete. It reports whether one was deleted.
func (m *Model) deletePasteMarker(k string) bool {
	if (k != "backspace" && k != "delete") || m.input.HasSelection() {
		return false
	}
	lines := strings.Split(m.input.Value(), "\n")
	row := m.input.Line()
	if row >= len(lines) {
		return false
	}
	line := []rune(lines[row])
	col := min(m.input.Column(), len(line))
	back := k == "backspace"
	side := string(line[col:])
	if back {
		side = string(line[:col])
	}
	marker, ok := m.pastes.edge(side, back)
	if !ok {
		return false
	}
	del := tea.KeyPressMsg{Code: tea.KeyDelete}
	if back {
		del = tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	for range utf8.RuneCountInString(marker) {
		m.input, _ = m.input.Update(del)
	}
	return true
}
