# Farm reviewer step — design

Status: draft, for owner approval (2026-11-24). Decisions marked
"owner" are the owner's. Those marked "author's call" are proposals to check.
Parent spec: `docs/superpowers/specs/2026-11-02-farm-design.md`, "Post-integrate
steps" and "Roles". This document refines the `reviewer` part of that spec,
and replaces its `post_integrate` plan field with a run option for the reviewer.
Where they disagree, this document wins, and the farm spec is updated in the
same change.

## Intent

The supervisor's `check` proves that the tests pass. It cannot prove that the
farm built **what the plan asked for**. Two failures slip through today
(owner, 2026-11-24):

- **Missed goals.** A part's `goal`, an `interfaces` entry or the plan's
  `task` is not done, or only half done, and no test covers it.
- **Plan drift.** Work lands outside the plan: an interface shape or contract
  changed, files outside a part's purpose were rewritten, or behavior nobody
  asked for was added.

The reviewer is an optional, read-only LLM step. It runs after the integrator
and reads the integrated diff **against the plan**. Its findings go to the
integrator for one fix round. A second review then checks the fix, and
anything still missed or drifting fails the farm. The farm must not report
`ok` when a goal is known to be missing.

## Goals

- Catch missed goals and plan drift that `check` cannot see.
- Give an honest exit code to CI and `--json`: a farm with a known missed
  goal exits 6, not 0.
- Keep findings structured: typed, logged, redacted and visible live in the
  chat dashboard and in `jas attach`. No free-text parsing decides the exit
  code.

## Non-goals

- General bug hunting, style, performance or security review. That is the
  PR-level jas review (`pr-review-loop`) or a future step.
- Fuzz / e2e (the separate `fuzz` step in the farm spec).
- More than one fix round.
- Reviewing individual parts before integration. The reviewer sees only the
  merged tree.
- Reviewing non-farm `jas run` sessions.

## Anti-goals

- A reviewer that "passes" by staying silent. A session that never says it
  finished must not count as "no findings".
- Findings that cite nothing. Every finding names a plan item and a place in
  the code, so the integrator and the owner can check it.
- A reviewer that blocks on taste. Only `missed` and `drift` gate. `unsure`
  is informational.
- Changing behavior when no review is requested. Without `--review` or
  `review: true`, the farm runs exactly as it does today.

## Constraints

- Do not break `jas run`, `--json`, exit codes 6/7/8, or existing `reason`
  values. Add one new reason, `review_unmet`, under exit 6.
- Inline chat only. The dashboard gets a reviewer row like the integrator's,
  and no new screen.
- Every farm call from the chat runs off the event loop (AGENTS.md).
- Reviewer writes: none. Tools: read, grep, ls, plus `board_post`. No shell.
- The approval ladder and redaction are unchanged. Board posts are redacted
  like every other post.
- Docs follow the feature: `docs/manual.md` (plan field, reason, events,
  manifest, config key), the manual skill summary, and the farm spec.

## Approaches considered

How findings get from the reviewer to the supervisor:

1. **Typed board topic `finding`** (picked by the owner). The board already
   validates fields, redacts, logs to `board.jsonl`, and notifies the
   supervisor through `SetOnPost`. Findings show live in attach and the
   dashboard. The risk is a silent reviewer, which a required closing post
   covers.
