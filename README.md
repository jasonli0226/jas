# jas

> A tiny, fast coding-agent harness in Go. Parallel tasks, live status, pluggable tools.

**Status:** v1 — `jas run`, `jas config`, `jas auth`. See [docs/design.md](docs/design.md).

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

## Install

The repo is private, so install from a clone (Go 1.27+):

```sh
go install ./cmd/jas            # from the repo root; or: make build  ->  ./bin/jas
```

The module path is `github.com/jasonli0226/jas`; once the repo is public,
`go install github.com/jasonli0226/jas/cmd/jas@latest` works too. The binary
is static (`CGO_ENABLED=0`), about 7 MB.

Release builds for Linux (amd64, arm64), macOS (amd64, arm64) and Windows
(amd64) are attached to each `v*` tag's GitHub release, with
`checksums.txt`. The `shell` tool runs `sh -c` on Linux and macOS and
`cmd /d /s /c` on Windows; on every platform the child's whole process tree
(process group, or a job object on Windows) is killed when the call ends.

## Quick start

Run `jas` with no arguments the first time for interactive onboarding
(provider, API key read without echo, default model); it then opens the chat.
Or configure by hand;
settings live in `~/.jas/` (`JAS_HOME` overrides):

```sh
jas auth add openrouter                            # key -> ~/.jas/auth.json (0600), no echo
jas auth add openrouter --env OPENROUTER_API_KEY   # store a reference, not the key
jas config set base_url https://openrouter.ai/api/v1
jas config set model z-ai/glm-5.3
jas config list                                    # effective values + where each came from
```

Then:

```sh
jas                                                 # inline chat in this project
jas --continue                                      # resume this project's last session
jas run "add a header comment to main.go"           # one-shot task
jas run --approve=mutating "..."                    # confirm shell + file edits
jas run --model z-ai/glm-5.3 "..."                  # one-run override
jas run --json "..."                                # event stream on stdout as JSONL
jas run --trust-project "..."                       # load AGENTS.md without asking
```

Subcommands: `jas` (chat), `jas run`, `jas config set|get|unset|list|init`,
`jas auth add|list|rm|rotate`, `jas --version`. Run flags: `--json`,
`--model`, `--provider`, `--base-url`, `--approve`, `--max-turns`,
`--trust-project`. The chat takes the same flags except `--json`, plus
`--continue`; it needs a terminal, so scripts use `jas run`. There is deliberately no `--api-key` flag (argv leaks via
shell history and `ps`).

## Configuration

Precedence: flag > `JAS_*` env var > `~/.jas/config.json` > built-in default.
Every key `k` has a `JAS_<K>` env var; `JAS_API_KEY` supplies the key (use
these in CI and containers, where there is no `~/.jas/`).

```sh
jas config set model z-ai/glm-5.3
jas config set base_url https://openrouter.ai/api/v1
jas config set approve mutating                 # persisted default for --approve
jas config set max_turns 80
jas config set models.my-local.context_window 32768
jas config get model
jas config unset base_url
jas config list                                 # value + source of every key
```

Keys: `provider`, `model`, `base_url`, `approve`, `max_turns`,
`max_tool_output`, `elide_at`, `keep_tool_results`, `redact_skip_paths`,
`compact_reserve_tokens`, `compact_keep_tokens`, `compact_model`, and
per-model `models.<name>.context_window`.

