package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type pendingCall struct {
	id, name string
	args     strings.Builder
}

// Accumulator folds StreamEvents into a complete assistant Message.
type Accumulator struct {
	text  strings.Builder
	calls map[int]*pendingCall
}

// Add folds one event into the accumulator.
func (a *Accumulator) Add(ev StreamEvent) {
	a.text.WriteString(ev.Text)
	d := ev.ToolCallDelta
	if d == nil {
		return
	}
	if a.calls == nil {
		a.calls = make(map[int]*pendingCall)
	}
	c := a.calls[d.Index]
	if c == nil {
		c = &pendingCall{}
		a.calls[d.Index] = c
	}
	if c.id == "" {
		c.id = d.ID
	}
	if c.name == "" {
		c.name = d.Name
	}
	c.args.WriteString(d.ArgsFragment)
}

// Done returns the assistant message and its tool_use blocks (in index order).
// It errors if a tool call never received a name or an id.
func (a *Accumulator) Done() (Message, []ContentBlock, error) {
	msg := Message{Role: RoleAssistant}
	if a.text.Len() > 0 {
		msg.Content = append(msg.Content, ContentBlock{Type: BlockText, Text: a.text.String()})
	}

	idx := make([]int, 0, len(a.calls))
	for i := range a.calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)

	var uses []ContentBlock
	for _, i := range idx {
		c := a.calls[i]
		if c.name == "" {
			return Message{}, nil, fmt.Errorf("tool call %d: %w", i, errMissingName)
		}
		if c.id == "" {
			return Message{}, nil, fmt.Errorf("tool call %d: %w", i, errMissingID)
		}
		args := c.args.String()
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		uses = append(uses, ContentBlock{
			Type:      BlockToolUse,
			ToolUseID: c.id,
			Name:      c.name,
			Input:     json.RawMessage(args),
		})
	}
	msg.Content = append(msg.Content, uses...)
	return msg, uses, nil
}

var (
	errMissingName = errors.New("missing tool name")
	errMissingID   = errors.New("missing tool call id")
)
