package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/jasonli0226/jas/internal/assert"
)

func pickKey(p *picker, k string) (pickResult, string) {
	switch k {
	case "enter":
		return p.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	case "esc":
		return p.key(tea.KeyPressMsg{Code: tea.KeyEscape})
	case "up":
		return p.key(tea.KeyPressMsg{Code: tea.KeyUp})
	case "down":
		return p.key(tea.KeyPressMsg{Code: tea.KeyDown})
	case "backspace":
		return p.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	var res pickResult
	var v string
	for _, r := range k {
		res, v = p.key(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return res, v
}

func TestPicker_FilterAndChoose(t *testing.T) {
	p := &picker{title: "pick", items: []pickItem{{label: "a-one", value: "1"}, {label: "b-two", value: "2"}}}
	res, _ := pickKey(p, "TWO")
	assert.Eq(t, res, pickOpen)
	assert.Eq(t, len(p.visible()), 1)
	res, v := pickKey(p, "enter")
	assert.Eq(t, res, pickChosen)
	assert.Eq(t, v, "2")
}

func TestPicker_MoveBackspaceAndCancel(t *testing.T) {
	p := &picker{title: "pick", items: []pickItem{{label: "a-one", value: "1"}, {label: "b-two", value: "2"}}}
	pickKey(p, "up") // clamps at the top
	pickKey(p, "down")
	pickKey(p, "down") // clamps at the bottom
	assert.Eq(t, p.cursor, 1)
	pickKey(p, "zz")
	assert.Eq(t, len(p.visible()), 0)
	res, _ := pickKey(p, "enter") // nothing to choose
	assert.Eq(t, res, pickOpen)
	pickKey(p, "backspace")
	pickKey(p, "backspace")
	assert.Eq(t, p.filter, "")
	assert.Eq(t, len(p.visible()), 2)
	res, _ = pickKey(p, "esc")
	assert.Eq(t, res, pickCancelled)
}

func TestPicker_ViewShowsTenRowsAroundCursor(t *testing.T) {
	var items []pickItem
	for i := range 15 {
		items = append(items, pickItem{label: fmt.Sprintf("item %02d", i), value: fmt.Sprint(i)})
	}
	p := &picker{title: "pick", items: items}
	for range 12 {
		pickKey(p, "down")
	}
	lines := strings.Split(ansi.Strip(p.view(80)), "\n")
	assert.Eq(t, len(lines), 11) // title + 10 rows
	assert.Contains(t, lines[len(lines)-1], "› item 12")
	assert.NotContains(t, strings.Join(lines, "\n"), "item 02")
}
