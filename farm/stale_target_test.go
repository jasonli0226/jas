package farm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ownerCommit commits a file in the main checkout as the owner.
func (r *runRig) ownerCommit(name string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.main, name), []byte("owner\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	for _, args := range [][]string{{"add", name}, {"commit", "-q", "-m", "owner " + name}} {
		if _, err := r.git(args...); err != nil {
			r.t.Fatal(err)
		}
	}
	head, _ := r.git("rev-parse", "HEAD")
	return head
}

// recordingAsk answers each Ask with answer (then keep) and records the prompts;
// before the first answer it runs first.
func recordingAsk(first func(), answer string) (func(string, []string) (string, bool), func() []string) {
	var mu sync.Mutex
	var prompts []string
	ask := func(prompt string, _ []string) (string, bool) {
		mu.Lock()
		defer mu.Unlock()
		prompts = append(prompts, prompt)
		if len(prompts) > 1 {
			return "", true
		}
		if first != nil {
			first()
		}
		return answer, true
	}
	return ask, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), prompts...) }
}

func (r *runRig) manifest() Manifest {
	r.t.Helper()
	m, err := LoadManifest(filepath.Join(r.farmDir(), manifestFile))
	if err != nil {
		r.t.Fatal(err)
	}
	return m
}

func TestRunFarm_MergeWhenTargetUnchanged(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	base, _ := r.git("rev-parse", "HEAD")
	r.opts.Ask, _ = recordingAsk(nil, "m")
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ev := r.last(EvOwnerDecision); ev["choice"] != ChoiceMerge {
		t.Fatalf("owner_decision %v", ev)
	}
	tip, _ := r.git("rev-parse", r.integrateBranch())
	if second, _ := r.git("rev-parse", "HEAD^2"); second != tip {
		t.Fatalf("HEAD^2 %s, integrate tip %s", second, tip)
	}
	m := r.manifest()
	if m.CheckedCommit != tip || m.TargetHead != base {
		t.Fatalf("manifest checked %q target %q; want %q %q", m.CheckedCommit, m.TargetHead, tip, base)
	}
}

func TestRunFarm_TargetMovedSinceCheckRefusesMerge(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	var moved string
	r.opts.Ask, _ = recordingAsk(func() { moved = r.ownerCommit("owner.txt") }, "m")
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if head, _ := r.git("rev-parse", "HEAD"); head != moved {
		t.Fatalf("main HEAD %s, want the owner's commit %s (no merge)", head, moved)
	}
	ev := r.last(EvOwnerDecision)
	if ev["choice"] != ChoiceKeep || !strings.Contains(ev["note"].(string), "moved") {
		t.Fatalf("owner_decision %v", ev)
	}
	if ev["target_head"] != moved {
		t.Fatalf("owner_decision target_head %v, want %s", ev["target_head"], moved)
	}
	if out, _ := r.git("branch", "--list", "jas/farm/*"); out == "" {
		t.Fatal("branches removed")
	}
	if m := r.manifest(); m.FinalChoice != ChoiceKeep {
		t.Fatalf("manifest final_choice %q", m.FinalChoice)
	}
}

func TestRunFarm_OlderBaseRefusesMergeAndSaysSo(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters) // the plan's base is the first commit
	r.ownerCommit("later.txt")
	var prompts func() []string
	r.opts.Ask, prompts = recordingAsk(nil, "m")
	if code, _ := r.run(context.Background()); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if p := prompts(); len(p) == 0 || !strings.Contains(p[0], "moved since the check") {
		t.Fatalf("final prompt does not warn: %q", p)
	}
	if ev := r.last(EvOwnerDecision); ev["choice"] != ChoiceKeep {
		t.Fatalf("owner_decision %v", ev)
	}
	if _, err := r.git("rev-parse", "--verify", "--quiet", "HEAD^2"); err == nil {
		t.Fatal("merged a farm checked without the target's newer commit")
	}
}
