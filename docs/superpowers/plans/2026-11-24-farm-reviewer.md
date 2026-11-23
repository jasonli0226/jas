# Farm Reviewer Step Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional, read-only post-integrate reviewer. It checks the
merged farm against the plan, sends missed goals and drift to the
integrator for one fix round, re-reviews the fix, and fails the farm (exit 6,
`review_unmet`) when anything is still open.

**Architecture:** A new `farm/review.go` runs two reviewer sessions through
the existing `Supervisor.NewSession` path. Findings arrive as typed board
posts (`finding`, `review_done`) through a reviewer-only post tool that adds
plan-aware validation on top of `Board.PostFrom`. `farmRun.run` calls the
review after a passing `Verdict` when every part finished. The fix round
reuses the integrator session code with a findings message. The review is
turned on by the run option `--review` or the config key `review: true`,
never by the plan.

**Tech Stack:** Go, existing `farm`, `core`, `cmd/jas`, `tui` packages; fake
provider (`providers/fake`) for tests.

**Spec:** `docs/superpowers/specs/2026-11-24-farm-reviewer-design.md`
(approved 2026-11-24). Read it first. This plan does not repeat its
reasoning.

## Global Constraints

- `CGO_ENABLED=0` builds; `go test -race ./...` stays green after every task.
- Do not change `jas run`, `--json` shape for existing events, exit codes, or existing `reason` values. New reason: `review_unmet` (exit 6) only.
- Reviewer tools: `read`, `grep`, `ls` plus its post tool. Writes: none. No shell.
- Plan approval hash unchanged: `--review` never edits the plan. `post_integrate` stays a parse error (`KnownFields`).
- Review skipped when `unmetParts(out) != ReasonOK` or `check` failed before round 1.
- Inline diff cap: 64 KiB of the redacted diff; past it, `git diff --stat` (also capped at 64 KiB, truncated with a marker).
- Finding ids `R1`, `R2`, … assigned by the supervisor in post order.
- Every farm call from the chat runs off the event loop; never log synchronously from `Update` (AGENTS.md).
- Commits: `type: description`. Every user-visible change updates `docs/manual.md` + `make manual` and `cmd/jas/manual/jas/SKILL.md` in the same commit as the feature it documents, or in the final docs task if a task leaves no user-visible surface on its own. `TestManual_*` must pass.

## Review Focus

1. A reviewer that ends its turn without `review_done` (turn cap, a plain final answer) must give `unfinished` → `review_unmet`, never `met`. Test: `TestReview_FinalAnswerWithoutDoneIsUnfinished` (Task 4).
2. `--review=false` with `review: true` in config must turn the review off, and an unchanged plan must still pass the hash check with `--review`. Test: `TestParseFarmRunArgs_ReviewTriState`, `TestRunFarm_ReviewKeepsPlanHash` (Tasks 6, 5).
3. A secret in the diff must not reach the reviewer: the inlined diff is redacted before the cap is measured. Test: `TestReviewDiff_RedactedBeforeCap` (Task 4).
4. A round-2 finding with an id not in round 1, or a `review_done` with the wrong round, is a tool error the reviewer can retry, not a crash or a silent pass. Test: `TestReviewPost_UnknownIDRejected`, `TestReviewPost_WrongRoundRejected` (Task 2).
5. A farm whose diff is empty after integrate (parts made no net change) still runs round 1 and can pass. Test: `TestReview_EmptyDiffStillReviews` (Task 4).

---

### Task 1: Config key, reviewer model, built-in role, plan validation

**Files:**
- Modify: `farm/config.go` (`Config`, `fileCfg`, `Models`, `apply`, `validate`)
- Modify: `farm/roles.go` (`BuiltinRoles`)
- Modify: `farm/validate.go` (`validateStructure`)
- Test: `farm/config_test.go`, `farm/roles_test.go`, `farm/validate_test.go`, `farm/plan_test.go`

**Interfaces:**
- Produces:
  - `Config.Review bool` and `Config.ReviewFrom string` (`""`, `"config"`, `"flag"`; set to `"config"` by `apply` when a layer sets `review: true`).
  - `Models.Reviewer string` (yaml `models.reviewer`).
  - const `reviewerRole = "reviewer"` in `farm/roles.go`.
  - `BuiltinRoles()["reviewer"] == RoleDef{Tools: []string{"read", "grep", "ls"}, Prompt: reviewerPrompt}`. `reviewerPrompt` is a const in `farm/review_prompt.go`; Task 4 fills in its text, and a placeholder one-liner is fine here.

