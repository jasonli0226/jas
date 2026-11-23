package farm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
)

func reviewPlan(contracts string) Plan {
	return Plan{Task: "t", Contracts: contracts,
		Interfaces: []Interface{{Name: "Svc", Owner: "p1", Shape: "func()"}},
		Parts:      []Part{{ID: "p1", Role: "coder", Goal: "g"}}}
}

func newReviewTool(t *testing.T, plan Plan, round int, prior []Finding) (ReviewPostTool, *Board) {
	t.Helper()
	b, _ := openTestBoard(t, nil, testBoardCfg)
	return ReviewPostTool{Board: b, Part: "review", SessionID: "s1", Round: newReviewRound(plan, round, prior)}, b
}

func callReview(t *testing.T, tool ReviewPostTool, topic string, fields map[string]any) core.Result {
	t.Helper()
	args, err := json.Marshal(map[string]any{"topic": topic, "fields": fields})
	assert.NoErr(t, err)
	res, err := tool.Call(context.Background(), core.ExecPolicy{}, args)
	assert.NoErr(t, err)
	return res
}

func finding(kind, ref, where string) map[string]any {
	return map[string]any{"kind": kind, "plan_ref": ref, "where": where, "text": "x"}
}

func TestBoard_FindingOnlyFromReviewer(t *testing.T) {
	b, _ := openTestBoard(t, nil, testBoardCfg)
	_, err := b.Post("p1", "coder", TopicFinding, map[string]any{"kind": "missed", "plan_ref": "task", "text": "x"})
	if err == nil || !strings.Contains(err.Error(), "may not post topic") {
		t.Fatalf("err = %v", err)
	}
	_, err = b.Post("p1", reviewerRole, TopicFinding, map[string]any{"kind": "missed", "plan_ref": "task", "text": "x"})
	assert.NoErr(t, err)
}

func TestReviewPost_AssignsIDsInOrder(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	r1 := callReview(t, tool, TopicFinding, finding("drift", "p1", "a.go:1"))
	r2 := callReview(t, tool, TopicFinding, finding("missed", "task", ""))
	assert.Eq(t, r1.Output, "posted finding R1")
	assert.Eq(t, r2.Output, "posted finding R2")
	fs := tool.Round.findings()
	assert.Eq(t, len(fs), 2)
	assert.Eq(t, fs[0].ID, "R1")
	assert.Eq(t, fs[1].ID, "R2")
	assert.Eq(t, b.All()[1].Fields["id"], "R2")
}

func TestReviewPost_UnknownPlanRefRejected(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	res := callReview(t, tool, TopicFinding, finding("drift", "nope", "a.go"))
	assert.Eq(t, res.IsError, true)
	assert.Eq(t, len(b.All()), 0)
	assert.Eq(t, len(tool.Round.findings()), 0)
	for _, ok := range []string{"task", "p1", "interface:Svc"} {
		assert.Eq(t, callReview(t, tool, TopicFinding, finding("drift", ok, "a.go")).IsError, false)
	}
	assert.Eq(t, callReview(t, tool, TopicFinding, finding("drift", "interface:Nope", "a.go")).IsError, true)
}

func TestReviewPost_ContractsRefNeedsContracts(t *testing.T) {
	without, _ := newReviewTool(t, reviewPlan("  "), 1, nil)
	assert.Eq(t, callReview(t, without, TopicFinding, finding("drift", "contracts", "a.go")).IsError, true)
	with, _ := newReviewTool(t, reviewPlan("must be fast"), 1, nil)
	assert.Eq(t, callReview(t, with, TopicFinding, finding("drift", "contracts", "a.go")).IsError, false)
}

func TestReviewPost_WhereRequiredUnlessMissed(t *testing.T) {
	tool, _ := newReviewTool(t, reviewPlan(""), 1, nil)
	assert.Eq(t, callReview(t, tool, TopicFinding, finding("drift", "task", " ")).IsError, true)
	assert.Eq(t, callReview(t, tool, TopicFinding, finding("unsure", "task", "")).IsError, true)
	assert.Eq(t, callReview(t, tool, TopicFinding, finding("missed", "task", "")).IsError, false)
}

func TestReviewPost_BadKindRejected(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	assert.Eq(t, callReview(t, tool, TopicFinding, finding("bug", "task", "a.go")).IsError, true)
	assert.Eq(t, len(b.All()), 0)
}

func TestReviewPost_UnknownIDRejected(t *testing.T) {
	prior := []Finding{{ID: "R1", Kind: FindingDrift}}
	tool, _ := newReviewTool(t, reviewPlan(""), 2, prior)
	f := finding("drift", "task", "a.go")
	f["id"] = "R9"
	assert.Eq(t, callReview(t, tool, TopicFinding, f).IsError, true)
	f["id"] = "R1"
	res := callReview(t, tool, TopicFinding, f)
	assert.Eq(t, res.IsError, false)
	assert.Eq(t, tool.Round.findings()[0].ID, "R1")
}

func TestReviewPost_Round1IDRejected(t *testing.T) {
	tool, _ := newReviewTool(t, reviewPlan(""), 1, nil)
	f := finding("drift", "task", "a.go")
	f["id"] = "R1"
	assert.Eq(t, callReview(t, tool, TopicFinding, f).IsError, true)
}

func TestReviewPost_Round2NewDriftGetsNextID(t *testing.T) {
	prior := []Finding{{ID: "R1"}, {ID: "R2"}}
	tool, _ := newReviewTool(t, reviewPlan(""), 2, prior)
	res := callReview(t, tool, TopicFinding, finding("drift", "task", "a.go"))
	assert.Eq(t, res.Output, "posted finding R3")
}

func TestReviewPost_WrongRoundRejected(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	assert.Eq(t, callReview(t, tool, TopicReviewDone, map[string]any{"summary": "ok", "round": "2"}).IsError, true)
	assert.Eq(t, tool.Round.finished(), false)
	assert.Eq(t, len(b.All()), 0)
}

func TestReviewPost_DoneFinishesRound(t *testing.T) {
	tool, _ := newReviewTool(t, reviewPlan(""), 1, nil)
	res := callReview(t, tool, TopicReviewDone, map[string]any{"summary": "ok", "round": "1"})
	assert.Eq(t, res.Output, "posted review_done")
	assert.Eq(t, tool.Round.finished(), true)
}

func TestReviewPost_OtherTopicRejected(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	res := callReview(t, tool, TopicNote, map[string]any{"text": "hi"})
	assert.Eq(t, res.IsError, true)
	assert.Eq(t, len(b.All()), 0)
	assert.Eq(t, len(tool.Round.findings()), 0)
}

func TestReviewPost_HookMayReadRound(t *testing.T) {
	tool, b := newReviewTool(t, reviewPlan(""), 1, nil)
	b.SetOnPost(func(Post) { tool.Round.findings(); tool.Round.finished() })
	done := make(chan core.Result, 1)
	go func() { done <- callReview(t, tool, TopicFinding, finding("drift", "task", "a.go")) }()
	select {
	case res := <-done:
		assert.Eq(t, res.IsError, false)
	case <-time.After(5 * time.Second):
		t.Fatal("deadlock: hook reading the round blocked the post")
	}
}
