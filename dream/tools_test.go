package dream

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonli0226/jas/core"
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
