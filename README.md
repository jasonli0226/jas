# jas

> A tiny, fast coding-agent harness in Go. Inline chat, templates and scenarios,
> a JSONL audit trail, and CI-friendly `jas run`.

**Status:** v1 — `jas` (chat), `jas run`, `jas config`, `jas auth`, `jas sessions`;
`jas farm` is experimental. See [docs/design.md](docs/design.md).

## The pitch

jas is a single static Go binary for day-to-day coding with a receipt:

- **chat** — inline pair programming in the terminal (`jas`); same tools,
  approvals and event log as `jas run`. Scrollback stays in your terminal
  (Bubble Tea `Println`); no fullscreen TUI.
- **templates & scenarios** — start a project in chat with `AGENTS.md` /
  skills as the working instructions; later, declarative
  `.jas/scenario.yaml` runtime policy and `jas bootstrap` to draft one from
  an existing repo (design §8, v2). Spec-driven scaffold (`jas spec`) is
  proposed.
- **visible** — every step is a versioned JSONL event (audit + resume)
- **CI-ready** — `jas run --json`, stable exit codes, secrets deny/redact
- **lean** — cache-friendly prompts, parallel reads, capped and elided tool
  output; streaming loop in ~200 lines
- **farm** *(experimental)* — one too-big task split into worktree parts
  behind a supervisor-run `check`; refuses most splits. Not the lead story.

OpenAI-compatible endpoints (OpenRouter, vLLM, ollama); MCP client *(planned
v2)*. The model is the brain; jas is the nervous system.

jas and pi do different jobs: pi goes deep on interactive coding product
surface; jas keeps chat and project setup on a CI-grade harness (event log,
exit codes) and a narrow farm for big, checked splits. Use whichever fits
the job — see design §14 and [docs/use-cases.md](docs/use-cases.md).

## Install

The repo is private, so install from a clone (Go 1.27+):

```sh
go install ./cmd/jas            # from the repo root; or: make build  ->  ./bin/jas
make chat                       # rebuild and open the chat on the current code
make chat ARGS=--continue       # same, resuming the last session (DIR=... picks the project)
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
jas run --max-tokens 200000 --timeout 30m "..."     # hard caps for CI (exit 7)
jas run --compact "..."                             # compact long runs like the chat
jas sessions                                        # other jas sessions in this repo
```

Subcommands: `jas` (chat), `jas run`, `jas config set|get|unset|list|init`,
`jas auth add|list|rm|rotate`, `jas sessions [--prune]`, `jas --version`.
Run flags: `--json`, `--model`, `--provider`, `--base-url`, `--approve`,
`--max-turns`, `--max-output-tokens`, `--trust-project`, `--name` (session
title), and the run-only `--max-tokens N` (stop before a request that would
cross N total tokens), `--timeout D` (wall-clock cap, e.g. `30m`) and
`--compact`. The chat takes the same flags
except `--json`, plus `--continue`; it needs a terminal, so scripts use
`jas run`. `jas run` prints diffs only on a terminal. There is deliberately
no `--api-key` flag (argv leaks via shell history and `ps`).

## Chat

`jas` opens an inline chat. Replies stream into the terminal's own scrollback
(no full-screen mode), above the input box and a status line showing the
model, approval level, context use and token counts. While a reply runs, a
dim spinner line above the input shows the run in progress (`working…`, or
`thinking…` while the model reasons).

- **Enter** sends; **Alt+Enter** or **Shift+Enter** adds a newline. Typing
  while a reply runs steers it: the text reaches the model at its next step.
  **Esc** stops the run; **Ctrl-L** clears the screen (keeps the session);
  **Ctrl-C** clears the input, and twice on an empty box quits.
- **`/`** opens a command menu, filtered as you type, with every command and
  `/skill:<name>`; Up/Down move, Tab or Enter completes. `/help` lists every
  command and key.
- **`/model`** lists every configured model (qualified when several providers
  are set) — Enter switches the session, **Ctrl+S** also stores the choice as
  the config default. A model on another provider is refused; `/new` starts a
  session on it.
- **Up/Down** from the first or last line of the input recall earlier input.
- Each tool call shows a running row while it runs, then one result row.
- Edits and writes show a diff (line numbers, `+`/`-`) in the approval prompt
  and under the tool row.
- **`jas --continue`** or **`/resume`** reopens a saved session and reprints
  its conversation; Up/Down history comes back with it.
- The chat shows a start banner when it opens fresh and on **`/new`** (which
  also clears the screen): the version, the key hints above, and the context
  files and skills it loaded (context files left unloaded by a declined trust
  decision are marked `not loaded`). Resuming — `--continue` or `/resume` —
  does not show it. **Ctrl-L** clears the screen and reprints the banner
  without starting a new session.
- **`/name <title>`** (or `jas --name`) names the session. The `/resume`
  picker lists it by that title instead of its first message, and the status
  line shows it.

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
`compact_reserve_tokens`, `compact_keep_tokens`, `compact_model`,
`max_output_tokens` (default 16000, sent as `max_tokens` on every request),
and per-model `models.<name>.context_window`.

