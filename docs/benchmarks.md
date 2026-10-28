# Benchmarks

Method follows design §7. Reproduce with `make bench` (jas) and
`BENCH_AGENT=pi make bench` (pi; the cold-start part runs for jas only).

## Setup

- Model: `z-ai/glm-5.3` via OpenRouter (`https://openrouter.ai/api/v1`) for both agents.
- Tasks (`bench/tasks.txt`), each run once in a fresh copy of `bench/fixture`:
  1. add a header comment to main.go explaining the retry logic
  2. write a table-driven test for parseConfig covering bad env vars
  3. list every exported function in pkg/api with its signature
- jas: `jas run --json`, usage summed from `usage` events, turns = number of
  usage events (one per provider response). Default approve level (`never`).
- pi: `pi --mode json -p --model z-ai/glm-5.3`, summed from `turn_end` events.
  pi reports `input` excluding cache reads, so prompt tokens = `input + cacheRead`
  to match jas's OpenAI-style `prompt_tokens`.
- Linux x86_64, Go 1.27.3, jas built with `make build` (7,479,456 bytes).

### Provenance

- Date: 2026-10-29. n = 1 per task per agent.
- jas: built from the working tree on top of commit `bc2e7be` (Task 15 changed
  no jas source files, only `bench/`, docs and a test), before the Task 15
  commit existed.
- pi: version 1.4.0 (`pi --version`), default tools and system prompt.
- Model: `z-ai/glm-5.3` via OpenRouter for both.
- The jas rows come from the first `bench/run.sh` run. The pi rows come from a
  re-run after `bench/run.sh` gained `</dev/null` on the agent command (pi read
  the task list from stdin and stopped after one task). jas does not read
  stdin in `--approve=never` mode, so its numbers were not re-run.

## Hygiene

| metric | result | target |
|---|---|---|
| binary size | 7.48 MB (7,479,456 bytes) | < 15 MB |
| `jas --version` cold start | median 1.6 ms (`TestVersion_ColdStart`, 20 runs) | < 50 ms |
| `jas --version` via `bench/coldstart.sh` | median 4.0 ms (50-run bash loop; includes fork/`date` overhead; hyperfine not installed) | < 50 ms |
| time to first HTTP request (`jas run`, local listener) | median 13.1 ms (min 11.3, max 14.9; 20 runs) | < 50 ms |

## Primary: turns, tokens, cache (single run per task)

| task | agent | turns | prompt tok | completion tok | cached tok | cache hit | wall s |
|---|---|---:|---:|---:|---:|---:|---:|
| header comment | jas | 3 | 3,140 | 361 | 768 | 24% | 5.5 |
| header comment | pi | 3 | 21,389 | 244 | 20,032 | 94% | 6.1 |
| table-driven test | jas | 7 | 12,556 | 3,419 | 8,256 | 66% | 27.2 |
| table-driven test | pi | 6 | 48,150 | 1,511 | 40,448 | 84% | 15.0 |
| list exported funcs | jas | 3 | 3,245 | 205 | 1,856 | 57% | 1.7 |
| list exported funcs | pi | 5 | 35,698 | 290 | 28,416 | 80% | 5.5 |
| **total** | jas | 13 | 18,941 | 3,985 | 10,880 | 57% | 34.4 |
| **total** | pi | 14 | 105,237 | 2,045 | 88,896 | 84% | 26.6 |

Reading it:

- "Add a header comment to main.go" took 3 turns in jas (target: at most 3). Met.
- Totals, mixed result: jas sent about 5.6x fewer prompt tokens (18.9k vs
  105k) and fewer uncached prompt tokens (8.1k vs 16.3k), but used about
  twice the completion tokens (3,985 vs 2,045) and was slower overall (34.4 s
  vs 26.6 s). Turns were 13 vs 14.
- Part of the prompt-token gap is the size of each agent's system prompt and
  tool definitions, not only loop efficiency. That also confounds cost:
  cached, uncached and completion tokens are priced differently, and no
  dollar cost was computed.
- pi's cache-hit rate was higher (84% vs 57% in total; 80-94% per task vs
  24-66%). The cause has not been investigated. Untested hypotheses: prompt
  prefix stability, or provider cache routing.
- jas used more completion tokens on the test-writing task (3.4k vs 1.5k) and
  was slower on it (7 turns, 27 s vs 6 turns, 15 s).

## Caveats

- One run per task per agent; LLM output varies run to run, so treat the
  numbers as indicative, not statistical. Cache hits also depend on
  OpenRouter's provider routing and on what was cached by earlier runs.
- Task success was not verified (for example, whether the generated test
  compiles and passes); only turns and tokens were measured.
- Wall time includes network and provider latency and is not optimized for.
- pi's default system prompt and tool definitions differ from jas's, so
  prompt-size gaps include that difference, not only loop efficiency.
