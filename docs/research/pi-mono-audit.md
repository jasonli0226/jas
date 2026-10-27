# Feature audit: pi-mono → jas

Source: `badlogic/pi-mono` cloned 2026-10-28 (`_pi-mono/`, reference clone).
Packages: agent, ai, chord, codemode, coding-agent, durable, env, evals, mcp,
telemetry, tui. Docs read: how-pi-works, security, sessions, compaction, skills,
packages, rpc, extensions, virtual-models, codemode, settings, tmux.

Legend: ✅ adopt v1/v2/v3 · 🔍 study later · ❌ skip

| Feature | pi does | jas today | Adopt |
|---|---|---|---|
| **Session as JSONL tree** | sessions are trees of entries (ID + parent), branches via `/tree`, fork, clone; branch summaries on switch | flat `events.jsonl` | ✅ v2 — add parent-ID to events; fork/clone = new file from a point. Branch summary reuses compaction |
| **Auto-compaction** | triggers when `contextTokens > window − reserveTokens` (16k default); summary replaces old messages in subsequent requests, originals kept in tree | **nothing — critical gap** | ✅ v2 — same threshold model; summary via a cheap model; keep recent N tokens |
| **Steering / follow-up queue** | queued user messages enter after current turn (steering) or after run ends (follow-up); one-at-a-time/all | not designed | ✅ v2 — interactive TUI needs it |
| **Project trust** | `ask/always/never` gate on project-supplied settings/resources (prompt-injection defense); trust is not a security boundary, only a startup gate | project `.jas/scenario.yaml` loads silently | ✅ v2 — ships with scenarios: first time jas sees a project scenario with hooks/MCP → ask trust; cache keyed by path + content hash. Cheap, important |
| **Modes: print / JSON / RPC** | print (one-shot), JSONL events mode, RPC mode (long-lived subprocess, JSONL protocol) for IDEs & other languages, SDK | `jas run` ≈ print mode only | ✅ JSON mode v1 (cheap: emit events.jsonl to stdout); RPC mode v2 — farm runner and future Telegram bot talk to it |
| **Skills (Agent Skills spec)** | follows agentskills.io spec: SKILL.md + name/description advertised, full instructions loaded on demand; bundles scripts/assets | scenarios/blueprints (§8) overlap | ✅ v3 — adopt the spec so jas skills interop with Claude Code/Hermes. Blueprint = scenario = skill (§8.4 convergence already planned) |
| **Packages (npm/git/local install)** | `pi install npm:…` bundles extensions+skills+templates+themes; project packages gated by trust | nothing | 🔍 v3 — jas equivalent: git-installable scenario/skill repos (`jas scenario install git:…`), no npm |
| **Prompt templates (slash commands)** | `/cmd` expands editor input before it becomes a user message | nothing | ✅ v2 — cheap (same loader as skills) |
| **Extensions (TS modules)** | in-process TypeScript modules: tools, commands, event handlers, providers, UI — full process permissions | nothing | ❌ **skip as compiled extensions** — Go plugins are painful. jas's extension story = hooks + MCP + skills. jas itself can be an MCP server |
| **MCP support** | `<agent-dir>/mcp.json`, tools wrapped into same interface | ✅ already in design | ✅ (v2) |
| **Virtual models** | a selectable model that routes each request to a physical model (`jev/auto:low → sonnet:high`); recorded in session; per-request dispatch | nothing | ✅ v3 — direct tie to backlog #3 (cheap-MoE router eval). Small model for easy turns, big for hard ones |
| **Codemode** | model-written JS in a **QuickJS WASM sandbox**; only script output enters context; parallel tool calls; output caps; `@options` line | planned #14 (v3) | ✅ v3 — Go equivalent: **goja** (pure-Go JS engine), tools injected as the only capability. Captures #14 mini-codemode |
| **Secrets** | auth.json (0600) + OAuth; keys never in settings | ✅ §9 four layers | ✅ (built) |
| **Settings scoping** | global → agent-dir → project, hot reload, file locking, `+name`/`-name` list merge | ✅ §10 | ✅ (built) |
| **Models config** | custom endpoints/models.json, per-model overrides | ✅ §10 models.json | ✅ (built) |
| **Containerization** | docs on running inside containers/VMs; strongest practical isolation | farm worktrees only | ✅ v3 — farm task option: run in a container (bubblewrap/Docker) |
| **Remote execution (pi-env)** | tools run on a remote machine via SSH daemon (Rust), storage/creds stay local | nothing | 🔍 v4 — interesting for GPU-box use; skip for now |
| **Durable harness** | crash-proof: checkpoint-per-step, pluggable storage, fork conversations, exactly-once submissions | events.jsonl replay planned v2 | ✅ v2 checkpoint/resume gets pi-durable's 80% cheaply; full durable orchestration ❌ (scope) |
| **Evals (docs-lift)** | vitest-evals: runs cases with/without docs in isolated containers, reports lift — they measure their own documentation | benchmark planned | ✅ v2 — copy the idea: jas eval harness (fixed tasks, with/without scenario, report lift). Ties to acceptance tests |
| **Telemetry** | OTel package | events.jsonl only | 🔍 v3 — OTel export of events.jsonl (trace = session) |
| **Local models** | llama.cpp (incl. NVIDIA RTX), vLLM pods, virtual-models routing | any OpenAI-compatible endpoint covers llama.cpp/vLLM servers | ✅ free (already compatible) |
| **TUI polish** (themes, keybindings, fullscreen, inline images) | extensive | Bubble Tea status board planned | ✅ v2 basics; polish later |
| **Platforms** (termux, windows, tmux extended keys) | yes | go build targets cover win/termux mostly free | 🔍 later |
| **Shell aliases** | shell-aliases.md | nothing | ❌ skip |

## The three gaps that matter most

1. **Compaction** — jas currently has zero story for long tasks. pi's model
   (threshold = window − reserve; summary entry; originals kept) is simple to
   port and is *the* blocker for real coding sessions.
2. **Project trust** — jas's scenario system (project-supplied hooks + MCP +
   system prompts) is pi's exact attack surface. `ask` on first load is a
   one-day feature that prevents the "malicious .jas/scenario.yaml" class.
3. **RPC mode** — everything downstream (farm runner, TUI, Telegram bot, IDE)
   wants a long-lived process speaking JSONL. Designing events.jsonl as the
   wire format from day one makes farm/TUI nearly free.

## What we deliberately do NOT copy

- **Compiled TS extensions** — jas keeps hooks + MCP + skills; no plugin ABI.
- **npm package distribution** — git-based scenario install instead.
- **Full durable orchestration** — checkpoint/resume from events.jsonl, not a
  second storage layer.
- **TUI polish depth** — status board + attach only.
