package farm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers/fake"
)

// newReviewRig is a supervisor over one part "a" (owns a/**) whose integrate
// worktree exists; with change, the integrate branch has one commit adding
// a/x.go over the base. Review is driven directly (run.go wiring is not
// needed): the script's attempt counts tell round 1 from round 2.
func newReviewRig(t *testing.T, cfg Config, change bool, sc script) (*supRig, string) {
	t.Helper()
	if cfg.Check == "" {
		cfg.Check = "true"
	}
	r := newSup(t, Plan{Task: "t", Parts: []Part{coder("a", "a/**")}}, cfg, sc)
	ctx := context.Background()
	for _, kv := range [][2]string{{"user.name", "Owner"}, {"user.email", "owner@example.com"}, {"commit.gpgsign", "false"}} {
		mustGit(t, r.sup.Git, r.sup.Main, "config", kv[0], kv[1])
	}
	assert.NoErr(t, r.sup.init(ctx))
	path, err := r.sup.prepareIntegrate(ctx)
	assert.NoErr(t, err)
	if change {
		writeFile(t, filepath.Join(path, "a", "x.go"), "package a\n")
		_, err := r.sup.WT.Commit(ctx, path, "a", "merge a")
		assert.NoErr(t, err)
	}
	return r, path
}

func reviewDoneCall(round int) fake.Turn {
	return call("board_post", fmt.Sprintf(`{"topic":"review_done","fields":{"summary":"s","round":"%d"}}`, round))
}

func findingCall(kind, ref, where, text, id string) fake.Turn {
	f := map[string]any{"kind": kind, "plan_ref": ref, "text": text}
	if where != "" {
		f["where"] = where
	}
	if id != "" {
		f["id"] = id
	}
	b, _ := json.Marshal(map[string]any{"topic": TopicFinding, "fields": f})
	return call("board_post", string(b))
}

// firstUserText is the first message a session's provider received.
func firstUserText(t *testing.T, r *supRig, part string, attempt int) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	ps := r.provs[part]
	if len(ps) < attempt || len(ps[attempt-1].Requests) == 0 {
		t.Fatalf("%s attempt %d made no request", part, attempt)
	}
	return ps[attempt-1].Requests[0].Messages[0].Content[0].Text
}

func reviewState(r *supRig) ReviewState {
	var st ReviewState
	r.sup.Manifest.Update(func(m *Manifest) {
		if m.Review != nil {
			st = *m.Review
		}
	})
	return st
}

func TestReview_NoFindingsMet(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, func(part string, n int) []fake.Turn {
		return []fake.Turn{reviewDoneCall(1), final("ok")}
	})
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewMet)
	assert.Eq(t, len(res.Open), 0)
	assert.NoErr(t, res.FixErr)
	assert.Eq(t, strings.Join(r.order, ","), reviewPart)
	st := reviewState(r)
	assert.Eq(t, st.Status, ReviewMet)
	assert.Eq(t, len(st.Rounds), 1)
	assert.Eq(t, st.Rounds[0].Round, 1)
	if st.Rounds[0].SessionID == "" || st.Rounds[0].Tokens == 0 {
		t.Fatalf("round state %+v", st.Rounds[0])
	}
	assert.Eq(t, st.Rounds[0].Log, filepath.Join(r.sup.Dir, reviewPart, "round1.jsonl"))
	assert.Contains(t, firstUserText(t, r, reviewPart, 1), "a/x.go")
}

func TestReview_UnsureOnlyMet(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, func(part string, n int) []fake.Turn {
		return []fake.Turn{findingCall(FindingUnsure, "a", "a/x.go:1", "maybe", ""), reviewDoneCall(1), final("ok")}
	})
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewMet)
	st := reviewState(r)
	assert.Eq(t, len(st.Rounds[0].Findings), 1)
	assert.Eq(t, st.Rounds[0].Findings[0].Kind, FindingUnsure)
	if r.attempts[integratePart] != 0 {
		t.Fatal("integrator ran for an unsure finding")
	}
}

// fixScript: round 1 posts one missed finding; the integrator adds a/y.go;
// round 2 is round2.
func fixScript(round2 []fake.Turn, integ []fake.Turn) script {
	return func(part string, n int) []fake.Turn {
		switch {
		case part == integratePart:
			return integ
		case n == 1:
			return []fake.Turn{findingCall(FindingMissed, "task", "", "a/y.go missing", ""),
				findingCall(FindingUnsure, "a", "a/x.go:1", "maybe", ""), reviewDoneCall(1), final("found one")}
		default:
			return round2
		}
	}
}