The chat compacts its context: once the prompt passes the window minus
`compact_reserve_tokens` (default 16384; never below half the window), older
messages are replaced by a summary, keeping about `compact_keep_tokens` (default
20000) of recent messages verbatim; on small windows that amount is capped at
half the compaction threshold. `compact_model` picks the summary model
(default: the session's). `/compact [focus]` compacts on demand. `jas run`
does not compact unless given `--compact`.

API keys come from `auth.json` (literal, mode 0600), a `$ENV_NAME` reference
stored there, or `JAS_API_KEY`. `jas auth list` shows names and sources,
never values.

Which providers work, and how to set each up (OpenRouter, ollama, local
servers, OpenAI, Microsoft Foundry), is in [docs/providers.md](docs/providers.md).

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
| user | `~/.agents/skills/`, `~/.claude/skills/`, `~/.config/jas/skills/` (Windows/macOS: `%AppData%`/`~/Library/Application Support` under `jas/skills`) |

Skills may sit in nested folders (e.g. `~/.agents/skills/team/review/SKILL.md`):
a folder with a `SKILL.md` is a skill and is not searched further; hidden
folders and `node_modules` are skipped, and the search stops 5 levels down.

Only each skill's name, description and path go in the system prompt; the
model reads the full `SKILL.md` (and any `references/` or `scripts/`) with
the `read` tool when a task matches. `read`, `ls` and `grep` may open listed
skill directories (the deny floor still applies there); `write`, `edit` and
`shell` stay confined to the project as before. In chat, a read of a
`SKILL.md` shows as a `◆ skill <name>` row, and `/skill:<name>` prints the
same marker above your message.

jas installs its own manual as the user skill `jas` under
`~/.config/jas/skills/` (refreshed from the binary at every start; delete or
shadow it freely — a same-named skill in `~/.agents/skills/` wins).

- On a name clash the first skill found wins, in the table's order: project
  before user, `.agents` before `.claude`, jas's config dir last. A warning
  names both. The same
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

### Always grants (chat)

The chat asks `approve <tool>: <call>? [y/N/always]`. `y` allows the call
once, Enter or `n` denies it, and any other text denies it and is passed to
the model as steering. `always` allows matching calls until jas quits:
grants survive `/new` but not `/resume` or `--continue`, are never saved, and
`/allowed` lists and removes them. A call that can't get a grant is allowed
once. An
`edit` or `write` grant covers the whole tool. A shell grant covers later
commands with the same first two words, a program and its subcommand
(`go test`, `git commit`, `npm run`), with any arguments.

No shell grant is offered for:

- commands containing `;`, `&`, `|`, `$(`, a backtick, `>`, `<` or a
  newline, or starting with `VAR=` (on Windows also `^` and `%`);
- one-word commands, programs given by path (`./run.sh`), or a second word
  that is a flag or path;
- shells, interpreters (`python`, `node`, …), wrappers that run another
  command (`env`, `sudo`, `xargs`, `timeout`, …), `find`, `rm` and similar.

Arguments after the two words are not checked, and some subcommands take
flags that run another program (`go test -exec`, `git fetch --upload-pack`).
Grant only what you would approve with any arguments.

## Exit codes

| code | meaning |
|---|---|
| 0 | model gave a final answer |
| 1 | provider failure after retries, unreadable or corrupt `config.json`/`auth.json`/`trust.json`, or internal error |
| 2 | usage error (bad flags, invalid setting value, missing model/key) |
| 3 | context full |
| 4 | turn cap (`max_turns`) hit |
| 5 | approval required but no TTY to ask |
| 7 | budget exceeded: `--max-tokens` or `--timeout` reached (`reason` is `tokens` or `timeout`) |
| 130 | interrupted (Ctrl-C / SIGTERM) |

Tool errors are results the model sees; they never set the exit code.
Exit 7 is preceded by a `budget_stop` event, and `session_ended` carries the
`reason` for every non-zero exit.

Each session also writes a small status record to
`<repo>/.jas/sessions/<id>.json` (git-excluded; file tools can't touch it),
so `jas sessions` lists the other jas sessions working in the same repo,
including linked worktrees. Every
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

## Farm (experimental)

```sh
jas farm plan "migrate logging to slog"    # draft a split plan; you approve it on a TTY
jas farm run plan.yaml [--json]             # run the parts in parallel worktrees (CI path)
jas farm "migrate logging to slog"          # plan → approve → run, one terminal
jas farm status [<farm-id>]                 # parts, tokens and the check verdict (offline)
jas farm clean [<farm-id>] [--branches]      # drop the farm's worktrees (and branches)
jas farm vars [--trust-project]             # each farm variable's value and source
```

Farm splits one big task into parts that run in parallel worktrees, then
merges them behind a `check` gate. Each part must also pass `part_check`
(defaults to `check`; `part_check: ""` in `.jas/farm.yaml` turns it off) in
its own worktree before it counts as done; a failure goes back to the worker
for one fix round. A clean merge whose `check` fails goes to the integrator.
Worktrees live in the repo under `.jas/worktrees/<farm-id>/<part>`
(git-excluded, hidden from your chat's file tools); set `worktree.dir` to an
absolute path to put them elsewhere. Without `--json`, the run prints a line per part event and
worker tool call, e.g. `[core] → shell go test ./core/...`. **Experimental** until the benchmark gate
(farm vs plain `jas run` on wall-clock) lands. Not yet: `farm resume`,
`researcher`/`reviewer` roles, `--spec` planning, `farm init`, interactive
`/farm` (attach and the live board stay [#12](https://github.com/jasonli0226/jas/issues/12)).
Full design:
[docs/superpowers/specs/2026-11-02-farm-design.md](docs/superpowers/specs/2026-11-02-farm-design.md).

## Design

Full design in [docs/design.md](docs/design.md); how jas compares with pi is
§14. Real-world use in
[docs/use-cases.md](docs/use-cases.md) — interactive pair programming, one-shot
`jas run`, project init and study→scenario (v2 / proposed), CI runs, farm,
and what jas deliberately isn't. Core bets:

1. Small core, pluggable everything — 6 interfaces total (§3)
2. Inline chat and `jas run` share one harness (tools, approvals, event log)
3. JSONL event log = checkpoint/resume + audit, no extra machinery

Design-review decisions: [#1](https://github.com/jasonli0226/jas/issues/1)–[#13](https://github.com/jasonli0226/jas/issues/13).

## License

MIT
