# jas farm — design

Date: 2026-11-02. Status: draft for owner review.
Replaces the best-of-N farm in `docs/design.md` §5 and `docs/use-cases.md` §4.
Calls marked **(author's call)** were not made by the owner and may be
overturned.

## Intent

The old farm ran the **same** prompt in N worktrees and let the human keep the
best diff. It pays N times for one result. The owner wants a farm that pays
for one job's worth of work and finishes faster: **split one big task into
parts, run the parts in parallel, then integrate them.**

Flow:

```
jas farm "task"
  → planner agent drafts plan.yaml        (LLM, once)
  → jas validates the plan                (code)
  → owner reviews / edits / approves      (human)
  → supervisor runs parts in DAG order    (code, no LLM)
      each part = one worker agent, own worktree + branch, one role
      workers share an append-only board
  → integrator agent merges branches, runs `check`, fixes seams   (LLM)
  → owner decides what happens to the integrated branch           (human)
```

Coordination is code, not an LLM. LLMs are used only where judgment is
needed: drafting the plan, doing a part, fixing merge seams.

### What success looks like

- The three acceptance scenarios below work end to end.
- Total tokens for a farm run are close to one agent doing the whole task
  serially, plus the planner and integrator; not N× like best-of-N.
- Nothing merges into the owner's branch without the owner saying so.
- A farm run can be audited and replayed from its files.

## Goals

1. `jas farm "<task>"`: plan → review → run → integrate, in one terminal.
2. A planner agent drafts the plan; the owner reviews it before any worker
   starts.
3. The planner **refuses to split** when splitting does not pay (rules
   below), and suggests `jas run` instead.
4. Roles (coder, tester, researcher, reviewer built in) define each worker's
   tools, write limits, prompt, model and budget. Roles are data.
5. Workers communicate one way through a shared board, auto-injected at turn
   boundaries.
6. On a worker failure, dependents pause and the owner is asked.
7. Works for any language: nothing language-specific is built into roles.

## Non-goals (this version)

- **Best-of-N.** Dropped from the roadmap.
- **Discussion mode** (propose / critique / decide on one task). Planned for
  the next version on the same machinery: roles instead of parts.
- **Free agent-to-agent conversation.** The board is one-way posts.
- **`jas attach` over a Unix socket, RPC mode, a live dashboard TUI, starting
  a farm from inside the chat.** Later versions. This version runs in the
  foreground of one terminal.
- **Per-task containers.** Still v3 (design §3.1).

## Anti-goals (would make it a failure even if it works)

- An LLM orchestrator that re-reads farm state on every decision.
- An agent defining or widening policy: roles, tools, approval levels, write
  limits. Agents may only **draft** these for the owner to approve.
- Auto-merging into the owner's branch.
- Language-specific behaviour hard-coded into built-in roles.
- Silently running a farm without a `check` gate.

## Acceptance scenarios

1. **Cross-cutting migration** — e.g. `log` → `slog` across packages.
   Planner splits by package, all `coder` parts, contract = the new call
   shape. Done when: each part's branch exists, the integrated branch passes
   `check`, and the owner's merge decision is recorded.
2. **Feature across layers** — e.g. a field added end to end (core, CLI/API,
   UI, docs). Optional `researcher` part first; `coder` part per layer;
   `tester` or `reviewer` after. Contract (types, names) in `plan.yaml`.
   Done when: as above, and at least one board post was injected into
   another worker during the run (the board is exercised).
3. **Batch of small issues** — e.g. several open `[Low]` issues. Planner
   groups issues by the files they touch; issues touching the same files go
   in the same part. Done when: as above; each issue is mapped to exactly
   one part in the plan.

## Commands

| Command | Does |
|---|---|
| `jas farm "<task>"` | Draft plan, review, run, integrate (interactive). |
| `jas farm plan "<task>" [-o plan.yaml]` | Draft + validate the plan only; no workers. |
| `jas farm run <plan.yaml>` | Run an already-approved plan file (for CI; no plan review prompt). |
| `jas farm init` | Detect variables and draft `.jas/farm.yaml` for review (below). |
| `jas farm vars` | Print each variable's value and its source (project / user / detected / unset). |

Without a TTY, `jas farm "<task>"` cannot get plan approval and stops
(same rule as approvals, design §3.1). CI uses `jas farm plan` +
human-approved file + `jas farm run`. **(author's call)**

## Configuration

`.jas/farm.yaml` (project) over `~/.jas/farm.yaml` (user) over built-in
defaults, merged key by key, most specific wins — the same layering as
design §10. The project file is subject to the existing project trust gate
(design §11): an untrusted project's `farm.yaml` is ignored with a notice.
**(author's call)**

```yaml
check: "go build ./... && go test ./..."   # required; see below
vars:                                       # override or add variables
  test_globs: ["*_test.go"]
  migrations_dir: db/migrations             # custom variable
split:
  min_parts: 2
  max_workers: 4
  disjoint_files: true
  min_turns_per_part: 8
budget:
  per_worker_tokens: 400000
  farm_tokens: 2000000
models:
  planner: ""        # "" = session model
  integrator: ""
roles:               # add or override; see Roles
  migrator:
    tools: [read, grep, edit, write, shell]
    writes: ["${migrations_dir}/**"]
    prompt: "Write database migrations only."
```

Numbers above are the defaults **(author's call; tune after first runs)**.

### `check` is required

The integrator's gate. If `check` is unset after all layers and detection,
**the farm refuses to start** and tells the owner to set it
(`jas config` / `.jas/farm.yaml`). (owner's decision)

### Variables

A small fixed set of built-in names, plus owner-defined names under `vars:`.

| Name | Meaning |
|---|---|
| `check` | integrator gate command (also settable top-level) |
| `test_globs` | test file patterns |
| `src_globs` | non-test source patterns |
| `build` | build command (optional) |
| `lint` | lint command (optional) |

Resolution, highest wins: project `vars:` → user `vars:` → detection →
unset.

Detection is deterministic (no LLM), from marker files:

| Marker | check | test_globs |
|---|---|---|
| `go.mod` | `go build ./... && go test ./...` | `**/*_test.go` |
| `package.json` | `npm test` (+ `npx tsc --noEmit` if `tsconfig.json`) | `**/*.test.*`, `**/*.spec.*`, `**/__tests__/**` |
| `pyproject.toml` / `requirements.txt` | `pytest` | `**/test_*.py`, `**/*_test.py`, `tests/**` |
| `Cargo.toml` | `cargo build && cargo test` | `tests/**` (unit tests live in source; see tester role) |

Monorepos: markers found in subdirectories produce per-path entries
(`check` per directory); the integrator runs the checks for directories its
diff touches. **(author's call)** Builder may extend the marker table.

Validation at load time:
- A `${name}` that is neither built in nor defined under `vars:` is a config
  error; the farm does not start.
- A known but unset variable makes every role that uses it **unavailable**;
  the planner is not offered unavailable roles.

### `jas farm init`

Runs detection, then an agent drafts `.jas/farm.yaml` (check, vars, and any
suggested roles from the repo's shape). The draft is shown to the owner and
saved only on approval. Owners should rarely need it: built-in roles plus
detection cover the acceptance scenarios with zero config.

## Roles

A role is data: `tools`, `writes` (globs), `prompt`, `approve` level,
`model`, `budget`. Layers: built-in → user → project (most specific wins,
whole-role replacement **(author's call)**).

Built-in roles (language-neutral):

| Role | tools | writes | Notes |
|---|---|---|---|
| `coder` | read, grep, write, edit, shell | the part's `owns` globs | default role |
| `tester` | read, grep, write, edit, shell | `${test_globs}` ∩ part's `owns` | "Write and fix tests only; if non-test code must change, post to the board." Unavailable if `test_globs` unset. |
| `researcher` | read, grep (+ web tools once shipped, design §12.1) | none | output is board posts and its final answer |
| `reviewer` | read, grep | none | output is a board post / final answer; may use a cheaper model |

Rust and similar (tests inside source files): when a part's tester cannot be
limited by `test_globs`, the planner gives it the coder's `owns` and orders
it `after` that coder. **(author's call)**

**Who defines roles:** the owner (config). The planner only assigns
existing, available roles. It may *propose* a new role inside the draft plan
(`proposed_roles:`); the owner approves or drops it during plan review, and
may save an approved one to `.jas/farm.yaml`. Agents never create policy.

### Write limits are enforced in code

`writes:` is enforced by the file tools (`write`, `edit`), like the existing
project-root confinement: a call outside the limit gets a `denied` tool
result. `shell` cannot be confined in-process (design §3.1), so the
supervisor also checks each part's final diff against its limit; files
changed outside it pause the part and ask the owner (same flow as a failure).

## The plan

The planner is an LLM agent with read-only tools (read, grep) run on the
repo at the base commit. `base` is the current `HEAD`; uncommitted changes
are not part of the farm, and jas warns when the tree is dirty.
**(author's call)** It outputs `plan.yaml`:

```yaml
task: "migrate logging to slog"
base: <commit sha>
contracts: |
  All packages call slog.Default(); no package-level loggers.
  Logger keys are snake_case.
parts:
  - id: research
    role: researcher
    goal: "Find slog idioms and gotchas for this codebase; post findings."
  - id: core
    role: coder
    goal: "Migrate core/ to slog."
    owns: ["core/**"]
    after: [research]
  - id: cli
    role: coder
    goal: "Migrate cmd/ to slog."
    owns: ["cmd/**"]
    after: [research]
  - id: tests-core
    role: tester
    goal: "Update core tests for the new log output."
    owns: ["core/**"]
    after: [core]
proposed_roles: {}
```

Or a refusal:

```yaml
refuse: "Single bug in one function; splitting would duplicate the investigation."
suggest: "jas run \"...\""
```

### Validation (code, before the owner sees it)

- Every `role` exists and is available; every `after` id exists; no cycles.
- `owns` is non-empty for writing roles. With `disjoint_files: true`, no two
  parts that can run at the same time own overlapping paths. Parts ordered by
  `after` may overlap (e.g. tester after coder).
- At least `split.min_parts` writing parts. No upper limit on the number of
  parts; concurrency is capped by `max_workers`.
- A plan that fails validation goes back to the planner once with the
  errors; a second failure is shown to the owner. **(author's call)**
- The refuse rule is enforced in code too: a plan that does not meet
  `min_parts` is treated as a refusal.

### Review

The owner sees a summary (parts, roles, owns, order, contracts, proposed
roles) and chooses: **approve**, **edit** (opens `$EDITOR` on `plan.yaml`,
re-validates), **replan** with feedback text, or **quit**.

## Running

### Supervisor

Plain Go, inside the `jas farm` process. Each worker is a `core.Session`
(goroutine) with its own event log, worktree and role policy.

- Starts parts whose `after` deps are done, up to `max_workers` at a time.
- Each part gets a worktree on branch `farm/<farm-id>/<part-id>`. A part with
  no deps branches from `base`; a part with deps branches from `base` with
  its deps' branches merged in. A conflict there pauses the part (failure
  flow). **(author's call)**
- Worker system prompt = role prompt + part goal + plan `contracts` + the
  list of other parts and what they own.
- When a worker gives its final answer, the supervisor commits any
  uncommitted changes on the part branch and checks the diff against
  `writes`. **(author's call)**
- Tracks tokens per worker and per farm from usage events; hitting a cap is
  a failure. Planner and integrator tokens count toward `farm_tokens`.
- Approvals: each worker's approval level comes from its role (`approve`),
  defaulting to the configured level. Approval prompts from parallel workers
  are serialized in the farm's terminal, each labelled with its part; other
  workers keep running while one waits. Without a TTY a gated call fails
  the part (failure flow). **(author's call)**
- Output in the terminal: one status line per part (queued / running /
  done / paused / dropped, tokens, last tool) and the board posts as they
  arrive. Plain inline lines, no fullscreen TUI (AGENTS.md constraints).

### Board

- File: `<farm dir>/board.jsonl`, append-only, written through the same
  versioned event-log code as `events.jsonl` (`v`, `id`, `seq`, `ts`) plus
  `part`, `topic`, `text`. (owner's decision: JSONL)
- Write: a `board_post` tool, `{topic, text}`, registered only for farm
  workers. `text` capped (about 500 chars **(author's call)**). Researchers
  and reviewers also have their final answer posted automatically.
- Read: **auto-injected at each turn boundary** (owner's decision) through
  the existing steering queue (`Session.Steer`). A worker receives posts from
  other parts it has not seen yet, oldest first, capped per turn (about 2k
  chars **(author's call)**); the rest wait for the next turn.
- When a part starts, it first receives all earlier posts (e.g. research
  findings).

### Failure: pause dependents, ask the owner

Failure = the worker errors out, hits its token budget, hits the turn cap,
writes outside its limit, or its branch can't be prepared. (owner's
decision)

- Parts that depend on the failed part (directly or transitively) pause.
  Independent parts keep running.
- The owner is asked, per failed part: **retry** (fresh session, told why
  the last try failed), **take over** (farm prints the command to continue
  that part's session in its worktree, after closing it so the session lock
  is free; the owner tells the farm when done),
  **drop** (the part and its dependents are dropped), or **abort** the farm.
- Without a TTY (`jas farm run` in CI), a failure ends the farm with a
  non-zero exit and leaves all branches for inspection.

### Integrator

An LLM agent with the coder's tools, on branch `farm/<farm-id>/integrate`
from `base`.

1. Merges part branches in DAG order (topological, ties by plan order).
2. On a merge conflict, resolves it, using the plan and board for context.
3. Runs `check`; fixes failures at the seams; reruns. Budget-capped like any
   worker.
4. Ends with a summary: parts merged, dropped, conflicts fixed, `check`
   result.

Then the owner chooses: **merge** the integrate branch into the branch they
started on, **keep** the branch (e.g. to open a PR themselves), or
**discard**. Worktrees are removed after this choice; branches stay unless
discarded. **(author's call)**

## Storage

```
~/.jas/farms/<farm-id>/
  plan.yaml          approved plan
  farm.jsonl         supervisor events (part started/done/failed, owner choices)
  board.jsonl        board posts
  parts/<part-id>/events.jsonl
  integrate/events.jsonl
  worktrees/<part-id>/   (removed after the final choice)
```

Every owner decision (plan approval, failure choice, final choice) is a
`farm.jsonl` event, so a run can be audited and its decisions replayed.
Exact event names and payloads are the builder's call, following design §3's
versioning rules.

## Left to the builder

- Planner and integrator prompt wording.
- Exit codes for refusal and failure (must not reuse an existing code with a
  different meaning; document them next to design §3's table).
- Status-line format.
- How detection walks a monorepo.
- Whether `board_post` topics are free text or a small enum.

## Docs to update when this ships

- `docs/design.md` §5 (farm, attach entries) and the farm package layout.
- `docs/use-cases.md` §4 (replace best-of-N) and §5 (attach later).
- `README.md` farm example.
- `AGENTS.md` "Farm UI … wait for their own design" line.
