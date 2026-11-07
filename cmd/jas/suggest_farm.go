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
	"Draft a self-contained task description; the owner sees it and answers plan this farm? yes/no/change. " +
	"On change, their next message says what to change: revise the task and call suggest_farm again. " +
	"Never use for small or single-package changes. In the chat this is the only way to start a farm: " +
	"never run jas farm through shell (it has no terminal for the owner's approvals)."

// farmUnavailableHint follows the reason a farm cannot run: the agent must not
// route around it (shell, or doing the task itself) without the owner.
const farmUnavailableHint = "Stop and ask the owner before doing anything else: " +
	"do not run jas farm through shell, and do not do the task yourself unless they say so. " +
	"If the only problem is a missing check, offer to set up a check first " +
	"(a test or build command, set as check: in .jas/farm.yaml, committed so the tree is clean), " +
	"then call suggest_farm again."

// suggestFarm lets the chat agent propose a farm: the session's UI shows the
// task and asks the owner whether to plan it; the tool starts nothing. It is registered in every
// chat; available is asked at each call, so a check added mid-chat counts and
// the agent learns why a farm cannot run instead of reaching for shell.
type suggestFarm struct {
	ui        core.UI
	available func() (bool, string)
	tree      func(context.Context) (bool, string) // nil skips the git check
}

func (suggestFarm) Name() string        { return suggestFarmName }
func (suggestFarm) Description() string { return suggestFarmDescription }
func (suggestFarm) Kind() core.Kind     { return core.ReadOnly }
func (suggestFarm) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"task":{"type":"string",` +
		`"description":"self-contained task description for the farm planner"}},"required":["task"]}`)
}

func (t suggestFarm) Call(ctx context.Context, _ core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct{ Task string }
	if err := json.Unmarshal(args, &a); err != nil {
		return core.Result{}, err
	}
	task := strings.TrimSpace(a.Task)
	if task == "" {
		return core.Result{}, errors.New("task is empty")
	}
	if ok, why := t.available(); !ok {
		return core.Result{}, fmt.Errorf("a farm cannot run here: %s. %s", why, farmUnavailableHint)
	}
	if t.tree != nil {
		if ok, why := t.tree(ctx); !ok {
			return core.Result{}, fmt.Errorf("a farm cannot run yet: %s. Do not run jas farm through shell", why)
		}
	}
	t.ui.Event(tui.EvSuggestFarm, map[string]any{"task": task})
	return core.Result{Output: fmt.Sprintf("suggested to the owner: /farm %q (they decide whether to run it)", task)}, nil
}
