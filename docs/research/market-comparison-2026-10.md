# Market comparison: jas vs coding-agent harnesses (Oct 2026)

**Date:** 2026-10-30 (Asia/Hong_Kong)
**Written against:** jas `1f0e9ba` (feat: session core and resume)
**Interactive Page:** https://cursor.com/canvas/store-5944ee4d-1ef2-45c9-9014-c9bb81b9bde0/e86ad835-0961-4c0b-98de-115045502d9b

Star counts, licenses and release dates were pulled from GitHub on 2026-10-30.
Benchmark figures are model + harness pairs, mostly vendor-submitted.
Competitor details go stale fast — re-check before relying on numbers.

## 1. Executive verdict

| Metric | Value | Note |
|---|---|---|
| jas static binary | 7.48 MB | vs 26.9 MB Crush and 83 MB Codex, both compressed |
| jas cold start | 1.6 ms | no peer publishes a figure |
| Cache hit, jas vs pi | 57% vs 84% | n=1, GLM-5.3 via OpenRouter |
| Success-verified benchmarks | 0 | peers publish Terminal-Bench rows |

The market has split three ways. Vendor harnesses (Claude Code, Codex CLI,
Gemini CLI) win on model co-design, OS sandboxes and Terminal-Bench scores.
Open community harnesses (opencode, pi, goose, Cline) win on provider breadth,
extensibility and traction, from 56k to 223k stars. Research baselines
(mini-swe-agent, SWE-agent) win on simplicity and reproducibility. jas fits
none of these yet. Its real niche is a small, MIT-licensed, Go-native,
headless-first harness whose run artifacts are trustworthy enough for CI and
audit.

The niche exists but is narrow. Crush is the only Go peer with traction
(28.9k stars), and it is FSL-licensed rather than OSI-licensed, so the
open-source Go slot is open. Small Go entrants (nib, san) show the slot is
also easy to enter. jas's defensible assets are its contracts, not its
intelligence: the exit-code contract with no-TTY fail-fast (exit 5), the
fsynced write-ahead JSONL log with synthesized cancellations, deterministic
read batches with mutating barriers, process-tree kill, and a compiled-in
secret deny floor that is on by default rather than opt-in config.

> **Bottom line.** Do not compete with pi and opencode on the interactive
> experience. Make jas the harness a platform team can run unattended in CI
> and audit afterwards. That requires closing four gaps first: provider
> fidelity (reasoning replay and explicit caching), a hardened and published
> event-log spec, an opt-in OS sandbox, and a success-verified benchmark.

## 2. Comparison tables

Cells marked **not published** have no primary-source figure. Artifact sizes
are release downloads for Linux x86_64. Codex, Crush and goose sizes are
compressed archives, so the gap to jas's uncompressed 7.48 MB is larger than
shown. Terminal-Bench (TB) rows pair one model with one harness and are
mostly vendor-submitted. They show what a harness can reach, not a controlled
harness-vs-harness comparison. The 2.1 figures come from a third-party
aggregator of official rows, dated 2026-09-08.

### Overview

