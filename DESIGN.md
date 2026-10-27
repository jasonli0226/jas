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
│   ├── sandbox.go           exec policy (cwd, timeout, deny patterns)
│   ├── redact.go            output redaction middleware (§9 layer 2)
│   ├── budget.go            token/cost accounting (compaction trigger, §7 metrics)
│   └── events.go            everything the agent does is a JSONL event (single writer)
│
├── providers/               OpenAI-compatible HTTP (chat completions + SSE)
│   └── openai.go            any OpenRouter/vLLM/ollama endpoint. No SDK deps.
│
├── tools/                   built-in toolset (v1)
│   ├── shell.go             run commands (sandboxed)
│   ├── files.go             read/write/patch/ls
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
for {
    resp := provider.Stream(ctx, messages)    // SSE stream
    tc, err := accumulateToolCalls(resp)      // tool calls arrive as *deltas*
    // (split across SSE chunks — reassembly is the fiddliest 100 lines here)
    for ev := range resp {
        if ev.Text != "" { ui.Print(ev) }
    }
    results := parallel.Execute(tc, ctx)      // parallel tool execution, per-call timeout
    // errors are RESULTS, not failures — the model sees the error and retries
    messages.Append(assistant, tc, results...)
    if done { break }                         // no tool calls → final answer
    // ctx can be cancelled: Ctrl-C aborts the stream, events.jsonl keeps state
}
```

Every step appends a JSONL event to `<session>/events.jsonl` — this is the
checkpoint/resume primitive (crash → replay from last event) and the audit log.
**Single writer:** all appends (loop, parallel tool exec, farm tasks) go through
one events.Writer goroutine; concurrent callers just send events.

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
- **Skills (later)**: a skill = instruction file + optional tool manifests; loaded into
  system prompt or a toolset, Hermes-style. Not in v1.

## 4. v1 scope (weekend one)

**Goal:** a single Go binary that beats `pi --print` on cold start and runs a real
coding task end-to-end.

- [ ] `jas run "task string"` — streaming chat loop, OpenAI-compatible endpoint
- [ ] built-in tools: shell, read/write/patch file, ls, grep
- [ ] parallel tool execution (errgroup, per-call timeout)
- [ ] SSE tool-call delta reassembly (fuzz-tested against recorded streams)
- [ ] JSONL event log per session (single writer); token usage logged per request
- [ ] output redaction middleware (§9 layer 2) active from day one
- [ ] `JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` env config (settings dir = v2)
- [ ] benchmark: cold start vs pi; tokens/latency on 3 fixed tasks
- [ ] README + demo GIF

**Explicitly out of v1:** MCP, TUI, farm, skills. Resist scope creep.

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
- [ ] **Project trust**: first encounter of a project `.jas/scenario.yaml` with
      hooks/MCP → interactive `ask/always/never` (pi's model; startup gate only)
- [ ] **Steering & follow-up queues**: user messages enter after the current
      turn or after the run ends; one-at-a-time/all
- [ ] **Evals**: fixed-task eval harness with fixtures — with/without scenario
      lift reports (pi's vitest-evals idea, simplified)

## 6. v3 ideas

- skills (instruction+tool manifests) — Hermes-style, aligned with the
  agentskills.io spec (pi follows it; interop with Claude Code/Hermes)
- **web access** (see §12): `web_search` + `web_fetch` as scenario tools
- **`jas learn <topic>`**: research → draft → save a skill (see §12.2)
- codemode: model-written JS in a **goja** (pure-Go JS) sandbox, tools as the
  only capability (absorbs backlog #14 mini-codemode)
- virtual models: routing model that dispatches each request to a physical
  model (`jas/auto:low → cheap-model`) (absorbs backlog #3)
- git-installable scenario/skill packages (`jas scenario install git:…`)
- containerized farm tasks (bubblewrap/Docker)
- checkpoint/resume from events.jsonl replay
- tool-trace replay & cost simulator (absorbs backlog #11)
- Jev-classifier tool-risk gate (absorbs #5)
- OTel export of sessions (telemetry)

## 7. Benchmarks (v1 acceptance)

| metric | target |
|---|---|
| cold start | < 50ms (`jas --version`, `jas --print` noop) |
| binary size | < 15MB static |
| simple task | "add a header comment to main.go" — correct, one round trip |
| parallel tools | 4 concurrent file reads + 1 grep < 2× single-call time |

## 8. Scenarios: declarative, bootstrap + blueprint lifecycle (v2)

A scenario = a preset bundle of (system prompt + toolset + sandbox policy + hooks).
**Scenarios are data, never hard-coded.** One artifact, two roles:

- **Runtime rules** — `.jas/scenario.yaml` in a repo governs how jas operates there
- **Blueprint** — a saved best-practice preset that governs how jas *creates* a new project

### 8.1 Discovery & precedence (most specific wins, layer-merge)

```
CLI flag            jas run --scenario <path>       explicit, wins
└── project .jas/   .jas/scenario.yaml              per-project, versioned with repo
    └── user global ~/.jas/scenarios/<name>/        personal blueprints
        └── built-in scenarios/ (Go-compiled)      2-3 starter presets
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

