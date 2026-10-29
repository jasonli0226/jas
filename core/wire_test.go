package core

import (
	"encoding/json"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

func TestWire_RoundTrip(t *testing.T) {
	in := []providers.ContentBlock{
		{Type: providers.BlockText, Text: "hello"},
		{Type: providers.BlockToolUse, ToolUseID: "c1", Name: "ls", Input: json.RawMessage(`{"path":"a"}`)},
		{Type: providers.BlockToolResult, ToolUseID: "c1", Text: "boom", IsError: true},
	}
	b, err := json.Marshal(toWire(in))
	assert.NoErr(t, err)
	var ws []wireBlock
	assert.NoErr(t, json.Unmarshal(b, &ws))
	out := fromWire(ws)
	assert.Eq(t, len(out), len(in))
	for i := range in {
		assert.Eq(t, out[i].Type, in[i].Type)
		assert.Eq(t, out[i].Text, in[i].Text)
		assert.Eq(t, out[i].ToolUseID, in[i].ToolUseID)
		assert.Eq(t, out[i].Name, in[i].Name)
		assert.Eq(t, string(out[i].Input), string(in[i].Input))
		assert.Eq(t, out[i].IsError, in[i].IsError)
	}
}
