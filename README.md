# jas

> A tiny, fast coding-agent harness in Go. Parallel tasks, live status, pluggable tools.

**Status:** 🚧 v1 in progress — see [docs/design.md](docs/design.md)

## The pitch

jas is a single Go binary (~12MB, <50ms cold start) that is:

- **tiny & fast** — static binary, no runtime, streaming loop in ~200 lines
- **parallel** — one agent per git worktree, N tasks at once
- **visible** — live status board; every step is a JSONL event (audit + resume)
- **pluggable** — Tool interface + MCP client; OpenAI-compatible endpoints (OpenRouter, vLLM, ollama)

The model is the brain; jas is the nervous system.

## Usage (planned)

```sh
export JAS_BASE_URL=https://openrouter.ai/api/v1
export JAS_MODEL=anthropic/claude-sonnet-4.6
export JAS_API_KEY=...

jas run "add a header comment to main.go"     # one-shot task
jas farm --n 3 "fix all failing tests"        # 3 parallel worktree agents
jas attach 2                                  # take over task 2 interactively
jas tasks                                     # status board
```

## Design

Full design in [docs/design.md](docs/design.md). Core bets:

1. Small core, pluggable everything — 6 interfaces total
2. goroutines + errgroup for parallel tools & tasks
3. JSONL event log = checkpoint/resume + audit, no extra machinery

## License

MIT