The chat compacts its context: once the prompt passes the window minus
`compact_reserve_tokens` (default 16384; never below half the window), older
messages are replaced by a summary, keeping about `compact_keep_tokens` (default
20000) of recent messages verbatim. `compact_model` picks the summary model
(default: the session's). `/compact [focus]` compacts on demand. `jas run`
does not compact.

API keys come from `auth.json` (literal, mode 0600), a `$ENV_NAME` reference
stored there, or `JAS_API_KEY`. `jas auth list` shows names and sources,
never values.

A local model through ollama needs no key:

```sh
jas config set provider ollama
jas config set base_url http://localhost:11434/v1
jas config set model qwen2.5-coder
```

Project instructions: `AGENTS.md` (or `CLAUDE.md`) in the project root is
loaded into the system prompt after a one-time trust prompt (answers are
stored in `~/.jas/trust.json`; `--trust-project` skips the prompt). The
content is redacted and size-capped first.

## Skills

jas loads [Agent Skills](https://agentskills.io): a directory holding a
`SKILL.md` whose frontmatter has a `name` and a `description`. Skills from
pi, Claude Code and opencode work as they are. jas scans, in order:

| Scope | Directories |
|---|---|
| project | `<root>/.agents/skills/`, `<root>/.claude/skills/` |
| user | `~/.agents/skills/`, `~/.claude/skills/` |

Skills may sit in nested folders (e.g. `~/.agents/skills/team/review/SKILL.md`):
a folder with a `SKILL.md` is a skill and is not searched further; hidden
folders and `node_modules` are skipped, and the search stops 5 levels down.

Only each skill's name, description and path go in the system prompt; the
model reads the full `SKILL.md` (and any `references/` or `scripts/`) with
the `read` tool when a task matches. `read`, `ls` and `grep` may open listed
skill directories (the deny floor still applies there); `write`, `edit` and
`shell` stay confined to the project as before.

- On a name clash the first skill found wins, in the table's order: project
  before user, `.agents` before `.claude`. A warning names both. The same
  folder reached twice (e.g. through a symlink) loads once, silently.
- Project skills are project-authored instructions, like `AGENTS.md`, so they
  share its trust prompt; adding or editing one asks again. Until the project
  is trusted its skills are ignored entirely, so they cannot hide a user
  skill of the same name. A project skill must resolve inside the project; one
  symlinked out of it is skipped with a warning.
- The skill list in the prompt is capped at 64 KB; skills past the cap are
  not listed, and a warning names them.
- Validation is lenient: a bad name warns, a missing description or broken
  frontmatter skips the skill. `disable-model-invocation: true` hides a skill
  from the model.

## Approval ladder

`--approve` (or `jas config set approve`) gates tool calls; each level gates a
superset of the one before.

| level | prompts before |
|---|---|
| `never` (default) | nothing |
| `shell` | every `shell` call |
| `mutating` | every `shell`, `write` and `edit` call |

A denied call returns `status: denied` to the model and the run continues.
With no TTY (CI, piped stdin) a gated call cannot be answered: jas records
`approval: unavailable` and exits 5.

## Exit codes

| code | meaning |
|---|---|
| 0 | model gave a final answer |
| 1 | provider failure after retries, unreadable or corrupt `config.json`/`auth.json`/`trust.json`, or internal error |
| 2 | usage error (bad flags, invalid setting value, missing model/key) |
| 3 | context full |
| 4 | turn cap (`max_turns`) hit |
| 5 | approval required but no TTY to ask |
| 130 | interrupted (Ctrl-C / SIGTERM) |

Tool errors are results the model sees; they never set the exit code. Every
run that gets past setup appends a versioned JSONL event log at
`~/.jas/sessions/<id>/events.jsonl`; the same code appears in its
`session_ended` event.

## Shell safety: this is YOLO

**The `shell` tool is not sandboxed.** Path confinement and the deny floor
apply to the file tools only. `shell` gets an allowlisted environment
(`PATH`, `HOME`, `USER`, `LANG`, `TERM`, `TMPDIR`) but can still reach
anything your user can: `cat ~/.aws/credentials`, `curl`, `python -c`. jas
does not pretend to filter command lines. Use `--approve=shell` or
`--approve=mutating` to review calls, and for isolation run jas inside
bubblewrap or Docker.

bubblewrap (read-only root, writable project, network kept for the API):

```sh
bwrap --ro-bind / / --bind "$PWD" "$PWD" --dev /dev --proc /proc \
  --unshare-all --share-net jas run "..."
```

The root is still readable, including `~/.aws` and `~/.ssh`. To hide `$HOME`,
add `--tmpfs "$HOME"` before the `--bind` (if the project is under `$HOME`,
the later bind re-exposes it) and pass the key through `JAS_API_KEY`, since
`~/.jas` will be hidden. The jas binary must live outside `$HOME` (for
example `/usr/local/bin`; `go install` puts it in `~/go/bin`, which the tmpfs
hides) or be bound in with `--ro-bind ~/go/bin/jas /usr/local/bin/jas`:

```sh
JAS_API_KEY="$OPENROUTER_API_KEY" bwrap --ro-bind / / --tmpfs "$HOME" \
  --bind "$PWD" "$PWD" --dev /dev --proc /proc --unshare-all --share-net \
  jas run --model z-ai/glm-5.3 "..."
```

Docker: mount only the project, e.g.
`docker run --rm -v "$PWD":/work -w /work -e JAS_API_KEY <image-with-jas> jas run "..."`.

## Benchmarks

`make bench` measures cold start and runs three fixed tasks against a fixture
repo; `BENCH_AGENT=pi make bench` does the same for pi. Results, jas vs pi on
the same model, are in [docs/benchmarks.md](docs/benchmarks.md).
`make perf` runs the gated cold-start test (`JAS_PERF_TESTS=1`).

## Demo

To record the demo GIF, install [vhs](https://github.com/charmbracelet/vhs)
and run `vhs demo.tape`.

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
