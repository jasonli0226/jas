---
name: pr-review-loop
description: Use in the jas repo after opening or updating a pull request and before merging it, or when `gh pr merge` is blocked with "merge blocked".
---

# PR review loop

Every jas PR gets a jas review of its head commit before it merges. A
PreToolUse hook (`scripts/check-pr-reviewed.sh`) blocks `gh pr merge` until
a PASS review for the current head is posted.

## Loop

1. Push the branch and open the PR.
2. Run `bash scripts/review-pr.sh <pr>` (Bash timeout 960000; a review takes
   2–10 minutes). It posts the review as a PR comment.
   - Exit 0, `VERDICT: PASS`: go to step 5.
   - Exit 1, `VERDICT: CHANGES`: go to step 3.
   - Exit 3, the review did not finish: rerun once; if it fails again, tell
     the user and stop.
3. Triage each finding with superpowers:receiving-code-review: read the
   cited code, then either fix it (high and medium must be fixed or rebutted;
   fix low ones when cheap) or reply on the PR with the reason it is wrong.
4. Run the tests, push, and go back to step 2. After the second CHANGES
   verdict, stop and ask the user instead of reviewing a third time.
5. Squash-merge: `gh pr merge <pr> --squash --delete-branch`.

## Rules

- A push after the review changes the head commit; the old PASS no longer
  counts, so review again.
- Never post a review comment by hand or edit the marker
  `<!-- jas-review sha=… verdict=… -->`.
- Report the verdict and what you changed or rebutted to the user.
