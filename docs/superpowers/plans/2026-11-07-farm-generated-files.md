# Farm Generated Files Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lockfiles outside every part's `owns` are undone in part worktrees without costing a fix round, and regenerated once on the merged tree before `check`.

**Architecture:** A new `generated` config list (detected from the base commit when unset) is resolved once per run onto the Supervisor. `reviewTurn` restores those paths before the diff check; `Integrate` runs each `regenerate` command after the merges (clean path) or after the integrator (conflict path) and commits only the path.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (existing `fileCfg`), git via `GitRunner`, `core.RunCheck`.

**Spec:** `docs/superpowers/specs/2026-11-07-farm-generated-files-design.md`

## Global Constraints

- `go test -race ./...` green; `CGO_ENABLED=0` builds; `gofmt` clean on touched files.
- Do not break `jas run`, `--json`, exit codes.
- Docs follow features in the same PR: `docs/manual.md`, `make manual`, `cmd/jas/manual/jas/SKILL.md`, `README.md` farm section, farm design spec events list.
- Commits: `type: description`.
- Default regenerate commands, verbatim from the spec:
  - `package-lock.json` → `npm install --package-lock-only --no-audit --no-fund`
  - `yarn.lock` with `.yarnrc.yml` at base root → `yarn install --mode=update-lockfile`
  - `yarn.lock` without it → `yarn install --ignore-scripts --non-interactive`
  - `pnpm-lock.yaml` → `pnpm install --lockfile-only`
  - `Cargo.lock` → `cargo update --workspace`
  - `uv.lock` → `uv lock`
  - root `package.json` and no `package-lock.json`/`yarn.lock`/`pnpm-lock.yaml` at base → `package-lock.json` with the npm command.
- Event names: `generated_restored{part, paths}`, `generated_regenerated{path, exit_code, duration_ms, timed_out, changed}`.
- Regenerate commit message: `farm <id>: regenerate <path>` + `Jas-Farm: <id>` / `Jas-Part: integrate` trailers (as `mergeLeaves`).
- Ordering invariant: regenerate runs before the first check of the merged tree, never between `integrate_check` and `Verdict`.

## Review Focus

- A generated path that is inside a part's `owns` (e.g. plan owns `**` or `package-lock.json`): must be committed as an owned file, never restored. → Task 2 test `TestRunFarm_GeneratedInsideOwnsIsCommitted`.
- A regenerate command that is not installed (`yarn: not found`, exit 127): farm continues, event logged, check decides. → Task 3 test `TestIntegrate_RegenerateFailureNotFatal` uses `exit 127`.
- A worker that deletes the tracked root lockfile (not just edits): restored from base. → Task 2 test `TestRunFarm_GeneratedDeleteRestored`.
- Regenerate touching other tracked files (e.g. root `package.json` reformatted): only the lockfile committed, others reverted. → Task 3 test `TestIntegrate_RegenerateCommitsOnlyItsPath`.
- `generated: []` in the user layer with no project key: off. → Task 1 test `TestLoadConfig_GeneratedOffInUserLayer`.

---

### Task 1: `generated` config and detection

**Files:**
- Create: `farm/generated.go`, `farm/generated_test.go`
- Modify: `farm/config.go` (Config, fileCfg, loadLayer, validate), `farm/config_test.go`, `farm/supervisor.go` (field), `farm/run.go:282` (resolve onto Supervisor)

**Interfaces:**
- Produces:
  - `type Generated struct { Path string `yaml:"path"`; Regenerate string `yaml:"regenerate"` }`
  - `Config.Generated []Generated` and unexported `Config.generatedSet bool` (some layer set the key, possibly to `[]`)
  - `fileCfg.Generated *[]Generated `yaml:"generated"``
  - `func DetectGenerated(ctx context.Context, git GitRunner, main, base string) ([]Generated, error)` — reads the base commit with `git ls-tree --name-only <base>` (root entries only); never runs the tools.
  - `func ResolveGenerated(ctx context.Context, cfg Config, git GitRunner, main, base string) ([]Generated, error)` — `cfg.Generated` when `generatedSet`, else `DetectGenerated`.
  - `Supervisor.Gen []Generated`, set in `newFarmRun` from `ResolveGenerated(..., pre.baseSHA)`.
  - `func generatedPaths(gen []Generated) []string`

