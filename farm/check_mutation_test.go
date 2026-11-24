package farm

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/providers/fake"
)

// formatCheck rewrites part a's tracked file once (like a formatter) and
// always passes.
const formatCheck = "grep -q formatted a/x.txt || echo formatted >> a/x.txt"

func checkRig(t *testing.T, check string, integrator []fake.Turn) *runRig {
	t.Helper()
	r := newRunRig(t, check, twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == integratePart {
			return integrator
		}
		return twoWriters(ws)
	})
	r.opts.Cfg.PartCheck = "" // only the merged tree's check is under test
	return r
}

func TestRunFarm_CheckRewritingTrackedFilesFails(t *testing.T) {
	r := checkRig(t, formatCheck, []fake.Turn{final("left it")})
	code, reason := r.run(context.Background())
	if code != core.ExitUnmet || reason != ReasonCheckFailed {
		t.Fatalf("exit %d %s", code, reason)
	}
	ev := r.last(EvIntegrateCheck)
	if ev["exit_code"] == float64(0) || !strings.Contains(fmt.Sprint(ev["changed"]), "a/x.txt") {
		t.Fatalf("integrate_check %v", ev)
	}
	if m := r.manifest(); m.Check == nil || !strings.Contains(m.Check.Output, "changed tracked files: a/x.txt") {
		t.Fatalf("verdict %+v", m.Check)
	}
}

func TestRunFarm_CheckRewriteCommittedByIntegratorPasses(t *testing.T) {
	// The integrator applies the rewrite itself and commits it.
	commit, _ := json.Marshal(map[string]string{"command": formatCheck + " && git add -A && git commit --no-verify -q -m format"})
	r := checkRig(t, formatCheck, []fake.Turn{call("shell", string(commit)), final("committed the formatting")})
	code, reason := r.run(context.Background())
	if code != 0 || reason != ReasonOK {
		t.Fatalf("exit %d %s; events %v", code, reason, r.events(""))
	}
	out, _ := r.git("show", r.integrateBranch()+":a/x.txt")
	if !strings.Contains(out, "formatted") {
		t.Fatalf("integrate branch a/x.txt lacks the rewrite: %q", out)
	}
	if m := r.manifest(); m.Check == nil || m.Check.ExitCode != 0 {
		t.Fatalf("verdict %+v", m.Check)
	}
}

func TestRunFarm_FailingCheckThatRewritesSaysSo(t *testing.T) {
	r := checkRig(t, "echo formatted >> a/x.txt && exit 3", []fake.Turn{final("cannot fix")})
	if code, _ := r.run(context.Background()); code != core.ExitUnmet {
		t.Fatalf("exit %d", code)
	}
	if m := r.manifest(); m.Check == nil || m.Check.ExitCode != 3 || !strings.Contains(m.Check.Output, "changed tracked files: a/x.txt") {
		t.Fatalf("verdict %+v", m.Check)
	}
}

func TestRunFarm_UntrackedBuildOutputIsHarmless(t *testing.T) {
	r := checkRig(t, "echo built > build.out", []fake.Turn{final("unused")})
	if code, reason := r.run(context.Background()); code != 0 || reason != ReasonOK {
		t.Fatalf("exit %d %s", code, reason)
	}
	if ev := r.last(EvIntegrateCheck); ev["exit_code"] != float64(0) {
		t.Fatalf("integrate_check %v", ev)
	}
}

func TestReusableMergeCheck_BoundToCommit(t *testing.T) {
	w, head := newRepo(t)
	s := &Supervisor{Git: w.Git}
	s.keepMergeCheck(&core.CheckResult{}, head)
	if s.reusableMergeCheck(context.Background(), w.Main) == nil {
		t.Fatal("pass on the same commit not reused")
	}
	writeFile(t, filepath.Join(w.Main, "a.txt"), "changed\n")
	if s.reusableMergeCheck(context.Background(), w.Main) != nil {
		t.Fatal("pass reused over a changed tracked file")
	}
	mustGit(t, w.Git, w.Main, "commit", "-q", "-am", "change")
	if s.reusableMergeCheck(context.Background(), w.Main) != nil {
		t.Fatal("pass reused on a new commit")
	}
	writeFile(t, filepath.Join(w.Main, "build.out"), "x\n") // untracked
	s.keepMergeCheck(&core.CheckResult{}, mustGit(t, w.Git, w.Main, "rev-parse", "HEAD"))
	if s.reusableMergeCheck(context.Background(), w.Main) == nil {
		t.Fatal("an untracked file invalidated the pass")
	}
}
