---
name: pr-review-loop
description: Use in the jas repo after opening or updating a pull request and before merging it, or when `gh pr merge` is blocked with "merge blocked".
---

# PR review loop

This skill is for the agent that opens and merges PRs. If you are the
reviewer (jas's `reviewer` profile, asked to review a PR), ignore it and
use `pr-review`.

Every jas PR gets a jas review of its head commit before it merges, unless
it changes docs only: every changed file is a `.md` that is not agent
instructions (`SKILL.md`, `AGENTS.md`, `CLAUDE.md`, anything under
`.agents/`). Paths below are relative to the repo root.

- Any agent can follow this skill once Setup below is done.
- The reviewer profile (its `AGENTS.md`, `config.json` and the `pr-review`
  skill) lives in `.agents/skills/pr-review-loop/reviewer/`. `review-pr.sh`
  installs it into `~/.jas/profiles/reviewer/` from the base branch, with
  the base branch's installer, before every review, so a change to it
  applies once it merges. Edit it there, not
  in the jas home: `AGENTS.md` and `skills/` are replaced each time, and only
  `config.json` (the model) is kept once written.
- In Claude Code, a hook
  (`.agents/skills/pr-review-loop/scripts/check-pr-reviewed.sh`, added by
  `make claude-setup`) also blocks `gh pr merge` until a PASS review for the
  current head is posted. Other agents have no such hook: the rule below is
  the only gate, so follow it exactly.

## Setup, once per machine

1. Install `jas` (`make install`, or a release), `gh`, `jq`, `git` and `tar`, and
   log in with `gh auth login`.
2. Give jas a key for the reviewer's model, `openrouter/z-ai/glm-5.3`
   (`reviewer/config.json`): `jas auth add openrouter`. To review with
   another model, run one review, which writes
   `~/.jas/profiles/reviewer/config.json`, then change `model` there; later
   reviews keep it.
3. Claude Code only: `make claude-setup` once per clone.

No profile needs creating: the first review installs it.

## Loop

1. Push the branch and open the PR. Docs only: go to step 5.
2. Run `bash .agents/skills/pr-review-loop/scripts/review-pr.sh <pr>` with a
   command timeout of at least 35 minutes (a review takes 5–30). It posts
   the review as a PR comment.
   - Exit 0, `VERDICT: PASS`: go to step 5. Its findings are all low; the
     script logs them in the "Low findings from jas reviews" issue.
   - Exit 1, `VERDICT: CHANGES`: go to step 3.
   - Exit 3, the review did not finish: rerun once; if it fails again, tell
     the user and stop.
3. Check each high and medium finding against the code it cites before
   acting on it (in Claude Code, with superpowers:receiving-code-review).
   Then fix it, or reply on the PR with why it is wrong. Fix a low finding
   only while you are pushing a high or medium fix anyway.
4. Run the tests, push, and go back to step 2. After the second CHANGES
   verdict, stop and ask the user instead of reviewing a third time.
5. Squash-merge only after a PASS for the current head (or for a docs-only
   PR):
   `gh pr merge <pr> --squash --delete-branch`.

## Rules

- A push after the review changes the head commit; the old PASS no longer
  counts, so review again. Never push a low fix onto a passed PR, and never
  open a PR only to fix low findings: they wait in the issue.
- A PR that fixes the issue's low findings in a batch says
  `Closes #<issue>` in its body and names each finding it leaves unfixed,
  with the reason. After it merges, copy any finding it did not cover (one
  posted while it was open) to a new issue with the same title. With the
  old issue closed, `review-pr.sh` logs to that one, or creates it.
- Never post a review comment by hand or edit the marker
  `<!-- jas-review sha=… verdict=… -->`.
- Report the verdict and what you changed or rebutted to the user.
