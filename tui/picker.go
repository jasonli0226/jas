package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const pickerRows = 10 // most rows a picker shows at once

// pickItem is one picker entry: label is shown and filtered on, value is
// what choosing it returns, and desc is an optional dim note after the label.
type pickItem struct{ label, value, desc string }

// picker is a filterable list shown in place of the input box. Typing filters
// by case-insensitive substring, labels starting with the filter first; Up
// and Down move, Enter chooses, Esc cancels.
type picker struct {
	title      string
	mark       string // drawn before every label, not filtered on
	items      []pickItem
	filter     string
	cursor     int                  // index into visible()
	canDefault bool                 // Ctrl+S also offers the choice as a default
	onDefault  func(string) tea.Cmd // what Ctrl+S runs on the highlighted value
}

type pickResult int

const (
	pickOpen pickResult = iota
	pickChosen
	pickCancelled
	pickSetDefault // Ctrl+S on a canDefault picker: choose and store as default
)

// visible returns the items matching the filter, prefix matches first.
func (p *picker) visible() []pickItem {
	f := strings.ToLower(p.filter)
	var prefix, inner []pickItem
	for _, it := range p.items {
		switch l := strings.ToLower(it.label); {
		case strings.HasPrefix(l, f):
			prefix = append(prefix, it)
		case strings.Contains(l, f):
			inner = append(inner, it)
		}
	}
	return append(prefix, inner...)
}

// key handles a key press; with pickChosen the value is the chosen item's,
// with pickSetDefault (Ctrl+S) it is the highlighted item's, to also store
// as a default.
func (p *picker) key(msg tea.KeyPressMsg) (pickResult, string) {
	switch msg.String() {
	case "enter":
		if vis := p.visible(); p.cursor < len(vis) {
			return pickChosen, vis[p.cursor].value
		}
	case "ctrl+s":
		if vis := p.visible(); p.canDefault && p.cursor < len(vis) {
			return pickSetDefault, vis[p.cursor].value
		}
	case "esc", "ctrl+c":
		return pickCancelled, ""
	case "up":
		p.cursor = max(p.cursor-1, 0)
	case "down":
		p.cursor = max(min(p.cursor+1, len(p.visible())-1), 0)
	case "backspace":
		if r := []rune(p.filter); len(r) > 0 {
			p.filter, p.cursor = string(r[:len(r)-1]), 0
		}
	default:
		if msg.Text != "" {
			p.filter, p.cursor = p.filter+msg.Text, 0
		}
	}
	return pickOpen, ""
}

// view renders the title with the filter, then the rows.
func (p *picker) view(width int) string {
	title := boldStyle.Render(ansi.Truncate(p.title+": "+p.filter, width, "…"))
	if len(p.visible()) == 0 {
		return title + "\n" + dimStyle.Render("  no matches")
	}
	view := title + "\n" + p.rows(width)
	if p.canDefault {
		view += "\n" + dimStyle.Render("  Ctrl+S set as default")
	}
	return view
}

// rows renders up to pickerRows matching items, scrolled so the cursor stays
// in sight, with descriptions aligned in a dim column.
func (p *picker) rows(width int) string {
	vis := p.visible()
	pad := 0
	for _, it := range vis {
		pad = max(pad, ansi.StringWidth(it.label))
	}
	var lines []string
	start := max(p.cursor-pickerRows+1, 0)
	for i := start; i < len(vis) && i < start+pickerRows; i++ {
		lead, st := "  ", lipgloss.NewStyle()
		if i == p.cursor {
			lead, st = "› ", boldStyle
		}
		row := st.Render(lead + p.mark + vis[i].label)
		if d := vis[i].desc; d != "" {
			row += strings.Repeat(" ", pad-ansi.StringWidth(vis[i].label)+2) + dimStyle.Render(d)
		}
		lines = append(lines, ansi.Truncate(row, width, "…"))
	}
	return strings.Join(lines, "\n")
}