| Product | Type | Language | License | GitHub stars (2026-10-30) | Latest release |
|---|---|---|---|---|---|
| jas (v1, 1f0e9ba) | Headless-first CLI harness | Go 1.27, CGO off | MIT | private repo | v1 (private repo) |
| pi | Minimal extensible CLI harness | TypeScript (Node) | MIT | 118,769 | v1.4.0 (2026-10-21) |
| Claude Code | Vendor CLI + IDE + SDK | TypeScript | Proprietary | 153,838 | v2.1.312 (2026-10-27) |
| OpenAI Codex CLI | Vendor CLI + app-server | Rust | Apache-2.0 | 131,555 | rust-v0.165.0 (2026-10-27) |
| opencode | Client / server CLI + desktop | TypeScript (Bun) | MIT | 222,988 | v2.2.1 (2026-10-29) |
| Gemini CLI | Vendor CLI | TypeScript (Node) | Apache-2.0 | 108,611 | v0.66.0 (2026-10-27) |
| goose | CLI + desktop, MCP-native | Rust | Apache-2.0 | 55,911 | v1.57.0 (2026-10-28) |
| Crush | Go TUI agent (Charm) | Go | FSL-1.1-MIT | 28,872 | v0.99.0 (2026-10-29) |
| Amp | Hosted agent: CLI, web, apps, cloud orbs | Closed source | Proprietary | closed source | Continuous (hosted) |
| aider | Pair-programming CLI | Python | Apache-2.0 | 49,814 | v0.86.0 (2025-08-09); last push 2026-05-22 |
| Cline | IDE extension + CLI (reference) | TypeScript | Apache-2.0 | 71,029 | v4.1.26 (2026-10-26) |
| Roo Code | IDE extension (Cline fork) | TypeScript | Apache-2.0 | 24,254 | v3.54.0 (2026-05-15); repo archived |
| mini-swe-agent | Research baseline | Python | MIT | 8,952 | v2 line |
| SWE-agent | Research harness (ACI) | Python | MIT | 20,684 | Maintenance; team now leads with mini |
| OpenHands | Agent platform (CLI, GUI, cloud) | TypeScript / Python | MIT | 92,196 | Active |
| nib / san | Go micro-harnesses | Go | nib MIT; san Apache-2.0 | 52 + 146 | Both pushed late Oct 2026 |

### Runtime

| Product | Distribution | Artifact size | Cold start |
|---|---|---|---|
| jas (v1, 1f0e9ba) | Single static binary; 3 direct deps (doublestar, x/sys, x/term) | 7.48 MB uncompressed | 1.6 ms median --version; 13.1 ms to first HTTP request |
| pi | npm (@earendil-works/pi-coding-agent); needs Node | not published | not published |
| Claude Code | Native installer / npm | not published | not published |
| OpenAI Codex CLI | Static musl binary, npm, Homebrew | Linux x86_64 musl: 115.8 MB .tar.gz / 83.0 MB .zst (compressed) | not published |
| opencode | Compiled binaries, npm, desktop apps | linux-x64: 79.7 MB .tar.gz (v1.19.5 asset) | not published |
| Gemini CLI | npm, Homebrew | not published | not published |
| goose | Binaries, desktop apps | Linux x86_64 musl: 43.3 MB .tar.bz2 (compressed) | not published |
| Crush | Binaries, Homebrew, apt, Nix | Linux x86_64: 26.9 MB .tar.gz (compressed) | not published |
| Amp | CLI + web + macOS / iOS / Android | not published | not published |
| aider | pip / pipx | not published | not published |
| Cline | VS Code / JetBrains extension, CLI | not published | not published |
| Roo Code | VS Code extension | not published | not published |
| mini-swe-agent | pip | not published | README: starts much faster than Claude Code (no figure) |
| SWE-agent | pip | not published | not published |
| OpenHands | Docker runtime, CLI | not published | not published |
| nib / san | Single static binary each | not published | not published |

### Providers

| Product | Providers / native APIs | Prompt caching & reasoning |
|---|---|---|
| jas (v1, 1f0e9ba) | OpenAI-compatible Chat Completions only (OpenRouter, Ollama, etc.). No Anthropic Messages, no Responses API | Implicit only: one-pass elision keeps the prefix stable. No cache_control, session_id or prompt_cache_key; reasoning / reasoning_details not sent or replayed; parses cached_tokens only |
| pi | pi-ai multi-provider: native Anthropic Messages, OpenAI Responses, Chat Completions with per-provider compat | Sends cache_control breakpoints, prompt_cache_key, session-affinity headers (x-session-id for OpenRouter); replays reasoning_details |
| Claude Code | Anthropic models via API, Bedrock, Vertex | Native prompt caching and extended thinking with effort levels |
| OpenAI Codex CLI | OpenAI Responses API native (ChatGPT sign-in or API key); configurable other providers | Native Responses caching and reasoning effort |
| opencode | Many providers via models.dev / AI SDK | Provider-native |
| Gemini CLI | Gemini via Google login, API key, Vertex | Gemini implicit caching; thinking models |
| goose | 15+ providers; Claude summarized thinking requested (v1.57) | Provider-dependent; not documented as a feature |
| Crush | Catwalk v3 provider types: completions, responses, messages (OpenAI-compatible, OpenAI Responses, Anthropic Messages) | Thinking always / never / toggleable per model; reasoning levels in picker |
| Amp | Curated multi-model, vendor-selected per mode; no BYO-model focus | Vendor-managed |
| aider | Any via litellm | Optional Anthropic prompt caching |
| Cline | Many providers, BYO key | Provider-native |
| Roo Code | Many providers | Provider-native |
| mini-swe-agent | litellm, OpenRouter, Portkey; /completion and /response endpoints, interleaved thinking | Via model layer |
| SWE-agent | litellm | Via model layer |
| OpenHands | Many via litellm | Provider-native |
| nib / san | nib: local-LLM friendly; san: not reviewed | not published |