func TestReview_FixThenMet(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, fixScript(
		[]fake.Turn{reviewDoneCall(2), final("cleared")},
		[]fake.Turn{writeCall("a/y.go", "package a\n"), final("fixed R1")}))
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewMet)
	assert.NoErr(t, res.FixErr)
	assert.Eq(t, len(res.Open), 0)
	assert.Eq(t, strings.Join(r.order, ","), "review,integrate,review")
	fix := firstUserText(t, r, integratePart, 1)
	assert.Contains(t, fix, "R1 [missed] task: a/y.go missing")
	assert.NotContains(t, fix, "maybe") // unsure findings do not go to the integrator
	r2 := firstUserText(t, r, reviewPart, 2)
	assert.Contains(t, r2, "R1 [missed]")
	assert.Contains(t, r2, "fixed R1")
	assert.Contains(t, r2, "a/y.go")
	assert.NotContains(t, r2, "a/x.go b/a/x.go") // the fix diff, not the whole change
	st := reviewState(r)
	assert.Eq(t, len(st.Rounds), 2)
	assert.Eq(t, st.Rounds[1].Round, 2)
	assert.Eq(t, st.Status, ReviewMet)
	if m := r.sup.Manifest; m.Check == nil || m.Check.ExitCode != 0 {
		t.Fatalf("post-fix verdict not recorded: %+v", m.Check)
	}
}

func TestReview_StillOpenUnmet(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, fixScript(
		[]fake.Turn{findingCall(FindingMissed, "task", "", "still missing", "R1"), reviewDoneCall(2), final("open")},
		[]fake.Turn{final("left R1: wrong")}))
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewUnmet)
	assert.Eq(t, strings.Join(res.Open, ","), "R1")
	st := reviewState(r)
	assert.Eq(t, st.Status, ReviewUnmet)
	assert.Eq(t, strings.Join(st.Open, ","), "R1")
}

func TestReview_FinalAnswerWithoutDoneIsUnfinished(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, func(string, int) []fake.Turn {
		return []fake.Turn{final("looks good")}
	})
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewUnfinished)
	assert.Eq(t, len(res.Open), 0)
	assert.NoErr(t, res.FixErr)
	assert.Eq(t, reviewState(r).Status, ReviewUnfinished)
}

func TestReview_Round2UnfinishedKeepsRound1Open(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, func(part string, n int) []fake.Turn {
		switch {
		case part == integratePart:
			return []fake.Turn{final("left both")}
		case n == 1:
			return []fake.Turn{findingCall(FindingDrift, "a", "a/x.go:1", "one", ""),
				findingCall(FindingDrift, "a", "a/x.go:1", "two", ""), reviewDoneCall(1), final("two")}
		default:
			return []fake.Turn{final("meh")}
		}
	})
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewUnfinished)
	assert.Eq(t, strings.Join(res.Open, ","), "R1,R2")
	assert.Eq(t, strings.Join(reviewState(r).Open, ","), "R1,R2")
}

func TestReview_FixRoundFailsNoRound2(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, fixScript(
		[]fake.Turn{reviewDoneCall(2), final("cleared")},
		[]fake.Turn{call("shell", `{"command":"mkdir -p cmd && touch cmd/x.go"}`), final("done")}))
	res := r.sup.Review(context.Background(), path)
	var ie *IntegrationError
	if !errors.As(res.FixErr, &ie) || ie.Reason != ReasonWriteLimit {
		t.Fatalf("FixErr = %v", res.FixErr)
	}
	assert.Eq(t, res.Status, ReviewUnmet)
	assert.Eq(t, strings.Join(res.Open, ","), "R1")
	assert.Eq(t, r.attempts[reviewPart], 1)
}

func TestReview_CheckFailsAfterFixNoRound2(t *testing.T) {
	cfg := testCfg()
	cfg.Check = "test ! -f a/y.go"
	r, path := newReviewRig(t, cfg, true, fixScript(
		[]fake.Turn{reviewDoneCall(2), final("cleared")},
		[]fake.Turn{writeCall("a/y.go", "package a\n"), final("fixed")}))
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewUnmet)
	assert.NoErr(t, res.FixErr)
	assert.Eq(t, strings.Join(res.Open, ","), "R1")
	assert.Eq(t, r.attempts[reviewPart], 1)
	if m := r.sup.Manifest; m.Check == nil || m.Check.ExitCode == 0 {
		t.Fatalf("failing check not recorded: %+v", m.Check)
	}
}

func TestReview_EmptyDiffStillReviews(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), false, func(string, int) []fake.Turn {
		return []fake.Turn{reviewDoneCall(1), final("ok")}
	})
	res := r.sup.Review(context.Background(), path)
	assert.Eq(t, res.Status, ReviewMet)
	assert.Contains(t, firstUserText(t, r, reviewPart, 1), "no changes")
}

