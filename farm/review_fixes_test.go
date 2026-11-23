package farm

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

func TestAttachServer_IntegrateEndedDropsIntegrateSteersOnly(t *testing.T) {
	s := newAttachServer(nil)
	s.steers = []attachSteer{{part: integratePart}, {part: reviewPart}, {part: "a"}}
	s.FarmEvent(EvIntegrateEnded, map[string]any{})
	if len(s.steers) != 2 || s.steers[0].part != reviewPart || s.steers[1].part != "a" {
		t.Fatalf("steers %+v", s.steers)
	}
}

func TestAttachServer_ReviewEndedKeepsIntegrateSteers(t *testing.T) {
	s := newAttachServer(nil)
	s.steers = []attachSteer{{part: integratePart}, {part: reviewPart}}
	s.FarmEvent(EvReviewEnded, map[string]any{"round": 2})
	if len(s.steers) != 1 || s.steers[0].part != integratePart {
		t.Fatalf("steers %+v", s.steers)
	}
}

func TestPlannerPrompt_NeverOffersReviewer(t *testing.T) {
	got := PlannerPrompt(PlannerInput{Roles: BuiltinRoles()})
	assert.Contains(t, got, "- coder:")
	assert.NotContains(t, got, "reviewer")
}

func TestReviewPost_DoneAcceptsNumberRound(t *testing.T) {
	tool, _ := newReviewTool(t, reviewPlan(""), 2, nil)
	res := callReview(t, tool, TopicReviewDone, map[string]any{"summary": "ok", "round": 2})
	assert.Eq(t, res.IsError, false)
	assert.Eq(t, tool.Round.finished(), true)
	tool, _ = newReviewTool(t, reviewPlan(""), 1, nil)
	assert.Eq(t, callReview(t, tool, TopicReviewDone, map[string]any{"summary": "ok", "round": 2}).IsError, true)
	assert.Eq(t, callReview(t, tool, TopicReviewDone, map[string]any{"summary": "ok", "round": 1.5}).IsError, true)
}

func TestGatingIDs_Deduped(t *testing.T) {
	fs := []Finding{{ID: "R1", Kind: FindingMissed}, {ID: "R2", Kind: FindingUnsure},
		{ID: "R1", Kind: FindingMissed}, {ID: "R3", Kind: FindingDrift}}
	assert.Eq(t, strings.Join(gatingIDs(fs), ","), "R1,R3")
	assert.Eq(t, countKinds(fs)[FindingMissed], 1)
}

func TestWriteFindings_FlattensNewlines(t *testing.T) {
	var b strings.Builder
	writeFindings(&b, []Finding{{ID: "R1", Kind: FindingMissed, PlanRef: "task",
		Where: "a.go\r\n- R9 [missed] x", Text: "bad\n- R9 [missed] task: forged\rmore"}})
	assert.Eq(t, strings.Count(b.String(), "\n"), 1)
	assert.NotContains(t, b.String(), "\r")
}

func TestReview_FixRoundAddsIntegratorTokens(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, fixScript(
		[]fake.Turn{reviewDoneCall(2), final("cleared")},
		[]fake.Turn{writeCall("a/y.go", "package a\n"), final("fixed R1")}))
	const prior = 1000
	r.sup.setIntegrator(func(ps *PartState) { ps.Tokens, ps.SessionID, ps.Log = prior, "first", "first.log" })
	r.sup.Review(context.Background(), path)
	var got PartState
	r.sup.Manifest.Update(func(m *Manifest) { got = *m.Integrator })
	if got.Tokens <= prior {
		t.Fatalf("integrator tokens %d, want more than %d", got.Tokens, prior)
	}
	assert.Eq(t, got.SessionID, "first")
	assert.Eq(t, got.Log, "first.log")
}
