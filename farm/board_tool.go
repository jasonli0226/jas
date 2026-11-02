package farm

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jasonli0226/jas/core"
)

// PostTool is the board_post tool a farm worker uses to publish a typed
// commitment. It writes only the board, so it needs no approval (ReadOnly).
type PostTool struct {
	Board *Board
	Part  string
	Role  string
	// SessionID is recorded on each post; optional.
	SessionID string
}

func (PostTool) Name() string { return "board_post" }
func (PostTool) Description() string {
	return "Post a short typed commitment for the other parts of this farm to see: " +
		"owns {paths[]} (the files you are touching), interface {name, shape, note} " +
		"(a signature others rely on), blocked {on, need} (a change you need but may not make). " +
		"Not a chat: keep posts brief and factual."
}
func (PostTool) Kind() core.Kind { return core.ReadOnly }
func (PostTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"topic":{"type":"string","enum":["owns","interface","blocked"]},` +
		`"fields":{"type":"object","description":"owns: {paths:[string]}; interface: {name, shape, note}; blocked: {on, need}."}},` +
		`"required":["topic","fields"]}`)
}

func (t PostTool) Call(_ context.Context, _ core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Topic  string         `json:"topic"`
		Fields map[string]any `json:"fields"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return core.Result{Output: fmt.Sprintf("invalid arguments: %v", err), IsError: true}, nil
	}
	p, err := t.Board.PostFrom(t.Part, t.Role, a.Topic, a.Fields, t.SessionID, "")
	if err != nil {
		return core.Result{Output: "post rejected: " + err.Error(), IsError: true}, nil
	}
	return core.Result{Output: fmt.Sprintf("posted #%d", p.Seq)}, nil
}