### Tools & safety

| Product | Tools & edit format | Permissions & sandbox |
|---|---|---|
| jas (v1, 1f0e9ba) | shell, read, write, edit (exact old_string/new_string), ls, grep; 32 KB result cap | Approval ladder never / shell / mutating; no OS sandbox (docs point to bwrap or Docker); env allowlist; compiled-in secret deny floor + redaction; trust gate for AGENTS.md / CLAUDE.md |
| pi | read, write, edit, bash by default | No built-in sandbox; gating left to extensions |
| Claude Code | Read, Edit (exact-string), Write, Bash, Grep, Glob, WebFetch, WebSearch, Monitor and more | Allow / ask / deny rules; modes manual, acceptEdits, plan, auto (classifier), bypass. Opt-in OS sandbox for shell (Seatbelt; bwrap + socat on Linux) with domain-allowlist proxy and credential deny / mask rules |
| OpenAI Codex CLI | shell + apply_patch (patch format), web search (cached index by default), MCP tools | OS sandbox on by default: Seatbelt, bwrap + seccomp, Windows MXC; read-only / workspace-write / danger-full-access × on-request / never / granular approvals; network off by default, domain-policy proxy; protected .git/.codex/.agents; auto-review agent |
| opencode | read, edit, write, bash, grep, glob, patch, LSP | Per-tool allow / ask / deny; no OS sandbox documented |
| Gemini CLI | File read / write / replace (exact-string), shell, glob, grep, web fetch, Google search | Approval modes incl. YOLO; sandbox via Seatbelt or Docker / Podman |
| goose | Everything is an MCP extension (developer: shell, editor); 70+ extensions | Approval modes; HTTPS allowlist with redirect validation (v1.57); no OS sandbox documented |
| Crush | File view / edit / write, bash, grep, glob, fetch, LSP context | Permission prompts, allow-listed tools, --yolo; no OS sandbox documented |
| Amp | Built-ins plus Oracle (read-only reasoning), Librarian, Finder | Isolation via per-thread cloud machines (orbs) |
| aider | No tool-calling loop: model emits edit blocks (search/replace diff, whole, udiff, architect / editor) | Confirms shell commands; auto-commits to git as the undo layer; no sandbox |
| Cline | File ops, terminal, browser; diff-based edits | Per-action approval with auto-approve settings; workspace checkpoints |
| Roo Code | Cline-derived | Per-mode tool groups, auto-approve |
| mini-swe-agent | bash only; no tool-calling interface required | Environment swap: local, Docker / Podman, Singularity, bubblewrap |
| SWE-agent | Custom agent-computer interface tools | Docker via SWE-ReX |
| OpenHands | Shell, editor, browser | Docker sandbox runtime |
| nib / san | Not reviewed | nib: tool approval |

### Context & sessions

