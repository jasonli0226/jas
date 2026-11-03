package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/tui"
)

const suggestFarmName = "suggest_farm"

// suggestFarmDescription is the tool's description (spec copy).
const suggestFarmDescription = "Suggest splitting a large task into a farm of parallel agents. " +
	"Use only for big, decomposable work (several packages or layers); the owner decides. " +
	"Draft a self-contained task description; it pre-fills the owner's input as /farm \"<task>\". " +
	"Never use for small or single-package changes."

// suggestFarm lets the chat agent propose a farm: it pre-fills the owner's
// input through the session's UI and starts nothing.
type suggestFarm struct{ ui core.UI }

func (suggestFarm) Name() string        { return suggestFarmName }
func (suggestFarm) Description() string { return suggestFarmDescription }
func (suggestFarm) Kind() core.Kind     { return core.ReadOnly }
func (suggestFarm) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string",` +
		`"description":"self-contained task description for the farm planner"}},"required":["task"]}`)
}

func (t suggestFarm) Call(_ context.Context, _ core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct{ Task string }
	if err := json.Unmarshal(args, &a); err != nil {
		return core.Result{}, err
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return core.Result{}, errors.New("task is empty")
	}
	t.ui.Event(tui.EvSuggestFarm, map[string]any{"task": task})
	return core.Result{Output: fmt.Sprintf("suggested to the owner: /farm %q (they decide whether to run it)", task)}, nil
}
