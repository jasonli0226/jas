package core

import (
	"encoding/json"

	"github.com/jasonli0226/jas/providers"
)

// wireBlock is a providers.ContentBlock as stored in the event log, with
// snake_case keys. The replayer rebuilds the conversation from these.
type wireBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func toWire(bs []providers.ContentBlock) []wireBlock {
	out := make([]wireBlock, len(bs))
	for i, b := range bs {
		out[i] = wireBlock{Type: b.Type, Text: b.Text, ToolUseID: b.ToolUseID, Name: b.Name, Input: b.Input, IsError: b.IsError}
	}
	return out
}

// fromWire rebuilds blocks from the log. Tool input comes back as the
// redactor's JSON round-trip left it (compacted, keys reordered), so a resumed
// session's first request may miss the provider's prompt cache once; accepted.
func fromWire(ws []wireBlock) []providers.ContentBlock {
	out := make([]providers.ContentBlock, len(ws))
	for i, w := range ws {
		out[i] = providers.ContentBlock{Type: w.Type, Text: w.Text, ToolUseID: w.ToolUseID, Name: w.Name, Input: w.Input, IsError: w.IsError}
	}
	return out
}