| Product | Context management | Sessions |
|---|---|---|
| jas (v1, 1f0e9ba) | Chunked elision at elide_at (60% of window) in one pass; no summarizing compaction (v2) | Versioned JSONL event log, fsynced write-ahead tool_started, synthesized cancel results; resume. Fork / tree in v2 |
| pi | Compaction plus branch summaries | Tree-structured sessions; resume, branch / fork |
| Claude Code | Auto-compaction and /compact | --resume / --continue, /branch |
| OpenAI Codex CLI | Auto-compaction | Resume; history persisted under CODEX_HOME |
| opencode | Auto-compaction | Server-held sessions; share, revert |
| Gemini CLI | Chat compression | Checkpointing, saved chats, resume |
| goose | Context summarization | Resume, fork; caller-supplied session IDs (v1.57) |
| Crush | Not documented in README | Multiple sessions per project; switch model mid-session |
| Amp | Vendor-managed; Task subagents with isolated windows | Shareable threads across devices |
| aider | tree-sitter repo map; chat-history summarization | Restore chat history from file |
| Cline | Context condensing | Task history, checkpoints / restore |
| Roo Code | Context condensing | Task history, checkpoints |
| mini-swe-agent | None: fully linear history (trajectory == messages) | Trajectory files |
| SWE-agent | History processors | Trajectories |
| OpenHands | Condensers | Conversations, resume |
| nib / san | san: lean context (claimed) | not published |

### Extensibility & headless

| Product | MCP / skills / hooks / plugins | Headless, CI, RPC |
|---|---|---|
| jas (v1, 1f0e9ba) | Agent Skills (.agents/skills, .claude/skills; 64 KB catalog). No MCP, hooks or plugins yet (v2) | jas run --json; exit codes 0/1/2/3 context full/4 turn cap/5 approval needed without TTY/130. No RPC yet (jas serve in v2) |
| pi | TS extensions, skills, prompt templates, packages (codemode, pi-durable, pi-telemetry) | Four modes: interactive, print / JSON, RPC, SDK |
| Claude Code | MCP, skills, hooks (PreToolUse etc.), plugins + marketplace, mods, subagents | -p with stream-json output; Agent SDK |
| OpenAI Codex CLI | MCP (client and server), skills, plugin marketplace, MCP Apps, AGENTS.md | codex exec (stdin, JSON), app-server JSON-RPC, SDK; ACP via adapter |
| opencode | MCP, plugins, custom agents and commands | opencode run, opencode serve HTTP API, SDK, native ACP |
| Gemini CLI | MCP, extensions, custom commands | -p with --output-format json / stream-json; native ACP |
| goose | MCP extensions, YAML recipes and subrecipes, code-mode | goose run --recipe, scheduler, ACP server (goose-acp) |
| Crush | MCP (stdio, http, sse) with OAuth, Agent Skills (incl. .claude/.cursor paths), hooks (v0.99), LSP | Non-interactive mode; editor-host tools (v0.99) |
| Amp | Plugins, MCP | CLI usable from scripts |
| aider | None (no plugin system) | --message scripting, Python API |
| Cline | MCP marketplace, rules, hooks | CLI |
| Roo Code | MCP, custom modes | not published |
| mini-swe-agent | Python subclassing | Batch runners for benchmarks |
| SWE-agent | YAML config, custom tools | Batch mode |
| OpenHands | MCP, microagents | Headless mode, SDK |
| nib / san | nib: MCP, plugins, subagents | not published |

### Multi-agent & observability

| Product | Multi-agent / parallel | Observability |
|---|---|---|
| jas (v1, 1f0e9ba) | Parallel read-only tool batches with mutating barriers; no subagents; farm (worktrees + review queue) in v2 | JSONL events with v/id/parent_id/seq/ts; OTel in v3 |
| pi | No subagents or plan mode by design; via extensions | pi-telemetry package |
| Claude Code | Subagents, background sessions, worktrees | OpenTelemetry export |
| OpenAI Codex CLI | Cloud tasks; desktop app parallel threads | Opt-in OTel (otlp-http / grpc): api_request, tool_decision, tool_result events |
| opencode | Subagents | not published |
| Gemini CLI | not published | OpenTelemetry telemetry |
| goose | Subrecipes / subagents | not published |
| Crush | not published | not published |
| Amp | Task subagents; orbs run detached in the cloud | not published |
| aider | Architect / editor model split | not published |
| Cline | Plan / Act modes | not published |
| Roo Code | Orchestrator mode delegating to sub-tasks | not published |
| mini-swe-agent | Scale-out via independent subprocess.run per action | Trajectories |
| SWE-agent | not published | Trajectories |
| OpenHands | Delegation | not published |
| nib / san | nib: subagents | not published |

