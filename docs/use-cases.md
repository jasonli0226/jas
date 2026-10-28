# Use cases

Concrete situations where jas fits — mapped to what actually ships in v1 vs
later cuts. The design doc (§4–§6) defines scope; this doc shows the intent
behind it. Each section names who it's for, the job to be done, and what
"done" looks like.

## 1. One-shot coding tasks (`jas run` — v1)

**Who:** a solo dev in a weekend loop. **Job:** get a reviewed change without
leaving the terminal.

Task in, files changed, you `git diff` — review stays with the human, as it
should until the v2 review queue.

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

## 2. Batch work across a repo (v1, one at a time)

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

### 2.1 Crash-resume (v1 design, resume ships v2)

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

### 2.2 Context-full and turn-cap stops (v1)

**Who:** the same dev, on a long task. **Job:** fail loudly, not silently.

v1 tracks prompt tokens against the model's context window (from the built-in
catalog, or `jas config set models.<name>.context_window …`). Past a threshold
it elides old tool results in one chunk (last K verbatim, rest stubbed), which
keeps the prompt cache warm. It stops cleanly on `context_length_exceeded`.
`max_turns` (default 50; `jas config set max_turns …`) is the runaway stop.

**Done when:** a context-full stop exits 3 and a turn-cap stop exits 4, each
with a clear message — and the CI contract in §3 holds.

## 3. CI / scripted use (`jas run --json` — v1)

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

## 4. Farm: N parallel worktree agents (v2)

**Who:** an experiments-heavy contributor. **Job:** get N attempts at the
same fix and pick the one to keep.

```sh
jas farm --n 3 "fix all failing tests in pkg/store"
```

- Non-overlapping git worktrees per task (they won't clobber each other's
  working trees). Worktrees still share the host FS and `$HOME` — not a
  sandbox. Isolation answer is bubblewrap/Docker in v1 docs, and per-task
  containers in v3 (design.md §3.1 / §9).
- Review queue: diffs come back side by side; accept / merge / discard.
- Built on the v1 core. Tasks are goroutines inside the `jas farm` process,
  each with its worktree as its project root, and each emitting the same
  event stream as `jas run --json`.

**Done when:** N diffs are returned, and the human's accept/merge/discard
decision is recorded — nothing merges without one.

## 5. Interactive attach (v2)

**Who:** the same contributor, mid-migration. **Job:** step into a running
agent when the model needs a human call.

```sh
jas farm --n 2 "migrate the config schema"   # supervisor + workers
jas attach 2   # from another terminal: connects to the farm's Unix socket
               # and takes over task 2's session
```

`jas farm` is the supervisor and stays up until its review queue is drained.
`attach` is a client on its Unix socket (`~/.jas/run/<farm-id>.sock`), not a
shell background job. Attach waits for the task's current turn to finish, then
your messages enter through the steering queue. If the supervisor has died,
attach falls back to `jas resume`: replay the event log and continue — the
same primitive crash recovery uses.

**Done when:** attach takes over at a turn boundary, and no mutating tool call
is re-executed on takeover or on the resume fallback (design §3 side-effect
rules).

## 6. Research (v2 — scenario web tools)

**Who:** a dev writing docs or evaluating a dependency. **Job:** pull real
answers from the web into a task, scrubbed like any other tool output.

v2 adds `web_fetch` (pure HTTP GET), `web_search` (pluggable provider chain,
DuckDuckGo keyless default), and `git_clone` (explicit, path-confined) as
scenario tools (design §12.1). An illustrative invocation:

```sh
jas run --scenario <web-tools-scenario> "read the Go 1.23 iterator proposal
and list the new range-over-func semantics for our docs"
```

(`--scenario` takes a name or a path; the name is illustrative — design §8.1
names the 2-3 built-in starters separately.)

Fetched pages are untrusted input that can carry prompt injection. When web
tools are on, the default approve level rises to `shell` unless you set
`never` explicitly (design §11).

**Done when:** the answer cites fetched URLs, and redaction (§9) has applied
to every fetched page before it entered the transcript.

## 7. Learning (`jas learn` — v3)

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

## 8. Per-project operating rules (v2 scenarios)

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
- **Not a chat assistant.** jas is task-shaped: a prompt, tool calls, a
  checkable result. For open-ended conversation use a chat client.
- **Not autonomous infra.** Shell is YOLO-mode in v1 (documented; containerize
  if you care). jas doesn't manage servers, deploy, or hold production keys.
- **Not a secret store.** Secrets never enter the transcript (§9), but jas is
  not a vault — blind secret access (v2) proxies *declared* keys only.
