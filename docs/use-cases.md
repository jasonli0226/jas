# Use cases

Concrete situations where jas fits — mapped to what actually ships in v1 vs
later cuts. The design doc (§4–§6) defines scope; this doc shows the intent
behind it. Each section names who it's for, the job to be done, and what
"done" looks like.

Owner day-to-day use (updated 2026-11-03 Asia/Tokyo): **interactive pair
programming**, **starting a project from instructions / a future scenario or
spec**, and **studying an existing repo to draft a scenario**. CI/`--json` and
farm stay first-class strengths; they are not the only lead story.

## 1. Interactive pair programming (`jas` chat — v1)

**Who:** a developer working in a repo. **Job:** pair with the agent in the
terminal — ask, steer, approve edits, keep going — without leaving the same
harness CI will use later.

```sh
jas                         # inline chat in this project
jas --continue              # resume this project's last session
jas --approve=mutating      # confirm shell + file edits as you go
```

- Replies stream into the terminal's own scrollback (no fullscreen TUI).
- Same tools, approval ladder, deny/redact, and JSONL event log as `jas run`.
- `/model`, `/resume`, `/name`, skills, and project `AGENTS.md`/`CLAUDE.md`
  (after trust) are available in-session; `/help` lists commands and keys.
- Typing while a reply runs steers it; Esc stops the run.

**Done when:** you can drive a multi-turn coding session, see diffs before
mutating tools apply (at `mutating`), and reopen the same session with
`--continue` or `/resume`.

**Why jas and not a general chat client:** the chat is task-shaped pair
programming over the coding harness — tools, approvals, and the event log are
the product, not open-ended conversation. For how that differs from pi's
interactive surface, see design §14.

## 2. One-shot coding tasks (`jas run` — v1)

**Who:** a solo dev in a weekend loop. **Job:** get a reviewed change without
leaving the terminal.

Task in, files changed, you `git diff` — review stays with the human.

```sh
jas run "add a header comment to main.go explaining the retry logic"
jas run "write a table-driven test for parseConfig covering bad env vars"
jas run --approve=mutating "upgrade the deprecated io/ioutil calls in pkg/"
```

- Streaming output — you watch the agent work in the terminal.
- `--approve` is a ladder: `never` (default, permissive) → `shell`
  (confirm every shell command) → `mutating` (confirm shell *and* file
  edits). A denied call goes back to the model as a `denied` result and the
  run continues. Audit runs use `never`; fix runs use `mutating` — two jobs,
  one flag. Set your usual level once with `jas config set approve mutating`.
- Config is set once and stored, pi-style: first run onboards you, then
  `jas config set model …` / `jas auth add openrouter` persist to `~/.jas/`.
  `--model` and `JAS_*` env vars override for one run (design §10).
- The versioned JSONL event log (`~/.jas/sessions/<id>/events.jsonl`) is the
  audit trail: every tool call, every result, every token count.
- Project conventions come free: v1 loads `AGENTS.md`/`CLAUDE.md` from the
  project root, after a one-time trust prompt per project (design §3).

**Done when:** the header-comment task finishes in ≤3 turns (design §7),
files changed, `git diff` shows exactly that change.

**Why jas and not `pi --print` / a raw API loop:** fewer turns and tokens per
task (design §7), from the tool policy (parallel reads, serialized mutations),
a cache-friendly prompt, and a context budget that elides old tool results
instead of blowing the window on a 20-turn task. The core approval gate and the
CI exit-code contract are also built in. Cold start is a hygiene bonus, not the
reason: the LLM round-trip dominates. Full comparison: design §14.

## 2a. Init a project from instructions (shipped chat; scenario / `jas spec` proposed)

**Who:** someone starting a greenfield app or a new package. **Job:** get a
working skeleton and conventions in place without a separate scaffolding CLI.

**Shipped today:** open `jas` in an empty or nearly empty directory (or
`jas run "…"`), put project rules in `AGENTS.md`, and use skills / chat to
scaffold files. There is **no** `jas new` / blueprint command — design §8
dropped scaffold-from-preset blueprints on purpose.

```sh
jas    # pair: "scaffold a Go module with cmd/, internal/, Makefile, AGENTS.md"
```

**Proposed (not shipped):**

- `jas spec spec.md` grill-me interview → approved spec, then
  `jas run --spec spec.md --phase scaffold` (or `/spec` / `/spec scaffold` in
  chat) — see [spec-driven flow](superpowers/specs/2026-11-02-spec-driven-flow.md).
- v2 **scenarios** (design §8): a checked-in `.jas/scenario.yaml` as the
  reusable *agent* template (system prompt, toolset, sandbox, hooks) for that
  kind of project — not a file-tree cookie-cutter.

