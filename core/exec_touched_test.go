package core_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

func TestExecutor_TouchedHook(t *testing.T) {
	root := t.TempDir()
	log, err := core.OpenEventLog(filepath.Join(t.TempDir(), "e.jsonl"), nil, nil)
	assert.NoErr(t, err)
	defer log.Close()
	type hit struct{ tool, path string }
	var hits []hit
	ex := &core.Executor{
		Reg:    core.NewRegistry(append(tools.ReadOnly(), tools.Mutating()...)...),
		Policy: core.ExecPolicy{Root: root}, Gate: core.AllowAll{}, Log: log,
		Touched: func(tool, path string) { hits = append(hits, hit{tool, path}) },
	}
	call := func(id, name, args string) providers.ContentBlock {
		return providers.ContentBlock{Type: providers.BlockToolUse, ToolUseID: id, Name: name, Input: json.RawMessage(args)}
	}
	_, err = ex.Run(context.Background(), []providers.ContentBlock{
		call("1", "write", `{"path":"a.txt","content":"x"}`),
		call("2", "write", `{"path":".env","content":"x"}`), // denied by the floor
		call("3", "shell", `{"command":"true"}`),
		call("4", "read", `{"path":"a.txt"}`),
	})
	assert.NoErr(t, err)
	assert.Eq(t, len(hits), 2)
	want, _ := filepath.EvalSymlinks(filepath.Join(root, "a.txt"))
	got, _ := filepath.EvalSymlinks(hits[0].path)
	assert.Eq(t, hits[0].tool, "write")
	assert.Eq(t, got, want)
	assert.Eq(t, hits[1], hit{"shell", ""})
}