## 9. Secrets & sensitive data (layers 1-2 + 4 = v1; blind secret access = v2)

**Principle: the agent never sees secret *values* — not in tool output, not in
the transcript, not in the event log.** How peers do it, and what jas adopts:

| Approach | Who uses it | jas |
|---|---|---|
| Env passthrough (agent process gets keys) | many naive agents | ❌ root of all leaks |
| Deny-list file reads | Claude Code (`.env` in default ignores) | ✅ baseline |
| Redaction middleware on tool output | pi, modern harnesses | ✅ output scrubber |
| Secret proxy (harness holds keys, agent requests "use key X" blind) | OpenAI Codex cloud, pi | ✅ v2 |
| Isolated sandbox FS (no dotfiles mounted) | cloud agents | 🔜 farm worktrees only mount project files |

Concretely, four layers (defense in depth — any one failing must not leak):

1. **File deny-list** (scenario `sandbox.deny`): `.env*`, `**/.env`, `secrets/**`,
   `*.pem`, `id_rsa*`, `*.p12`, `.git-credentials`, `~/.aws/**`, `~/.ssh/**`.
   Loader adds a **hard floor**: these patterns are always denied, even if a
   scenario tries to re-allow them (`deny` is additive; the floor cannot be lifted).
2. **Output redaction** (core middleware, runs on every tool result before it
   enters the model transcript or event log): regex scrub of high-entropy
   strings + known token shapes (`sk-…`, `ghp_…`, `AKIA…`, `Bearer …`,
   JWTs, `-----BEGIN … PRIVATE KEY-----`, long base64). Replaced with
   `[REDACTED:<kind>]`. The scrubber itself is tested against a fixture corpus.
3. **Blind secret access (v2):** scenario can declare `secrets: [OPENROUTER_KEY]`.
   jas loads them from the environment into its own memory; tools reference
   them as `{{secret:NAME}}` placeholders resolved at execution time *inside*
   jas — the value never round-trips through the model. A tool call needing an
   API key takes the placeholder, not the key itself.
4. **Session hygiene:** `events.jsonl` and the model transcript are the same
   redaction gate; a `jas session redact` one-shot command re-scrubs any log
   file written before the middleware existed (upgrade path). Crash-resume
   replays read only already-redacted events.

Also denied at the **shell** layer: `env`, `printenv`, `cat .env*` style
commands are matched against the same deny semantics (the shell tool inspects
the command string for deny-pattern hits before exec). Not bulletproof —
that's why layer 2 (output redaction) exists as the backstop.

**Acceptance test for v1:** a scenario with a `.env` present; agent is asked
"print the config"; transcript must contain zero secret values (CI assertion
against a fixture repo).

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

**Resolution order** (most specific wins, maps merge key-by-key; lists in lists
use pi's `+name`/`-name` add/remove semantics):

```
CLI flag > project .jas/scenario.yaml > ~/.jas/config.json > built-in defaults
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

**What env vars still do:** highest-precedence transient overrides
(`JAS_MODEL`, `JAS_BASE_URL`, `JAS_API_KEY` beat config.json) — useful for
one-off runs and CI; persistent config lives in `~/.jas/`.

**Session storage** stays simple: `~/.jas/sessions/<id>/events.jsonl` (§3) is
the whole story — no DB, replay = tail the file. Farm task state (v2) lives in
`~/.jas/farm.json`.

## 11. v1 acceptance: security + trust

- **Project trust (v1, one-day feature):** the first time jas sees a project
  `.jas/scenario.yaml` that declares hooks or MCP servers, it asks
  `trust this project? [ask/always/never]`; the answer is cached in
  `~/.jas/trust.json`. This is a startup gate only — not a security boundary
  (pi's model). It exists because scenarios carry executable hooks.
- **Secrets fixture test (from §9):** CI assertion, zero secret values in the
  transcript when a `.env` is present.
- **Redaction middleware active in v1** — it's ~60 lines of regex and it is the
  backstop for every later tool that produces output.

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
  blind spots (symlinks, novel secret formats). Mitigation: the redaction
  middleware is the backstop, and the fixture-corpus tests run on every release.