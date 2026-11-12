package dream

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/memory"
)

// Change kinds the model may report.
var changeKinds = []string{"fix", "merge", "correct", "add", "remove", "prune", "promote"}

// Change is one reported edit to the proposed store. Evidence items are
// "<session-id>#<seq>"; they are verified later, not here.
type Change struct {
	Kind     string
	Paths    []string
	Reason   string
	Evidence []string
}

// Changes collects reported changes; safe for concurrent use.
type Changes struct {
	mu  sync.Mutex
	all []Change
}

// All returns a copy of the reported changes in call order.
func (c *Changes) All() []Change {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.all)
}

func (c *Changes) add(ch Change) {
	c.mu.Lock()
	c.all = append(c.all, ch)
	c.mu.Unlock()
}

func errResult(format string, a ...any) (core.Result, error) {
	return core.Result{Output: fmt.Sprintf(format, a...), IsError: true}, nil
}

type reportChange struct{ c *Changes }

// ReportChange returns the report_change tool, which records into c.
func ReportChange(c *Changes) core.Tool { return reportChange{c} }

func (reportChange) Name() string { return "report_change" }
func (reportChange) Description() string {
	return "Record one change you made to the proposed store, with the reason and the evidence (session-id#seq) behind it. Call it once per change."
}
func (reportChange) Kind() core.Kind { return core.ReadOnly }
func (reportChange) Schema() json.RawMessage {
	kinds, _ := json.Marshal(changeKinds)
	return json.RawMessage(`{"type":"object","properties":{` +
		`"kind":{"type":"string","enum":` + string(kinds) + `,"description":"What sort of change this is."},` +
		`"paths":{"type":"array","items":{"type":"string"},"description":"Store paths the change touches."},` +
		`"reason":{"type":"string","description":"Why the change is right."},` +
		`"evidence":{"type":"array","items":{"type":"string"},"description":"Evidence items as <session-id>#<seq>."}},` +
		`"required":["kind","paths","reason"]}`)
}

func (r reportChange) Call(_ context.Context, _ core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Kind     string   `json:"kind"`
		Paths    []string `json:"paths"`
		Reason   string   `json:"reason"`
		Evidence []string `json:"evidence"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	if !slices.Contains(changeKinds, a.Kind) {
		return errResult("kind %q is not one of %s", a.Kind, strings.Join(changeKinds, ", "))
	}
	if len(a.Paths) == 0 {
		return errResult("paths must list at least one path")
	}
	if strings.TrimSpace(a.Reason) == "" {
		return errResult("reason must not be empty")
	}
	if slices.Contains(a.Paths, "") {
		return errResult("paths must not contain an empty string")
	}
	r.c.add(Change{Kind: a.Kind, Paths: a.Paths, Reason: a.Reason, Evidence: a.Evidence})
	return core.Result{Output: "recorded"}, nil
}

type removeNote struct{ proposed string }

// RemoveNote returns the remove_note tool, confined to the proposed folder.
func RemoveNote(proposed string) core.Tool { return removeNote{proposed} }

func (removeNote) Name() string { return "remove_note" }
func (removeNote) Description() string {
	return "Delete one note file from the proposed store. MEMORY.md and folders cannot be removed; a projects/<key>/ folder left empty is removed too."
}
func (removeNote) Kind() core.Kind { return core.Mutating }
func (removeNote) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{` +
		`"path":{"type":"string","description":"Note path, relative to the workspace or absolute, inside proposed/."}},` +
		`"required":["path"]}`)
}

func (t removeNote) Call(_ context.Context, p core.ExecPolicy, args json.RawMessage) (core.Result, error) {
	var a struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return errResult("invalid arguments: %v", err)
	}
	full, err := p.Resolve(a.Path)
	if err != nil {
		return errResult("%v", err)
	}
	if err := refuseLink(p, a.Path); err != nil {
		return errResult("%v", err)
	}
	root := memory.EvalDir(t.proposed)
	if !memory.Within(root, full) || full == root {
		return errResult("dreams write only inside proposed/")
	}
	if memory.IsIndex(filepath.Base(full)) {
		return errResult("MEMORY.md cannot be removed; edit it instead")
	}
	info, err := os.Lstat(full)
	if err != nil {
		return errResult("remove_note %s: %v", a.Path, err)
	}
	if info.IsDir() {
		return errResult("remove_note %s: is a folder, not a note", a.Path)
	}
	if err := os.Remove(full); err != nil {
		return errResult("remove_note %s: %v", a.Path, err)
	}
	if dir := filepath.Dir(full); dir != root && allowedFolder(root, dir) {
		_ = os.Remove(dir) // fails harmlessly unless the folder is empty
	}
	return core.Result{Output: "removed " + a.Path}, nil
}

// refuseLink refuses a path whose in-tree entry is a symlink: proposed/ is a
// copy and holds none, so a link is never removed through the tool.
func refuseLink(p core.ExecPolicy, path string) error {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(p.Root, abs)
	}
	for _, cand := range []string{filepath.Clean(abs), filepath.Join(memory.EvalDir(filepath.Dir(abs)), filepath.Base(abs))} {
		if info, err := os.Lstat(cand); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("remove_note %s: is a symbolic link", path)
		}
	}
	return nil
}