**Done when (shipped):** the repo has the skeleton you asked for, an event log
of how it got there, and `AGENTS.md` (or equivalent) so the next chat session
keeps the same rules.

## 2b. Study an existing project → draft a scenario (`jas bootstrap` — v2, proposed)

**Who:** someone adopting jas on a real repo. **Job:** turn "how this project
already works" into a draft agent policy they can edit and commit.

**Not shipped.** Design §8.3: `jas bootstrap` is a **deterministic** scan (no
LLM) that writes a draft `.jas/scenario.yaml` from signals like `go.mod`,
`package.json`, `Makefile`, `AGENTS.md`, `.env` presence, `.mcp.json`, etc.
You hand-edit the draft; scenarios stay data.

```sh
# proposed (v2) — illustrative; not in the current binary
jas bootstrap                 # draft .jas/scenario.yaml from the repo
```

Until that ships, the practical path is the same as §1: chat over the repo,
capture conventions in `AGENTS.md`, and keep the session log as the audit of
what you learned.

**Done when (v2):** a repeatable draft scenario appears, deny patterns cover
secrets paths the scan found, and a first-time contributor gets the scenario
trust prompt (design §11).

## 3. Batch work across a repo (v1, one at a time)

**Who:** a maintainer clearing mechanical debt. **Job:** many small, similar
changes with an audit trail per run.

```sh
jas run --approve=mutating "rename function foo to bar in pkg/api/, update all callers"
jas run "list every exported function in pkg/api with its signature"
```

Each run is an isolated session with its own event log. No daemon, no state to
corrupt — crash mid-task, re-run, the log still tells you what happened.

**Done when:** each run reports its turn/token counts (design §7) and leaves
an event log that reconstructs what it did.

### 3.1 Crash-resume (v1 design, resume ships v2)

**Who:** anyone running a 20-turn task on a laptop. **Job:** know what
happened when the run dies mid-task.

A mutating call runs **at most once**: jas fsyncs `tool_started` *before* the
call executes (design §3). A crash between the fsync and the result means the
replay records `interrupted`. The call is **not** re-executed; the model sees
the interrupted result and decides next. File writes are atomic (temp file +
rename), so a file is either old or new. A shell command may have run
partially, so the model is told to inspect before retrying. In v1 you find the
call that was in flight when the run died:

```sh
# tool_started events with no matching tool_result
jq -s 'map(select(.type == "tool_result") | .call_id) as $done
       | map(select(.type == "tool_started" and (.call_id | IN($done[]) | not)))' \
  ~/.jas/sessions/<id>/events.jsonl
```

**Done when (v2):** `jas resume <id>` continues from the log; no mutating
tool call from before the crash ever runs twice.

### 3.2 Context-full and turn-cap stops (v1)

**Who:** the same dev, on a long task. **Job:** fail loudly, not silently.

v1 tracks prompt tokens against the model's context window (from the built-in
catalog, or `jas config set models.<name>.context_window …`). Past a threshold
it elides old tool results in one chunk (last K verbatim, rest stubbed), which
keeps the prompt cache warm. It stops cleanly on `context_length_exceeded`.
`max_turns` (default 50; `jas config set max_turns …`) is the runaway stop.

**Done when:** a context-full stop exits 3 and a turn-cap stop exits 4, each
with a clear message — and the CI contract in §4 holds.

## 4. CI / scripted use (`jas run --json` — v1)

**Who:** a CI author wrapping agent runs in a pipeline. **Job:** machine-
readable runs with a clean failure contract.

Approver defaults to `never` (YOLO), so treat the job container as the real
boundary — same stance as design.md §3.1 and the anti-use-cases below. CI has
no TTY, so a run with `--approve=shell|mutating` exits 5 on its first gated
call instead of hanging. There is no `~/.jas/` in CI, so configure with
`JAS_*` env vars from the CI secret store.

```sh
# Prefer a throwaway container; jas itself is not the sandbox.
# <image-with-jas>: any image that has the jas binary on PATH.
docker run --rm -e JAS_BASE_URL -e JAS_MODEL -e JAS_API_KEY \
  -v "$PWD":/work -w /work <image-with-jas> \
  jas run --json "check all TODO comments in src/ and list them with file:line" \
  | jq -c 'select(.type == "tool_result")'
```

Exit contract (v1, design §3): `0` final answer · `1` provider/config/internal
failure · `2` usage error · `3` context full · `4` turn cap · `5` approval
needed but no TTY · `130` interrupted. Tool errors alone never fail the run:
the model sees them as results. The last event, `session_ended`, carries the
same code.
The event stream is the same JSONL written to the session log — one format for
humans (terminal) and machines (CI, scripts). This is the seam the v2 RPC mode
will formalize.

Untrusted context files are skipped in CI unless `--trust-project` is passed —
fine for your own repo, think twice for third-party PRs.

