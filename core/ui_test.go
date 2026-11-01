package core_test

import (
	"bytes"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func diffResultEvent() map[string]any {
	return map[string]any{
		"tool":   "edit",
		"status": "ok",
		"output": "edited f.txt",
		"diff": map[string]any{
			"path": "f.txt",
			"hunks": []any{[]any{
				map[string]any{"k": " ", "o": 3.0, "n": 3.0, "t": "c"},
				map[string]any{"k": "-", "o": 4.0, "t": "d"},
				map[string]any{"k": "+", "n": 4.0, "t": "D"},
			}},
		},
	}
}

func TestPrinter_DiffsWhenEnabled(t *testing.T) {
	var buf bytes.Buffer
	p := core.NewPrinter(&buf, true)
	p.Event(core.EvToolResult, diffResultEvent())
	assert.Contains(t, buf.String(), "     4 + D")
}

func TestPrinter_NoDiffsWhenDisabled(t *testing.T) {
	var buf bytes.Buffer
	p := core.NewPrinter(&buf, false)
	p.Event(core.EvToolResult, diffResultEvent())
	assert.Eq(t, buf.String(), "")
}
