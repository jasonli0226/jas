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
- `--approve` is a ladder: `never` (default, permissive) → `mutating`
  (confirm file edits and shell) → `shell` (also confirm every shell
  command). Audit runs use `never`; fix runs use `mutating` — two jobs, one
  flag.
- The versioned JSONL event log (`~/.jas/sessions/<id>/events.jsonl`) is the
  audit trail: every tool call, every result, every token count.
- Project conventions come free: v1 loads `AGENTS.md`/`CLAUDE.md` from cwd.

**Done when:** the header-comment task finishes in ≤3 turns (design §7),
files changed, `git diff` shows exactly that change.

**Why jas and not `pi --print` / a raw API loop:** cold-start speed, the tool
policy (parallel reads, serialized mutations), and the context budget that
elides old tool results instead of blowing the window on a 20-turn task.

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

A mutating call is never "half-happened": jas fsyncs `tool_started` *before*
the call executes (design §3). A crash between the fsync and the result means
the replay records `interrupted` — the call is **not** re-executed; the model
sees the cancelled result and decides next. In v1 you inspect with:

```sh
tail ~/.jas/sessions/<id>/events.jsonl | jq 'select(.type == "tool_started")'
```

**Done when (v2):** `jas resume <id>` continues from the log; no mutating
tool call from before the crash ever runs twice.

### 2.2 Context-full and turn-cap stops (v1)

**Who:** the same dev, on a long task. **Job:** fail loudly, not silently.

v1 tracks `usage.prompt_tokens` against the model window, elides old tool
results (last K verbatim, rest stubbed), and stops cleanly on
`context_length_exceeded`. `JAS_MAX_TURNS` (default 50) is the runaway stop.

**Done when:** a context-full or turn-cap stop exits non-zero with a clear
message — and the CI contract in §3 holds.

## 3. CI / scripted use (`jas run --json` — v1)

**Who:** a CI author wrapping agent runs in a pipeline. **Job:** machine-
readable runs with a clean failure contract.

Approver defaults to `never` (YOLO), so treat the job container (or an
explicit `--approve` / FS / network posture) as the real boundary — same
stance as design.md §3.1 and the anti-use-cases below.

```sh
# Prefer a throwaway container; jas itself is not the sandbox.
docker run --rm -v "$PWD":/work -w /work jas:ci \
  jas run --json "check all TODO comments in src/ and list them with file:line" \
  | jq -c 'select(.type == "tool_result")'
```

Exit contract (v1): `0` only when the task finishes cleanly; non-zero on
context-full, turn-cap, unrecoverable tool errors, or a denied approval.
The event stream is the same JSONL written to the session log — one format for
humans (terminal) and machines (CI, scripts). This is the seam the v2 RPC mode
will formalize.

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
- Built on the v1 core + the JSONL wire format (each worker speaks the same
  protocol).

**Done when:** N diffs are returned, and the human's accept/merge/discard
decision is recorded — nothing merges without one.

## 5. Interactive attach (v2)

**Who:** the same contributor, mid-migration. **Job:** step into a running
agent when the model needs a human call.

```sh
jas farm --serve --n 2 "migrate the config schema"   # supervisor + workers
jas attach 2   # from another terminal: connects to the supervisor over the
               # v2 RPC/JSONL protocol and takes over task 2's session
```

The farm supervisor is the long-lived process (`jas serve`-style, design §5);
attach is a client of it — not a shell background job. Session state lives in
the event log, so attach = replay + continue — the same resume primitive
crash recovery uses.

**Done when:** attach takes over from the event log and no mutating tool call
is re-executed on takeover (design §3 side-effect rules).

## 6. Research (v2 — scenario web tools)

**Who:** a dev writing docs or evaluating a dependency. **Job:** pull real
answers from the web into a task, scrubbed like any other tool output.

v2 adds `web_fetch` (pure HTTP GET), `web_search` (pluggable provider chain,
DuckDuckGo keyless default), and `git_clone` (explicit, path-confined) as
scenario tools (design §12.1). An illustrative invocation:

```sh
jas run --scenario <your-web-tools-preset> "read the Go 1.23 iterator proposal
and list the new range-over-func semantics for our docs"
```

(The preset is illustrative — design §8.1 names the 2-3 built-in starters
separately.)

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
sessions see name + description and load instructions on demand.

**Done when:** the skill shows in `jas skills list`, loads on demand in a
fresh session, and never overwrites an existing skill without `--force`.

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
(`ask/always/never`) — a startup gate, **not a security boundary** (pi's
model; design §11). Hooks execute with your permissions; review `.jas/hooks/`
like you review CI config. Anyone cloning the repo gets the same agent
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
