# jas

> A tiny, fast coding-agent harness in Go. Parallel tasks, live status, pluggable tools.

**Status:** 🚧 v1 in progress — see [docs/design.md](docs/design.md)

## The pitch

jas is a single static Go binary that is:

- **lean** — fewer turns and tokens per task: cache-friendly prompts, parallel
  reads, capped and elided tool output; streaming loop in ~200 lines
- **parallel** — one agent per git worktree, N tasks at once *(planned v2)*
- **visible** — every step is a JSONL event (audit + resume); live status
  board *(planned v2)*
- **pluggable** — Tool interface; OpenAI-compatible endpoints (OpenRouter,
  vLLM, ollama); MCP client *(planned v2)*

The model is the brain; jas is the nervous system.

## Usage (v1)

Configure once from the CLI; settings are stored in `~/.jas/` (pi-style):

```sh
jas                                         # first run: interactive onboarding
jas auth add openrouter                     # key → ~/.jas/auth.json (0600), no echo
jas config set base_url https://openrouter.ai/api/v1
jas config set model anthropic/claude-sonnet-4.6
jas config list                             # effective values + where each came from
```

Then:

```sh
jas run "add a header comment to main.go"           # one-shot task
jas run --approve=mutating "…"                      # confirm shell + file edits (#1)
jas run --model z-ai/glm-5.3 "…"                    # one-run override
jas run --json "…"                                  # emit events to stdout as JSONL
```

`JAS_BASE_URL`, `JAS_MODEL`, `JAS_API_KEY` (and a `JAS_*` var per setting)
override stored config — use them in CI and containers.

v1 loads `AGENTS.md` / `CLAUDE.md` from the project root (after a one-time
trust prompt), tracks a context budget (elides old tool results), and writes a
versioned JSONL event log. Exit codes are a documented contract for CI. See
[docs/design.md](docs/design.md) §4.

## Planned (v2+)

```sh
jas farm --n 3 "fix all failing tests"        # 3 parallel worktree agents
jas attach 2                                  # take over task 2 interactively
jas tasks                                     # status board
```

Farm, attach, and the TUI status board ship in v2 — not part of the v1 cut
([#12](https://github.com/jasonli0226/jas/issues/12)).

## Design

Full design in [docs/design.md](docs/design.md); how jas compares with pi is
§14. Real-world scenarios in
[docs/use-cases.md](docs/use-cases.md) — one-shot tasks, CI runs, N-way farm,
attach, per-project scenario policies (and what jas deliberately isn't).
Core bets:

1. Small core, pluggable everything — 6 interfaces total (§3)
2. goroutines + errgroup for parallel tools & tasks
3. JSONL event log = checkpoint/resume + audit, no extra machinery

Design-review decisions: [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13).

## License

MIT