- [ ] **Step 1: Write failing tests**

```go
func TestLoadConfig_ReviewKey(t *testing.T)            // home farm.yaml "review: true" → cfg.Review, cfg.ReviewFrom == "config"; absent → false, ""
func TestLoadConfig_ModelsReviewer(t *testing.T)       // "models: {reviewer: x}" → cfg.Models.Reviewer == "x"
func TestLoadConfig_ReviewerRoleWithWriteToolRejected(t *testing.T) // roles.reviewer.tools [read, write] → error containing "role reviewer: read-only"; same for edit, shell
func TestBuiltinRoles_Reviewer(t *testing.T)           // tools exactly [read grep ls], no writes
func TestValidate_ReviewerRoleInPartsRejected(t *testing.T) // part role reviewer → Problem "role reviewer runs after integration; it cannot be a plan part"
func TestParsePlan_PostIntegrateRejected(t *testing.T) // "post_integrate: [reviewer]" → ParsePlan error
```

- [ ] **Step 2: Run, verify they fail**

Run: `go test ./farm -run 'Review|PostIntegrate|Reviewer'`
Expected: FAIL (undefined fields, or missing errors). `TestParsePlan_PostIntegrateRejected` already passes: it pins today's behavior.

- [ ] **Step 3: Implement**

`fileCfg.Review *bool \`yaml:"review"\``; `fileCfg.Models.Reviewer *string`. In `validate`, reject any `cfg.Roles["reviewer"]` whose tools include a name from `tools.Mutating()`, with the message `role reviewer: read-only, tool %q not allowed`. In `validateStructure`, add the Problem for `pt.Role == reviewerRole` before the availability check.

- [ ] **Step 4: Run, verify pass**

Run: `go test ./farm`
Expected: PASS

- [ ] **Step 5: Commit** — `feat: farm review config key and reviewer role`

---

### Task 2: Reviewer board topics and post tool

**Files:**
- Modify: `farm/board.go` (`topicFields`, `topicRequired`, `topicRoles`, the unknown-topic message)
- Create: `farm/review_tool.go`
- Test: `farm/board_test.go`, `farm/review_tool_test.go`

**Interfaces:**
- Consumes: `Board.PostFrom`, `Plan`, `reviewerRole` (Task 1).
- Produces:
  - Topic consts `TopicFinding = "finding"`, `TopicReviewDone = "review_done"`. Fields: finding `{kind, plan_ref, where, text, id}`, required `{kind, plan_ref, text}`; review_done `{summary, round}`, required `{summary}`. Both restricted to `reviewerRole`.
  - Finding kinds: `FindingMissed = "missed"`, `FindingDrift = "drift"`, `FindingUnsure = "unsure"`.
  - `type Finding struct { ID, Kind, PlanRef, Where, Text string }` (json tags `id, kind, plan_ref, where, text`).
  - `type reviewRound struct` holding the plan, the round number (1 or 2), the round-1 ids (round 2), and, under a mutex, the collected `[]Finding` and `done bool`. Methods: `findings() []Finding`, `finished() bool`.
  - `type ReviewPostTool struct { Board *Board; Part, SessionID string; Round *reviewRound }`, implementing `core.Tool` with `Name() == "board_post"` and `Kind() == core.ReadOnly`. Its schema's topic enum is `["finding","review_done"]`.

