package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

// guardCall is one WriteGuard invocation.
type guardCall struct {
	tool, resolved, content string
	existed                 bool
}

// memRig is an executor with the real write/edit tools, a fake $HOME whose
// ~/.jas/profiles/ds/memory is the active profile memory, and a recording
// WriteGuard that returns guardErr.
type memRig struct {
	memory   string
	log      *recLog
	ex       *core.Executor
	guarded  []guardCall
	guardErr error
}

func newMemRig(t *testing.T, gate core.Gate) *memRig {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	memory := filepath.Join(home, ".jas", "profiles", "ds", "memory")
	assert.NoErr(t, os.MkdirAll(memory, 0o755))
	root, err := filepath.EvalSymlinks(t.TempDir())
	assert.NoErr(t, err)
	r := &memRig{memory: memory, log: &recLog{}}
	r.ex = &core.Executor{
		Reg:    core.NewRegistry(tools.Mutating()...),
		Policy: core.ExecPolicy{Root: root, ProfileMemory: memory},
		Gate:   gate, Log: r.log,
	}
	r.ex.WriteGuard = func(tool, resolved string, existed bool, newContent string) error {
		r.guarded = append(r.guarded, guardCall{tool, resolved, newContent, existed})
		return r.guardErr
	}
	return r
}

func (r *memRig) run(t *testing.T, tool string, args any) []providers.ContentBlock {
	t.Helper()
	raw, _ := json.Marshal(args)
	out, err := r.ex.Run(context.Background(), []providers.ContentBlock{{
		Type: providers.BlockToolUse, ToolUseID: "c1", Name: tool, Input: raw,
	}})
	assert.NoErr(t, err) // a refusal never stops the turn
	return out
}

func (r *memRig) hasEvent(typ string) bool {
	r.log.mu.Lock()
	defer r.log.mu.Unlock()
	for _, e := range r.log.evs {
		if e.typ == typ {
			return true
		}
	}
	return false
}

// countGate grants every call and records what it saw.
type countGate struct {
	calls   int
	mustAsk bool
	onAllow func()
}

func (g *countGate) Allow(_ context.Context, t core.Tool, _ json.RawMessage, _ func() *core.Diff) (core.Decision, error) {
	g.calls++
	g.mustAsk = core.MustAsk(t)
	if g.onAllow != nil {
		g.onAllow()
	}
	return core.Granted, nil
}

const note = "---\nname: user-role\ndescription: role\ntype: user\n---\nline one\nline two\nline three\n"

func TestMemoryWrite_GuardRefusesBeforeGate(t *testing.T) {
	for _, tool := range []string{"write", "edit"} {
		gate := &countGate{}
		r := newMemRig(t, gate)
		path := filepath.Join(r.memory, "user-role.md")
		assert.NoErr(t, os.WriteFile(path, []byte(note), 0o644))
		r.guardErr = errors.New("memory note needs frontmatter: type must be one of user, feedback, project, reference")
		args := map[string]string{"path": path, "content": "no frontmatter\n"}
		if tool == "edit" {
			args = map[string]string{"path": path, "old_string": "type: user", "new_string": "type: bogus"}
		}
		out := r.run(t, tool, args)
		assert.Eq(t, out[0].IsError, true)
		assert.Eq(t, out[0].Text, r.guardErr.Error())
		ev := r.log.result(t)
		assert.Eq(t, ev["status"].(string), "error")
		assert.Eq(t, ev["output"].(string), r.guardErr.Error())
		assert.Eq(t, gate.calls, 0)
		assert.Eq(t, r.hasEvent(core.EvApproval), false)
		assert.Eq(t, r.hasEvent(core.EvToolStarted), false)
		got, _ := os.ReadFile(path)
		assert.Eq(t, string(got), note)
	}
}

func TestMemoryWrite_PreviewFailureRefuses(t *testing.T) {
	gate := &countGate{}
	r := newMemRig(t, gate)
	path := filepath.Join(r.memory, "missing.md")
	out := r.run(t, "edit", map[string]string{"path": path, "old_string": "a", "new_string": "b"})
	assert.Eq(t, out[0].IsError, true)
	if !strings.HasPrefix(out[0].Text, "memory write could not be checked: ") {
		t.Fatalf("output = %q", out[0].Text)
	}
	assert.Eq(t, r.log.result(t)["status"].(string), "error")
	assert.Eq(t, gate.calls, 0)
	assert.Eq(t, len(r.guarded), 0)
	assert.Eq(t, r.hasEvent(core.EvApproval), false)
}

func TestMemoryWrite_PrecheckRunsBeforePreview(t *testing.T) {
	gate := &countGate{}
	r := newMemRig(t, gate)
	var prechecked []string
	r.ex.WritePrecheck = func(tool, resolved string) error {
		prechecked = append(prechecked, tool+" "+resolved)
		return errors.New("memory is read-only in this run: no one can approve the write")
	}
	path := filepath.Join(r.memory, "missing.md") // an edit here cannot preview
	out := r.run(t, "edit", map[string]string{"path": path, "old_string": "a", "new_string": "b"})
	assert.Eq(t, out[0].IsError, true)
	assert.Eq(t, out[0].Text, "memory is read-only in this run: no one can approve the write")
	assert.Eq(t, r.log.result(t)["status"].(string), "error")
	assert.Eq(t, strings.Join(prechecked, ";"), "edit "+path)
	assert.Eq(t, len(r.guarded), 0)
	assert.Eq(t, gate.calls, 0)
}