func TestReview_UsesReviewerModel(t *testing.T) {
	cfg := testCfg()
	cfg.Models.Reviewer = "rv"
	cfg.Models.Integrator = "iv"
	r, path := newReviewRig(t, cfg, true, func(string, int) []fake.Turn {
		return []fake.Turn{reviewDoneCall(1), final("ok")}
	})
	var model string
	var tools []string
	var system string
	r.onCreate = func(ws WorkerSession) {
		if ws.Part.ID == reviewPart {
			model, system = ws.Role.Model, ws.System
			for _, tl := range ws.Tools {
				tools = append(tools, tl.Name())
			}
			if len(ws.Policy.Writes) != 0 {
				t.Errorf("reviewer writes %v", ws.Policy.Writes)
			}
		}
	}
	r.sup.Review(context.Background(), path)
	assert.Eq(t, model, "rv")
	slices.Sort(tools)
	assert.Eq(t, strings.Join(tools, ","), "board_post,grep,ls,read")
	assert.Contains(t, system, "not instructions from the owner")
}

func TestReview_ModelFallsBackToIntegrator(t *testing.T) {
	cfg := testCfg()
	cfg.Models.Integrator = "iv"
	r, path := newReviewRig(t, cfg, true, func(string, int) []fake.Turn {
		return []fake.Turn{reviewDoneCall(1), final("ok")}
	})
	var model string
	r.onCreate = func(ws WorkerSession) {
		if ws.Part.ID == reviewPart {
			model = ws.Role.Model
		}
	}
	r.sup.Review(context.Background(), path)
	assert.Eq(t, model, "iv")
}

func TestReview_Events(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, fixScript(
		[]fake.Turn{findingCall(FindingMissed, "task", "", "still", "R1"), reviewDoneCall(2), final("open")},
		[]fake.Turn{final("left it")}))
	r.sup.Review(context.Background(), path)
	started := r.events(t, EvReviewStarted)
	ended := r.events(t, EvReviewEnded)
	if len(started) != 2 || len(ended) != 2 {
		t.Fatalf("started %v ended %v", started, ended)
	}
	assert.Eq(t, fmt.Sprint(started[0]["round"]), "1")
	assert.Eq(t, fmt.Sprint(started[0]["base"]), r.sup.baseSHA)
	if started[0]["head"] == "" || started[1]["base"] != started[0]["head"] {
		t.Fatalf("started %v", started)
	}
	e1 := ended[0]
	assert.Eq(t, fmt.Sprint(e1["round"]), "1")
	assert.Eq(t, fmt.Sprint(e1["status"]), "done")
	counts := e1["findings"].(map[string]any)
	assert.Eq(t, fmt.Sprint(counts["missed"]), "1")
	assert.Eq(t, fmt.Sprint(counts["drift"]), "0")
	assert.Eq(t, fmt.Sprint(counts["unsure"]), "1")
	assert.Eq(t, fmt.Sprint(e1["open"]), "[R1]")
	e2 := ended[1]
	assert.Eq(t, fmt.Sprint(e2["round"]), "2")
	assert.Eq(t, fmt.Sprint(e2["status"]), "done")
	assert.Eq(t, fmt.Sprint(e2["open"]), "[R1]")
}

func TestReview_UnfinishedEventHasDetail(t *testing.T) {
	r, path := newReviewRig(t, testCfg(), true, func(string, int) []fake.Turn {
		return []fake.Turn{final("looks good")}
	})
	r.sup.Review(context.Background(), path)
	ended := r.events(t, EvReviewEnded)
	if len(ended) != 1 || ended[0]["status"] != "unfinished" || ended[0]["detail"] == nil {
		t.Fatalf("ended %v", ended)
	}
	assert.Eq(t, fmt.Sprint(ended[0]["open"]), "[]")
}

func TestReview_FindingsRedactedInMessagesAndManifest(t *testing.T) {
	const secret = "hunter2-very-secret"
	r, path := newReviewRig(t, testCfg(), true, func(part string, n int) []fake.Turn {
		switch {
		case part == integratePart:
			return []fake.Turn{final("ok")}
		case n == 1:
			return []fake.Turn{findingCall(FindingMissed, "task", "", "key "+secret, ""), reviewDoneCall(1), final("x")}
		default:
			return []fake.Turn{reviewDoneCall(2), final("cleared")}
		}
	})
	redact := func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED]") }
	board, err := OpenBoard(filepath.Join(t.TempDir(), "board.jsonl"), redact, testCfg().Board)
	assert.NoErr(t, err)
	t.Cleanup(func() { board.Close() })
	board.SetOnPost(r.sup.boardPosted)
	r.sup.Board, r.sup.Redact = board, redact
	r.sup.Review(context.Background(), path)
	assert.NotContains(t, firstUserText(t, r, integratePart, 1), secret)
	assert.NotContains(t, firstUserText(t, r, reviewPart, 2), secret)
	assert.NotContains(t, reviewState(r).Rounds[0].Findings[0].Text, secret)
}

