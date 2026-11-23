package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/farm"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

func boolPtr(b bool) *bool { return &b }

func TestParseFarmRunArgs_ReviewTriState(t *testing.T) {
	for _, c := range []struct {
		args []string
		want *bool
	}{
		{[]string{"plan.yaml"}, nil},
		{[]string{"plan.yaml", "--review"}, boolPtr(true)},
		{[]string{"--review=true", "plan.yaml"}, boolPtr(true)},
		{[]string{"plan.yaml", "--review=false"}, boolPtr(false)},
	} {
		ra, err := parseFarmRunArgs(c.args)
		assert.NoErr(t, err)
		if (ra.review == nil) != (c.want == nil) || (c.want != nil && *ra.review != *c.want) {
			t.Fatalf("%v: review %v, want %v", c.args, ra.review, c.want)
		}
		if _, ok := ra.flags["review"]; ok {
			t.Fatal("--review passed on as a setting")
		}
	}
}

func TestParsePlanArgs_ReviewOnlyForDirect(t *testing.T) {
	if _, err := parsePlanArgs([]string{"--review", "task"}); err == nil {
		t.Fatal("jas farm plan accepted --review")
	}
	pa, err := parseDirectArgs([]string{"--review=false", "task"})
	assert.NoErr(t, err)
	if pa.review == nil || *pa.review {
		t.Fatalf("review %v, want false", pa.review)
	}
	if _, ok := pa.flags["review"]; ok {
		t.Fatal("--review passed on as a setting")
	}
}

// --review=false turns a configured review off: no review events.
func TestFarmRun_ReviewFlagOverridesConfig(t *testing.T) {
	repo, home := farmRepo(t), t.TempDir()
	assert.NoErr(t, os.WriteFile(filepath.Join(home, "farm.yaml"), []byte("check: \"true\"\nreview: true\nsplit:\n  max_workers: 1\n"), 0o644))
	plan := approvedPlan(t, repo)
	url, _ := testutil.FakeOpenAI(t, []fake.Turn{{Text: "a done"}, {Text: "b done"}, {Text: "c done"}})
	runJasIn(t, repo, farmRunEnv(url, home, t), "farm", "run", plan, "--json", "--trust-project", "--review=false")
	dirs := farmDirs(t, home)
	assert.Eq(t, len(dirs), 1)
	raw, err := os.ReadFile(filepath.Join(dirs[0], "farm.jsonl"))
	assert.NoErr(t, err)
	if strings.Contains(string(raw), "review_started") || strings.Contains(string(raw), "review_status") {
		t.Fatalf("review ran despite --review=false:\n%s", raw)
	}
}

func TestFarmStatus_ShowsReview(t *testing.T) {
	var b strings.Builder
	printFarmStatus(&b, farm.Manifest{
		FarmID: "x", Reason: "review_unmet", ExitCode: 6,
		Review: &farm.ReviewState{
			Status: "unmet", Open: []string{"R1"},
			Rounds: []farm.ReviewRoundState{{Round: 1, Findings: []farm.Finding{
				{ID: "R1", Kind: "missed", PlanRef: "part:docs", Where: "README.md", Text: "no mention"},
				{ID: "R2", Kind: "drift", PlanRef: "-", Where: "a.go", Text: "fixed already"},
			}}},
		},
	})
	assert.Contains(t, b.String(), "review: unmet  open 1")
	assert.Contains(t, b.String(), "    R1 [missed] part:docs README.md: no mention")
	if strings.Contains(b.String(), "R2") {
		t.Fatalf("closed finding listed:\n%s", b.String())
	}
	b.Reset()
	printFarmStatus(&b, farm.Manifest{FarmID: "x"})
	if strings.Contains(b.String(), "review") {
		t.Fatal("review line without a review")
	}
}