- [ ] **Step 1: Write failing tests**

`farm/generated_test.go` (use `newRepo`, commit files with `writeFile` + `mustGit add/commit`):
- `TestDetectGenerated` table: base with `package-lock.json` → one entry with the npm command; `yarn.lock` + `.yarnrc.yml` → Berry command; `yarn.lock` alone → classic command; `pnpm-lock.yaml`, `Cargo.lock`, `uv.lock` → their commands; `package.json` only → `package-lock.json` + npm command; `package.json` + `yarn.lock` → only the yarn entry; nothing → empty; `package-lock.json` present in the working tree but untracked (or gitignored) → empty.
- `TestResolveGenerated_SetWinsOverDetect`: cfg with `generatedSet` and `[]` → empty even with a lockfile at base.

`farm/config_test.go`:
- `TestLoadConfig_GeneratedProjectReplacesUser`: user lists two, project lists one → one.
- `TestLoadConfig_GeneratedOffInUserLayer`: user `generated: []`, no project key → `generatedSet` true, list empty.
- `TestLoadConfig_GeneratedValidation`: each of `path: ""`, `path: /abs`, `path: ../x`, duplicate path, `regenerate: ""` → error mentioning `generated`.

- [ ] **Step 2: Run** `go test ./farm -run 'Generated'` — expect build failure / FAIL.

- [ ] **Step 3: Implement** `Generated`, config parsing (`if f.Generated != nil { cfg.Generated, cfg.generatedSet = *f.Generated, true }`), validation in `validate`, `DetectGenerated`, `ResolveGenerated`, `generatedPaths`, and the `Supervisor.Gen` wiring in `newFarmRun` (error → return it like other setup errors). Include `Gen` in `ConfigDigest` only if `ConfigDigest` already covers config lists; otherwise leave it.

- [ ] **Step 4: Run** `go test ./farm` — PASS.

- [ ] **Step 5: Commit** `feat: add farm generated config detected from the base commit`

---

### Task 2: Undo generated writes in part worktrees

**Files:**
- Modify: `farm/part.go` (`reviewTurn`, new `restoreGenerated`), `farm/events.go`, `farm/worker.go` (`WorkerPrompt`), `farm/progress.go`
- Test: `farm/run_test.go`, `farm/supervisor_test.go` (`TestWorkerPrompt`), `farm/progress_test.go`

**Interfaces:**
- Consumes: `Supervisor.Gen`, `generatedPaths` (Task 1); existing `restorePaths(ctx, w, paths)`, `DiffViolations`, `core.MatchWriteRules`.
- Produces:
  - `EvGeneratedRestored = "generated_restored"`
  - `func (s *Supervisor) restoreGenerated(ctx context.Context, w *worker) error` — for each `Gen` path outside `w.pol.Writes` (`!core.MatchWriteRules(w.pol.Writes, p)`) that `DiffViolations` reports, restore it via `restorePaths`; if any were restored, log `generated_restored{part, paths}`.
  - `WorkerPrompt(plan Plan, part Part, role Role, base string, generated []string) string` — adds, when `generated` (already filtered to paths outside `owns`) is non-empty: "`<paths>` are regenerated by jas after the merge; changes to them in your worktree are undone, so install dependencies as usual and don't try to keep or restore them."

Call order in `reviewTurn`: `part_check` (when it runs) → `restoreGenerated` → `blamedViolations`. Runs on `BLOCKED:` answers too.