### Benchmarks

| Product | Published results | GitHub stars |
|---|---|---|
| jas (v1, 1f0e9ba) | Internal n=1 vs pi on GLM-5.3: 18.9k vs 105.2k prompt tokens, 57% vs 84% cache hit, 34.4 s vs 26.6 s; task success not verified | private repo |
| pi | Terminal-Bench 2.1: 61.8% with Kimi K2.7 Code (vendor-reported) | 118,769 |
| Claude Code | TB 2.1: 83.8% (Fable 5 xhigh); TB 2.0: 58.0% (Opus 4.6) | 153,838 |
| OpenAI Codex CLI | TB 2.1: 87.4% (GPT-6 Astra); TB 2.0: 82.2% (GPT-5.5) | 131,555 |
| opencode | TB 2.0: 51.7% (Opus 4.5) | 222,988 |
| Gemini CLI | TB 2.1: 65.8%; TB 2.0: 61.4% (Gemini 3.1 Pro) | 108,611 |
| goose | TB 2.0: 54.3% (Opus 4.5) | 55,911 |
| Crush | Not listed on Terminal-Bench | 28,872 |
| Amp | not published | closed source |
| aider | Maintains the Aider Polyglot model benchmark; no Terminal-Bench entry | 49,814 |
| Cline | TB 2.0: 73.0% (vendor-reported) | 71,029 |
| Roo Code | not published | 24,254 |
| mini-swe-agent | README: >74% SWE-bench Verified; powers the bash-only leaderboard; TB 2.1 76.2% (Muse Spark 1.1) | 8,952 |
| SWE-agent | Origin of SWE-bench agent baselines | 20,684 |
| OpenHands | TB 2.0: 51.9% (Opus 4.5) | 92,196 |
| nib / san | not published | 52 + 146 |

## 3. Design review against the market

### Ahead

| Area | Detail |
|---|---|
| Footprint and startup | 7.48 MB static binary, 3 direct deps, 1.6 ms cold start. Roughly 4× to 10× smaller than any peer's compressed download. |
| CI exit-code contract | Distinct codes for context full (3), turn cap (4) and approval needed without a TTY (5). Peers document JSON output but no equivalent approval-needed status. |
| Crash-consistent log | `tool_started` is written ahead and fsynced, and Ctrl-C synthesizes results, so the log replays cleanly after a kill. Peers keep JSONL transcripts but do not document write-ahead semantics. |
| Secrets floor | No `--api-key` flag, a compiled-in deny floor and redaction, on by default. Claude Code's credential deny / mask rules are opt-in sandbox settings. |
| Process-tree kill | Process groups and Windows Job Objects. goose added process-tree termination on cancel in v1.57, which confirms the problem is real. |
| Deterministic tool scheduling | Read-only calls run as parallel batches, with each mutating call as a barrier. Ordering is explicit and replayable. |

### Parity

| Area | Detail |
|---|---|
| Edit tool | Exact `old_string` / `new_string` replacement, as in Claude Code Edit, Gemini replace and pi edit. Codex `apply_patch` and aider's edit formats are the alternatives. |
| Agent Skills | `.agents/skills` and `.claude/skills` with a catalog cap. Crush, Claude Code, Codex and pi read the same layout. |
| Approval ladder | `never` / `shell` / `mutating` is comparable to Gemini's approval modes and Codex's approval axis, but without the sandbox axis. |
| Project trust gate | `AGENTS.md` / `CLAUDE.md` load behind trust, as in Codex project `trust_level`. |
| JSON mode and resume | `jas run --json` and session resume match `claude -p stream-json`, `codex exec` and gemini stream-json. |

### Behind

| Area | Detail |
|---|---|
| Provider API surface | Chat Completions only, with no reasoning replay, `cache_control` or session affinity. pi and Crush ship Messages + Responses + Completions. |
| OS sandbox | None built in. Codex has one on by default; Claude Code and Gemini CLI offer one opt-in. |
| MCP | Absent; every major peer supports it. |
| Compaction | Elision only, with no summarization or branch summaries. |
| RPC / editor protocol | None. Peers expose an app-server, HTTP serve, RPC or ACP. |
| Multi-agent, OTel, TUI | All planned for v2 or v3. Peers ship subagents, OTel export and full TUIs. |
| Benchmarks | No task-success numbers, while peers publish Terminal-Bench rows. |

