package dream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers"
	"github.com/jasonli0226/jas/tools"
)

func call(t *testing.T, tool core.Tool, ws string, args any) core.Result {
	t.Helper()
	b, _ := json.Marshal(args)
	res, err := tool.Call(context.Background(), core.ExecPolicy{Root: ws}, b)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

func TestRemoveNote_RefusesIndexAndOutside(t *testing.T) {
	g, ws := guardFixture(t)
	idx := filepath.Join(g.Proposed, "MEMORY.md")
	touch(t, idx)
	touch(t, filepath.Join(ws, "evidence.md"))
	touch(t, filepath.Join(g.Proposed, "projects", "x", "MeMoRy.md"))
	tool := RemoveNote(g.Proposed)
	for _, p := range []string{"proposed/MEMORY.md", "proposed/projects/x/MeMoRy.md", "evidence.md", "../x.md", "proposed/projects", "proposed", "proposed/projects/x"} {
		if res := call(t, tool, ws, map[string]string{"path": p}); !res.IsError {
			t.Errorf("%s: want error, got %q", p, res.Output)
		}
	}
	if !exists(idx) || !exists(filepath.Join(ws, "evidence.md")) || !exists(filepath.Join(g.Proposed, "projects", "x")) {
		t.Fatal("a refused removal deleted something")
	}
}

func TestRemoveNote_RemovesEmptyProjectFolder(t *testing.T) {
	g, ws := guardFixture(t)
	a := filepath.Join(g.Proposed, "projects", "x", "a.md")
	b := filepath.Join(g.Proposed, "projects", "x", "b.md")
	touch(t, a)
	touch(t, b)
	tool := RemoveNote(g.Proposed)
	if res := call(t, tool, ws, map[string]string{"path": a}); res.IsError {
		t.Fatal(res.Output)
	}
	if !exists(filepath.Dir(a)) {
		t.Fatal("folder removed while non-empty")
	}
	if res := call(t, tool, ws, map[string]string{"path": "proposed/projects/x/b.md"}); res.IsError {
		t.Fatal(res.Output)
	}
	if exists(filepath.Dir(a)) {
		t.Fatal("empty project folder kept")
	}
	if !exists(filepath.Join(g.Proposed, "projects")) {
		t.Fatal("projects/ itself must stay")
	}
}

func TestRemoveNote_NonMdAllowed(t *testing.T) {
	g, ws := guardFixture(t)
	p := filepath.Join(g.Proposed, "not-allowed")
	touch(t, p)
	if res := call(t, RemoveNote(g.Proposed), ws, map[string]string{"path": "proposed/not-allowed"}); res.IsError || exists(p) {
		t.Fatalf("res=%+v", res)
	}
}

func TestReportChange_CollectsAndValidates(t *testing.T) {
	var c Changes
	tool := ReportChange(&c)
	if tool.Kind() != core.ReadOnly || tool.Name() != "report_change" {
		t.Fatal("wrong name or kind")
	}
	r1 := call(t, tool, "", map[string]any{"kind": "fix", "paths": []string{"a.md"}, "reason": "r1", "evidence": []string{"s#1"}})
	r2 := call(t, tool, "", map[string]any{"kind": "merge", "paths": []string{"a.md", "b.md"}, "reason": "r2"})
	if r1.IsError || r2.IsError {
		t.Fatal(r1.Output, r2.Output)
	}
	all := c.All()
	if len(all) != 2 || all[0].Kind != "fix" || all[1].Kind != "merge" || all[0].Evidence[0] != "s#1" {
		t.Fatalf("all = %+v", all)
	}
	if !call(t, tool, "", map[string]any{"kind": "rewrite", "paths": []string{"a.md"}, "reason": "r"}).IsError {
		t.Error("unknown kind accepted")
	}
	if !call(t, tool, "", map[string]any{"kind": "fix", "paths": []string{}, "reason": "r"}).IsError {
		t.Error("empty paths accepted")
	}
	if len(c.All()) != 2 {
		t.Error("rejected call was recorded")
	}
}

func TestRemoveNote_RefusesSymlink(t *testing.T) {
	g, ws := guardFixture(t)
	ev := filepath.Join(ws, "evidence.md")
	touch(t, ev)
	link := filepath.Join(g.Proposed, "link.md")
	if err := os.Symlink(ev, link); err != nil {
		t.Skip(err)
	}
	inner := filepath.Join(g.Proposed, "real.md")
	touch(t, inner)
	link2 := filepath.Join(g.Proposed, "link2.md")
	if err := os.Symlink(inner, link2); err != nil {
		t.Skip(err)
	}
	for _, p := range []string{"proposed/link.md", "proposed/link2.md"} {
		if res := call(t, RemoveNote(g.Proposed), ws, map[string]string{"path": p}); !res.IsError {
			t.Errorf("%s: want refusal", p)
		}
	}
	if !exists(ev) || !exists(inner) {
		t.Fatal("target removed")
	}
}

func TestReportChange_RejectsEmptyReasonAndPath(t *testing.T) {
	var c Changes
	tool := ReportChange(&c)
	if !call(t, tool, "", map[string]any{"kind": "fix", "paths": []string{"a.md"}, "reason": " "}).IsError {
		t.Error("empty reason accepted")
	}
	if !call(t, tool, "", map[string]any{"kind": "fix", "paths": []string{""}, "reason": "r"}).IsError {
		t.Error("empty path accepted")
	}
}

func TestExecutor_DreamGuardConfinesWrites(t *testing.T) {
	g, ws := guardFixture(t)
	var changes Changes
	ex := &core.Executor{
		Reg:            core.NewRegistry(tools.Write{}, tools.Edit{}, RemoveNote(g.Proposed), ReportChange(&changes)),
		Policy:         core.ExecPolicy{Root: ws},
		Log:            nopLog{},
		GuardAllWrites: true,
		WritePrecheck:  g.Precheck,
		WriteGuard:     g.Check,
	}
	run := func(tool string, args any) providers.ContentBlock {
		raw, _ := json.Marshal(args)
		out, err := ex.Run(context.Background(), []providers.ContentBlock{{Type: providers.BlockToolUse, ToolUseID: "c", Name: tool, Input: raw}})
		if err != nil {
			t.Fatal(err)
		}
		return out[0]
	}
	if r := run("write", map[string]string{"path": "evidence.md", "content": "pwn"}); !r.IsError {
		t.Error("evidence.md write allowed")
	}
	if exists(filepath.Join(ws, "evidence.md")) {
		t.Error("evidence.md created")
	}
	if r := run("write", map[string]string{"path": "proposed/x.md", "content": "no frontmatter"}); !r.IsError {
		t.Error("invalid note allowed")
	}
	if r := run("write", map[string]string{"path": "proposed/a-note.md", "content": goodNote}); r.IsError {
		t.Fatalf("valid note refused: %s", r.Text)
	}
	if !exists(filepath.Join(g.Proposed, "a-note.md")) {
		t.Error("valid note not written")
	}
}

// nopLog discards events; the executor needs a log.
type nopLog struct{}

func (nopLog) Append(string, any) error             { return nil }
func (nopLog) AppendID(string, any) (string, error) { return "", nil }
func (nopLog) AppendSync(string, any) error         { return nil }
func (nopLog) Sync() error                          { return nil }
func (nopLog) Close() error                         { return nil }
