# Use cases

Concrete situations where jas fits — mapped to what actually ships in v1 vs
later cuts. The design doc (§4–§6) defines scope; this doc shows the intent
behind it.

## 1. One-shot coding tasks (`jas run` — v1)

The core loop: a task in, a diff out, human reviews.

```sh
jas run "add a header comment to main.go explaining the retry logic"
jas run "write a table-driven test for parseConfig covering bad env vars"
jas run --approve=mutating "upgrade the deprecated io/ioutil calls in pkg/"
```

- Streaming output — you watch the agent work in the terminal.
- `--approve=mutating` gates file edits/shell behind a confirm prompt.
- The versioned JSONL event log (`~/.jas/sessions/<id>/events.jsonl`) is the
  audit trail: every tool call, every result, every token count.
- Project conventions come free: v1 loads `AGENTS.md`/`CLAUDE.md` from cwd.

**Why jas and not `pi --print` / a raw API loop:** cold-start speed, the tool
policy (parallel reads, serialized mutations), and the context budget that
elides old tool results instead of blowing the window on a 20-turn task.

## 2. Batch work across a repo (v1, one at a time)

Same primitive, several invocations — deliberately dumb-and-simple:

```sh
jas run --approve=mutating "rename function foo to bar in pkg/api/, update all callers"
jas run "summarize what cmd/farm does in 3 bullet points"
```

Each run is an isolated session with its own event log. No daemon, no state to
corrupt — crash mid-task, re-run, the log still tells you what happened.

## 3. CI / scripted use (`jas run --json` — v1)

Machine-readable output for pipelines and wrappers. Approver defaults to
`never` (YOLO), so treat the job container (or an explicit
`--approve` / FS / network posture) as the real boundary — same stance as
design.md §3.1 and the anti-use-cases below.

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

## 4. Farm: N parallel worktree agents (v2)

One task, N attempts, pick the best diff:

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

## 5. Interactive attach (v2)

A long-running task needs a human in the middle:

```sh
jas farm --n 2 "migrate the config schema" &
jas attach 2   # take over task 2's session, chat/steer it live
```

Session state lives in the event log, so attach = replay + continue — the same
resume primitive crash recovery uses.

## 6. Research & learning (v2/v3)

Web access lands as scenario tools (`web_fetch`, `web_search`, `git_clone`):

```sh
jas run --scenario research "read the Go 1.23 iterator proposal and summarize
the new range-over-func semantics for our docs"
```

And the long-term path — `jas learn <topic>` distills a research session into
a reusable skill (`SKILL.md`) that future sessions pick up automatically.

## 7. Per-project operating rules (v2 scenarios)

A repo can carry its own agent policy — checked in, versioned, reviewable:

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

First time jas sees a scenario with hooks or MCP servers it asks for trust —
prompt-injection defense, pi-style. Anyone cloning the repo gets the same
agent behavior.

## Anti-use-cases (what jas deliberately isn't)

- **Not an IDE plugin.** No LSP/editor integration — that's what the v2 RPC
  mode enables others to build.
- **Not a chat assistant.** jas is task-shaped: a prompt, tool calls, a result.
  For open-ended conversation use a chat client.
- **Not autonomous infra.** Shell is YOLO-mode in v1 (documented; containerize
  if you care). jas doesn't manage servers, deploy, or hold production keys.
- **Not a secret store.** Secrets never enter the transcript (§9), but jas is
  not a vault — blind secret access (v2) proxies *declared* keys only.