- [ ] **Step 1: Write failing tests** in `farm/run_test.go`. Rig: add `r.opts.Cfg.Generated = []Generated{{Path: "root.lock", Regenerate: "true"}}` with `generatedSet` (add an unexported test helper `withGenerated(cfg *Config, g ...Generated)` in the test file if needed), and commit `root.lock` at base where a test needs it tracked.
  - `TestRunFarm_GeneratedWriteUndoneNoFixRound`: part a writes `a/x.txt`, shell `echo x > root.lock`, `final("a done")` — and its script has **no** further turns. Expect exit 0, `len(r.events(EvGeneratedRestored)) == 1` with `paths == [root.lock]`, and `git ls-tree -r --name-only <a branch>` lacks `root.lock` changes (`git show <a branch>:root.lock` equals base content).
  - `TestRunFarm_GeneratedRestoredOnBlocked`: `PartCheck = "test -d a && test -d b"`; a: write, `echo x > root.lock`, final("a done"), final("BLOCKED: needs b"). Expect exit 0, one `part_check_deferred` for a, branch has base `root.lock`.
  - `TestRunFarm_GeneratedDeleteRestored`: tracked `root.lock`; shell `rm root.lock`; expect done and `root.lock` on branch equals base.
  - `TestRunFarm_GeneratedInsideOwnsIsCommitted`: part a owns `a/**` and `root.lock`; writes `root.lock`; expect no `generated_restored`, branch `root.lock` has the new content.
  - `TestRunFarm_GeneratedRestoredOncePerChange`: check that writes `root.lock` (`echo y > root.lock; exit 0`) as `PartCheck`, worker never touches it → zero `generated_restored` (part_check's own write is undone by `made`, not double-logged).
- [ ] **Step 2:** `TestWorkerPrompt`: call with `[]string{"package-lock.json"}` → prompt contains `package-lock.json are regenerated by jas`; with `nil` → no "regenerated by jas".
- [ ] **Step 3:** `farm/progress_test.go` `TestProgressLine_Generated`: `ProgressLine(EvGeneratedRestored, {"part":"a","paths":[]any{"root.lock"}})` == `[a] undid generated root.lock`.
- [ ] **Step 4: Run** `go test ./farm -run 'Generated|WorkerPrompt|ProgressLine'` — FAIL.
- [ ] **Step 5: Implement** the event constant, `restoreGenerated`, the `reviewTurn` call, the `WorkerPrompt` parameter (update `openWorker`'s call with `Gen` paths outside `part.Owns`), and the progress line.
- [ ] **Step 6: Run** `go test -race ./farm` — PASS.
- [ ] **Step 7: Commit** `feat: undo generated files in farm part worktrees without a fix round`

---

### Task 3: Regenerate on the merged tree

**Files:**
- Modify: `farm/integrate.go` (`Integrate`, new `regenerate`, `integratorWrites`, `integratorMessage`), `farm/events.go`, `farm/progress.go`
- Test: `farm/run_test.go` (run rig lives there), `farm/progress_test.go`

**Interfaces:**
- Consumes: `Supervisor.Gen` (Task 1); existing `checkSpec`, `core.RunCheck`, `s.Git`, `mergeLeaves`, `runIntegrator`, `integrateCheck`.
- Produces:
  - `EvGeneratedRegenerated = "generated_regenerated"`
  - `func (s *Supervisor) regenerate(ctx context.Context, path string) (failed []core.CheckResult, err error)` — per entry: run `Regenerate` with `s.checkSpec(cmd, path)`; `changed` = `git status --porcelain -- <Path>` non-empty; restore any other changed tracked file (`git diff --name-only` minus `Path` → `git checkout HEAD -- <files>`); if changed, `git add -- <Path>` and `git commit --no-verify -m` with the message and trailers from Global Constraints using the farm identity (`farmGitEnv()` / the env `mergeLeaves` uses); log the event. `err` only for git failures; failing commands are returned in `failed`.
  - `integratorWrites(plan Plan, gen []Generated) []string` — adds generated paths; update `integratorRoleFor`.
  - `integratorMessage(leaves, conflicts, failing, regenFailed []core.CheckResult)` — appends "Regenerating `<path>` with `<cmd>` failed (exit N):" + output tail per entry.

`Integrate` becomes:

```text
merge leaves
if conflicts: integrator(conflicts) → regenerate → (failed: event + progress only)
else:         regenerate → integrate_check → fail: integrator(failing, regenFailed)
verifyMerged
```

- [ ] **Step 1: Write failing tests** in `farm/run_test.go` (run rig; base commits `root.lock` = `v1`):
  - `TestIntegrate_RegenerateCommitsBeforeCheck`: `Gen = {root.lock, "echo v2 > root.lock"}`, `check = "grep -q v2 root.lock"`; two parts that both `echo x > root.lock` in their turn. Expect exit 0, no conflicts, one `generated_regenerated` with `changed: true`, integrate branch log contains `regenerate root.lock`, and that commit precedes the `integrate_check` event (seq order).
  - `TestIntegrate_RegenerateUnchangedNoCommit`: regenerate `true` → `changed: false`, no `regenerate` commit on the branch.
  - `TestIntegrate_RegenerateFailureNotFatal`: regenerate `exit 127`, `check = "true"` → exit 0, event `exit_code: 127`.
  - `TestIntegrate_RegenerateFailureReachesIntegrator`: regenerate `echo nope; exit 3`, `check = "false"` then the integrator script fixes nothing (`final("tried")`): the integrator's first user message contains `nope` and `exit 3`.
  - `TestIntegrate_RegenerateAfterIntegratorOnConflict`: copy the setup of `TestRunFarm_ConflictGoesToIntegrator` (`farm/run_test.go:307`: parts a and b both own `a.txt`, `Split.DisjointFiles = false`, integrator script resolves) and add `Gen` regenerate `echo v2 > root.lock`; expect the `generated_regenerated` event's seq is after `integrate_ended`'s integrator work started (`integrate_started`) and before `check_ran`, and the branch's `root.lock` == `v2`.
  - `TestIntegrate_RegenerateCommitsOnlyItsPath`: regenerate `echo v2 > root.lock && echo junk >> a.txt` (tracked at base) → branch `a.txt` equals base, `root.lock` == `v2`.
- [ ] **Step 2:** `TestProgressLine_Generated` (extend): `generated_regenerated` changed → `[integrate] regenerated root.lock`; exit 3 → `[integrate] regenerate root.lock failed (exit 3)`; `changed: false`, exit 0 → `""`.
- [ ] **Step 3: Run** `go test ./farm -run 'Regenerate|ProgressLine'` — FAIL.
- [ ] **Step 4: Implement** as in Interfaces. Keep `mergeCheck` semantics: nothing between `integrateCheck` and `Verdict` changes the tree.
- [ ] **Step 5: Run** `go test -race ./farm` — PASS.
- [ ] **Step 6: Commit** `feat: regenerate farm generated files on the merged tree before check`

---

### Task 4: Planner, chat and docs

**Files:**
- Modify: `farm/planner.go` (planner prompt text), `tui/farm.go` (show the two events), `docs/manual.md`, `cmd/jas/manual/jas/SKILL.md`, `README.md`, `docs/superpowers/specs/2026-11-02-farm-design.md` (events list + Supervisor/Integrator paragraphs), run `make manual`
- Test: `farm/planner_test.go`, `tui/farm_review_test.go`

**Interfaces:**
- Consumes: event names (Tasks 2–3), `ProgressLine`.

- [ ] **Step 1: Write failing tests**
  - `farm/planner_test.go` `TestPlannerPrompt_GeneratedFiles`: `PlannerPrompt(...)` contains `regenerated by jas after the merge` and `package-lock.json`.
  - `tui/farm_review_test.go` `TestFarmGeneratedEventsShown`: `f.event(farm.EvGeneratedRegenerated, {"path":"package-lock.json","exit_code":0,"changed":true})` → main chat printed contains `regenerated package-lock.json`.
- [ ] **Step 2: Run** `go test ./farm ./tui -run 'PlannerPrompt_Generated|FarmGeneratedEventsShown'` — FAIL.
- [ ] **Step 3: Implement**: planner prompt sentence — "Root lockfiles (package-lock.json, yarn.lock, pnpm-lock.yaml, Cargo.lock, uv.lock) are regenerated by jas after the merge: leave them out of owns and shared." Add `farm.EvGeneratedRestored, farm.EvGeneratedRegenerated` to the `show` list in `onFarmEvent`.
- [ ] **Step 4: Docs**: manual config-key row for `generated` (unset = detected defaults table, `[]` = off, user-layer `[]` turns it off for projects without their own list); farm section paragraph; skill and README one-liners; design spec events list + Supervisor/Integrator notes. `make manual`.
- [ ] **Step 5: Run** `go test -race ./...` and `go vet ./...` — PASS (incl. `TestManual_*`).
- [ ] **Step 6: Commit** `docs: document farm generated files` (with the planner/tui changes: `feat: tell the planner and chat about generated files` if split).

---

## Finish

- Whole-branch review, then PR from `feat/farm-generated-files`, squash-merge (owner rule), `make install`.
- Live check: reset `test-app`, root npm workspaces with a committed `package-lock.json`, run a two-part farm; expect `generated_restored` per part with no fix round spent on it, one `generated_regenerated`, and the check passing on the merged tree.
