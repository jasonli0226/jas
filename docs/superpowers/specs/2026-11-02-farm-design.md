# jas farm — design

Date: 2026-11-02 (Asia/Tokyo). Status: **MVP shipped (#95, 2026-11-03);
after-MVP work waits on the validation pass (see Validation).**
Replaces the best-of-N farm in `docs/design.md` §5 and `docs/use-cases.md` §4.
Calls marked **(author's call)** were not made by the owner and may be
overturned.

Inputs:
- Community research: [`docs/research/farm-multi-agent-community-2026-11.md`](../../research/farm-multi-agent-community-2026-11.md)
  (cited below as *research*).
- Builds on [`2026-11-02-session-awareness.md`](2026-11-02-session-awareness.md)
  (per-repo session records, write-overlap warnings), which ships first and
  is a core-loop feature, not a farm feature.
- Consumes specs from [`2026-11-02-spec-driven-flow.md`](2026-11-02-spec-driven-flow.md)
  (`jas spec` interview, scaffold phase, acceptance criteria). User stories
  and step-by-step CLI and interactive scenarios for spec-driven farm live
  there ("User stories & scenarios") and in `docs/use-cases.md` §4a.

## Changes from the 2026-11-02 first draft

Driven by the branch review (findings N1–N18) and the research doc:

- Positioning: dropped the "close to one agent's tokens" success criterion.
- Planner refuses by default; structured reasons; code-enforced refuse rules; exit 8.
- `max_workers` default 3 (was 4).
- Worktrees moved from `~/.jas/farms/<id>/worktrees` (inside the deny floor)
  to `<repo>/.jas/worktrees/<farm>/<part>` (superseded by the second review:
  user cache dir, outside the repo); copy list, setup command, dirty-tree refusal.
- Board: typed topics, redacted, rounds-capped, delivered as fenced
  informational context at the tool-results boundary and recorded per
  delivery. This **revises the owner's earlier "auto-inject through
  `Session.Steer`" decision**: Steer enters as a user-role message and, after
  a final answer, re-wakes a finished worker with a fresh turn count. Same
  intent (auto-injected, no polling), different mechanism — owner to confirm.
- Verdict: supervisor runs `check` (was: integrator self-reports).
- CI: pinned exit codes 6/7/8, `--json`, `manifest.json`, no-TTY = keep;
  `on_failure: drop|abort` (was: any failure ends the farm in CI).
- Budgets gated before each call; farm total, integrator reserve, rate
  limiter, timeouts.
- Commit rule reconciled with design §8.2; plan hash approval for CI.
- Added (owner, later on 2026-11-02): Spec-driven planning, Interactive mode
  (inline, `/farm`), and the "web app from zero" worked scenario.
- Discussion mode replaced by a future `reviewer` part.
- Prerequisites in bet order and a farm-vs-solo benchmark gate.

Second review (2026-11-02, owner-approved fixes):

- **MVP cut** named (below); spec-driven planning, interactive mode, resume
  and take-over move to "after MVP".
- Board delivery wording corrected: on the chat-completions wire it **is** a
  user-role message (`encodeMessage` sends text in a tool-results message as
  `role: "user"`). What `Session.Inject` adds over `Steer` is the delivery
  point, the fence/label, and never re-waking a done worker.
- Budget gate needs a per-request output cap: `max_tokens` is added to the
  request (jas sends none today).
- Worktrees move **out of the repo** to the user cache dir: in-repo
  `.jas/worktrees/` is hidden from git but not from pytest, jest, tsc,
  eslint or file watchers.
- Undelivered `/farm attach` text is handed back, never dropped.
- Plan hash canonicalization defined.
- `suggest_farm` registered only when a farm can actually run.
- Exit 6/7/8 always carry a `reason`; merging an unmet farm needs a typed
  confirmation and leaves a commit trailer; planner input is an editable,
  visible task description.

Third update (2026-11-03, Asia/Tokyo; owner-requested after the Rob Shocks
orchestrator/worker video and the research doc's "verification beats debate"
finding):

- **Blast-radius tags** on plan parts (and optionally on spec feature
  sections): `low | medium | high`. They drive keep/merge policy and are
  recorded in `manifest.json` and farm events. After MVP; see Blast radius.
- **Post-integrate `fuzz` role**, distinct from the LLM `reviewer`: an
  optional automated hammer (the project's fuzz/e2e script against the
  integrated tree) that can fail the farm with reason `fuzz_failed`. After
  MVP; see Post-integrate steps and Roles.

## Positioning

Farm is **not** "better than one agent". The evidence says the opposite for
most coding work: cooperating agents succeed less often than one agent doing
both halves (CooperBench: ~30% lower; 2→4 agents 68.6% → 46.5% → 30.0%),
blackboard-style coordination is slower overall and writes more code
(CodeCRDT), and multi-agent systems spend far more tokens (Anthropic: ~15× chat
tokens vs ~4× for one agent; Claude Code agent teams ~7×). See *research*,
"Evidence".

Farm is for **tasks too big or too slow for one agent** — cross-cutting
migrations, codemods, test backfill per package, a feature across layers — and
its value is doing them **safely and auditably**: a human-approved plan, code
(not an LLM) coordinating, disjoint file ownership enforced in code, one
verified result to review, budgets, exit codes and a complete JSONL trail.
That headless-CI, audit-first packaging is the gap in the landscape
(*research*, "CI angle"). Farm is marketed as "faster and more reviewable on
decomposable work, at higher token cost" — never "near one agent's tokens".

The default answer of the planner is **don't split** (see Refusal).

## Intent

The old farm ran the **same** prompt in N worktrees and let the human keep the
best diff. It pays N times for one result. The owner wants a farm that
**splits one big task into parts, runs the parts in parallel, then
integrates them.**

Flow:

```
jas farm plan "task" -o plan.yaml
  → planner agent drafts plan.yaml or refuses        (LLM, once)
  → jas validates the plan + refuse rules             (code)
  → owner reviews / edits / approves (plan hash)      (human)
jas farm run plan.yaml
  → supervisor runs parts in DAG order                (code, no LLM)
      each part = one worker agent, own worktree + branch, one role
      workers post typed commitments to a board
  → integrator agent merges branches, fixes seams     (LLM)
  → supervisor runs `check` — the verdict             (code)
  → (after MVP) optional fuzz on the integrated tree  (role + command)
  → owner decides what happens to the integrated branch (human; no TTY = keep;
      blast-radius policy may require an extra confirm)
```

`jas farm "<task>"` is a thin interactive wrapper over the two commands.
Coordination is code, not an LLM. LLMs are used only where judgment is
needed: drafting the plan, doing a part, fixing merge seams.

### What success looks like

- The three acceptance scenarios below work end to end.
- ~~The benchmark gate (below) shows farm beating plain `jas run` on wall-clock
  for decomposable fixtures at equal or better `check` pass rate, with its
  token multiplier measured and reported (not gated; see Validation).~~
  Cancelled by the owner, 2026-11-07.
- Nothing merges into the owner's branch without the owner saying so.
- A farm run can be **audited** from its files, and an interrupted farm can be
  **resumed** with at-most-once side effects. (LLM output is not replayable;
  decisions, deliveries and verdicts are.)

## Goals

1. `jas farm plan` + `jas farm run`: plan → review → run → integrate → verify,
   CI-first; `jas farm "<task>"` for one terminal.
2. A planner agent drafts the plan; the owner reviews it before any worker
   starts. The planner **refuses by default** unless a split clearly helps.
3. Roles (coder, tester, researcher, reviewer, fuzz built in) define each worker's
   tools, write limits, prompt, model and budget. Roles are data.
4. Workers communicate one way through a **typed** board of commitments.
5. The supervisor, not a model, decides success by running `check`.
6. A pinned CI contract: exit codes, `--json` stream, `manifest.json`.
7. Works for any language: nothing language-specific is built into roles.

## Non-goals (this version)

- **Best-of-N.** Dropped from the roadmap.
- **Discussion / debate mode.** Replaced by an optional post-integrate `reviewer` step in the
  next version (see Next version). Debate rarely beats self-consistency at
  equal compute (*research*, "Evidence"). A post-integrate `fuzz` step is
  also planned (automated check, not debate); it is not discussion mode.
- **Free agent-to-agent conversation.** The board is typed one-way posts.
- **`jas attach` from another terminal over a Unix socket, RPC mode, any
  fullscreen dashboard.** Later versions. Running a farm from inside the
  inline chat is **in scope** (see Interactive mode) but ships after the CI
  path, on the same supervisor.
- **Per-task containers.** Still v3 (design §3.1); the opt-in OS sandbox is a
  prerequisite instead.

## Anti-goals (would make it a failure even if it works)

- An LLM orchestrator that re-reads farm state on every decision.
- An agent defining or widening policy: roles, tools, approval levels, write
  limits, ownership. Agents may only **draft** these for the owner to approve.
- Auto-merging into the owner's branch, or jas committing on a branch it did
  not create.
- Language-specific behaviour hard-coded into built-in roles.
- Running a farm without a `check` gate, or letting a model report the
  verdict.
- A board that becomes chat.

## MVP cut

The spec describes the full farm. The **first shippable version** is:

- `jas farm plan "<task>"` and `jas farm run <plan.yaml>` (plus the thin
  `jas farm "<task>"` wrapper), `status`, `clean`, `vars`.
- Roles `coder` and `tester`; typed board; supervisor-run `check`; exit codes
  6/7/8 with `reason`; `--json`; `manifest.json`; budgets and caps; TTY
  failure prompt with **retry / drop / abort** (no take-over).
- Prerequisites 3 (token/time caps, with `max_tokens`), 5 (supervisor-run
  `check`), 7 (trust injection, headless compaction, provenance meta), and
  **session-awareness rollout step 1 only** (records + stale cleanup +
  `jas sessions`): workers register records so a human session can see
  them. Overlap warnings (rollout step 3) are not needed for MVP; worker
  ownership is enforced by the plan and write limits.

**Shipped** in #95 (2026-11-03), with prerequisites 3, 5, 7 and
session-awareness step 1.

**After MVP**, once the validation pass is done, in this order (re-ranked
from the benchmark results): the post-integrate `reviewer` step,
the `researcher` role and `jas farm init`; resume (`jas farm resume`) and
take-over; interactive mode (`/farm`); **blast-radius tags** (policy +
log/manifest fields) and the post-integrate **`fuzz` step** (alongside, not
instead of, `reviewer`). Spec-driven planning (`--spec`) is held until the
spec-driven-flow spec is approved and built. Prerequisites 1, 2, 4 and 6
(event-log schema, provider fidelity, OS sandbox, and session-awareness
steps 2–3) remain bet-order work that farm benefits from; MVP does not wait
for them, but the
README marks farm experimental until they land (the benchmark gate was
cancelled, 2026-11-07).
**(owner-approved, 2026-11-02 review; blast-radius + fuzz owner-requested
2026-11-03; order, reviewer placement and `--spec` hold confirmed in the
2026-11-04 grilling)**

## Validation (owner-approved, 2026-11-04)

No new farm features until the MVP has been measured against plain
`jas run`.

1. **Dogfood.** One real farm on this repo: issues #48 (`core/replay.go`)
   and #51 (skills frontmatter tests), GLM, plan approved by the owner on
   a TTY. If the planner refuses (likely `small_change`), that is recorded
   and a hand-written two-part plan is run instead, so the run path is still
   exercised. Findings go to `docs/research/farm-dogfood-2026-11.md`;
   breakages become issues. Nothing merges without review.
2. **Benchmark harness.** `bench/farm.sh` beside `bench/run.sh`, fixtures
   under `bench/farm-fixtures/<scenario>/`: small multi-package Go repos with
   a working `check`, one per acceptance scenario plus one refusal twin.
   Each fixture runs `jas run` and `jas farm run` with a checked-in,
   pre-approved plan, 3 runs per arm, GLM. Reported: `check` pass rate,
   wall-clock, total tokens, merge conflicts, write-limit violations. Planner
   split/refuse is tested separately on the same fixtures, so plan variance
   does not leak into the run comparison.
3. **Gate.** Per split scenario, farm passes when its `check` pass rate is
   at least solo's **and** its median wall-clock is at most 0.7× solo's.
   Token multiplier is reported, not gated. A failing scenario becomes a
   refuse rule or a lower default.
4. **Spend.** About 30M tokens for the whole pass, enforced per run through
   `farm_tokens` / `--max-tokens`; stop and report when close.
5. **If farm loses everywhere:** freeze it as experimental, record the
   results in the research doc, no new farm features until it beats solo on
   at least one scenario.

## Prerequisites and benchmark gate

Farm is built **after** these land, in bet order (design §5 / §13):

1. **Event-log spec published** (JSON Schema for `events.jsonl`). The farm and
   board logs extend it; publish their schemas together.
2. **Provider fidelity** (#29 fixtures, Foundry matrix). N workers multiply
   any stream-reassembly bug; the shared rate limiter lives in the provider
   layer.
3. **Token and time caps in `jas run`**: `--max-tokens` checked **before**
   each request (estimated input + the request's output cap), and `--timeout`
   wall-clock; both end with exit **7** (CI contract below). jas sends no
   `max_tokens` today (`oaiRequest` has no such field), so the provider's
   default output size is unbounded from jas's view; this prerequisite adds
   a per-request `max_tokens` (config `max_output_tokens`, per role
   overridable) so the gate has a real upper bound.
4. **Opt-in OS sandbox** for shell (the real answer for parallel YOLO shells;
   worktrees are not isolation).
5. **Supervisor-run success check + success-verified benchmarks**: the same
   `check` mechanism, built once.
6. **Session awareness** (separate spec) — records, overlap warnings, the
   `.jas/` git-exclude plumbing farm reuses. Farm MVP needs rollout step 1
   (records) only; see MVP cut.
7. Small core changes: trust injection (§Trust), opt-in compaction for headless
   sessions (§Context), provenance meta in `session_started` (§Provenance).
8. **`jas --resume <log-path>`** (after MVP; needed by take-over): open a chat
   on a given session log in the current directory, honouring the session
   lock. Today only `--continue` / `/resume` exist.

**Benchmark gate — cancelled (owner, 2026-11-07).** Not being built; farm
stays experimental and defaults are tuned from use. The original text follows
for the record. Before farm is promoted in the README (beyond
"experimental"), a farm-vs-solo harness runs the acceptance scenarios on jas
fixtures, CooperBench-style: same task, same model, `jas farm run` vs
`jas run`, reporting `check` pass rate, wall-clock, tokens, merge conflicts and
contract violations. Defaults (`max_workers`, refuse thresholds) are tuned
from it; scenarios where farm does not beat solo stay "refuse".

## Acceptance scenarios

1. **Cross-cutting migration** — e.g. `log` → `slog` across packages.
   Planner splits by package, all `coder` parts, contract = the new call
   shape. Done when: each part's branch exists, the supervisor's `check` on
   the integrated branch passes, and the owner's (or default `keep`) decision
   is recorded.
2. **Feature across layers** — e.g. a field added end to end (core, CLI/API,
   UI, docs). Optional `researcher` part first; `coder` part per layer;
   `tester` after. Contract (types, names) in `plan.yaml`. Done when: as
   above, and at least one `interface` board post was delivered to another
   part (the board is exercised).
3. **Batch of small issues** — e.g. several open `[Low]` issues. Planner
   groups issues by the files they touch; issues touching the same files go
   in the same part. Done when: as above; each issue maps to exactly one part.

Each scenario also has a **refusal twin** (a single-package bug fix) where the
planner must refuse and suggest `jas run`.

## Commands

| Command | Does |
|---|---|
| `jas farm plan --spec spec.md [--sections F-a,F-b] [-o plan.yaml]` | Plan from an approved spec, optionally limited to some feature sections (see Spec-driven planning). |
| `jas farm plan "<task>" [-o plan.yaml]` | Draft, validate and (with a TTY) review the plan; on approval writes `plan.yaml` with `approved:` (hash). No workers. |
| `jas farm run <plan.yaml> [--json] [--allow-dirty]` | Run an approved plan. CI path; no plan prompt. |
| `jas farm "<task>"` | `plan` then `run` in one terminal. Without a TTY: usage error (exit 2) — use `plan` + `run`. |
| `jas farm status [<farm-id>]` | Print parts, status, tokens, check result from `manifest.json` (works offline / in CI). |
| `jas farm resume <farm-id>` | Continue an interrupted farm (see Failure, cancel, resume). |
| `jas farm clean [<farm-id>]` | Remove worktrees (and, with `--branches`, `jas/farm/<id>/*` branches); `git worktree prune`. |
| `jas farm init` | Detect variables and draft `.jas/farm.yaml` for review. |
| `jas farm vars` | Print each variable's value and its source. |
| `/farm …` (inside `jas` chat) | Interactive mode; see Interactive mode. |

## Configuration

`.jas/farm.yaml` (project) over `~/.jas/farm.yaml` (user) over built-in
defaults, merged key by key, most specific wins — the same layering as
design §10. The project file is subject to the project trust gate
(design §11): an untrusted project's `farm.yaml` is ignored with a notice
(it can define `check`, `setup` and role prompts, i.e. code execution and
instructions).

```yaml
check: "go build ./... && go test ./..."   # required; supervisor runs it
part_check: ""            # optional; unset = check; "" = off (see Supervisor)
vars:
  test_globs: ["*_test.go"]
  fuzz_command: ""            # optional; e.g. "npx playwright test" — enables fuzz role
  migrations_dir: db/migrations
split:
  max_workers: 3          # default 3; 2 is fine; >4 warns
  min_parts: 2
  disjoint_files: true
worktree:
  dir: ""                 # "" = <repo>/.jas/worktrees (see Worktrees); inside the repo only there
  copy: [".env.test"]     # gitignored files copied in (deny floor still applies)
  setup: "go mod download" # run once per worktree before the worker starts
budget:
  per_worker_tokens: 1500000 # cached prompt prefix counts in full; raised after dogfood run 2
  farm_tokens: 5000000
  max_output_tokens: 16000 # sent as max_tokens on every request; the gate's output bound
  integrator_reserve: 0.2  # fraction of farm_tokens held back for the integrator
  per_worker_timeout: 30m
  farm_timeout: 2h
  requests_per_minute: 0   # 0 = provider default; shared across workers
board:
  max_rounds: 3            # see Board
  post_chars: 500
  deliver_chars: 2000
on_failure: drop           # drop | abort (non-TTY policy)
models:
  planner: ""              # "" = session model
  integrator: ""
roles:
  migrator:
    tools: [read, grep, edit, write, shell]
    writes: ["${migrations_dir}/**"]
    prompt: "Write database migrations only."
```

Numbers above are defaults **(author's call; tune with the benchmark gate)**.
`max_workers` defaults to 3, not 4: success falls as agents are added and
practitioners report a 3–5 ceiling (*research*, "Risks").

### `check` is required

The verdict. If `check` is unset after all layers and detection, **the farm
refuses to start** (exit 2) and tells the owner to set it. (owner's decision)
jas warns when `check` looks trivial (e.g. build only, or no test files under
the packages the plan owns).

### Variables

A small fixed set of built-in names, plus owner-defined names under `vars:`.

| Name | Meaning |
|---|---|
| `check` | verdict command (also settable top-level) |
| `test_globs` | test file patterns |
| `src_globs` | non-test source patterns |
| `build` | build command (optional) |
| `lint` | lint command (optional) |
| `fuzz_command` | post-integrate fuzz/e2e command (optional; after MVP) |

Resolution, highest wins: project `vars:` → user `vars:` → detection → unset.
Detection is deterministic (no LLM), from marker files:

| Marker | check | test_globs |
|---|---|---|
| `go.mod` | `go build ./... && go test ./...` | `**/*_test.go` |
| `package.json` | `npm test` (+ `npx tsc --noEmit` if `tsconfig.json`) | `**/*.test.*`, `**/*.spec.*`, `**/__tests__/**` |
| `pyproject.toml` / `requirements.txt` | `pytest` | `**/test_*.py`, `**/*_test.py`, `tests/**` |
| `Cargo.toml` | `cargo build && cargo test` | `tests/**` (unit tests live in source; see tester role) |

Monorepos: markers in subdirectories produce per-path `check` entries; the
supervisor runs the checks for directories the integrated diff touches.
**(author's call)**

Validation at load time: an unknown `${name}` is a config error (exit 2); a
known but unset variable makes every role using it **unavailable**, and the
planner is not offered unavailable roles.

### `jas farm init`

Runs detection, then an agent drafts `.jas/farm.yaml`. The draft is shown to
the owner and saved only on approval.

## Roles

A role is data: `tools`, `writes` (globs), `prompt`, `approve` level,
`model`, `budget`. Layers: built-in → user → project (most specific wins,
whole-role replacement **(author's call)**).

| Role | tools | writes | Notes |
|---|---|---|---|
| `coder` | read, grep, write, edit, shell | the part's `owns` | default role |
| `tester` | read, grep, write, edit, shell | paths matching **both** `${test_globs}` and `owns` | "Write and fix tests only; if non-test code must change, post `blocked`." Unavailable if `test_globs` unset. |
| `researcher` | read, grep (+ web tools once shipped, design §12.1) | none | output: `interface`/`note` posts and its final answer |
| `reviewer` | read, grep | none | **Post-integrate only** (not a DAG part). LLM, clean context, may use a different/cheaper model; output: findings in its final answer, sent to the integrator for one fix round. After MVP. |
| `fuzz` | read, grep, ls, shell | none | **Post-integrate only** (not a parallel DAG part). Runs `${fuzz_command}` (or the role prompt's script) against the integrated tree; reports via final answer + board `note`. Unavailable if `fuzz_command` unset. Distinct from supervisor `check` and from LLM `reviewer`. After MVP. |

Rust and similar: when a tester cannot be limited by `test_globs`, the planner
gives it the coder's `owns` and orders it `after` that coder.
**(author's call)**

**Who defines roles:** the owner (config). The planner only assigns existing,
available roles. It may *propose* a new role in the draft (`proposed_roles:`);
the owner approves or drops it during review. `jas farm run` rejects any plan
whose roles are not in trusted config or approved in that plan's hash (§Plan
trust). Agents never create policy.

### Write limits are enforced in code

`writes:` is enforced by the file tools (`write`, `edit`) as a new
`ExecPolicy` allow-list, like project-root confinement: a call outside the
limit gets a `denied` tool result. Matching: repo-relative paths; a delete or
rename counts as a write on both old and new paths. `shell` cannot be
confined in-process (design §3.1), so the supervisor also checks each part's
final diff (`git diff --name-status base..branch`, untracked files included)
against its limit; files changed outside it fail the part (policy violation).

## The plan

The planner is an LLM agent with read-only tools (read, grep) on the repo at
the base commit. `base` is the current `HEAD`. **A dirty tree is refused**
(exit 2) unless `--allow-dirty`, in which case uncommitted changes are *not*
part of the farm and the warning is logged. **(author's call)**

```yaml
task: "migrate logging to slog"
base: <commit sha>
contracts: |
  All packages call slog.Default(); no package-level loggers.
  Logger keys are snake_case.
interfaces:              # structured contracts the integrator checks
  - name: logging.New
    owner: core
    shape: "func New(level slog.Level) *slog.Logger"
parts:
  - id: research
    role: researcher
    goal: "Find slog idioms and gotchas for this codebase; post findings."
    blast_radius: low            # optional; see Blast radius (after MVP)
  - id: core
    role: coder
    goal: "Migrate core/ to slog."
    owns: ["core/**"]
    after: [research]
    blast_radius: medium
  - id: cli
    role: coder
    goal: "Migrate cmd/ to slog."
    owns: ["cmd/**"]
    after: [research]
    blast_radius: medium
  - id: tests-core
    role: tester
    goal: "Update core tests for the new log output."
    owns: ["core/**"]
    after: [core]
    blast_radius: low
shared: ["go.mod", "go.sum"]   # owned by the integrator only
post_integrate: [fuzz]         # optional; after MVP — see Post-integrate steps
why_split: "4 packages, no shared files beyond go.mod; ~2x wall-clock expected"
proposed_roles: {}
approved:                # written by `jas farm plan` on owner approval
  sha256: <canonical plan hash; see Plan trust in CI>
  by: owner
  at: 2026-11-02T23:30:00+09:00
```

### Refusal (the default)

The planner must either split with a `why_split` justification or refuse with
a **structured reason**:

```yaml
refuse:
  reason: small_change     # small_change | single_package | overlapping_ownership |
                           # serial_dependencies | no_check | unclear_task |
                           # no_clear_contract (spec-driven; see Spec-driven planning)
  detail: "Single bug in one function; splitting would duplicate the investigation."
suggest: "jas run \"...\""
```

Code enforces refusal too, even if the planner splits:

- fewer than `split.min_parts` writing parts, or all writing parts under one
  package/directory root → `single_package`;
- concurrently runnable writing parts whose `owns` overlap → `overlapping_ownership`;
- the DAG's longest chain covers every writing part (no two can run at once) →
  `serial_dependencies`;
- no `check` resolved → `no_check`;
- planner estimate below a small-change threshold (files touched)
  **(author's call)** → `small_change`.

A refusal exits **8** and prints the `jas run` suggestion. Refusal is a
normal, cheap outcome, not an error.

### Validation (code, before the owner sees it)

- Every `role` exists and is available; every `after` id exists; no cycles.
- `owns` is non-empty for writing roles. Overlap is checked by expanding globs
  against the file list at `base`, plus a prefix rule for new files (a new
  file belongs to the part whose `owns` literal prefix is longest).
- `shared` files are writable only by the integrator; workers that need a
  change there post `blocked` or `interface`.
- A plan that fails validation goes back to the planner once with the errors;
  a second failure is shown to the owner (or exits 2 without a TTY).

### Blast radius (after MVP)

Optional tag on each plan part: `blast_radius: low | medium | high`
**(author's call on the enum)**. Omitted parts default to `medium`
**(author's call)**. The tag is advisory for the planner and **policy for
the supervisor** after the verdict path.

**Spec-driven inheritance.** When planning from a spec, a feature section may
declare the same tag (see spec-driven-flow). The planner copies a section's
tag onto every writing part that covers it. A part that covers several
sections takes the **max** severity. A part may **raise** severity above its
sections' max (e.g. the planner knows the change hits auth wiring); it may
**not lower** it. Validation fails if a section tagged `high` is covered only
by parts that omit the field (inheritance must materialize the tag).

**Policy** **(author's call)**:

| tag | after supervisor `check` (and fuzz, if any) passes |
|---|---|
| `low` | keep and merge choices same as today (interactive offers merge; no-TTY = keep). |
| `medium` | interactive: short confirm before merge is offered (`merge medium-blast? y/n`). CI / no-TTY: always **keep** (unchanged). `--accept-medium-blast` records owner acknowledgment on `farm_ended` / the manifest for humans and PR bots; it does **not** enable auto-merge (auto-merge remains forbidden without a TTY). |
| `high` | never treat as routine: interactive still requires a typed `merge high-blast` confirm even when check passes; CI / no-TTY stays at keep with `blast_gate: high` recorded. The gate is always logged. |

**Recorded fields.** Each part's tag is written into `manifest.json` under
that part. `part_started` carries `blast_radius`; `farm_ended` carries
`blast_radius_max` (the max over required writing parts) and, when a gate
applied, `blast_gate: medium|high`. Aggregate max uses `low < medium < high`.

**Planner guidance (prompt, not a hard code rule).** If the task clearly
touches auth, payments, secrets, or destructive data paths and the draft
would tag every writing part `low`, the planner should refuse
(`unclear_task` or a future `under_tagged_blast`) or force at least one
`high` part. Code may later warn when `owns` globs match well-known sensitive
paths (`**/auth/**`, `**/billing/**`, …) and every tag is `low`; that warning
is left to the builder.

MVP ignores unknown `blast_radius` fields (forward compatible) and does not
enforce the policy.

### Review

The owner sees a summary (parts, roles, owns, shared, order, contracts,
interfaces, proposed roles, `why_split`, blast-radius tags when present) and
chooses: **approve** (writes `approved:`), **edit** (opens `$EDITOR`,
re-validates), **replan** with feedback, or **quit**.

### Plan trust in CI

`jas farm run` recomputes the plan hash and requires it to equal
`approved.sha256`; otherwise exit 2.

**Canonical hash:** sha256 of the file's bytes after (1) removing the
top-level `approved:` key and its indented block, (2) normalizing line endings
to LF, and (3) stripping trailing whitespace at end of file. No YAML
re-serialization: comments and formatting are part of what the owner
approved, and any edit (including in `$EDITOR` during review) requires
re-approval. It also requires `base` to be an ancestor
of (or equal to) `HEAD`, rejects `proposed_roles` unless they are inside the
approved hash, and resolves role definitions from **trusted** config only — a
plan cannot widen tools, `writes`, approval levels or ownership beyond what
was approved. The hash, base and resolved role digests are recorded in
`farm_started`. A plan file from an untrusted source (e.g. a fork PR) must be
re-approved by a human. **(author's call)**

## Spec-driven planning

With `--spec spec.md` (an approved spec from
[`jas spec`](2026-11-02-spec-driven-flow.md)), the planner splits **by spec
sections** instead of free text:

- `jas spec --check` runs first; an incomplete or unapproved spec stops with
  exit 2 and the list of open problems (spec-driven-flow spec).
- Each writing part maps to one or more feature sections (`F-…`) and carries
  their acceptance criteria (`AC-…`) and the section's owned-path hints as
  its `owns` (the planner may narrow, never widen, a hint without saying why
  in `why_split`). The plan records `spec: {path, sha256, sections}`, so the
  approved plan hash pins the spec hash too. After MVP, a section may declare
  `blast_radius`; the planner inherits it onto covering parts (see Blast
  radius).
- The spec's Data model and API contract become plan `contracts` and
  `interfaces` verbatim. Generated clients, schema files and manifests
  (`package.json`, `go.mod`, lockfiles) go to `shared:` (integrator-owned).
- **Extra refuse rule:** `no_clear_contract` — features whose sections share
  entities or routes with no API contract / data model entry between them
  cannot be split; the planner refuses (exit 8) or merges them into one part.
- **Part done-check:** besides the worker's final answer, a part is done only
  when its criteria table (spec-driven-flow, Traceability) lists every
  assigned `AC-` as met with evidence, and the part's own scoped test command
  (`part_check`, derived from the criteria's `test:` references, e.g. one
  e2e file) passes in its worktree, run by the supervisor. Unmet criteria
  fail the part (normal failure flow).
- **Final verdict:** the supervisor runs `check` **and** `contract_check`
  (if the spec defines it) on the integrated branch; both must pass for exit
  0. The manifest lists every `AC-` with status and evidence.
- A scaffold phase (`jas run --spec … --phase scaffold`) normally precedes
  the farm, so parts start from a building, tested skeleton with stubs.

## Worktrees

- Location: `<repo>/.jas/worktrees/<farm-id>/<part-id>`, git-excluded
  (`/.jas/worktrees/` in `.git/info/exclude`). Configurable with
  `worktree.dir` (absolute), e.g. a cache dir outside the repo.
  **(owner's decision, 2026-11-04; reverses the second review's move to the
  user cache dir.)**
- **Why in the repo is acceptable:** the dot-dir is skipped by the tools that
  matter most (Go's `./...`, pytest's default `norecursedirs`, tsc's
  wildcards), it is easy to find, and it goes away with the repo. Tools that
  do scan dot-dirs (ESLint 9 flat config, some watchers, Docker `COPY . .`
  without `.dockerignore`) can see the parts' half-done copies; such
  projects should ignore `.jas/` or set `worktree.dir` outside the repo.
- **Load-time validation** of the resolved directory, default or configured,
  after resolving symlinks: inside the main checkout (or any of its linked
  worktrees) only `.jas/worktrees` is allowed, and it must not be inside
  `~/.jas` or `JAS_HOME` (the deny floor would refuse every file tool in its
  own root). A violation is a config error (exit 2) naming the rule.
- Sessions whose root is the main checkout deny file-tool access to
  `.jas/sessions/**` (session awareness) and `.jas/worktrees/**`, so a chat
  does not read or edit a part's half-done copy.
- Branch: `jas/farm/<farm-id>/<part-id>`; integrator `jas/farm/<farm-id>/integrate`.
- A part with no deps branches from `base`; a part with deps branches from
  `base` with its deps' branches merged in. A conflict there fails the part.
- Worktrees are created serially by the supervisor; `git worktree prune` runs
  at start.
- `worktree.copy` copies listed gitignored files from the main checkout; each
  path still passes the deny floor (so `.env`, keys and `secrets/` cannot be
  copied unless the owner's deny overrides allow it — they don't by default).
- `worktree.setup` runs once per worktree (logged, with its exit code) before
  the worker starts; failure fails the part.
- Each worker gets its own `TMPDIR` (`<system temp>/jas-farm/<farm-id>/<part-id>`,
  made with the worktree, removed with it; never inside a checkout, or the
  project's own tests that expect a plain temp dir fail; dogfood run 2) and
  `GIT_CONFIG_NOSYSTEM=1`; repo hooks do not run on jas's own commits
  (`--no-verify`). Ports and shared services are the project's problem
  (document; sandbox prerequisite).
- Cleanup: worktrees are removed after the final choice; kept on failure or
  interrupt until `jas farm clean`.

## Running

### Supervisor

Plain Go, inside the `jas farm` process. Each worker is a `core.Session`
(goroutine) with its own event log, worktree and role policy.

- Starts parts whose `after` deps are done, up to `max_workers` at a time.
- Worker system prompt = role prompt + part goal + plan `contracts` +
  `interfaces` + the list of other parts and what they own.
- **Worker lifecycle:** a worker is *done* when it gives its final answer
  **and** `part_check` passes in its worktree. `part_check` defaults to
  `check`; set it to a narrower command, or `""` when parts cannot pass
  `check` on their own (the check only holds on the merged tree). A failed
  `part_check` (logged as `part_check{part, round, exit_code}`) goes back to
  the worker once with the output tail; failing again fails the part with
  reason `check`. **(owner-approved, 2026-11-04, after the dogfood run: a
  part was marked done with code that did not compile.)** The supervisor
  then closes the session (no steering is drained after the
  final answer), commits its changes on the part branch (see Commits), runs
  the write-limit diff check and marks the part done. A done part is never
  re-woken by board posts.
- Tracks tokens per worker and per farm (see Budgets).
- Approvals: each worker's approval level comes from its role (`approve`),
  defaulting to the configured level. With a TTY, prompts from parallel
  workers are serialized and labelled with the part; other workers keep
  running. Without a TTY a gated call fails the part (exit 5 in the manifest),
  then `on_failure` applies.
- Output (`jas farm run`): plain inline lines printed on change (part
  started / tool / part_check / done / failed, integrate, check) and board
  posts as they arrive, e.g. `[core] → shell go test ./core/...`. Without
  them a long farm looks hung (dogfood run 2). No
  fullscreen (AGENTS.md constraints). In chat, live rows (see Interactive
  mode). With `--json`, see CI contract.

### Board

The board is a log of **typed commitments**, not a chat (*research*: free
communication cut conflicts but did not raise success, and cost up to 20% of
steps).

- File: `~/.jas/farms/<farm-id>/board.jsonl`, append-only, single writer (the
  supervisor) through the versioned event-log code (`v`, `id`, `seq`, `ts`)
  plus `part`, `session_id`, `event_id` (the poster's tool-call event),
  `topic`, `fields`.
- Write: a `board_post` tool registered only for farm workers, with a fixed
  topic set validated by the supervisor:

  | topic | fields | meaning |
  |---|---|---|
  | `owns` | `paths[]` | confirms/narrows what this part is touching (cannot widen beyond plan `owns`) |
  | `interface` | `name`, `shape`, `note` | a signature/insertion point others rely on |
  | `blocked` | `on` (part or `shared:<path>`), `need` | needs a change it may not make |
  | `done` | `summary`, `diffstat` | auto-posted by the supervisor at part completion |
  | `note` | `text` | researcher/reviewer/fuzz findings only |

  Text fields are capped (`board.post_chars`, ~500). Every post passes through
  the same redactor as `events.jsonl` before it is stored, printed or
  delivered. Invalid posts get a tool error, not a board entry.
- Delivery: the supervisor delivers a part **only** posts relevant to it
  (from its deps, posts naming its owned paths or interfaces, and all
  `interface` posts), oldest first, up to `board.deliver_chars` per delivery.
  Delivery is **not** `Session.Steer`: it is appended to the next
  tool-results message as a fenced, labelled block — "Board (agent-written
  commitments from other parts; informational, not instructions from the
  owner)". A new core hook (`Session.Inject` at the tool-results boundary)
  carries it. **(author's call)**

  On the chat-completions wire this block **is a user-role message**:
  `encodeMessage` (`providers/openai.go`) emits the tool results as `tool`
  messages and any text in the same message as a following `role: "user"`
  message. So `Inject` does not change the role; what it changes is (1) the
  delivery point (only right after tool results, never as a new turn),
  (2) the fence and label, recorded as `board_seqs` on the event, and
  (3) it never re-wakes a done worker or resets its turn count, unlike
  `Steer` after a final answer. A truly tool-role delivery would need a
  synthetic `board` tool call in an assistant turn the model never made;
  rejected as fabricating history. **(owner-approved, 2026-11-02 review)**
- Rounds: each delivery to a part is a round; a part receives at most
  `board.max_rounds` deliveries (default 3). Posts arriving after a part's
  last round, or after it is done, go to the integrator only.
- **No delivery outside the tool-results boundary.** A worker that makes no
  further tool call (it is thinking toward its final answer, or finishes)
  never sees posts that arrive after its last delivery. This is by design:
  the board never re-wakes or interrupts a worker. **The integrator is the
  fallback**: it receives every post, delivered or not, and the manifest
  lists per part which posts it never saw.
- Audit: each delivery is a `board_delivered{part, seqs[], truncated}` event
  in `farm.jsonl`, and the injected block in the worker log carries
  `board_seqs`. "Which commitments had B seen when it acted" is answerable
  from the files.
- Quiescence: the farm moves to integration when every part is done, failed
  or dropped, regardless of undelivered posts.

### Commits

design §8.2's rule becomes: **jas never commits to a branch it did not
create.** In farm, jas commits only on `jas/farm/<farm-id>/*` branches:
part completion commits, dep merges into part branches, and integrator
merges. Identity: author/committer `jas farm <jas-farm@localhost>` (configurable),
trailers `Jas-Farm: <id>` and `Jas-Part: <part>`; `--no-verify`. The owner's
branch is touched only by the owner's explicit **merge** choice (a `--no-ff`
merge, reversible), never by default and never without a TTY.

### Integrator

An LLM agent with the coder's tools (plus write access to `shared`) on
`jas/farm/<farm-id>/integrate` from `base`.

1. Merges **leaf** part branches of the DAG (each leaf already contains its
   deps), ties by plan order; branches of dropped parts are skipped.
2. Resolves conflicts and `blocked` requests, using the plan, `interfaces` and
   the board.
3. May run `check` itself to iterate, within its budget.

When every leaf merges cleanly the integrator session is skipped, unless the
supervisor's `check` on the merged tree fails (logged as `integrate_check`):
then the integrator runs with that output to fix the seams, and the verdict
`check` runs after it as usual. **(owner-approved, 2026-11-04, dogfood)**
4. Ends with a summary: parts merged/dropped, conflicts fixed, interfaces
   honoured/violated, per-part diffstat.

Then the **supervisor runs `check`** in the integrate worktree (with
`per_worker_timeout`) and records `check_ran{cmd, exit_code, duration, output}`
(output redacted and truncated). That result — not the integrator's summary —
is the verdict and drives the exit code.

Final choice: with a TTY the owner picks **merge** (into the branch they
started on), **keep** (e.g. open a PR themselves) or **discard**, subject to
blast-radius policy when that ships (see Blast radius). Without a
TTY the choice is always **keep**. Worktrees are removed after the choice;
branches stay unless discarded.

### Post-integrate steps (after MVP)

Parallel `parts:` run **before** the integrator. Anything that must see the
merged tree is a **post-integrate step**, not a DAG part. Declared on the
plan as:

```yaml
post_integrate: [fuzz, reviewer]    # optional list; unknown names are a validation error
```

A plan part with `role: fuzz` or `role: reviewer` inside `parts:` is a
validation error — both are scheduled here, not in the DAG.
**(owner-approved, 2026-11-04)**

**Supervisor order after the integrator finishes** **(author's call)**:

1. Integrator completes (merged tree on `jas/farm/<id>/integrate`).
2. Supervisor runs **`check`** (required; today's verdict).
3. If `check` passed and `post_integrate` lists `fuzz` and the `fuzz` role is
   available (`fuzz_command` set): run the **fuzz** step on the same integrate
   worktree.
4. If `post_integrate` lists `reviewer`: run the **reviewer** step on the
   integrated diff. Findings go to the integrator for **one** fix round;
   the supervisor then re-runs `check` (and fuzz, if listed). A failure
   there ends the farm with exit 6 as usual. No findings → no fix round.
5. Apply blast-radius gate (if tags present), then owner / policy final choice.

Unit/`check` therefore gates expensive e2e. If `check` failed, fuzz is
skipped and the farm ends with exit 6 / `check_failed` as today.

**Fuzz step.** The supervisor starts a short-lived session with the `fuzz`
role (read, grep, ls, shell; writes: none) whose goal is to run
`${fuzz_command}` (or follow the role prompt) against the integrated tree /
preview surface, then summarize. Preferred path: the role prompt tells the
agent to invoke the configured command and report exit code + failures; the
supervisor also records `fuzz_ran{cmd, exit_code, duration, output}` like
`check_ran`. Non-zero → farm exit **6** with `reason: fuzz_failed` (distinct
from `check_failed` for audit). Exit 0 → continue to the blast-radius gate /
final choice.

**Distinct from `check`.** `check` is the owner-required command
(tests/lint/build) and is always run. Fuzz is optional, agent-or-script
exploration of edge cases (Playwright, property tests, a project fuzz
target). **Distinct from `reviewer`.** Reviewer is an LLM read of the diff;
fuzz is code-driven. A plan may request both (`post_integrate: [fuzz,
reviewer]`); the order is always integrate → check → fuzz → reviewer,
whatever the list order. **(owner-approved, 2026-11-04)**

Until this ships, `post_integrate` is not implemented: a plan that includes
it should be rejected at validation (fail closed). Examples in this doc show
the after-MVP shape.

## Budgets

- **Gated before each call**: the supervisor's shared budget estimates
  `input + max_tokens` for every request and refuses the request if it would
  cross the worker's `per_worker_tokens` or the farm's remaining budget
  (fixing the "fleet cap overshoots by one call per worker" problem,
  *research*).
- `farm_tokens` covers planner, workers and integrator. `integrator_reserve`
  is held back and only the integrator may spend it.
- Per-worker cap hit → the part fails (`budget`). Farm cap hit (non-reserved
  pool exhausted) → stop scheduling new parts, let running parts end their
  current request, then integrate what is done; if even the reserve is
  exhausted, the farm ends with exit 7.
- Time: `per_worker_timeout` fails a part; `farm_timeout` stops the farm
  (exit 7, `reason: timeout`).
- One shared rate limiter per provider/key across all workers; 429 back-off
  is shared, not per request.
- `manifest.json` and the final report show tokens per part, per role and
  total, and the multiplier vs the planner's single-agent estimate.

## Failure, cancel, resume

Failure = the worker errors out, hits its token/time budget or turn cap,
writes outside its limit, needs approval without a TTY, or its branch/setup
can't be prepared. (owner's decision)

- Parts that depend on the failed part (directly or transitively) pause.
  Independent parts keep running.
- With a TTY, the owner is asked per failed part: **retry** (fresh session,
  told why the last try failed), **take over**, **drop** (part and dependents),
  or **abort**. An answer that names none of them (a whole word or prefix) is
  asked again, never guessed. **(dogfood, 2026-11-04: "end" was taken as
  drop.)**
- Without a TTY, `on_failure` decides: `drop` (default) drops the part and its
  dependents, finishes the rest and integrates what remains; `abort` cancels
  all workers. Either way the farm cannot exit 0 if a part was dropped
  (exit 6), and all branches are kept.
- **Take over:** the supervisor closes the part's session, prints
  `jas --resume <log-path>` (prerequisite 8) to run in the part's
  worktree, waits for the owner to press Enter, then re-runs the write-limit
  check and commit before marking the part done.
- **Ctrl-C** once: stop scheduling, let running requests finish, close
  sessions, write `farm_ended{status: interrupted}`, exit 130. Twice: cancel
  contexts immediately (process-group kill for shells).
- **Crash / resume:** `jas farm resume <farm-id>` reads `farm.jsonl` and
  `manifest.json`, re-validates the plan hash, resumes unfinished parts
  through `ResumeSession` (write-ahead `tool_started` keeps side effects
  at-most-once), and continues the DAG. Board delivery state is rebuilt from
  `board_delivered` events.

## CI contract

### Exit codes

Extends design §3's table; existing codes keep their meaning, farm-level codes
never reuse 3/4/5 (those stay per-part, in the manifest).

| code | `jas farm run` / `jas farm` | `jas farm plan` |
|---|---|---|
| 0 | every required part done and the supervisor's `check` passed | plan written (and approved with a TTY) |
| 1 | internal, provider-after-retries, or git failure | same |
| 2 | usage/config error: bad flags, no `check`, invalid or unapproved plan or spec, hash mismatch, dirty tree without `--allow-dirty` | same |
| 6 | **farm unmet**: `check`/`contract_check`/fuzz failed, or a part failed/was dropped (also `jas run --spec` scaffold/section when jas's `check` fails) | — |
| 7 | **budget exceeded**: farm token pool + reserve or `farm_timeout` exhausted (also used by `jas run --max-tokens/--timeout`) | planner hit its budget |
| 8 | — | **planner refused** to split (suggests `jas run`), with or without a TTY; also `jas farm "<task>"` when the planner refuses (no workers start) |
| 130 | interrupted | interrupted |

The same code appears in `farm_ended`, always with a `reason`, because one
code covers several causes: 6 → `check_failed` | `contract_check_failed` |
`fuzz_failed` | `part_failed` | `part_dropped` | `criteria_unmet`; 7 →
`tokens` | `timeout`; 8 → the refusal reason (`small_change`, …).
`manifest.json` repeats it. design §3's table gains rows 6, 7, 8.
`fuzz_failed` is after MVP; until then the reason set is unchanged.

### `--json`

`--json` streams **only** `farm.jsonl` events to stdout (never interleaved
worker events). Each farm event carries `farm_id`; worker detail is linked by
`session_id` and log path. Event types (versioned per design §3):
`farm_plan_refused` (planner log; reason, suggest), `farm_started` (plan hash,
spec hash, base, roles digest, config digest, trust decision), `farm_resumed`,
`part_started` (includes `blast_radius` when set), `part_setup`,
`part_ended` (status, exit code, tokens, diffstat), `board_posted`,
`board_delivered`, `owner_decision`, `part_check`, `integrate_started`,
`integrate_check`, `integrate_ended`, `check_ran`, `fuzz_ran` (after MVP; same shape as `check_ran`),
`budget_stop`, `farm_ended` (exit code, reason, totals, `blast_radius_max`,
optional `blast_gate`).

### `manifest.json`

Written atomically (temp + rename) on every part transition, under a lock:
farm id, plan hash, base, per part {role, branch, worktree, session id, log
path, status, exit code, tokens, diffstat, failure reason, `blast_radius`},
integrator result, `check` result, optional `fuzz` result, `blast_radius_max`,
optional `blast_gate`, final choice, totals. `jas farm status` and CI read it.

## Interactive mode

> **Superseded in detail by
> [`2026-11-04-farm-interactive-design.md`](2026-11-04-farm-interactive-design.md)**
> (one-keystroke flipping, `/farm` dashboard, approval and prompt routing).
> Where the two differ, that document wins; the text below is the original
> sketch.

Same supervisor, same logs, same exit-code semantics as `jas farm run`; only
the front end differs. Ships after the CI path. Inline only (AGENTS.md): no
alt-screen, no fullscreen dashboard.

- **Launch:** `/farm "<task>"`, `/farm --spec spec.md`, or `/farm run plan.yaml`
  from the chat. The chat agent may *suggest* a farm (a `suggest_farm` tool)
  when a task looks big and splittable; it never starts one itself.
  - `suggest_farm` is registered only when a farm could run here: the project
    is trusted and a `check` resolves. Otherwise its schema would ride along
    on every chat request for nothing.
  - Its argument is a **task description the chat agent drafts** from the
    conversation. jas pre-fills the input box with `/farm "<description>"`;
    the owner edits or deletes it before sending.
  - The planner gets only that task text (and spec), never the chat history:
    the owner sees and controls exactly what is sent, and the plan stays
    reproducible from its inputs.
- **Plan review inline:** the plan summary is printed into scrollback;
  `a` approve · `e` edit in `$EDITOR` · `r` replan with feedback · `q` quit.
- **Refusal returns to chat:** the structured reason prints inline and the
  task is offered as the next chat message (`run it here? y/n`); no farm
  state is created beyond the planner's log.
- **Live rows:** while the farm runs, the inline view (below scrollback,
  re-rendered in place, height = parts + 1) shows one row per part —
  status, current tool, tokens, last board post — plus a totals row
  (tokens / budget, elapsed, `check` pending). Finished events (part done,
  failures, board posts) are printed to scrollback once. Narrow terminals
  truncate columns, never wrap.

  ```
  backend-auth   ● running  edit server/auth/session.go   61k   ready: POST /api/login
  backend-api    ● running  shell go test ./server/api/... 74k   blocked: need user.id
  frontend-pages ◌ paused   —                              38k   —
  e2e-tests      · queued   —                               0k   —
  farm 173k / 1.5M tok · 12m · check pending
  ```
- **Approvals:** prompts from parallel workers are serialized and labelled
  `[backend-api] shell: npm install zod`; other parts keep running. Options:
  yes · no · always **for this part** · always **for this farm**. "Always"
  never outlives the farm and never applies to other sessions.
- **Steering:** `/farm attach <part>` routes the input box to that running
  worker (messages enter through `Session.Steer` — these are genuinely the
  owner's words, so user role is correct, unlike board posts); `/farm back`
  returns to the main chat. `/farm pause <part>` (stops at the next turn
  boundary), `/farm resume <part>`, `/farm drop <part>` (drops it and its
  dependents), `/farm status`. Attaching to a done part is refused (done
  parts are never re-woken); use take-over on failure instead.
  - **No silent loss:** if a worker gives its final answer while attached
    text is still queued, the supervisor takes it with
    `Session.TakeSteering()` when closing the session, prints
    `[part] not delivered (part finished): <text>` and puts the text back in
    the input box. The drop is recorded as
    `owner_decision{choice:"steer_undelivered"}`.
- **Failures** prompt inline with the same retry / drop / abort choices as
  the TTY flow (take over arrives with resume, after MVP).
- **Finish:** after the supervisor's `check`, an inline summary — per-part
  status and diffstat, criteria met/unmet (spec runs), tokens and cost per
  part and total, `check`/`contract_check` result — then `m` merge · `k` keep
  · `d` discard. An unmet outcome (dropped part, failed check) still offers
  merge, but only as a deliberate act: `m` asks the owner to type
  `merge anyway`, and the merge commit carries the trailer
  `Jas-Farm-Status: unmet (<reason>)` so the failure is visible in `git log`,
  not only in `farm.jsonl`. The choice and the unmet status are both
  recorded. Without a TTY the result is only ever kept. The outcome code
  is written to `farm_ended` (the chat process does not exit).
- **Ctrl-C** in the farm view behaves as in CI (first: graceful stop; second:
  hard cancel) and returns to the chat prompt; `/farm resume <farm-id>` or
  `jas farm resume` continues later.

## Worked scenario: web app from zero

Expectations, not measurements (the benchmark gate was cancelled, 2026-11-07).

1. **Spec.** `jas spec spec.md`: grill-me interview → approved spec (stack:
   Go API + TypeScript web; data model; OpenAPI contract; pages; features
   F-auth, F-todos, F-pages with acceptance criteria and owned-path hints).
2. **Scaffold (single agent).** `jas run --spec spec.md --phase scaffold`:
   layout, build, test harness, CI, `check`/`contract_check`, generated API
   stubs (501) and typed clients, empty pages. jas runs `check` → 0.
3. **Plan.** `jas farm plan --spec spec.md -o plan.yaml` (or `/farm --spec
   spec.md`):

   | part | sections | owns | after |
   |---|---|---|---|
   | backend-auth | F-auth | `server/auth/**` | — |
   | backend-api | F-todos (API) | `server/api/**`, `db/migrations/**` | — |
   | frontend-pages | F-pages, F-todos (UI) | `web/pages/**`, `web/components/**` | — |
   | e2e-tests | all AC with `test: tests/e2e/…` | `tests/e2e/**` | backend-auth, backend-api, frontend-pages |

   `shared:` `package.json`, `go.mod`, lockfiles, `web/src/api/generated/**`
   (owned by the integrator).
4. **Parallel run.** Three parts at once (`max_workers: 3`). Typed board
   posts carry the seams, e.g. backend-api `interface {name: "GET /api/todos",
   note: "ready, returns Todo[]"}`; backend-api `blocked {on: backend-auth,
   need: "user.id on request context"}`; backend-auth `interface {name:
   "auth.UserID(ctx)"}` unblocks it. Each part finishes only when its scoped
   tests and assigned AC pass.
5. **Integrate.** The integrator merges, runs `codegen` to regenerate the
   typed clients from the final contract, updates shared manifests and
   lockfiles; the supervisor runs `check` + `contract_check` → 0; owner picks
   merge/keep/discard with diffstat and cost shown.

Where it is expected to help: roughly **2–3× faster wall-clock** for the first
cut of the features than one agent doing them in sequence, and smaller,
focused contexts per part (fewer compactions, less cross-feature confusion).
Where it does not: the spec interview, scaffold, visual taste and final
review stay single-threaded and human-paced; total tokens are **higher**
than one agent (research: multi-agent ≈ several× tokens); and if the API is
still churning (contract edits every part), parts collide on the seams —
re-run the spec interview, or fall back to `jas run --spec --section` one
feature at a time.

## Storage

```
~/.jas/farms/<farm-id>/
  plan.yaml          approved plan (copy)
  farm.jsonl         supervisor events
  board.jsonl        board posts
  manifest.json      current state (atomic)
  parts/<part-id>/events.jsonl
  integrate/events.jsonl
<repo>/.jas/worktrees/<farm-id>/<part-id>/   (git-excluded; removed after the final choice)
```

Logs stay under `~/.jas` (protected from workers' file tools by the deny
floor); worktrees live in the repo's git-excluded `.jas/worktrees`, outside `~/.jas`.

## Other core consistency

- **Trust (by path).** `TrustStore` keys on the resolved root
  (`core/trust.go`), so every worktree would be untrusted. The supervisor
  decides trust once for the main root at `base` (content hashes of
  `AGENTS.md`/skills) and injects the decision into workers; a worker whose
  copy of a context file differs from `base` drops trust for it. Logged in
  `farm_started`.
- **Context.** Farm workers enable compaction (it is logged and replay-safe)
  via an opt-in headless flag that `jas run` also gets (prerequisite 7);
  otherwise long parts end at exit 3.
- **Provenance.** `session_started` for workers gains `farm_id`, `part_id`,
  `base_sha`, `plan_sha256`, model, temperature and seed (when set). Backward
  compatible additions.
- **Session awareness.** Workers register session records (separate spec) in
  the main repo's `.jas/sessions/`, so a human chat in the main checkout sees
  "farm <id> part core is editing core/**".
- **Scenarios.** When scenarios ship, a worker loads `.jas/scenario.yaml` from
  its worktree at `base`; role policy can only narrow it. Hooks inherit the
  farm trust decision.
- **Isolation.** Parallel YOLO shells can still reach sibling worktrees, the
  shared `.git` and `$HOME`. The threat model (design §9) gains a farm
  paragraph; the opt-in sandbox is the recommended mode for farm.

Every owner decision (plan approval, failure choice, take-over, attach,
pause/drop, scoped "always" approvals, final choice) is an `owner_decision`
event in `farm.jsonl`; policy-made choices (no TTY ⇒ keep, `on_failure`)
are recorded the same way with `by: "policy:…"`.

## Next version

- **Reviewer step** (replaces discussion mode): an optional post-integrate
  read-only step with a clean context and optionally a different model,
  reviewing the integrated diff against the plan; its findings go to the
  integrator for one fix round, then `check` re-runs (see Post-integrate
  steps). This is the shipped shape of "many agents
  propose, one answer" (parallel finders → verify → single writer;
  *research*, "Fit for jas"). LLM findings only — not a substitute for fuzz.
- **Blast-radius tags** (see Blast radius): `low|medium|high` on parts /
  spec sections; keep/merge policy; `manifest` + `part_started` /
  `farm_ended` fields; planner prompt guidance for auth/payments/secrets.
  Sourced from the Rob Shocks orchestrator video (blast radius / never-merge
  starter rule) plus owner request 2026-11-03.
- **Post-integrate `fuzz` role** (see Post-integrate steps): optional
  `post_integrate: [fuzz]`; supervisor order integrate → check → fuzz;
  `fuzz_command` variable; `fuzz_ran` event; exit 6 / `fuzz_failed`.
  Automated hammer (Playwright / edge-case script), not debate — matches
  *research* that parallel read-only verification works. Distinct from
  `reviewer` and from required `check`.
- `jas attach` from another terminal over a Unix socket, approval inbox
  across farms.

## Left to the builder

- Planner and integrator prompt wording (including blast-radius guidance for
  auth/payments/secrets, and the fuzz role prompt that prefers running
  `${fuzz_command}` over improvising).
- Status-line and live-row format (blast-radius column optional).
- How detection walks a monorepo.
- The small-change threshold for `small_change` refusals (tune from use).
- Whether well-known sensitive path globs emit a soft warning when every
  part is tagged `low` (prompt-only vs code warn).
- Exact UX copy for medium/high merge confirms and whether `--accept-medium-blast`
  is also accepted on interactive `jas farm run`.

## Docs to update when this ships

- `docs/design.md` §3 exit-code table (6, 7, 8), §5, §9 farm threat model.
- `docs/use-cases.md` §4/§5, `README.md` farm example (marked experimental).
