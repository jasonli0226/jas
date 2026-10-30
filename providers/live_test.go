//go:build live

package providers

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

const (
	liveBaseURL  = "https://openrouter.ai/api/v1"
	liveModel    = "z-ai/glm-5.3"
	liveMaxCalls = 6
)

var liveLookupValues = map[string]string{"alpha": "41", "beta": "7"}

// TestLive_GLMMultiTurnToolsWithoutReasoning checks that dropping reasoning
// from the history (the current behaviour) doesn't break a two-step tool loop
// that also has consecutive user messages.
func TestLive_GLMMultiTurnToolsWithoutReasoning(t *testing.T) {
	key := os.Getenv("OPENROUTER_API_KEY")
	if key == "" {
		t.Skip("OPENROUTER_API_KEY not set")
	}
	p := &OpenAI{BaseURL: liveBaseURL, APIKey: key, IdleTimeout: 60 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	req := Request{
		Model: liveModel,
		Tools: []ToolSpec{{
			Name:        "lookup",
			Description: "Look up the value stored under a key.",
			Schema:      json.RawMessage(`{"type":"object","properties":{"key":{"type":"string"}},"required":["key"]}`),
		}},
		Messages: []Message{userText("Use the lookup tool to look up the key alpha, then the key beta. " +
			"Call the tool one at a time, never both in one turn. Then answer with both values.")},
	}

	var final string
	toolRounds := 0
	for i := 0; i < liveMaxCalls; i++ {
		msg, uses := liveRound(t, ctx, p, req)
		req.Messages = append(req.Messages, msg)
		if len(uses) == 0 {
			final = blockText(msg)
			break
		}
		var results []ContentBlock
		for _, u := range uses {
			var in struct{ Key string }
			assert.NoErr(t, json.Unmarshal(u.Input, &in))
			out, ok := liveLookupValues[in.Key]
			if !ok {
				out = "unknown key"
			}
			results = append(results, ContentBlock{Type: BlockToolResult, ToolUseID: u.ToolUseID, Text: out})
		}
		req.Messages = append(req.Messages, Message{Role: RoleUser, Content: results})
		if toolRounds == 0 {
			// Consecutive user messages: tool results, then a plain user turn.
			req.Messages = append(req.Messages, userText("continue"))
		}
		toolRounds++
	}
	assert.Contains(t, final, "41")
	assert.Contains(t, final, "7")
}

// liveRound streams one request and returns the assistant message and its
// tool calls. Reasoning deltas are ignored so they never reach the history.
func liveRound(t *testing.T, ctx context.Context, p Provider, req Request) (Message, []ContentBlock) {
	t.Helper()
	evs, errc := p.Stream(ctx, req)
	var acc Accumulator
	for ev := range evs {
		acc.Add(ev)
	}
	assert.NoErr(t, <-errc)
	msg, uses, err := acc.Done()
	assert.NoErr(t, err)
	return msg, uses
}

func userText(s string) Message {
	return Message{Role: RoleUser, Content: []ContentBlock{{Type: BlockText, Text: s}}}
}

func blockText(m Message) string {
	var s string
	for _, b := range m.Content {
		if b.Type == BlockText {
			s += b.Text
		}
	}
	return s
}