The tool checks, in order, and returns `core.Result{IsError: true}` with a message for each failure (the board's own field checks come after):
1. `kind` is one of the three kinds.
2. `plan_ref` is `task`, a plan part id, `interface:<name>` for a plan interface, or `contracts` when `strings.TrimSpace(plan.Contracts) != ""`.
3. `where` is non-empty unless `kind == missed`.
4. Round 1: `id` must be absent. Round 2: `id`, if present, must be in the round-1 set; an absent id gets the next free `R<n>`.
5. `review_done.round` must equal `strconv.Itoa(round)`.

On success, it posts through `Board.PostFrom(part, reviewerRole, …)`, then appends the `Finding` (with its assigned id) or sets `done`. It replies `posted finding R3` / `posted review_done`.

- [ ] **Step 1: Write failing tests**

```go
func TestBoard_FindingOnlyFromReviewer(t *testing.T)     // coder posting finding → "may not post topic"
func TestReviewPost_AssignsIDsInOrder(t *testing.T)      // two round-1 findings → R1, R2; findings() has both
func TestReviewPost_UnknownPlanRefRejected(t *testing.T) // plan_ref "nope" → IsError, nothing stored
func TestReviewPost_ContractsRefNeedsContracts(t *testing.T) // plan without contracts → rejected; with → accepted
func TestReviewPost_WhereRequiredUnlessMissed(t *testing.T)
func TestReviewPost_BadKindRejected(t *testing.T)
func TestReviewPost_UnknownIDRejected(t *testing.T)      // round 2, id "R9" not in {R1} → IsError
func TestReviewPost_Round2NewDriftGetsNextID(t *testing.T) // round-1 {R1,R2}; new drift → R3
func TestReviewPost_WrongRoundRejected(t *testing.T)     // round 1, review_done round "2" → IsError, finished()==false
func TestReviewPost_DoneFinishesRound(t *testing.T)
```

- [ ] **Step 2: Run, verify fail** — `go test ./farm -run 'ReviewPost|FindingOnly'` → FAIL (undefined).
- [ ] **Step 3: Implement** per the Interfaces block.
- [ ] **Step 4: Run, verify pass** — `go test ./farm` → PASS.
- [ ] **Step 5: Commit** — `feat: reviewer board topics and post tool`

---

### Task 3: Integrator runs from a given message

**Files:**
- Modify: `farm/integrate.go` (`runIntegrator`)
- Test: `farm/run_test.go` (the existing integrator tests are the safety net)

**Interfaces:**
- Produces: `func (s *Supervisor) runIntegratorMsg(ctx context.Context, path, msg string) error`. This is the existing body of `runIntegrator` with `s.integratorMessage(...)` replaced by `msg`. `runIntegrator` becomes a one-line wrapper that builds the message. The return contract is unchanged: `*IntegrationError` for unmet results (`BudgetStop` on the farm pool), `nil` on success after `finishIntegrator`.

This is a pure refactor, with no behavior change.

- [ ] **Step 1: Refactor.**
- [ ] **Step 2: Run** `go test -race ./farm` → PASS (`TestRunFarm_ConflictGoesToIntegrator`, `TestRunFarm_CleanMergeFailingCheckGoesToIntegrator`, `TestRunFarm_IntegratorFarmPoolExit7`, `TestHandle_SteerIntegrator` included).
- [ ] **Step 3: Commit** — `refactor: let the integrator run from a given message`

---

### Task 4: Review flow (`farm/review.go`)

**Files:**
- Create: `farm/review.go`, `farm/review_prompt.go`
- Modify: `farm/pools.go`, `farm/events.go`, `farm/manifest.go`
- Test: `farm/review_test.go` (uses `newRunRig` from `farm/run_test.go`; the session script switches on `ws.Part.ID == reviewPart`)

**Interfaces:**
- Consumes: `ReviewPostTool`, `reviewRound`, `Finding` (Task 2); `runIntegratorMsg` (Task 3); `reviewerRole`, `Config.Models.Reviewer` (Task 1).
- Produces:
  - const `reviewPart = "review"` (agent id and `Part.ID` for both rounds; logs at `<farm dir>/review/round<N>.jsonl`).
  - consts `EvReviewStarted = "review_started"`, `EvReviewEnded = "review_ended"`.
  - Review statuses: `ReviewMet = "met"`, `ReviewUnmet = "unmet"`, `ReviewUnfinished = "unfinished"`, `ReviewSkipped = "skipped"`.
  - `type ReviewRoundState struct { Round int; SessionID, Log string; Tokens int; Findings []Finding }` and `type ReviewState struct { RequestedBy, Status string; Rounds []ReviewRoundState; Open []string }` (json `requested_by, status, rounds, open`). New field `Manifest.Review *ReviewState \`json:"review,omitempty"\``.
  - `func (p Pools) Reviewer(round, limit int) *core.TokenPool`: round 1's parent is `p.Work`, round 2's is `p.Farm`. The pool is named `"review"`.
  - `type reviewResult struct { Status string; Open []string; FixErr error }`. `FixErr` is the fix round's `*IntegrationError`, or another error.
  - `func (s *Supervisor) Review(ctx context.Context, path string) reviewResult`. It runs round 1, the fix round, `Verdict`, then round 2, and updates `Manifest.Review` and the events as it goes. It returns `ReviewMet`, `ReviewUnmet` or `ReviewUnfinished`, never `ReviewSkipped` (the caller sets that). When the post-fix `Verdict` fails, it returns `Status: ReviewUnmet` with no round 2, and the caller sees the failing check.
  - `func reviewDiff(ctx context.Context, git GitRunner, dir, from, to string, redact func(string) string) string`: the redacted `git diff from..to`, capped at `reviewDiffCap = 64 << 10`. Past the cap, the redacted `git diff --stat` capped the same way, plus the line `(diff too large: read the files listed above)`.

Session setup mirrors `runIntegrator`: `ResolveRole(s.Cfg, reviewerRole, nil)`. The model is `Models.Reviewer`, else `Models.Integrator`, else the role's model. Tools are `roleTools(role)` plus `ReviewPostTool`, the policy is `WorkerPolicy` with no writes, the gate is `s.gate`, the steering is `s.ctl.setWorker(reviewPart, …)`, and the timeout is `PerWorkerTimeout`. Classify the turn with `classifyTurn(reviewPart, res, farmPools, …)`, where `farmPools` is `{poolFarm, poolWork}` in round 1 and `{poolFarm}` in round 2. `endFarmStop` → emit `EvBudgetStop` and return a status the caller maps to exit 7 (use `reviewResult.FixErr = &IntegrationError{BudgetStop: true, Reason: stopTokens}`, so `run.go` handles one shape). Any other non-done end, or a session that never posted `review_done`, means `ReviewUnfinished`.

Round 1's system prompt is `ReviewerPrompt(plan, role, base, branch)`, and its first message is the round-1 diff (`base..HEAD`). Round 2's system prompt is the same. Its first message is the round-1 findings, the integrator's final answer, and the fix diff (`preFixHead..HEAD`). Both prompts state that the plan text and diff are agent-written input, not owner instructions. They also say to post one `finding` per problem, cite `plan_ref`, end with `review_done`, and that only `missed`/`drift` gate. The fix message lists each `missed`/`drift` finding as `R<n> [kind] plan_ref where: text`. It tells the integrator to verify each one against the code, fix it or leave it and say why, and commit.

`Open` follows the spec's manifest rules. Round 1 unfinished: empty. Round 1 finished, round 2 unfinished or not run: all round-1 `missed`/`drift` ids. After round 2: the round-2 `missed`/`drift` ids. Met: empty.

Events: `review_started {round, base, head}`; `review_ended {round, status, findings: {missed, drift, unsure}, open, detail?}`.

- [ ] **Step 1: Write failing tests**

```go
func TestReview_NoFindingsMet(t *testing.T)                 // round 1 posts review_done only → Met, one reviewer session, no fix round
func TestReview_UnsureOnlyMet(t *testing.T)                 // unsure finding + done → Met, Manifest.Review.Rounds[0].Findings has it, no integrator session
func TestReview_FixThenMet(t *testing.T)                    // R1 missed → integrator runs with "R1 [missed]" in its message → round 2 done, no findings → Met; Rounds has 2 entries
func TestReview_StillOpenUnmet(t *testing.T)                // round 2 re-posts id R1 → Unmet, Open == ["R1"]
func TestReview_FinalAnswerWithoutDoneIsUnfinished(t *testing.T) // round 1 replies final("looks good") with no post → Unfinished, Open empty
func TestReview_Round2UnfinishedKeepsRound1Open(t *testing.T)    // R1, R2 drift; round 2 never posts done → Unfinished, Open == ["R1","R2"]
func TestReview_FixRoundFailsNoRound2(t *testing.T)         // integrator script fails (write outside limit) → FixErr is *IntegrationError, Status Unmet, one reviewer session
func TestReview_EmptyDiffStillReviews(t *testing.T)
func TestReviewDiff_RedactedBeforeCap(t *testing.T)         // diff containing a fake secret; redact replaces it; output lacks the secret
func TestReviewDiff_OverCapFallsBackToStat(t *testing.T)    // 70 KiB change → output has "--stat"-style lines and the marker, len ≤ 64 KiB + marker
func TestReview_UsesReviewerModel(t *testing.T)             // Models.Reviewer "rv" → NewSession sees model "rv" (check ws.Role.Model)
func TestReview_Events(t *testing.T)                        // review_started/review_ended per round with the counts above
```

- [ ] **Step 2: Run, verify fail** — `go test ./farm -run 'Review'` → FAIL (undefined).
- [ ] **Step 3: Implement** per the Interfaces block.
- [ ] **Step 4: Run, verify pass** — `go test -race ./farm` → PASS.
- [ ] **Step 5: Commit** — `feat: farm review rounds with one integrator fix round`

---

### Task 5: Wire the review into the farm run

**Files:**
- Modify: `farm/run.go` (`farmRun.run`, `farmRun.end`, reason consts)
- Modify: `farm/supervisor.go` (if `StartFarm` sets `Manifest.Review` there)
- Test: `farm/run_test.go`

**Interfaces:**
- Consumes: `Supervisor.Review`, `reviewResult`, `ReviewState` (Task 4); `Config.Review`/`ReviewFrom` (Task 1).
- Produces: `ReasonReviewUnmet = "review_unmet"`; `farm_ended` carries `review_status` and `review_open` (a list of ids) only when `Cfg.Review`.

Wiring in `run`:
- When `Cfg.Review`, set `Manifest.Review = &ReviewState{RequestedBy: Cfg.ReviewFrom}` at farm start.
- After `Verdict`, run the review only if `reason == ReasonOK && res.ExitCode == 0`. Otherwise set `Review.Status = ReviewSkipped` when the review was requested.
- Map the result:
  - `FixErr` with `BudgetStop` → exit 7 (`stopTokens`).
  - Another `*IntegrationError` → `ReasonPartFailed`.
  - A non-`IntegrationError` error → `ReasonError`, exit 1.
  - A post-fix check failure → `ReasonCheckFailed`.
  - `ReviewUnmet`/`ReviewUnfinished` → `ReasonReviewUnmet`.
- Check `ctx.Err()`/`s.hard()` after the review, as after `Verdict`.

- [ ] **Step 1: Write failing tests**

```go
func TestRunFarm_ReviewOffUnchanged(t *testing.T)       // Cfg.Review false → no review events, manifest has no "review" key, farm_ended lacks review_status
func TestRunFarm_ReviewMetExit0(t *testing.T)
func TestRunFarm_ReviewUnmetExit6(t *testing.T)         // reason "review_unmet", farm_ended review_open ["R1"]
func TestRunFarm_ReviewUnfinishedExit6(t *testing.T)
func TestRunFarm_ReviewSkippedWhenCheckFails(t *testing.T) // reason check_failed, Review.Status "skipped", no reviewer session
func TestRunFarm_ReviewSkippedWhenPartDropped(t *testing.T) // reason part_dropped, skipped
func TestRunFarm_ReviewFixBreaksCheck(t *testing.T)     // fix round makes check fail → check_failed, Review.Status "unmet", no round 2
func TestRunFarm_ReviewRound1BudgetExit7(t *testing.T)  // tiny farm_tokens so round 1 drains work pool → exit 7
func TestRunFarm_ReviewKeepsPlanHash(t *testing.T)      // same approved plan file runs with Review on and off; plan bytes unchanged after
```

- [ ] **Step 2: Run, verify fail** — `go test ./farm -run 'RunFarm_Review'` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run, verify pass** — `go test -race ./farm` → PASS.
- [ ] **Step 5: Commit** — `feat: farm review gates the farm with review_unmet`

---

### Task 6: `--review` flag and `farm status`

**Files:**
- Modify: `cmd/jas/farm_run.go` (`farmRunArgs`, `parseFarmRunArgs`, `farmRunOnlyFlags`, `runPlan`)
- Modify: `cmd/jas/farm_cmd.go` (`parsePlanArgs`, the `jas farm "<task>"` path)
- Modify: `cmd/jas/farm_info.go` (status printer)
- Modify: `docs/manual.md`, `cmd/jas/manual/jas/SKILL.md` (then `make manual`)
- Test: `cmd/jas/farm_run_test.go`, `cmd/jas/farm_cmd_test.go`

**Interfaces:**
- Consumes: `Config.Review`, `Config.ReviewFrom`, `Manifest.Review`.
- Produces: `farmRunArgs.review *bool` (nil = flag not given). `runPlan` gets a `review *bool` parameter. When it's non-nil, after `LoadConfig` it sets `cfg.Review = *review` and `cfg.ReviewFrom = "flag"` (or `""` when false). `--review` is a farm-run-only flag (add it to `farmRunOnlyFlags`), accepted by `jas farm run` and `jas farm "<task>"`, but not by `jas farm plan`. The chat path (`cmd/jas/chat_farm.go`) is untouched: it gets the config key through `LoadConfig`.

`farm status` prints, after the check line and only when `m.Review != nil`:
`review: <status>  open <n>`, then one line per open finding: `    R1 [missed] <plan_ref> <where>: <text>`.

Manual: document `--review`, the config keys `review` and `models.reviewer`, the `reviewer` role (read-only; it can't be a plan part), the `finding`/`review_done` topics, the reason `review_unmet`, the events `review_started`/`review_ended`, the manifest `review` field, and the `farm_ended` fields `review_status`/`review_open`. The SKILL.md summary gains `--review` wherever it lists farm run flags.

- [ ] **Step 1: Write failing tests**

```go
func TestParseFarmRunArgs_ReviewTriState(t *testing.T) // none → nil; --review → true; --review=false → false; not stored in ra.flags
func TestFarmRun_ReviewFlagOverridesConfig(t *testing.T) // farm.yaml review: true + --review=false → farm.jsonl has no review_started
func TestFarmStatus_ShowsReview(t *testing.T)          // manifest with Review{Status unmet, Open [R1], Rounds[...]} → "review: unmet  open 1" and the R1 line
```

- [ ] **Step 2: Run, verify fail** — `go test ./cmd/jas -run 'Review'` → FAIL.
- [ ] **Step 3: Implement**, update the manual, run `make manual`.
- [ ] **Step 4: Run** `go test -race ./cmd/jas` → PASS (includes `TestManual_*`, `TestManualSkill_*`).
- [ ] **Step 5: Commit** — `feat: jas farm --review and review in farm status`

---

### Task 7: Progress lines and chat dashboard row

**Files:**
- Modify: `farm/progress.go` (`farmLine`)
- Modify: `tui/farm.go` (`farmState` event handling and `refresh`)
- Test: `farm/progress_test.go`, `tui/farm_test.go` or `tui/farm_view_test.go`

**Interfaces:**
- Consumes: `EvReviewStarted`, `EvReviewEnded`, `Manifest.Review`, `reviewPart`.
- Produces: `farmLine` cases:
  - `review: round N started`
  - `review: round N done (missed X, drift Y, unsure Z)`
  - `review: round N unfinished (<detail>)`

  The finding posts already print as `board_posted` lines; verify that the topic and the text show.

  In `tui/farm.go`: on `EvReviewStarted`, append the agent `reviewAgent = "review"` (role `"reviewer"`) once, as for `integrateAgent`. On `EvReviewEnded`, set its status (`done` → done, `unfinished` → failed). `refresh` takes the tokens from the sum of `man.Review.Rounds[].Tokens`. Steering and the agent number work as for the integrator, because `s.ctl.setWorker(reviewPart, …)` is already set in Task 4.

- [ ] **Step 1: Write failing tests**

```go
func TestFarmLine_ReviewEvents(t *testing.T)     // the three lines above
func TestFarmState_ReviewerRow(t *testing.T)     // review_started adds one "review" agent; second round does not add another; review_ended sets status
```

- [ ] **Step 2: Run, verify fail** — `go test ./farm ./tui -run 'Review'` → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./farm ./tui` → PASS.
- [ ] **Step 5: Commit** — `feat: show the farm reviewer in progress lines and the chat dashboard`

---

### Task 8: Farm spec and AGENTS.md follow-up

**Files:**
- Modify: `docs/superpowers/specs/2026-11-02-farm-design.md`
- Modify: `AGENTS.md` ("Current focus")

Farm spec, per the design's "Doc changes" section:
- Mark the reviewer as shipped and link the design doc.
- Add `review_unmet` to the reason list (the §exit-codes table near line 935 and the reason list near 942).
- Reword post-integrate step 4: the reviewer is turned on by `--review`/`review: true`, not `post_integrate`.
- Fix the roles table row: tools read, grep, ls; findings as `finding` board posts.

AGENTS.md: move the reviewer from the post-MVP list to "Shipped".

- [ ] **Step 1: Edit both files.**
- [ ] **Step 2: Run** `go test ./cmd/jas -run Manual` → PASS (no manual drift).
- [ ] **Step 3: Commit** — `docs: mark the farm reviewer step shipped`
