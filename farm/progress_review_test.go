package farm

import (
	"strings"
	"testing"
)

func TestFarmLine_ReviewEvents(t *testing.T) {
	cases := []struct {
		typ  string
		m    map[string]any
		want string
	}{
		{EvReviewStarted, map[string]any{"round": 1}, "review: round 1 started"},
		{EvReviewEnded, map[string]any{"round": 1, "status": "done",
			"findings": map[string]int{"missed": 2, "drift": 1, "unsure": 0}},
			"review: round 1 done (missed 2, drift 1, unsure 0)"},
		{EvReviewEnded, map[string]any{"round": float64(2), "status": "done",
			"findings": map[string]any{"missed": float64(0), "drift": float64(0), "unsure": float64(3)}},
			"review: round 2 done (missed 0, drift 0, unsure 3)"},
		{EvReviewEnded, map[string]any{"round": 2, "status": "unfinished", "detail": "budget"},
			"review: round 2 unfinished (budget)"},
	}
	for _, c := range cases {
		if got := ProgressLine(c.typ, c.m); got != c.want {
			t.Errorf("%s: got %q want %q", c.typ, got, c.want)
		}
	}
}

func TestFarmLine_FindingPostShowsText(t *testing.T) {
	got := ProgressLine(EvBoardPosted, map[string]any{"part": "review", "topic": TopicFinding, "text": "R1 [missed] task a/x.go:3: nope"})
	if !strings.Contains(got, "finding") || !strings.Contains(got, "R1 [missed] task a/x.go:3: nope") {
		t.Fatalf("line %q", got)
	}
}

func TestFormatPost_ReviewTopics(t *testing.T) {
	f := formatPost(Post{Seq: 1, Part: "review", Topic: TopicFinding, Fields: map[string]any{
		"id": "R1", "kind": "missed", "plan_ref": "task a", "where": "x.go:3", "text": "no check"}})
	if !strings.Contains(f, "R1 [missed] task a x.go:3: no check") {
		t.Fatalf("finding %q", f)
	}
	d := formatPost(Post{Seq: 2, Part: "review", Topic: TopicReviewDone, Fields: map[string]any{"summary": "all good", "round": 1}})
	if !strings.Contains(d, "review done (round 1): all good") {
		t.Fatalf("done %q", d)
	}
}
