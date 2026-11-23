package farm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

// reviewRunScript writes both parts; reviewer rounds are r1 then r2 (the
// reviewer session is built once per round), the integrator runs integ.
func reviewRunScript(r1, r2, integ []fake.Turn) sessionScript {
	var mu sync.Mutex
	rounds := 0
	return func(ws WorkerSession) []fake.Turn {
		switch ws.Part.ID {
		case reviewPart:
			mu.Lock()
			defer mu.Unlock()
			rounds++
			if rounds == 1 {
				return r1
			}
			return r2
		case integratePart:
			if integ == nil {
				return []fake.Turn{final("ok")}
			}
			return integ
		}
		return twoWriters(ws)
	}
}

func metRound() []fake.Turn { return []fake.Turn{reviewDoneCall(1), final("ok")} }

func unmetRound() []fake.Turn {
	return []fake.Turn{findingCall(FindingMissed, "task", "", "gap", ""), reviewDoneCall(1), final("found")}
}

func reviewRig(t *testing.T, check string, parts []Part, sc sessionScript) *runRig {
	r := newRunRig(t, check, parts, sc)
	r.opts.Cfg.Review, r.opts.Cfg.ReviewFrom = true, reviewFromConfig
	return r
}

func (r *runRig) reviewSessions() int {
	n := 0
	for _, p := range r.sessions {
		if p == reviewPart {
			n++
		}
	}
	return n
}

func TestRunFarm_ReviewOffUnchanged(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	code, reason := r.run(context.Background())
	assert.Eq(t, code, 0)
	assert.Eq(t, reason, "ok")
	assert.Eq(t, len(r.events(EvReviewStarted)), 0)
	raw, err := os.ReadFile(filepath.Join(r.farmDir(), manifestFile))
	assert.NoErr(t, err)
	if strings.Contains(string(raw), `"review"`) {
		t.Fatalf("manifest has a review key: %s", raw)
	}
	end := r.last(EvFarmEnded)
	for _, k := range []string{"review_status", "review_open", "review_detail"} {
		if _, ok := end[k]; ok {
			t.Fatalf("farm_ended has %s: %v", k, end)
		}
	}
}

func TestRunFarm_ReviewMetExit0(t *testing.T) {
	r := reviewRig(t, "true", twoParts(), reviewRunScript(metRound(), nil, nil))
	code, reason := r.run(context.Background())
	assert.Eq(t, code, 0)
	assert.Eq(t, reason, "ok")
	end := r.last(EvFarmEnded)
	assert.Eq(t, end["review_status"], any(ReviewMet))
	if open, ok := end["review_open"].([]any); !ok || len(open) != 0 {
		t.Fatalf("review_open %v", end["review_open"])
	}
	m := r.manifest()
	if m.Review == nil || m.Review.Status != ReviewMet || m.Review.RequestedBy != reviewFromConfig {
		t.Fatalf("manifest review %+v", m.Review)
	}
}

func TestRunFarm_ReviewUnmetExit6(t *testing.T) {
	r := reviewRig(t, "true", twoParts(), reviewRunScript(unmetRound(),
		[]fake.Turn{findingCall(FindingMissed, "task", "", "gap", "R1"), reviewDoneCall(2), final("still")},
		[]fake.Turn{final("could not fix")}))
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitUnmet)
	assert.Eq(t, reason, ReasonReviewUnmet)
	end := r.last(EvFarmEnded)
	assert.Eq(t, end["reason"], any("review_unmet"))
	assert.Eq(t, end["review_status"], any(ReviewUnmet))
	open, _ := end["review_open"].([]any)
	if len(open) != 1 || open[0] != "R1" {
		t.Fatalf("review_open %v", end["review_open"])
	}
	assert.Eq(t, r.last(EvOwnerDecision)["choice"], any("keep"))
}

func TestRunFarm_ReviewUnfinishedExit6(t *testing.T) {
	r := reviewRig(t, "true", twoParts(), reviewRunScript([]fake.Turn{final("no verdict")}, nil, nil))
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitUnmet)
	assert.Eq(t, reason, ReasonReviewUnmet)
	end := r.last(EvFarmEnded)
	assert.Eq(t, end["review_status"], any(ReviewUnfinished))
	if d, _ := end["review_detail"].(string); d == "" {
		t.Fatalf("no review_detail: %v", end)
	}
}

func TestRunFarm_ReviewSkippedWhenCheckFails(t *testing.T) {
	r := reviewRig(t, "false", twoParts(), reviewRunScript(metRound(), nil, nil))
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitUnmet)
	assert.Eq(t, reason, ReasonCheckFailed)
	assert.Eq(t, r.reviewSessions(), 0)
	assert.Eq(t, r.manifest().Review.Status, ReviewSkipped)
	assert.Eq(t, r.last(EvFarmEnded)["review_status"], any(ReviewSkipped))
}

func TestRunFarm_ReviewSkippedWhenPartDropped(t *testing.T) {
	r := reviewRig(t, "true", twoParts(), func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return []fake.Turn{{Err: errors.New("boom")}}
		}
		return twoWriters(ws)
	})
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitUnmet)
	assert.Eq(t, reason, ReasonPartDropped)
	assert.Eq(t, r.reviewSessions(), 0)
	assert.Eq(t, r.manifest().Review.Status, ReviewSkipped)
}

func TestRunFarm_ReviewFixBreaksCheck(t *testing.T) {
	r := reviewRig(t, "test ! -f a/y.go", twoParts(), reviewRunScript(unmetRound(), metRound(),
		[]fake.Turn{writeCall("a/y.go", "package a\n"), final("fixed")}))
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitUnmet)
	assert.Eq(t, reason, ReasonCheckFailed)
	assert.Eq(t, r.reviewSessions(), 1)
	assert.Eq(t, r.manifest().Review.Status, ReviewUnmet)
	assert.Eq(t, r.last(EvFarmEnded)["review_status"], any(ReviewUnmet))
}

func TestRunFarm_ReviewRound1BudgetExit7(t *testing.T) {
	big := findingCall(FindingUnsure, "task", "a/x.txt:1", "hm", "")
	big.Usage.PromptTokens = 60000 // settles past the pool; the next turn cannot reserve
	r := reviewRig(t, "true", twoParts(), reviewRunScript([]fake.Turn{big, reviewDoneCall(1), final("ok")}, nil, nil))
	r.opts.Cfg.Budget.FarmTokens = 50000
	code, reason := r.run(context.Background())
	assert.Eq(t, code, core.ExitBudget)
	assert.Eq(t, reason, "tokens")
	assert.Eq(t, r.last(EvFarmEnded)["review_status"], any(ReviewUnfinished))
}

func TestRunFarm_ReviewKeepsPlanHash(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), reviewRunScript(metRound(), nil, nil))
	before, err := os.ReadFile(r.planPath)
	assert.NoErr(t, err)
	code, _ := r.run(context.Background())
	assert.Eq(t, code, 0)
	r.opts.Cfg.Review, r.opts.Cfg.ReviewFrom = true, reviewFromConfig
	code, reason := r.run(context.Background())
	assert.Eq(t, code, 0)
	assert.Eq(t, reason, "ok")
	after, err := os.ReadFile(r.planPath)
	assert.NoErr(t, err)
	assert.Eq(t, string(before), string(after))
}