2. **Parse the reviewer's final answer** (a `## Findings` / `VERDICT:`
   format like `review-pr.sh`). Rejected: the PR loop shows that models
   ignore the format at the turn cap (#230 needed a rewrite turn), and a
   missed parse would silently skip the gate.
3. **Dedicated `report_findings` tool.** Rejected: it would be a second typed
   channel parallel to the board, outside the board log, attach and
   redaction. It is more code for what option 1 already gives.

What happens after the fix round (owner picked **B**):

- A. Report only; exit 0 if `check` passes. Rejected: a missed goal ships as
  `ok`, which is the exact case this step exists for.
- **B. Re-review once and gate.** Anything still `missed`/`drift` after the
  second review means exit 6 with `review_unmet`.
- C. Gate on the integrator's own `fixed`/`rejected` answers without a
  re-review. Rejected: the integrator would grade its own work.

## Design

### Turning it on

- **A run option, not a plan field** (owner, 2026-11-24). The review is on
  when either of these is set:
  - the flag `--review` on `jas farm "<task>"` and `jas farm run <plan>`;
  - the farm config key `review: true` (all config layers, like `check`).
    This is also how a chat `/farm` gets a review, with no new chat command.

  The flag overrides the config for that run; `--review=false` turns a
  configured review off.
- The plan is **not** changed, so its approval hash is unaffected:
  `jas farm run <plan>` with or without `--review` validates the same
  approved plan. Skipping approval is safe because a review only adds checks.
  It never widens what workers can read, write or run. The manifest records
  `review_requested: true` and where the request came from (`flag` or
  `config`), so a run can be audited and reproduced.
- The planner never turns the review on. A review costs tokens on every run,
  so it is the owner's choice, like `check`.
- The farm spec's plan field `post_integrate` is **not** built. A plan that
  contains it stays a parse error (`KnownFields`), which fails closed. When
  fuzz ships, its own design decides between the plan field and run options.
- A part with `role: reviewer` in `parts:` is a validation error.
- Config key `models.reviewer` (farm config, all layers, like
  `models.integrator`). If it is unset, the reviewer uses the integrator's
  model, then the default model.

### Built-in role `reviewer`

- Tools: `read`, `grep`, `ls`; `board_post` is added as for every worker.
  Writes: none. Approve: the farm default (all its tools are read-only).
- The `reviewer` role can be overridden in config like `coder`/`tester`
  (whole-role replacement). An override that grants write tools is a config
  validation error: the reviewer stays read-only.
- Budget: the role's `budget`, or `budget.per_worker_tokens`.
  **Author's call:** round 1 draws from the work pool, never from
  `integrator_reserve`, because the reserve protects the integrator's fix
  round. Round 2 runs after the fix round, with no fix round after it, so it
  may draw from what is left of `integrator_reserve`. If round 1 drains the
  work pool, the farm stops with exit 7, as when a part drains it
  (`classifyTurn` with `poolWork`). If round 2 drains the reserve too, it also
  stops with exit 7.

### Board topics

Two new topics, both restricted to the `reviewer` role. `board_post`'s schema
is static today (`farm/board_tool.go`: `owns`, `interface`, `blocked`), so
the reviewer's `board_post` gets its own schema whose topic enum is
`finding` and `review_done`. The reviewer cannot post the part topics, and
parts cannot post these two.

| topic | fields | required |
|---|---|---|
| `finding` | `kind` (`missed` \| `drift` \| `unsure`), `plan_ref`, `where`, `text`, `id` (round 2 only) | `kind`, `plan_ref`, `text` |
| `review_done` | `summary`, `round` | `summary` |

- `plan_ref` is `task`, a part id, `interface:<name>`, or `contracts` (the
  plan's free-form `contracts:` block, valid only when the plan has one; the
  finding's `text` quotes the line it means). The supervisor checks it
  against the plan. An unknown ref is rejected at post time with a tool
  error, so the reviewer can correct it.
- `where` is `path:line` or `path`. It is optional only for `missed`, where
  there may be no code to point at.
- `kind` is validated. Anything else is rejected at post time.
- **Ids.** In round 1 the reviewer posts no `id`. The supervisor assigns
  `R1`, `R2`, … in post order, and the manifest and the fix message use them.
  In round 2 a finding that re-reports a round-1 finding must carry its id,
  and the id is checked against the round-1 set. A new drift finding that
  the fix introduced has no id and gets the next free one. An unknown id is a
  tool error.
- `review_done` closes a round. Its `round` must equal the round the
  supervisor is running, otherwise it is a tool error. A reviewer session
  that ends without a valid `review_done` is an **unfinished review**.
- Findings are not delivered to other parts' boards. Every part is finished
  by then. They are delivered to the integrator in its fix message instead.

### Supervisor flow

After `Integrate` returns without an `IntegrationError`:

0. **Author's call:** if any part failed, was paused or was dropped
   (`unmetParts` is not `ok`), the review is `skipped`. The farm is unmet
   already, and the reviewer would only report the missing parts' goals as
   `missed`. So the reviewer always reviews a plan whose parts all finished,
   and every part goal is in scope.
1. `Verdict` runs `check`, as today. If it fails: reason `check_failed`, no
   review (`skipped`). The flow continues to the final choice.
2. **Round 1.** Start a reviewer session in the integrate worktree. Its
   prompt gives the plan (task, parts with goals and owns, interfaces,
   contracts, shared), the base SHA and the integrate branch. Its first
   message carries the integrated diff from base (see "Diff size"). Collect
   its `finding` posts until it posts `review_done` and the session ends.
3. If round 1 has no `missed`/`drift` findings: the review is met and the
   flow goes to the final choice. `unsure` findings are recorded only.
4. **Fix round.** Run the integrator (`runIntegrator`, fresh session, same
   role and writes) with a message that lists each `missed`/`drift` finding
   with a stable id (`R1`, `R2`, …). The integrator fixes each one or, if
   it judges a finding wrong, leaves it and says why in its final answer.
   It verifies each finding against the code before acting, so it does not
   follow a finding blindly. If the fix round fails, the farm ends the way an
   integrator failure does today. If the session errors, times out, hits the
   turn cap or breaks its write limit, the `IntegrationError` gives
   `part_failed`. If it hits the farm budget, the farm stops with exit 7.
   There is no round 2 in either case, and `review.status` is `unmet`.
5. Re-run `check` on the fixed tree, through `Verdict`, so the manifest's
   `check` and `checked_commit` reflect the final tree. If it fails: reason
   `check_failed`, and no second review.
6. **Round 2.** A fresh reviewer session gets the round-1 findings, the
   integrator's final answer, and the fix diff (integrate HEAD before the
   fix round, up to the new HEAD). For each round-1 finding it posts a
   `finding` with `id: R<n>` if the finding is still open. It also posts
   new `drift` findings that the fix introduced. Then it posts `review_done`.
   It does not re-open the whole review.
7. **Gate.** Any `missed`/`drift` finding from round 2 means exit 6,
   `reason: review_unmet`. Otherwise the review is met.

This is intended: if the integrator judges a finding wrong and leaves it,
and round 2 re-reports it, the farm exits 6 `review_unmet`. The reviewer is
the gate. The owner's way out is the final choice (merge with the typed
phrase) or editing the plan. Do not "fix" this by letting the integrator's
rejection close a finding. That is approach C, which was rejected.

