# Spec-driven flow — design

Date: 2026-11-02 (Asia/Tokyo). Status: **proposed**, owner review pending.
Scope: core loop (chat + `jas run`). Works **without farm**; farm's planner
consumes the same spec ([farm spec](2026-11-02-farm-design.md), "Spec-driven
planning"). Calls marked **(author's call)** may be overturned.

## Why

Agents build the wrong thing fastest when the task is vague; every later
step (scaffold, parallel parts, review) inherits the ambiguity. The fix the
owner asked for is a "grill-me" interview: jas asks, one question at a time,
until there is a spec concrete enough to build and check against. The spec
then becomes the contract: runs cite it, `check` enforces it, farm splits by
it.

## Flow

```
jas spec [spec.md]                      interactive interview → draft → approve (hash)
jas spec --check spec.md                headless completeness check (code, no LLM)
jas run --spec spec.md --phase scaffold bounded scaffold; `check` must pass
jas run --spec spec.md --section F-todos   one feature, single agent
jas farm plan --spec spec.md            (optional) split by sections → farm
```

## Goals

1. `jas spec`: an interactive interview that produces an approved spec file.
2. A deterministic completeness check usable in CI.
3. A bounded **scaffold** phase that sets up structure, not features.
4. Section-by-section building with one agent, no farm needed.
5. Traceability: every spec-driven run records which section(s) it served
   and which acceptance criteria it claims to meet.

## Non-goals

- Generating the whole app in one run.
- A spec language or schema beyond Markdown with fixed headings and IDs.
- Replacing the owner's judgment on taste, UX or architecture: the
  interview proposes, the owner decides.
- Any fullscreen UI: the interview is inline chat (AGENTS.md).

## The spec file

Markdown with YAML front matter. Fixed headings; feature and criterion IDs are
stable strings so runs and tests can cite them.

```markdown
---
spec: 1
title: Todo app
check: "make check"                 # becomes the project's `check` if none set
contract_check: "make contract-test" # optional; API contract tests
codegen: "make gen"                  # optional; regenerates stubs/clients from the contract
approved:                            # written by `jas spec` on approval
  sha256: <canonical spec hash; see "Approval hash" below>
  at: 2026-11-02T23:40:00+09:00
---

## Goals
## Non-goals
## Stack            (languages, frameworks, DB, hosting, versions)
## Data model       (entities, fields, types, relations, invariants)
## API contract     (OpenAPI file path, or typed routes table: method, path, request, response, errors)
## Pages            (route, purpose, data used)

## Features
### F-todos: Todo list
- Pages: /todos
- API: GET /api/todos, POST /api/todos
- Owns (hint): server/api/todos/**, web/pages/todos/**
- Blast radius: medium   # optional; farm planner inherits onto covering parts (farm design, Blast radius)
- Acceptance criteria:
  - AC-todos-1: a signed-in user sees only their todos (test: tests/e2e/todos.spec.ts "lists own todos")
  - AC-todos-2: empty title is rejected with 422

## Open questions   (must be empty for approval)
```

Required sections: Goals, Non-goals, Stack, Data model, API contract, Pages,
Features (each with ≥1 acceptance criterion and an owned-path hint), Open
questions (empty). API contract may point at an OpenAPI file or a typed-route
module instead of an inline table.

## `jas spec` — the interview

- Inline chat, same UI as `jas` interactive mode. An interviewer agent with
  read-only tools (read, grep) so it can prefill Stack etc. from an existing
  repo; it never writes code.
- **One question at a time.** Each question names the section it fills and,
  where useful, offers a recommended default the owner can accept with one
  key ("Recommend Postgres 16 — accept / other?"). Accepted defaults are
  marked as assumptions in the spec.
- **Pushes back on vague answers.** "Fast", "simple", "standard auth",
  "etc.", "like Trello" get a follow-up asking for a measurable or concrete
  version (latency number, provider name, list of fields). Acceptance
  criteria must be testable statements; the interviewer rewrites vague ones
  and asks the owner to confirm.
- Coverage is tracked **in code** against the required sections, shown as a
  one-line inline status (`spec: 5/8 sections · 2 features · 1 open question`);
  the interviewer is told which sections are still missing.
- `/skip` records the question under Open questions; `/draft` shows the
  current draft; `/done` ends the interview (approval still requires the
  check below).
- Resumable: the interview is a normal session (`--continue`).
- In chat the same flows are slash commands: `/spec [spec.md]` (interview),
  `/spec scaffold` (= `--phase scaffold`), `/spec section <id>` (= `--section`),
  `/spec check`. They run in the current chat session with the same events.

### Review and approve

The draft is shown (or opened in `$EDITOR`). The owner chooses **approve**,
**edit**, **keep interviewing**, or **quit** (draft saved, unapproved).
Approval runs `jas spec --check`; it cannot approve with open questions.
**Approval hash.** Same canonicalization as the farm plan hash (farm spec,
"Plan trust in CI"): sha256 of the whole file's bytes, front matter and body
together, after (1) removing the front-matter key `approved:` and its
indented block (so `sha256` and `at` are not hashed), (2) normalizing line
endings to LF, and (3) stripping trailing whitespace at end of file. No YAML
or Markdown re-serialization; any edit requires re-approval.

On approval jas writes `approved.sha256` and logs
`spec_approved{path, sha256, sections, features}` in the session's
`events.jsonl`. Editing an approved spec invalidates the hash until
re-approved.

## Headless: `jas spec --check` and `jas run --spec`

`jas spec --check spec.md` is deterministic code (no model call): required
headings present and non-empty, no `TODO`/`TBD`, Open questions empty, every
feature has ≥1 `AC-` item and an Owns hint, every API route referenced by a
feature exists in the contract, IDs unique, approved hash matches.

`jas run --spec spec.md ...` runs the same check first. If it fails, no model
is called and jas prints the list of open problems:

```
spec incomplete (spec.md):
  - Data model: section empty
  - F-todos: no acceptance criteria
  - Open questions: 1 ("which auth provider?")
  - approved.sha256 does not match (edited after approval)
```

**Exit code 2** (usage/config error). Event shape, so `--json` consumers can
rely on it:

- `jas spec --check` starts **no session**: `--json` emits only
  `spec_check{ok, problems:[{section, kind, message}]}`.
- `jas run --spec …` with a failing check **does** start a session (it is a
  run): `session_started{spec:{…}}`, then `spec_check{ok:false, …}`, then
  `session_ended{exit_code:2, reason:"spec_incomplete"}`. No `turn_started`
  and no model call in between. Rationale: an incomplete spec is a bad *input* found before
any model call, the same class as farm's invalid/unapproved plan (also 2);
the §3 table stays small and 6/7/8 keep their farm/budget meanings. CI that
must tell "spec incomplete" apart from "bad flags" reads the `spec_check`
event or runs `jas spec --check` as its own step (exit 0/2). (owner-approved, 2026-11-02 review: keep 2, no dedicated code 9; the
`spec_check` event discriminates.)

## Scaffold phase

`jas run --spec spec.md --phase scaffold` — one agent, bounded:

- **Does:** repo layout, build setup, test harness, CI workflow, the `check`
  (and `contract_check`) commands, API stubs generated from the contract
  (via `codegen` where possible, handlers return 501/NotImplemented), empty
  pages for each route with a placeholder, `.jas/farm.yaml` `check` filled in.
- **Does not:** feature logic, styling beyond a default, data beyond
  migrations for the data model's tables.
- **Enforced:** the run ends with jas running `check` itself (exit 6 if it
  fails, per the CI contract); a scaffold verifier (code) confirms every
  contract route has a stub and every page route has a file; write limits
  keep it out of anything outside the layout it declared in its first
  `scaffold_plan` step. "No feature logic" is not fully machine-checkable:
  contract tests for feature behaviour are expected to fail/skip as pending,
  and the owner reviews the diff. **(author's call)** Two cheap code checks
  narrow the gap: (1) every contract route's stub answers with the
  not-implemented status (501 or the stack's equivalent), verified by a
  generated smoke test the verifier runs; (2) a per-file line cap on
  scaffold-written source files (default 150, config
  `scaffold.max_file_lines`; generated code under `shared:` paths exempt),
  over which the verifier fails the phase with exit 6
  `reason: scaffold_too_large`. Both stay heuristics; owner review remains
  the final check.
- Prompt budget and turn cap are lower than a normal run (it's a bounded
  phase).

## Section-by-section mode (no farm)

`jas run --spec spec.md --section F-todos [--section F-auth]` gives one agent
the spec (full, cached), the named section(s) and their acceptance criteria,
and the Owns hint as a **soft** write scope (hard limits are a farm
feature). Before session-awareness rollout step 3 ships, the hint is advice
in the prompt only; once it ships, writes outside the hint that overlap
another session warn via the overlap check. Done = `check` passes (run by
jas) and the agent's final answer includes a criteria table:

```
F-todos  AC-todos-1  met     tests/e2e/todos.spec.ts "lists own todos"
F-todos  AC-todos-2  met     server/api/todos/create_test.go
```

Unmet or unproven criteria are listed as such, never silently dropped.

## Traceability

- `session_started` gains `spec{path, sha256, phase, sections[]}` for any
  `--spec` run.
- `spec_trace{section, criteria:[{id, status, evidence}]}` is logged from the
  final answer's criteria table (parsed in code; malformed table = a logged
  warning, not success).
- Chat sessions that load a spec cite section IDs in their final answers;
  `jas run` without `--section` but with `--spec` must name the sections it
  served (empty = warning).
- Farm parts carry their section IDs in `part_started` and the manifest.

## Events (additive, versioned per design §3)

`spec_question`, `spec_answer`, `spec_approved`, `spec_check`, `spec_trace`,
plus the `spec` field on `session_started`. Answers pass through the
redactor like any user message.

## User stories & scenarios

Stories and walkthroughs for the spec-driven flow, alone and with farm, in
both front ends. Commands, events and exit codes follow the
[farm spec](2026-11-02-farm-design.md) CI contract and design §3, which are
the source of truth; where this section disagrees, they win. Any speed or
cost number here is an **expectation**, not a measurement. Event names are
the ones proposed in these specs; payloads are abbreviated.

### User stories

| # | Story | Acceptance criteria |
|---|---|---|
| US-1 | As a **solo developer**, I want jas to interview me one question at a time and push back on vague answers, so that I end up with a spec concrete enough to build and test against. | Every required section is filled or the spec cannot be approved; vague answers ("fast", "etc.") get a follow-up; approval writes `approved.sha256` and logs `spec_approved`. |
| US-2 | As a **CI maintainer**, I want a headless spec check that lists open questions, so that incomplete specs fail fast without spending tokens. | `jas spec --check` makes no model call; exit 0 when complete, 2 with a problem list otherwise; `--json` emits `spec_check`. |
| US-3 | As a **developer**, I want a bounded scaffold phase, so that features start from a skeleton that builds and tests, without feature logic sneaking in. | jas runs `check` at the end (exit 6 on failure); every contract route has a stub and every page a file; the scaffold verifier result is logged. |
| US-4 | As a **developer without farm**, I want to build one spec section at a time, so that I get spec traceability with a single agent. | `--section` runs cite section IDs in `session_started`; the final answer's criteria table is logged as `spec_trace`; unmet criteria are listed, not hidden. |
| US-5 | As a **CI maintainer**, I want a nightly farm run from an approved plan and spec, so that big decomposable work progresses unattended without ever touching `main`. | `plan.yaml` and spec hashes must match their approvals (else exit 2); no TTY ⇒ keep; exit 0 only if `check` and `contract_check` pass; `manifest.json` and `farm.jsonl` are uploadable artifacts. |
| US-6 | As a **repo owner**, I want hard token and time caps on PR-triggered runs, so that a runaway agent cannot burn the budget. | Caps are checked before each request; crossing one ends the run with exit 7 and a `budget_stop`/`session_ended` reason; partial work is left for inspection, not committed. |
| US-7 | As a **developer**, I want the planner to refuse splits that won't pay and tell me why, so that I don't pay farm overhead for a one-agent job. | Structured `refuse.reason`; exit 8 headless; in chat the task is offered back as a normal message. |
| US-8 | As a **team lead**, I want to audit a finished farm from its files, so that I can see who approved what, what each worker saw, and why the verdict was what it was. | Plan/spec hashes and approvals, every `board_delivered`, every `owner_decision`, `check_ran`, and per-criterion evidence are in `farm.jsonl` / `manifest.json` / part logs. |
| US-9 | As a **developer in chat**, I want to watch a farm as one live row per part and step into a worker when it's stuck, so that I can unblock it without stopping the others. | Rows update in place inline (no fullscreen); `/farm attach <part>` steers a running worker; `/farm back` returns; other parts keep running. |
| US-10 | As a **developer in chat**, I want approvals labelled by part with "always" scoped to that part or the farm, so that I can trust one worker's shell without trusting all of them forever. | Prompts are serialized and labelled; "always" never outlives the farm or leaks to other sessions; each decision is an `owner_decision` event. |
| US-11 | As a **developer**, I want to drop a failing part and still merge the rest, so that one stuck feature doesn't block the others. | Drop removes the part and its dependents; integration and `check` run on the rest; the outcome is recorded as unmet (6) but merge is still offered with a TTY. |
| US-12 | As a **developer**, I want a farm to survive Ctrl-C or a crash, so that I don't lose an hour of parallel work. | `farm_ended{interrupted}` or a stale manifest is detected; `/farm resume` / `jas farm resume` continues; no mutating tool call runs twice. |
| US-13 | As a **developer running a second session by hand**, I want to be warned before I edit files a farm part or another session is working on, so that we don't clobber or conflict. | Session-awareness overlap check asks in chat, warns or fails headless; `overlap_detected` logged. |

### CLI / headless scenarios

#### C1 — Nightly farm from an approved plan + spec (US-5)

1. Earlier, with a TTY: `jas spec spec.md` (approved, `sha256: 9c1…`), then
   `jas farm plan --spec spec.md -o plan.yaml` (approved; the plan records
   `spec.sha256: 9c1…` and its own `approved.sha256: 4be…`). Both files are
   committed by the owner.
2. Nightly workflow:

   ```yaml
   - run: jas spec --check spec.md
   - run: jas farm run plan.yaml --json > farm-events.jsonl
   - if: always()
     uses: actions/upload-artifact@v4
     with:
       name: farm
       path: |
         farm-events.jsonl
         ~/.jas/farms/*/manifest.json
   ```
3. jas shows (stderr, plain lines): `farm 7f3 · 4 parts · base a1b2c3 · plan 4be… · spec 9c1…`,
   then one line per part transition and `check: pass · contract_check: pass`.
4. Events: `farm_started{plan_sha256, spec_sha256, base}`, `part_started`…,
   `check_ran{exit_code:0}`, `owner_decision{choice:"keep", by:"policy:no-tty"}`,
   `farm_ended{exit_code:0}`.
5. Outcome: **exit 0**; branch `jas/farm/7f3/integrate` kept. Pushing it or
   opening a PR is a separate workflow step the owner wrote, not jas.
   If someone edited `spec.md` after approval, step 2's first command exits
   **2** (`approved.sha256 does not match`) and the farm never starts.

#### C2 — PR-triggered section implementation hits the budget (US-4, US-6)

1. A label `jas:F-export` on a PR triggers
   `jas run --spec spec.md --section F-export --max-tokens 300000 --timeout 20m --json`.
2. jas checks the spec (pass), logs `session_started{spec:{sha256, sections:["F-export"]}}`,
   and works. Before each request it estimates input + `max_tokens`; at
   ~292k used, the next request would cross 300k, so it is **not sent**.
3. jas shows: `budget: next request would exceed --max-tokens 300000 (used 291k) — stopping`.
4. Events: `budget_stop{kind:"tokens", used:291k, limit:300k}`,
   `session_ended{exit_code:7}`. Edits so far stay in the CI workspace (jas
   does not commit); the workflow uploads the log and diff.
5. Outcome: **exit 7**. The owner re-runs with a larger cap, narrows the
   section, or continues locally with `jas --continue` on the downloaded log.

#### C3 — Planner refusal falls back to `jas run` (US-7)

1. `jas farm plan --spec spec.md --sections F-rename-field -o plan.yaml` in CI.
2. The planner sees one feature touching `server/api/users/**` only and
   returns `refuse{reason:"single_package", detail:"one package, ~6 files"}`.
3. jas shows: `planner refused: single_package — one package, ~6 files`
   and `suggest: jas run --spec spec.md --section F-rename-field`.
4. Events (planner log): `farm_plan_refused{reason, suggest}`,
   `session_ended{exit_code:8}`. No worktrees or branches are created.
5. Outcome: **exit 8**. The workflow treats 8 as "use the single agent":
   `if [ $? -eq 8 ]; then jas run --spec spec.md --section F-rename-field; fi`.

#### C4 — `check` fails; triage from artifacts (US-5, US-8)

1. Nightly `jas farm run plan.yaml --json` (as C1). All parts finish, the
   integrator merges, the supervisor runs `check`: `go test ./...` fails in
   `server/api/todos`.
2. jas shows: `check: FAIL (exit 1, 41s) — see manifest.json` and the
   redacted, truncated tail of the output.
3. Events: `check_ran{exit_code:1, output:"--- FAIL: TestCreateTodo…"}`,
   `owner_decision{choice:"keep", by:"policy:no-tty"}`, `farm_ended{exit_code:6, reason:"check_failed"}`.
4. Triage from artifacts: `manifest.json` lists each part's branch, diffstat
   and criteria (`AC-todos-2: met (evidence: create_test.go)` by
   backend-api); `farm-events.jsonl` shows the `board_delivered` seqs each
   part saw, revealing that frontend-pages never received backend-api's
   changed `interface` post (it arrived after its last round). The developer
   checks out `jas/farm/7f3/integrate` and fixes it, or retries the part
   locally with `jas farm resume`.
5. Outcome: **exit 6**; all branches kept.

#### C5 — Incomplete spec fails headless (US-2)

1. A PR edits `spec.md` to add feature F-share with no acceptance criteria;
   CI runs `jas spec --check spec.md --json`.
2. jas shows:
   ```
   spec incomplete (spec.md):
     - F-share: no acceptance criteria
     - F-share: no owned-path hint
     - Open questions: 1 ("public links expire?")
     - approved.sha256 does not match (edited after approval)
   ```
3. Events: `spec_check{ok:false, problems:[…4 items]}` (no session, no model call).
4. Outcome: **exit 2**. The author runs `jas spec spec.md` locally; the
   interview resumes at the open items, then re-approves.

#### C6 — Team lead audits a finished farm (US-8)

1. `jas farm status 7f3` prints parts, status, tokens, criteria and the
   final choice from `manifest.json` (works offline / from an artifact).
2. Questions and where the answers are:
   - Who approved the plan and spec, and which versions? `farm_started{plan_sha256, spec_sha256}` + the `approved:` blocks.
   - What did backend-api know when it changed the route? its `board_delivered{part:"backend-api", seqs:[…]}` entries and the fenced board blocks in `parts/backend-api/events.jsonl`.
   - Did any part write outside its `owns`? `part_ended{policy_violation}` (none ⇒ absent).
   - Which criteria are met, with what evidence? `manifest.json` `criteria[]` / `spec_trace` in part logs.
   - Who merged, and when? `owner_decision{choice:"merge", by:"owner", ts}`.
   - What did it cost? per-part and total tokens in `manifest.json`.
3. Example: `jq 'select(.type=="owner_decision")' ~/.jas/farms/7f3/farm.jsonl`.
4. Outcome: no exit-code semantics (read-only); `jas farm status` exits 0.

### Interactive scenarios

#### I1 — Web app from zero: grill-me, scaffold, `/farm` (US-1, US-3, US-9)

1. `jas` → `/spec spec.md` (same as `jas spec spec.md`). The interviewer asks
   one question at a time: "Who signs in, and how — email+password, OAuth
   (which provider), magic link?" Answer "standard auth" → pushback:
   "Which provider, and do you need password reset in v1?" Inline status:
   `spec: 3/8 sections · 2 features · 0 open`.
2. `/draft` shows the spec; `a` approves → `spec_approved{sha256}`.
3. `/spec scaffold` (or `jas run --spec spec.md --phase scaffold`). jas
   runs `check` at the end: `scaffold: 14 routes stubbed · 5 pages · check pass`.
4. `/farm --spec spec.md`. Plan summary prints (4 parts, as in the farm
   spec's "Web app from zero"); `a` approve.
5. Live rows show three parts running, e2e-tests queued. Board posts print
   once to scrollback: `[backend-api] interface GET /api/todos — ready`.
6. Integrate → `codegen` → `check` + `contract_check` pass. Summary:
   per-part diffstat, criteria 11/11 met, tokens per part and total;
   `m` merge → `owner_decision{merge}`; `farm_ended{exit_code:0}`; back to
   the chat prompt.
7. Expectation (not measured): the four features' first cut lands roughly
   2–3× sooner than one agent doing them in sequence, at higher total tokens.

#### I2 — Add a feature to an existing repo via a spec section (US-1, US-4)

1. Repo already has `spec.md`. `/spec spec.md` → "add feature" → the
   interviewer only asks about the new F-export section (route, format,
   limits, acceptance criteria, owned paths), pushing back on "export
   everything" ("which entities, which format, max rows?").
2. Re-approve (new hash). The agent suggests the single-agent path (one
   package, no farm): `/spec section F-export`.
3. The run ends with the criteria table; jas runs `check`; `spec_trace`
   logged. Outcome: `session_ended{exit_code:0}` for that run, back to chat.

#### I3 — Blocked board post; owner steers via `/farm attach` (US-9)

1. During I1, the row for backend-api shows `blocked: need user.id`.
2. backend-auth hasn't posted the interface yet; the owner types
   `/farm attach backend-auth` and writes "export `auth.UserID(ctx)` first and
   post it as an interface". The message goes in through `Session.Steer`
   (owner authority) at the next turn boundary.
3. backend-auth posts `interface auth.UserID(ctx) (string, bool)`; the
   supervisor delivers it to backend-api in its next round
   (`board_delivered{part:"backend-api", seqs:[17]}`); the row turns
   `running`.
4. `/farm back` returns the input to the main chat. Events:
   `owner_decision{kind:"attach", part:"backend-auth"}` and the steered
   `user_message{steer:true}` in that part's log.

#### I4 — Part-scoped approvals (US-10)

1. Config `approve: shell`. Prompt: `[frontend-pages] shell: npm install @tanstack/react-query — y / n / always (part) / always (farm)`.
2. The owner picks **always (part)**: later shell calls from frontend-pages
   run without asking; backend-api's `go test` still prompts.
3. `[e2e-tests] shell: npx playwright install` → **always (farm)**: all
   remaining parts skip shell prompts until the farm ends; nothing carries
   over to the chat or to the next farm.
4. Events: `owner_decision{kind:"approve", scope:"part"|"farm", part, tool}`.

#### I5 — Drop a failing part, merge the rest (US-11)

1. e2e-tests fails twice (flaky browser install). jas prompts:
   `[e2e-tests] failed: setup exit 1 — retry / take over / drop / abort`.
2. `drop` → e2e-tests (no dependents) is dropped; integration proceeds with
   three parts; `check` (unit + contract tests) passes.
3. Summary shows `e2e-tests: dropped · AC-todos-1, AC-auth-3: unproven`.
   The outcome is unmet (`farm_ended{exit_code:6, reason:"part_dropped"}`),
   but with a TTY the owner may still pick `m` merge — the record says so.
4. Follow-up: `/spec section F-e2e` later, single agent.

#### I6 — Resume after Ctrl-C or a crash (US-12)

1. Ctrl-C once: `stopping farm 7f3 — finishing current requests (Ctrl-C again to cancel)`.
   Running requests end; `farm_ended{exit_code:130, status:"interrupted"}`;
   back to the chat prompt. Rows freeze into scrollback.
2. Or the terminal dies. Next `jas` in that repo shows:
   `farm 7f3 interrupted: 2 done · 1 running · 1 queued — /farm resume 7f3`
   (stale manifest + dead pid, via session-awareness records).
3. `/farm resume 7f3` re-checks the plan hash, resumes the running part
   from its log (`ResumeSession`; a `tool_started` without a result is
   reported to the model as interrupted, not re-run), and continues the DAG.
   Board delivery state is rebuilt from `board_delivered` events.
4. Outcome: the farm ends normally; its log shows `farm_resumed{by:"owner"}`.

#### I7 — Session awareness with a second manual session (US-13)

1. While farm 7f3 runs, the owner opens a second terminal, `jas`, in the
   main checkout. Start-up note: `peer: farm 7f3 part frontend-pages owns
   web/pages/**, web/components/** · peer: chat "fix login copy" 4m ago`.
2. The second session's agent tries `edit web/components/Header.tsx`.
   Prompt: `frontend-pages (farm 7f3) owns web/components/** and will merge
   into main — edit anyway? y/n`. Owner says **n**; the model gets `denied`
   and proposes doing the change after the farm merges.
3. Headless twin: a `jas run --on-overlap fail` in the same checkout gets a
   `denied` tool result for that path and continues; with the default
   `warn` it writes and the result notes the overlap.
4. Events: `peers_seen`, `overlap_detected{path, peer_session_id, action:"asked"→"denied"}`.

#### I8 — Refusal returns to chat (US-7)

1. In chat: "rename `userName` to `displayName` in the users API". The agent
   does **not** suggest a farm (one package). The owner tries anyway:
   `/farm "rename userName to displayName in the users API"`.
2. jas prints `planner refused: single_package — 1 package, 5 files` and
   `run it here instead? y/n`.
3. `y` → the task becomes the next chat message in the same session; no
   worktrees, branches or farm dir beyond the planner's log.
4. Events: planner log `farm_plan_refused`; the chat continues
   (`farm_ended` is not written because no farm started).

## Relation to other specs

- **Farm:** `jas farm plan --spec` splits by feature sections; see the farm
  spec's "Spec-driven planning" and "Web app from zero". Optional per-feature
  `Blast radius:` is inherited onto covering plan parts (farm design, Blast
  radius; after MVP).
- **Session awareness:** section runs register their sections in the session
  record, so parallel humans/sessions see "F-auth in progress".
- **Prerequisites:** scaffold and section runs need jas-run `check`
  (supervisor-run success check) and token/time caps; the interview needs
  nothing new beyond the inline chat. Session-awareness features here are
  additive, not prerequisites: section records need rollout step 1, overlap
  warnings (and US-13 / I7) need step 3.