## 4. Gaps and risks

1. **Quality and cost from provider fidelity.** OpenRouter requires
   `reasoning_details` to be passed back unmodified alongside `tool_calls` to
   preserve reasoning across tool turns. jas drops it. It also sends no
   `session_id`, so OpenRouter's sticky routing engages only after a cache hit
   is observed. This plausibly explains the lower cache hit despite jas's 5.5×
   smaller prompt volume (hypothesis, not yet measured).

2. **Safety claim vs mechanism.** Safety-conscious YOLO rests on an env
   allowlist and a deny floor, neither of which stops a shell call from reading
   and exfiltrating files. Codex and Claude Code moved the boundary into the
   OS. Building a correct sandbox is costly, so reuse bwrap / Seatbelt and keep
   the scope tight.

3. **Integrity of the main differentiator.** #41 (malformed tool arguments
   break the log) and #43 (event version ignored) sit in the component jas
   markets as its strength. A single unreplayable log in CI undermines the
   whole auditability pitch.

4. **Benchmark credibility.** The published comparison is n=1 with success
   unverified. jas used fewer prompt tokens but about 2× the completion
   tokens, and its wall time was 29% higher. A technical evaluator will read
   it as inconclusive.

5. **Thin moat in the Go slot.** Crush already pairs Go with MCP, LSP, skills,
   hooks and three native API types. Its FSL license is the main opening, and
   nib and san show the slot is cheap to enter.

6. **Roadmap breadth.** v2 lists about 12 items (farm, attach, MCP, TUI,
   compaction, fork, RPC, scenarios, blind secrets, web tools, steering,
   evals), and the newest spec chases pi's strongest area, the interactive
   TUI. Peers release weekly (Codex) or near-daily (Claude Code), so
   parity-chasing is a losing race.

7. **Traction and visibility.** The repo is private, with no stars, users or
   public benchmarks. Market signals favour harnesses with an ecosystem
   (plugins, marketplaces, ACP registries). Aider's stalled releases since
   2025-08 and Roo Code's archival show that strong early products fade
   without one.

## 5. Recommendations

Ordered by priority. **Informed by** names the products whose shipped design
drives the recommendation; **Overlaps** maps to open jas issues.