Reason precedence, unchanged first: interrupted, then budget stop
(exit 7), then `part_failed`/`part_dropped`, then `check_failed`, then
`review_unmet`. Because of step 0, `review_unmet` is only ever the reason on
a farm whose parts all finished and whose `check` passed.

**Unfinished review** (no `review_done`; the session failed, timed out or
hit the turn cap): exit 6, `reason: review_unmet`, with the manifest's
`review.status: unfinished` and the session's end reason as detail.
**Author's call:** an unverified farm is unmet, not ok. If the farm token
budget ran out, exit 7 as today.

### Diff size

The supervisor computes `git diff <base>..HEAD` (round 1) or the fix diff
(round 2) in the integrate worktree. **Author's call:** it inlines the diff
up to a cap of 64 KiB of the redacted diff. Beyond the cap it inlines the
`--stat` view and tells the reviewer to `read` files itself. The stat view
is capped at the same 64 KiB and truncated with a marker. The reviewer has
no shell, so it cannot run git.

### Exit, events, manifest

- New reason `review_unmet` (exit 6). The final-choice prompt shows it like
  any unmet reason. Merging still needs the typed merge phrase.
- Events in `farm.jsonl`:
  - `review_started` `{round, base, head}`
  - `review_ended` `{round, status: done|unfinished, findings: {missed, drift, unsure}, open, detail?}`
  - Board posts appear as `board_posted` already.
- Manifest field `review` (absent when not requested):
  `{status: met|unmet|unfinished|skipped, rounds: [{round, session_id, log,
  tokens, findings: [{id, kind, plan_ref, where, text}]}], open: [ids]}`.
  `skipped` means `check` failed before the review.
- `farm status` prints one review line (status, open count) and lists the
  open findings.
- `--json` `farm_ended` carries `review_status` and `review_open`. Both are
  absent when no review was requested, like the manifest field.

### Chat and attach

- The dashboard shows a `reviewer` row (round, status, tokens) under the
  integrator row. The owner can steer the reviewer session like the
  integrator's (`s.ctl.setWorker`).
- `finding` posts appear in the live board view the same way `owns` and
  `interface` posts do. (`note` is not shipped yet.) No new keys or
  commands.
- `jas attach` gets the same rows and posts through the existing stream.

## Left to the builder

- Exact prompt wording for the reviewer role and for the integrator's fix
  message. Prompts must say that the plan and diff are agent-written input,
  as the board label does.
- Where the reviewer flow lives (a new `farm/review.go` is expected) and how
  it shares session setup with `runIntegrator`.
- Test layout. Expected: fake-provider farm tests for met, fix-then-met,
  still-open → `review_unmet`, unfinished → `review_unmet`, `check` fails →
  `skipped`, and validation tests for the plan field, topics and role
  override.
- Dashboard row layout, following the integrator row.

## Doc changes in the same change

- `docs/manual.md`: `--review`, the config keys `review` and `models.reviewer`, the
  `reviewer` role, the `finding`/`review_done` topics, `review_unmet`, the
  new events, the manifest `review` field. Then `make manual`.
- `cmd/jas/manual/jas/SKILL.md` if the summary lists farm flags.
- Farm spec: mark the reviewer as shipped and point to this doc. Fix the
  reason list (`review_unmet`), the post-integrate step 4 wording (the
  reviewer is turned on by the run option, not `post_integrate`), and the
  roles table (`reviewer` tools are read, grep, ls; findings come as board
  posts, not in its final answer).

## Later: fuzz

When the fuzz step ships, the farm spec's order still holds: integrate →
check → fuzz → reviewer. After the fix round, step 5 re-runs `check` **and**
fuzz before round 2. Keep this document's step 5 and the farm spec's
"re-runs check (and fuzz)" in sync when fuzz is built. This change does
nothing about fuzz.
