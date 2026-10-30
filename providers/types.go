// Package providers defines provider-neutral request/stream types and the
// helpers (SSE reader, tool-call accumulator) shared by provider adapters.
package providers

import (
	"context"
	"encoding/json"
)

// Role is the author of a message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Content block types.
const (
	BlockText       = "text"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
)

// ContentBlock is one piece of message content.
type ContentBlock struct {
	Type      string // BlockText | BlockToolUse | BlockToolResult
	Text      string
	ToolUseID string
	Name      string
	Input     json.RawMessage
	IsError   bool
}

// Message is one conversation turn.
type Message struct {
	Role    Role
	Content []ContentBlock
}

// ToolSpec describes a tool offered to the model.
type ToolSpec struct {
	Name, Description string
	Schema            json.RawMessage
}

// Request is a provider-neutral completion request.
type Request struct {
	Model    string
	System   string
	Tools    []ToolSpec
	Messages []Message
}

// Usage reports token counts for a request.
type Usage struct {
	PromptTokens, CompletionTokens, CachedTokens int
}

// ToolCallDelta is a fragment of a streamed tool call. ID and Name usually
// arrive only in the first fragment for an Index.
type ToolCallDelta struct {
	Index                  int
	ID, Name, ArgsFragment string
}

// StreamEvent is one provider-neutral streaming event.
type StreamEvent struct {
	Text          string
	Reasoning     string // thinking delta; display only, never part of history
	ToolCallDelta *ToolCallDelta
	Usage         *Usage
	FinishReason  string
}

// Provider streams a completion. The error channel yields at most one error;
// both channels are closed when the stream ends.
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, <-chan error)
}