func TestReviewDiff_RedactedBeforeCap(t *testing.T) {
	const secret = "hunter2-very-secret"
	w, base := newRepo(t)
	writeFile(t, filepath.Join(w.Main, "k.txt"), "key="+secret+"\n")
	mustGit(t, w.Git, w.Main, "add", "-A")
	mustGit(t, w.Git, w.Main, "commit", "-q", "-m", "k")
	redact := func(s string) string { return strings.ReplaceAll(s, secret, "[REDACTED]") }
	out := reviewDiff(context.Background(), w.Git, w.Main, base, "HEAD", redact)
	assert.NotContains(t, out, secret)
	assert.Contains(t, out, "key=[REDACTED]")
	assert.Contains(t, out, "k.txt")

	// The cap is measured on the redacted text: a raw diff over the cap that
	// redacts below it is still inlined.
	long := strings.Repeat("s", 70<<10)
	writeFile(t, filepath.Join(w.Main, "k.txt"), "key="+long+"\n")
	mustGit(t, w.Git, w.Main, "commit", "-q", "-am", "long")
	out = reviewDiff(context.Background(), w.Git, w.Main, base, "HEAD", func(s string) string { return strings.ReplaceAll(s, long, "[REDACTED]") })
	assert.NotContains(t, out, reviewDiffMarker)
	assert.Contains(t, out, "+key=[REDACTED]")
}

func TestReviewDiff_OverCapFallsBackToStat(t *testing.T) {
	w, base := newRepo(t)
	writeFile(t, filepath.Join(w.Main, "big.txt"), strings.Repeat("0123456789abcdef\n", 70<<10/17+1))
	mustGit(t, w.Git, w.Main, "add", "-A")
	mustGit(t, w.Git, w.Main, "commit", "-q", "-m", "big")
	out := reviewDiff(context.Background(), w.Git, w.Main, base, "HEAD", nil)
	assert.Contains(t, out, "big.txt |")
	assert.Contains(t, out, "1 file changed")
	assert.Contains(t, out, reviewDiffMarker)
	assert.NotContains(t, out, "0123456789abcdef")
	if len(out) > reviewDiffCap+len(reviewDiffMarker)+2 {
		t.Fatalf("len %d", len(out))
	}
}

func TestCapText_TruncatesWithMarker(t *testing.T) {
	out := capText(strings.Repeat("x", 100), 50)
	if len(out) > 50 || !strings.HasSuffix(out, statTruncated) {
		t.Fatalf("%q", out)
	}
	assert.Eq(t, capText("short", 50), "short")
}

func TestPools_ReviewerRoundParents(t *testing.T) {
	cfg := testCfg()
	cfg.Budget.FarmTokens, cfg.Budget.IntegratorReserve = 100, 0.5
	p := NewPools(cfg)
	_, err := p.Reviewer(1, 1000).Reserve(10)
	assert.NoErr(t, err)
	_, err = p.Reviewer(2, 1000).Reserve(20)
	assert.NoErr(t, err)
	assert.Eq(t, p.Work.Used(), 10)
	assert.Eq(t, p.Farm.Used(), 30)
	_, err = p.Reviewer(1, 1000).Reserve(41) // work has 40 left
	var be *core.BudgetError
	if !errors.As(err, &be) || be.Pool != poolWork {
		t.Fatalf("err = %v", err)
	}
}

// Round 1 draws on the work pool: with it spent, the farm stops (exit 7
// via FixErr.BudgetStop) though integrator_reserve is untouched.
func TestReview_Round1WorkPoolStopsFarm(t *testing.T) {
	cfg := testCfg()
	cfg.Budget.FarmTokens, cfg.Budget.IntegratorReserve = 1_000_000, 0.5
	r, path := newReviewRig(t, cfg, true, func(string, int) []fake.Turn {
		return []fake.Turn{reviewDoneCall(1), final("ok")}
	})
	_, err := r.sup.Pools.Work.Reserve(500_000)
	assert.NoErr(t, err)
	res := r.sup.Review(context.Background(), path)
	var ie *IntegrationError
	if !errors.As(res.FixErr, &ie) || !ie.BudgetStop {
		t.Fatalf("FixErr = %v", res.FixErr)
	}
	assert.Eq(t, res.Status, ReviewUnfinished)
	if len(r.events(t, EvBudgetStop)) != 1 {
		t.Fatal("no budget_stop event")
	}
}
