package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const pickerRows = 10 // most rows a picker shows at once

// pickItem is one picker entry: label is shown and filtered on, value is
// what choosing it returns.
type pickItem struct{ label, value string }

// picker is a filterable list shown in place of the input box. Typing filters
// by case-insensitive substring, Up and Down move, Enter chooses, Esc cancels.
type picker struct {
	title  string
	items  []pickItem
	filter string
	cursor int // index into visible()
}

type pickResult int

const (
	pickOpen pickResult = iota
	pickChosen
	pickCancelled
)

// visible returns the items matching the filter.
func (p *picker) visible() []pickItem {
	f := strings.ToLower(p.filter)
	var out []pickItem
	for _, it := range p.items {
		if strings.Contains(strings.ToLower(it.label), f) {
			out = append(out, it)
		}
	}
	return out
}

// key handles a key press; with pickChosen the value is the chosen item's.
func (p *picker) key(msg tea.KeyPressMsg) (pickResult, string) {
	switch msg.String() {
	case "enter":
		if vis := p.visible(); p.cursor < len(vis) {
			return pickChosen, vis[p.cursor].value
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

// view renders the title with the filter, then up to pickerRows rows scrolled
// so the cursor stays in sight.
func (p *picker) view(width int) string {
	vis := p.visible()
	lines := []string{boldStyle.Render(ansi.Truncate(p.title+": "+p.filter, width, "…"))}
	if len(vis) == 0 {
		lines = append(lines, dimStyle.Render("  no matches"))
	}
	start := max(p.cursor-pickerRows+1, 0)
	for i := start; i < len(vis) && i < start+pickerRows; i++ {
		row := ansi.Truncate("  "+vis[i].label, width, "…")
		if i == p.cursor {
			row = boldStyle.Render(ansi.Truncate("› "+vis[i].label, width, "…"))
		}
		lines = append(lines, row)
	}
	return strings.Join(lines, "\n")
}