func TestMemoryWrite_AlwaysAsks(t *testing.T) {
	for _, nilGuard := range []bool{false, true} {
		var prompt bytes.Buffer
		gate := &core.Approver{Level: core.Never, Interactive: true, In: strings.NewReader("y\n"), Out: &prompt}
		r := newMemRig(t, gate)
		if nilGuard {
			r.ex.WriteGuard = nil
		}
		path := filepath.Join(r.memory, "user-role.md")
		out := r.run(t, "write", map[string]string{"path": path, "content": note})
		assert.Eq(t, out[0].IsError, false)
		if !strings.Contains(prompt.String(), "approve write: "+path) {
			t.Fatalf("nilGuard=%v: level never did not ask; prompt = %q", nilGuard, prompt.String())
		}
		assert.Eq(t, r.hasEvent(core.EvApproval), true)
		got, _ := os.ReadFile(path)
		assert.Eq(t, string(got), note)
		if nilGuard {
			assert.Eq(t, len(r.guarded), 0)
		} else {
			assert.Eq(t, len(r.guarded), 1)
			assert.Eq(t, r.guarded[0], guardCall{"write", path, note, false})
		}
	}
}

func TestMemoryWrite_EditPassesWholeFile(t *testing.T) {
	r := newMemRig(t, &countGate{})
	path := filepath.Join(r.memory, "user-role.md")
	assert.NoErr(t, os.WriteFile(path, []byte(note), 0o644))
	r.run(t, "edit", map[string]string{"path": path, "old_string": "line two", "new_string": "line 2"})
	want := strings.Replace(note, "line two", "line 2", 1)
	assert.Eq(t, len(r.guarded), 1)
	assert.Eq(t, r.guarded[0], guardCall{"edit", path, want, true})
	got, _ := os.ReadFile(path)
	assert.Eq(t, string(got), want)
}

func TestMemoryWrite_StaleEditFails(t *testing.T) {
	gate := &countGate{}
	r := newMemRig(t, gate)
	path := filepath.Join(r.memory, "MEMORY.md")
	assert.NoErr(t, os.WriteFile(path, []byte("- [A](a.md) — a\n"), 0o644))
	other := "- [A2](a.md) — rewritten by another session\n"
	// Another session rewrites the index while this call waits for approval.
	gate.onAllow = func() { assert.NoErr(t, os.WriteFile(path, []byte(other), 0o644)) }
	out := r.run(t, "edit", map[string]string{"path": path, "old_string": "- [A](a.md) — a\n", "new_string": "- [A](a.md) — a\n- [B](b.md) — b\n"})
	assert.Eq(t, len(r.guarded), 1)
	assert.Eq(t, gate.calls, 1)
	assert.Eq(t, out[0].IsError, true)
	assert.Eq(t, out[0].Text, "old_string not found in "+path)
	got, _ := os.ReadFile(path)
	assert.Eq(t, string(got), other)
}

func TestMemoryWrite_NonMemoryUnchanged(t *testing.T) {
	for _, tool := range []string{"write", "edit"} {
		gate := &countGate{}
		r := newMemRig(t, gate)
		r.guardErr = errors.New("must not be called")
		assert.NoErr(t, os.WriteFile(filepath.Join(r.ex.Policy.Root, "a.md"), []byte("x\n"), 0o644))
		args := map[string]string{"path": "a.md", "content": "y\n"}
		if tool == "edit" {
			args = map[string]string{"path": "a.md", "old_string": "x", "new_string": "y"}
		}
		out := r.run(t, tool, args)
		assert.Eq(t, out[0].IsError, false)
		assert.Eq(t, len(r.guarded), 0)
		assert.Eq(t, gate.calls, 1)
		assert.Eq(t, gate.mustAsk, false)
	}
}

func TestSession_SetProfileMemory(t *testing.T) {
	r := newRig(t, nil)
	newSession(t, r).SetProfileMemory("/p/memory")
	assert.Eq(t, r.cfg.Exec.Policy.ProfileMemory, "/p/memory")
}

func TestExecutor_GuardAllWrites(t *testing.T) {
	for _, all := range []bool{false, true} {
		r := newMemRig(t, nil)
		r.ex.GuardAllWrites = all
		r.guardErr = errors.New("nope")
		path := filepath.Join(r.ex.Policy.Root, "plain.txt")
		out := r.run(t, "write", map[string]string{"path": path, "content": "x"})
		assert.Eq(t, out[0].IsError, all)
		assert.Eq(t, len(r.guarded) == 1, all)
		_, err := os.Stat(path)
		assert.Eq(t, err == nil, !all)
	}
}
