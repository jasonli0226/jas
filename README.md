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
Or configure by hand. Global settings live in `~/.jas/` (`JAS_HOME`
overrides); project prefs go in `<root>/.jas/config.json` and personal
overrides in `<root>/.jas/config.local.json`:

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

Every command, flag, key, chat command, setting, exit code and event type
is listed in the **[manual](docs/manual.md)**.

## Chat

`jas` opens an inline chat. Replies stream into the terminal's own scrollback
(no full-screen mode), above the input box and a status line showing the
model, approval level, context use and token counts. While a reply runs, a
dim spinner line above the input shows the run in progress (`working…`, or
`thinking…` while the model reasons).

- **Enter** sends; while a reply runs, Enter **steers** it (the text reaches
  the model at its next step) and **Ctrl+Q** queues a **follow-up** for when
  it ends. **Esc** stops the run.
- **`/`** opens the command menu; `/help` lists every command and key.
- `/model` switches models, `/resume` or `jas --continue` reopens a session,
  `/compact` frees context, `/reload` picks up edited `AGENTS.md` and skills.
- Edits and writes show a diff in the approval prompt and under the tool row.

One-line `tip:` hints teach a command the first time it would help
(`hints=false` turns them off). All keys and commands:
[manual → Chat](docs/manual.md#chat).

## Configuration

Settings use Claude Code's three layers (prefs only — runtime policy stays in
`.jas/scenario.yaml` later). `auth.json` and `trust.json` stay global-only.

```
~/.jas/config.json                 # global defaults
<project>/.jas/config.json         # shared team prefs (commit)
<project>/.jas/config.local.json   # personal override (gitignore)
```

Precedence: flag > `JAS_*` env var > local > project > global > built-in
default. Every key `k` has a `JAS_<K>` env var; `JAS_API_KEY` supplies the key
(use these in CI and containers, where there is no `~/.jas/`). `jas config list`
and chat `/settings` (picker or typed) show each effective value and its source.

```sh
jas config set model z-ai/glm-5.3                 # → ~/.jas/config.json (default)
jas config set --project approve shell             # → <root>/.jas/config.json
jas config set --local max_turns 80                # → <root>/.jas/config.local.json
jas config set base_url https://openrouter.ai/api/v1
jas config set models.my-local.context_window 32768
jas config get model                               # effective value
jas config get --project approve                   # value stored in that layer
jas config unset --local max_turns
jas config list                                    # value + source of every key
```

In chat, bare `/settings` opens a filterable picker (closed-set keys like
`approve`, `provider`, `web_tools`, and `farm_bell` get a second value picker;
free-text keys prefill `/settings <key> `). Typed `/settings <key> <value>`
still writes to **local** by default (scope with `--local` / `--project` /
`--global`). `/model` + **Ctrl+S** still stores the default model in the
global file. Add `.jas/config.local.json` to `.gitignore` so personal
overrides stay local.

Every key, its default and its env var: [manual → Settings](docs/manual.md#settings).

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

## Web tools (opt-in)

`web_fetch` and `web_search` are off by default. Turn them on with
`web_tools=true` (layered settings / `JAS_WEB_TOOLS` / `/settings`). Chat and
`jas run` then offer both tools; farm parts do not.

```sh
jas config set --local web_tools true
jas config set --local search_base_url http://localhost:8080   # optional SearXNG
```

Without `search_base_url`, search uses DuckDuckGo lite (no API key). Fetched
pages are untrusted: when `web_tools` is on and `approve` is still the
built-in default, the session approve level rises to `shell` (an explicit
`never` is kept). Tool output still goes through redaction. `git_clone` is
not shipped yet.

## Approvals and exit codes

`--approve never|shell|mutating` (or `jas config set approve`) gates tool
calls: `never` (default) asks nothing, `shell` asks before every `shell`
call, `mutating` also before `write` and `edit`. In the chat, answering
`always` grants matching calls until jas quits. With no terminal, a gated
call exits 5. `jas run` exits 0 on a final answer and non-zero for usage
errors, a full context, the turn cap, budgets and interrupts.

Rules for always grants, every exit code and the `--json` event types:
[manual → Approvals](docs/manual.md#approvals),
[Exit codes](docs/manual.md#exit-codes), [Event log](docs/manual.md#event-log).

Each session writes a small status record to
`<repo>/.jas/sessions/<id>.json` (git-excluded), so `jas sessions` lists the
other jas sessions working in the same repo, including linked worktrees.
Every run that gets past setup appends a versioned JSONL event log at
`~/.jas/sessions/<id>/events.jsonl`.

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

![jas chat: ask for a change, the agent edits main.go, git diff shows it](docs/demo.gif)

To re-record it, install [vhs](https://github.com/charmbracelet/vhs) (it also
needs `ttyd` and `ffmpeg`), build `bin/jas`, export `OPENROUTER_API_KEY`, and
run `vhs demo.tape` from the repo root. It makes real model calls on a
throwaway copy of `bench/fixture`.

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
worker tool call, e.g. `[core] → shell go test ./core/...`. **Experimental.**
Not yet: `farm resume`,
`researcher`/`reviewer` roles, `--spec` planning, `farm init`, attach from
another terminal ([#12](https://github.com/jasonli0226/jas/issues/12)).
Full design:
[docs/superpowers/specs/2026-11-02-farm-design.md](docs/superpowers/specs/2026-11-02-farm-design.md).

**In the chat:** the agent may suggest a farm for big, splittable work (its
`suggest_farm` tool; where a farm cannot run, it tells the agent why): it
shows the drafted task and asks `plan this farm? [yes/no/edit]` (yes plans it,
edit puts `/farm "<task>"` in your input, no or Enter drops it), and never
starts one itself. `/farm "<task>"` plans a farm from the conversation; the plan
summary opens a picker (approve, edit in `$EDITOR`, replan with feedback, or
quit). Once approved, one live row per agent sits under the input (status,
last activity, tokens) with a totals line, and finished events print into the
chat. `/farm` prints the dashboard; `/farm run <plan.yaml>` starts an approved
plan. One farm per window; it needs a trusted project with a `check` and a
clean working tree. A final **merge** (CLI or chat) is refused while the main
checkout has uncommitted changes; the branches are kept. **Ctrl+1–9** (or Alt+1–9) flips the window to that agent:
the screen clears and shows its transcript live, as the main chat does;
**Ctrl+0** (or Alt+0) comes back. While you watch an agent, main-chat output
is held (the bottom strip counts it) and printed when you return.
`/farm attach <part|N>` and `/farm back` do the same where the keys do not
reach jas. In an agent's view, Enter steers that agent (`queued for <part>`
until it reads it; text it never reads comes back to the input); Esc and
Ctrl+C only clear the input and never stop an agent. `/farm pause|resume|drop
<part|N>` and `/farm stop` (twice to cancel now) control the farm; quitting
with a farm running asks first. An agent's approval prompt opens wherever you
are, labelled `[part]` (answers: y, N, always for that part, farm for every
part); your draft is set aside and comes back after. A failure (retry, drop,
abort) or the final merge/keep/discard waits for the main chat with no turn
running; meanwhile the strip shows `farm needs you · ctrl+0`. The terminal
bell rings once per such prompt (`jas config set farm_bell false` turns it
off). When the farm ends, its summary (outcome, branch, final choice, each
part) prints and is attached to your next message, so you can ask the agent
about the result; it never starts a turn on its own. Earlier outcomes the agent
did not see (a suggestion dismissed, planning failed or refused, a plan quit,
a farm started) ride along with your next message the same way. A running farm belongs to
the window: it survives `/new` and `/resume`.

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