**Done when:** the secrets fixture test from design §11 passes under `--json`
in CI — zero secret values in the transcript and event log.

## 5. Farm: one big task split into parts (experimental MVP)

**Who:** a contributor with a change too big or too slow for one agent.
**Job:** get it done in parallel, safely and auditably. Farm is not "better
than one agent" and costs more tokens than one; it pays on decomposable work
([research](research/farm-multi-agent-community-2026-11.md)).

```sh
jas farm plan "migrate logging to slog across all packages" -o plan.yaml
jas farm run plan.yaml          # CI path; `jas farm "<task>"` in one terminal
```

- A planner agent drafts `plan.yaml`: parts, a role per part (coder, tester,
  researcher, reviewer, or the project's own), the files each part owns, the
  order between parts, and the contracts they share. Refusing is the
  default: small or single-package changes, overlapping ownership or purely
  serial work get a structured reason, exit 8 and a `jas run` suggestion.
- The owner reviews and approves (or edits, or asks for a new plan) before
  any worker starts; the approved plan hash is what CI runs.
- The supervisor (plain code, no LLM) runs 2–3 parts at a time, each in its
  own worktree (in the user cache dir, outside the repo) and branch. Workers post typed
  commitments (owns, interface, blocked, done) to an append-only board,
  delivered as labelled context, a few rounds at most.
- A failed part pauses the parts that depend on it and asks the owner:
  retry, take over, drop, or abort. In CI, `on_failure: drop|abort` decides.
- An integrator agent merges the branches and fixes the seams; the
  supervisor then runs the project's `check` — that is the verdict. With a
  TTY the owner merges, keeps or discards; without one the result is kept.
- Budgets are checked before each model call (per worker, farm total,
  integrator reserve, time limits).
- Worktrees still share the host FS and `$HOME` — not a sandbox. Per-task
  containers are v3 (design.md §3.1 / §9).

Good fits: cross-cutting migrations, a feature across layers (core, CLI,
UI, docs), a batch of small issues grouped by the files they touch. Poor
fits: debugging one bug, tightly coupled refactors, small tasks.

**Done when:** the integrated branch passes the supervisor's `check` and the
owner's decision is recorded — nothing merges without one. Full design:
[superpowers/specs/2026-11-02-farm-design.md](superpowers/specs/2026-11-02-farm-design.md).

## 6. Spec-driven work, alone or with farm (proposed)

**Who:** anyone starting a new app or a sizeable feature. **Job:** turn a
vague idea into a checked spec, then build it section by section — with one
agent, or with farm when the work splits cleanly.

```sh
jas spec spec.md                                    # grill-me interview → approved spec
jas spec --check spec.md                            # CI: exit 0, or 2 with open questions
jas run --spec spec.md --phase scaffold             # skeleton; jas runs `check`
jas run --spec spec.md --section F-todos            # one feature, single agent
jas farm plan --spec spec.md -o plan.yaml && jas farm run plan.yaml   # split by sections
```

In chat the same steps are `/spec`, `/spec scaffold`, `/spec section <id>`
and `/farm --spec spec.md` (live row per part, `/farm attach <part>`).

| Story | Front end | Outcome |
|---|---|---|
| As a solo dev, I want jas to grill me one question at a time, so that my spec is concrete enough to build and test. | chat | approved spec, hash logged |
| As a CI maintainer, I want incomplete specs to fail without model calls, so that I don't pay for vague work. | CLI | exit 2 + open-question list |
| As a CI maintainer, I want a nightly farm from an approved plan + spec, so that big work progresses without touching `main`. | CLI | exit 0, branch kept |
| As a repo owner, I want hard caps on PR-triggered section runs, so that a runaway agent stops before overspending. | CLI | exit 7 |
| As a developer, I want the planner to refuse a split that won't pay and fall back to `jas run`. | both | exit 8 / back to chat |
| As a team lead, I want to audit a farm from its files. | CLI | manifest + `farm.jsonl` answer who/what/why |
| As a developer, I want to unblock a stuck worker and scope approvals to one part. | chat | `/farm attach`, "always (part)" |
| As a developer, I want to drop a failing part, merge the rest, and resume after a crash. | chat | unmet recorded; `/farm resume` |
| As a developer in a second terminal, I want a warning before editing files a farm part owns. | both | overlap ask / warn / fail |

Fourteen step-by-step walkthroughs (commands, what jas shows, events, exit
codes) are in the spec-driven-flow spec's
[User stories & scenarios](superpowers/specs/2026-11-02-spec-driven-flow.md#user-stories--scenarios).
Speed-ups there (e.g. ~2–3× wall-clock for a web app's first cut with farm)
are expectations, not measurements; total tokens
are higher with farm than with one agent.

**Done when:** a spec-driven `--section` run and a spec-driven farm both end
with every acceptance criterion listed as met (with evidence) or explicitly
unmet, and `check` run by jas passes.

## 7. Interactive attach (after farm)

**Who:** the same contributor, mid-migration. **Job:** step into a running
part when the model needs a human call.

In the first farm version, a failed part offers **take over**: the farm
prints the command to continue that part's session in its worktree. In farm
interactive mode, `/farm attach <part>` steers a running worker from the
same chat (farm spec, Interactive mode). Later, `jas attach <part>` connects to the running farm over a Unix socket
(`~/.jas/run/<farm-id>.sock`) and takes over at a turn boundary through the
steering queue, without stopping the farm. If the supervisor has died,
attach falls back to resuming the part's event log.

**Done when:** attach takes over at a turn boundary, and no mutating tool call
is re-executed on takeover or on the resume fallback (design §3 side-effect
rules).

## 8. Research (v2 — opt-in web tools)

**Who:** a dev writing docs or evaluating a dependency. **Job:** pull real
answers from the web into a task, scrubbed like any other tool output.

In-tree `web_fetch` (pure HTTP GET → HTML stripped to text) and `web_search`
(SearXNG when `search_base_url` is set, else DuckDuckGo lite) ship behind
`web_tools=true` (default off; design §12.1). `git_clone` is still deferred.
Enable for chat / `jas run`:

```sh
jas config set --local web_tools true
# optional private search:
# jas config set --local search_base_url http://localhost:8080
jas run "read the Go 1.23 iterator proposal and list the new range-over-func
semantics for our docs"
```

Fetched pages are untrusted input that can carry prompt injection. When web
tools are on, the default approve level rises to `shell` unless you set
`never` explicitly (design §11).

**Done when:** the answer cites fetched URLs, and redaction (§9) has applied
to every fetched page before it entered the transcript.

## 9. Learning (`jas learn` — v3)

**Who:** the dev who solved something fiddly once. **Job:** turn that
research session into a reusable skill.

```sh
jas learn "how to write a uv venv workflow"     # interactive
```

The agent researches (§6 tools) → drafts `SKILL.md` + `references/` per the
agentskills.io spec → shows the draft for approval → saves it. Future
sessions see name + description and load instructions on demand. A skill is
injected into every later session, so nothing web-derived installs unreviewed:
`jas learn --auto` only queues drafts in `~/.jas/skills/.pending/`, installed
via `jas skills approve <name>`.

**Done when:** the skill shows in `jas skills list`, loads on demand in a
fresh session, never overwrites an existing skill without `--force`, and an
`--auto` draft is never advertised before approval.

## 10. Per-project operating rules (v2 scenarios)

**Who:** a repo owner. **Job:** version the agent's behavior with the code.

A repo can carry its own agent policy — checked in, reviewable:

```yaml
# .jas/scenario.yaml
system: |
  You work on an ML repo. Experiments live in experiments/;
  never touch data/; log every run to results.jsonl.
sandbox:
  deny: ["data/**:write", ".env*", "secrets/**"]
hooks:
  pre_apply: [{script: .jas/hooks/lint.sh}]
```

First time jas sees a scenario with hooks or MCP servers it asks for trust
— the same gate v1 uses for `AGENTS.md`, extended to scenarios. It is a
startup gate, **not a security boundary** (pi's model; design §11). The trust
hash covers the scenario and the hook scripts it names, not whatever those
scripts call. Hooks execute with your permissions; review `.jas/hooks/` like
you review CI config. Anyone cloning the repo gets the same agent
behavior.

**Done when:** a first-time contributor gets the trust prompt, and the
secrets fixture (design §11) passes under this scenario's deny floor.

## Anti-use-cases (what jas deliberately isn't)

- **Not an IDE plugin.** No LSP/editor integration — that's what the v2 RPC
  mode enables others to build.
- **Not an open-ended general chat product.** Interactive chat **is** a core
  feature (§1), but it is task-shaped pair programming over the same harness
  as `jas run` (same tools, approvals, event log) — not a social chatbot or
  a replacement for a general chat client.
- **Not a file-tree cookie-cutter.** Blueprints / `jas new` were dropped
  (design §8). Init today is chat + `AGENTS.md` / skills; reusable *agent*
  policy is scenarios (`jas bootstrap`, v2) and, proposed, `jas spec` scaffold.
- **Not autonomous infra.** Shell is YOLO-mode in v1 (documented; containerize
  if you care). jas doesn't manage servers, deploy, or hold production keys.
- **Not a secret store.** Secrets never enter the transcript (§9), but jas is
  not a vault — blind secret access (v2) proxies *declared* keys only.
- **Not "pi, but better."** Different jobs: pi optimizes interactive coding
  product depth; jas keeps chat + project setup on a CI-grade receipt and a
  narrow farm. Pick by the job (design §14).
