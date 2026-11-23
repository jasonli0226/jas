package farm

import (
	"context"
	"slices"
	"strings"
)

// reviewPart is the reviewer's agent id and Part.ID in both rounds; its logs
// are <farm dir>/review/round<N>.jsonl.
const reviewPart = ReviewPart

// ReviewPart names the reviewer in progress lines, attach and its log dir.
const ReviewPart = "review"

// review_ended statuses of one round.
const (
	roundDone       = "done"
	roundUnfinished = "unfinished"
)

// reviewResult is the review's outcome for run.go. FixErr is the fix round's
// *IntegrationError (a BudgetStop one also when a reviewer round hit the
// farm budget), or another, internal error.
type reviewResult struct {
	Status string
	Open   []string
	FixErr error
	Detail string // unfinished: why the round ended (redacted)
	// CheckFailed: the check failed on the tree after the fix round, so there
	// was no round 2 (Manifest.Check holds the run).
	CheckFailed bool
}

// roundEnd is how one reviewer round ended.
type roundEnd struct {
	head     string // integrate HEAD the round reviewed
	findings []Finding
	done     bool   // the turn ended ok after a valid review_done
	detail   string // why the round is unfinished
	err      error  // farm budget stop (*IntegrationError) or internal error
}

// Review runs the reviewer on the integrate worktree at path: round 1 over
// base..HEAD; if it reports missed/drift findings, one integrator fix round,
// the verdict on the fixed tree, and round 2 over the fix diff. It keeps
// Manifest.Review and the review events up to date and returns met, unmet or
// unfinished (never skipped: the caller decides that before calling).
func (s *Supervisor) Review(ctx context.Context, path string) reviewResult {
	s.setReview(func(*ReviewState) {})
	r1 := s.reviewRound(ctx, path, 1, nil, s.baseSHA, s.round1Message)
	if !r1.done {
		return s.reviewUnfinished(nil, r1)
	}
	open := gatingIDs(r1.findings)
	if len(open) == 0 {
		return s.reviewEnd(ReviewMet, nil, nil)
	}
	answer, err := s.runIntegratorFinal(ctx, path, s.fixMessage(r1.findings))
	if err != nil {
		return s.reviewEnd(ReviewUnmet, open, err)
	}
	if chk := Verdict(ctx, s, path); chk.ExitCode != 0 || chk.TimedOut {
		res := s.reviewEnd(ReviewUnmet, open, nil)
		res.CheckFailed = true
		return res
	}
	r2 := s.reviewRound(ctx, path, 2, r1.findings, r1.head, func(diff string) string {
		return s.round2Message(r1.findings, answer, diff)
	})
	if !r2.done {
		return s.reviewUnfinished(open, r2)
	}
	if open2 := gatingIDs(r2.findings); len(open2) > 0 {
		return s.reviewEnd(ReviewUnmet, open2, nil)
	}
	return s.reviewEnd(ReviewMet, nil, nil)
}

// reviewRound runs one reviewer round over the diff from..HEAD, whose text
// message turns into the first message, and logs review_started and
// review_ended.
func (s *Supervisor) reviewRound(ctx context.Context, path string, round int, prior []Finding, from string, message func(diff string) string) roundEnd {
	head, err := s.Git.Run(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return roundEnd{err: err, detail: err.Error()}
	}
	head = strings.TrimSpace(head)
	s.event(EvReviewStarted, map[string]any{"round": round, "base": from, "head": head})
	end := s.runReviewer(ctx, path, round, prior, message(reviewDiff(ctx, s.Git, path, from, head, s.Redact)))
	end.head = head
	status, open := roundUnfinished, []string{}
	switch {
	case end.done:
		status, open = roundDone, gatingIDs(end.findings)
	case round > 1: // round 1 finished, so its findings stay open
		open = gatingIDs(prior)
	}
	ev := map[string]any{"round": round, "status": status, "findings": countKinds(end.findings), "open": open}
	if !end.done {
		ev["detail"] = end.detail
	}
	s.event(EvReviewEnded, ev)
	return end
}

// reviewEnd records the review's final status and open ids.
func (s *Supervisor) reviewEnd(status string, open []string, err error) reviewResult {
	if open == nil {
		open = []string{}
	}
	s.setReview(func(st *ReviewState) { st.Status, st.Open = status, slices.Clone(open) })
	return reviewResult{Status: status, Open: open, FixErr: err}
}

// reviewUnfinished ends the review as unfinished, recording why the round
// ended (redacted) in the result and the manifest.
func (s *Supervisor) reviewUnfinished(open []string, end roundEnd) reviewResult {
	res := s.reviewEnd(ReviewUnfinished, open, end.err)
	res.Detail = s.redact(end.detail)
	s.setReview(func(st *ReviewState) { st.Detail = res.Detail })
	return res
}

// gatingIDs is the ids of the missed and drift findings, in order; never nil.
func gatingIDs(fs []Finding) []string {
	out := []string{}
	for _, f := range fs {
		if gates(f) && !slices.Contains(out, f.ID) {
			out = append(out, f.ID)
		}
	}
	return out
}

func gates(f Finding) bool { return f.Kind == FindingMissed || f.Kind == FindingDrift }

func countKinds(fs []Finding) map[string]int {
	out := map[string]int{FindingMissed: 0, FindingDrift: 0, FindingUnsure: 0}
	seen := map[string]bool{}
	for _, f := range fs {
		if seen[f.ID] { // a re-reported id counts once
			continue
		}
		seen[f.ID] = true
		out[f.Kind]++
	}
	return out
}

// setReview changes the manifest's review entry (made if absent) and saves it.
func (s *Supervisor) setReview(fn func(*ReviewState)) {
	s.Manifest.Update(func(m *Manifest) {
		if m.Review == nil {
			m.Review = &ReviewState{Rounds: []ReviewRoundState{}, Open: []string{}}
		}
		fn(m.Review)
		m.TotalTokens = s.Pools.Farm.Used()
	})
	s.save()
}

// setReviewRound changes the manifest entry of the given round.
func (s *Supervisor) setReviewRound(round int, fn func(*ReviewRoundState)) {
	s.setReview(func(st *ReviewState) {
		for i := len(st.Rounds) - 1; i >= 0; i-- {
			if st.Rounds[i].Round == round {
				fn(&st.Rounds[i])
				return
			}
		}
	})
}