| Priority | Recommendation | Rationale | Informed by | Overlaps |
|---|---|---|---|---|
| High | Provider fidelity: replay reasoning and drive caching explicitly | In the Chat Completions adapter, replay `reasoning_details` on assistant tool-call turns; send top-level or block `cache_control` for Anthropic / Gemini / Qwen routes; send `session_id` (OpenRouter sticky routing) and `prompt_cache_key`; parse `cache_write_tokens` and reasoning tokens. Then add native Anthropic Messages and OpenAI Responses adapters. This is the most likely cause of the 57% vs 84% cache-hit gap against pi, and missing reasoning replay degrades frontier reasoning models during tool loops. | pi-ai openai-completions.ts (session affinity, cache_control, reasoning_details replay); Crush Catwalk v3 (completions / responses / messages); OpenRouter caching and reasoning docs | #29 (SSE fixtures for OpenRouter-Anthropic), #28 (first-turn budget undercount) |
| High | Make the event log a published, versioned spec before adding surface | The log is jas's core differentiator, so defects there hurt the product's main claim. Fix malformed-argument handling first, enforce the `v` field on read, label never-run calls correctly, then publish JSON Schemas for events.jsonl and the `--json` stream with golden replay tests. Every v2 feature (TUI, attach, fork, farm, RPC) consumes this format. | Codex exec JSON + app-server, Claude Code stream-json, pi RPC / JSON mode | #41 (High), #42, #43, #47 to #54 |
| High | Opt-in OS sandbox with fail-closed mode | The approval ladder plus env allowlist does not stop an approved or YOLO shell call from reading `~/.ssh` and exfiltrating it. Ship `--sandbox read-only\|workspace-write` using bwrap + seccomp on Linux and Seatbelt on macOS, network off by default, protected paths (`.git`, `.jas`, `.agents`), and a fail-if-unavailable flag for CI. Keep the scope small: sandbox correctness is hard, and reusing bwrap / Seatbelt profiles beats building one. | Codex CLI (default-on sandbox, protected paths), Claude Code (/sandbox, failIfUnavailable, credential masking), Gemini CLI (Seatbelt / containers) | #45 (broad shell always grant matters less inside a sandbox) |
| High | Publish success-verified benchmarks | The current comparison is n=1 with no success check; jas used fewer prompt tokens but more completion tokens, a lower cache hit and 29% more wall time than pi. Add a Harbor / Terminal-Bench 2.x adapter and run n≥5 per task on a fixed model against pi and mini-swe-agent, reporting pass rate, cost and tokens. Without this, the efficiency claim is not credible to a technical buyer. | Terminal-Bench 2.x leaderboard, mini-swe-agent bash-only SWE-bench baseline | #33 (binary size claim); docs/benchmarks.md |
| Medium | MCP client, stdio first | MCP is supported by every major peer and is the expected integration surface. Use the official Go SDK; add HTTP plus OAuth later. Gate MCP tools through the same approval ladder and sandbox. | Crush (stdio / http / sse with OAuth in Go), Claude Code, Codex, goose | none open; v2 roadmap item |
| Medium | Cache-aware summarizing compaction | Elision alone fails on long sessions. Compact at a stable boundary (window minus reserve, per the v2 plan), pin skills and system prompt so compaction never drops them, and log compaction as an event so replay stays exact. | pi (compaction + branch summaries), Claude Code auto-compact, Codex auto-compaction | #44 (skill bodies elided), #28 |
| Medium | Adopt ACP instead of a bespoke jas serve protocol | The Agent Client Protocol registry lists Gemini CLI, goose and opencode natively, and Codex and Claude Code via adapters. Implementing ACP gives Zed and other editors for free and avoids maintaining a private RPC. Keep the JSONL log as the audit stream next to it. | goose-acp, Gemini CLI, opencode, Zed external agents | #46 (spec / code gaps for plans 2 and 3) |
| Medium | Pattern-based permission rules | Replace first-two-words always grants with allow / ask / deny patterns such as `Bash(git push *)`, with deny precedence, and quiet the approval event stream. | Claude Code permission rules, Codex execpolicy and granular approvals | #45, #32 |
| Medium | Farm: worktrees plus a review queue | Parallel isolated runs with a reviewable diff and log per run is where a CI-native harness can lead. Sequence it after the High items, because it multiplies any event-log or sandbox weakness. | Claude Code worktrees, Amp orbs, Codex cloud tasks, Roo orchestrator | v2 roadmap |
| Low | OTel export mapped from JSONL events | This is cheap given the existing event model and expected by platform teams. Redact prompts by default. | Codex `[otel]` block, Claude Code, Gemini CLI telemetry | v3 roadmap; pull forward |
| Low | Hooks via scenarios (pre_apply, post_run) | Gives policy and CI integration without plugins. Run hooks inside the sandbox where possible: Claude Code documents that its hooks run outside it. | Claude Code PreToolUse hooks, Crush hooks (v0.99) | v2 scenarios |
| Low | Keep the TUI thin and close docs debt | Interactive polish is where pi, Crush and opencode are strongest and jas is unlikely to win. Ship the pi-like inline TUI from the 2026-10-30 interactive spec, not a full-screen app. Close the doc debt. | pi inline TUI, Crush (Bubble Tea) | #34, #35, #46, stale design.md |

## 6. Differentiation opportunities

1. **The harness you can leave running in CI.** Package jas as a GitHub Action
   and a distroless image. Compete with `codex exec` and `claude -p` on
   guarantees: exit-code contract, fail-fast without a TTY, write-ahead log,
   sandbox fail-closed, MIT. Raw model scores are not the axis.

2. **An open, versioned agent-trace format.** Publish the events.jsonl schema
   and add `jas replay` and `jas verify`, with an optional hash chain for
   tamper evidence. No peer publishes a stable trace spec beyond Codex's OTel
   event names. This suits regulated and audit-heavy teams.

