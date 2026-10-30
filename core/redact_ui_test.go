package core

import (
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
)

type captureUI struct {
	text   strings.Builder
	reason strings.Builder
	events []any
}

func (c *captureUI) Text(d string)         { c.text.WriteString(d) }
func (c *captureUI) Reasoning(d string)    { c.reason.WriteString(d) }
func (c *captureUI) Event(_ string, d any) { c.events = append(c.events, d) }

func TestRedactingUI_TextAndEvents(t *testing.T) {
	r := &Redactor{Exact: []string{"s3cr3tvalue"}}
	c := &captureUI{}
	ui := NewRedactingUI(c, r.Redact)
	ui.Text("answer s3cr3tvalue")
	ui.Event(EvToolStarted, map[string]any{"tool": "shell", "args": map[string]any{"command": "echo s3cr3tvalue"}})
	assert.Eq(t, c.text.String(), "answer [REDACTED:exact]")
	m := c.events[0].(map[string]any)
	assert.Eq(t, m["args"].(map[string]any)["command"].(string), "echo [REDACTED:exact]")
}

func TestRedactingUI_RedactsReasoning(t *testing.T) {
	r := &Redactor{Exact: []string{"sk-secret"}}
	c := &captureUI{}
	NewRedactingUI(c, r.Redact).Reasoning("key sk-secret")
	assert.Eq(t, c.reason.String(), "key [REDACTED:exact]")
	assert.Eq(t, c.text.String(), "")
}
