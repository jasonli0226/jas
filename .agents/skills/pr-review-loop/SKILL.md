---
name: pr-review-loop
description: Use in the jas repo after opening or updating a pull request, to ask the owner whether to run a jas review, merge, or leave it open, and to run the review when they choose it.
---

# PR review loop

This skill is for the agent that opens and merges PRs. If you are the
reviewer (jas's `reviewer` profile, asked to review a PR), ignore it and
use `pr-review`.

A jas review is the owner's call, not a gate. After opening or updating a
PR, ask the owner which of these they want, and wait for the answer:

- run a jas review (the loop below), then merge on PASS;
- merge now, without a review;
- leave the PR open.

Suggest a review when the PR changes code, scripts or agent instructions
(`SKILL.md`, `AGENTS.md`, `CLAUDE.md`, anything under `.agents/`); say it
can be skipped when it changes other docs only. Paths below are relative
to the repo root.

- Any agent can follow this skill once Setup below is done.
- The reviewer profile (its `AGENTS.md`, `config.json` and the `pr-review`
  skill) lives in `.agents/skills/pr-review-loop/reviewer/`. `review-pr.sh`
  installs it into `~/.jas/profiles/reviewer/` from the base branch, with
  the base branch's installer, before every review, so a change to it
  applies once it merges. Edit it there, not
  in the jas home: `AGENTS.md`, `skills/` and `config.json` are replaced
  each time, except the `model` in an existing `config.json`.

## Setup, once per machine

1. Install `jas` (`make install`, or a release), `gh`, `jq`, `git`, `tar`
   and `make`, and log in with `gh auth login`.
2. Give jas a key for the reviewer's model, `openrouter/z-ai/glm-5.3`
   (`reviewer/config.json`): `jas auth add openrouter`. To review with
   another model, run one review, which writes
   `~/.jas/profiles/reviewer/config.json`, then change `model` there; later
   reviews keep it.
3. Claude Code only: `make claude-setup` once per clone.

No profile needs creating: the first review installs it.

## Loop

1. Push the branch and open the PR, then ask the owner as above. Merge
   now: go to step 5. Leave it open: stop.
2. Run `bash .agents/skills/pr-review-loop/scripts/review-pr.sh <pr>` with a
   command timeout of at least 60 minutes (tests take a few, capped at 20;
   the review 5–30; a format rewrite up to 5). It first runs the base branch's `make test` recipe on
   the PR head, then the reviewer, and posts the result as a PR comment.
   - Exit 0, `VERDICT: PASS`: go to step 5. Its findings are all low; the
     script logs them in the "Low findings from jas reviews" issue.
   - Exit 1, `VERDICT: CHANGES`: go to step 3. When `make test` failed,
     the comment lists the failing tests and the reviewer did not run;
     the full output is on the script's stderr.
   - A review that finished but ignored the format (no `## Findings` or
     `VERDICT:` line, common when it answers at the turn cap) gets one
     tools-off turn to rewrite itself; only then does the script give up.
   - Exit 3, the review did not finish: rerun once; if it fails again, tell
     the user and stop. When the message says the format rewrite failed,
     the review itself finished: do not rerun, tell the user.
3. Check each high and medium finding against the code it cites before
   acting on it (in Claude Code, with superpowers:receiving-code-review).
   Then fix it, or reply on the PR with why it is wrong. Fix a low finding
   only while you are pushing a high or medium fix anyway.
4. Run the tests, push, and go back to step 2. After the second CHANGES
   verdict, stop and ask the user instead of reviewing a third time.
5. Squash-merge after a PASS for the current head, or when the owner said
   to merge without a review:
   `gh pr merge <pr> --squash --delete-branch`.

## Rules

- A push after the review changes the head commit; the old PASS no longer
  counts, so ask the owner again. Never push a low fix onto a passed PR, and never
  open a PR for one review's low findings: they wait in the issue.
- The one PR that may fix only low findings is a batch fix of the open
  issue. Its body says `Closes #<issue>` and names each finding it leaves
  unfixed, with the reason; those are dropped, not carried over. After it
  merges, copy any finding posted while it was open to the open issue with
  the same title, creating it if none is open: `review-pr.sh` logs to the
  open one, or creates it.
- Never post a review comment by hand or edit the marker
  `<!-- jas-review sha=… verdict=… -->`.
- Report the verdict and what you changed or rebutted to the user.