3. **Best-in-class secret handling in OSS.** Combine the compiled-in deny floor
   with the planned blind secret access and proxy-side injection, modelled on
   Claude Code's TLS-proxy credential masking. Ship it on by default rather
   than as opt-in config.

4. **Embeddable MIT Go agent core.** Go infrastructure (operators, CI runners,
   Dagger-style pipelines) has no MIT-licensed agent loop with traction, and
   Crush is FSL. Expose `core/` as a stable Go package with the event log as
   its API.

5. **Farm with deterministic review artifacts.** Each parallel worktree run
   yields a diff, a replayable log and an exit status, queued for human or
   classifier review. This is a self-hosted alternative to Amp orbs and Codex
   cloud tasks.

6. **Minimal-core credibility.** Treat about 7.2k non-test lines and 3
   dependencies as a published budget, the way mini-swe-agent markets its
   ~100-line agent. A small core is easy to audit, fork and use as a Go
   research baseline.

## 7. Sources

GitHub stars, licenses, release dates and asset sizes were read via the GitHub
API on 2026-10-30 (Asia/Hong_Kong). jas facts come from the repo at `1f0e9ba`
(README, docs/design.md, docs/benchmarks.md, providers/openai.go).

- [jas repository (private)](https://github.com/jasonli0226/jas)
- [pi](https://github.com/earendil-works/pi)
- [pi changelog](https://pi.dev/changelog)
- [pi-ai openai-completions.ts (cache, session affinity, reasoning_details)](https://github.com/earendil-works/pi/blob/e7a0df27c09d11bf4161d3605246cbf57a129cb2/packages/ai/src/api/openai-completions.ts)
- [Claude Code releases](https://github.com/anthropics/claude-code/releases)
- [Claude Code: sandboxed Bash tool](https://code.claude.com/docs/en/sandboxing)
- [Codex CLI repository](https://github.com/openai/codex)
- [Codex rust-v0.165.0 release assets](https://github.com/openai/codex/releases/tag/rust-v0.165.0)
- [Codex: agent approvals & security (sandbox, OTel)](https://developers.openai.com/codex/agent-approvals-security)
- [opencode releases](https://github.com/anomalyco/opencode/releases)
- [Gemini CLI](https://github.com/google-gemini/gemini-cli)
- [goose v1.57.0](https://github.com/aaif-goose/goose/releases/tag/v1.57.0)
- [goose docs](https://goose-docs.ai/)
- [Crush README](https://github.com/charmbracelet/crush)
- [Crush v0.99.0: Catwalk v3, hooks](https://github.com/charmbracelet/crush/releases/tag/v0.99.0)
- [Amp manual](https://ampcode.com/manual)
- [Amp modes, models & subagents](https://ampcode.com/docs/models-and-subagents)
- [aider](https://github.com/Aider-AI/aider)
- [Cline](https://github.com/cline/cline)
- [Roo Code (archived)](https://github.com/RooCodeInc/Roo-Code)
- [mini-swe-agent](https://github.com/SWE-agent/mini-swe-agent)
- [SWE-agent](https://github.com/SWE-agent/SWE-agent)
- [OpenHands](https://github.com/OpenHands/OpenHands)
- [nib (Go)](https://github.com/mudler/nib)
- [san (Go)](https://github.com/genai-io/san)
- [SWE-bench leaderboards (incl. bash-only)](https://www.swebench.com/)
- [Terminal-Bench 2 (Harbor)](https://github.com/harbor-framework/terminal-bench-2)
- [Coding-harness leaderboard aggregator (third-party, as of 2026-09-08)](https://swadhin.ai/leaderboards/coding-harnesses/)
- [OpenRouter: prompt caching & sticky routing](https://openrouter.ai/docs/guides/best-practices/prompt-caching)
- [OpenRouter: reasoning tokens / reasoning_details](https://openrouter.ai/docs/guides/best-practices/reasoning-tokens)
- [Agent Client Protocol: agents](https://agentclientprotocol.com/get-started/agents)
- [MCP Go SDK](https://github.com/modelcontextprotocol/go-sdk)
