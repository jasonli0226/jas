# jas

> A tiny, fast coding-agent harness in Go. Parallel tasks, live status, pluggable tools.

**Status:** 🚧 v1 in progress — see [docs/design.md](docs/design.md)

## The pitch

jas is a single Go binary (~12MB, <50ms cold start) that is:

- **tiny & fast** — static binary, no runtime, streaming loop in ~200 lines
- **parallel** — one agent per git worktree, N tasks at once *(planned v2)*
- **visible** — live status board; every step is a JSONL event (audit + resume)
- **pluggable** — Tool interface + MCP client; OpenAI-compatible endpoints (OpenRouter, vLLM, ollama)

The model is the brain; jas is the nervous system.

## Usage (v1)

```sh
export JAS_BASE_URL=https://openrouter.ai/api/v1
export JAS_MODEL=anthropic/claude-sonnet-4.6
export JAS_API_KEY=...

jas run "add a header comment to main.go"           # one-shot task
jas run --approve=mutating "…"                      # gate mutating tools (#1)
jas run --json "…"                                  # emit events to stdout as JSONL
```

v1 loads `AGENTS.md` / `CLAUDE.md` from cwd into the system prompt, tracks a
context budget (elides old tool results), and writes a versioned JSONL event
log. See [docs/design.md](docs/design.md) §4.

## Planned (v2+)

```sh
jas farm --n 3 "fix all failing tests"        # 3 parallel worktree agents
jas attach 2                                  # take over task 2 interactively
jas tasks                                     # status board
```

Farm, attach, and the TUI status board ship in v2 — not part of the v1 cut
([#12](https://github.com/jasonli0226/jas/issues/12)).

## Design

Full design in [docs/design.md](docs/design.md). Real-world scenarios in
[docs/use-cases.md](docs/use-cases.md) — one-shot tasks, CI runs, N-way farm,
attach, per-project scenario policies (and what jas deliberately isn't).
Core bets:

1. Small core, pluggable everything — 6 interfaces total (§3)
2. goroutines + errgroup for parallel tools & tasks
3. JSONL event log = checkpoint/resume + audit, no extra machinery

Design-review decisions: [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13).

## License

MIT
