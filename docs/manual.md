# jas manual

The complete reference: every command, flag, key, chat command, setting,
exit code and event type. The [README](../README.md) is the overview; this
page is where to look things up. Inside the chat, `/help` lists commands and
keys, and you can ask jas itself ("how do I queue a follow-up?") — it ships
this knowledge as its built-in `jas` skill.

A test (`cmd/jas/docs_manual_test.go`) fails when a flag, setting, chat
command, exit code or event type is missing here, so the page cannot drift
from the binary.

Contents: [Commands](#commands) · [Chat](#chat) · [Settings](#settings) ·
[Providers and models](#providers-and-models) ·
[Approvals](#approvals) · [Tools](#tools) ·
[Project context and skills](#project-context-and-skills) ·
[Profiles](#profiles) · [Memory](#memory) · [Farm](#farm) · [Exit codes](#exit-codes) · [Event log](#event-log) ·
[Files](#files)

## Commands

| command | does |
|---|---|
| `jas` | inline chat in this project (needs a terminal); first run onboards |
| `jas run "task"` | one-shot task, no terminal needed (CI, scripts) |
| `jas config set\|get\|unset\|list\|init` | settings |
| `jas profile list\|use\|new\|show` | named bundles of skills, instructions and settings |
| `jas auth add\|list\|rm\|rotate` | API keys |
| `jas sessions [--prune]` | other jas sessions working in this repo |
| `jas memory [--path]` | memory dirs, index size and note counts; `--path` prints the project memory dir |
| `jas farm …` | experimental parallel worktree runner |
| `jas --version` | print the version |

### Settings flags (chat, `jas run`, `jas farm plan|run`)

Each overrides its setting's env var and stored config for one run.

| flag | setting |
|---|---|
| `--profile NAME` | `profile` |
| `--provider P` | `provider` |
| `--model M` | `model` (plain or `provider/model`) |
| `--base-url U` | `base_url` |
| `--approve L` | `approve`: `never` \| `shell` \| `mutating` |
| `--max-turns N` | `max_turns` |
| `--max-output-tokens N` | `max_output_tokens` |

There is deliberately no `--api-key` flag: argv leaks through shell history
and `ps`. Use `jas auth add` or `JAS_API_KEY`.

### `jas` (chat)

`jas [--continue] [--name T] [--trust-project] [settings flags]`

| flag | does |
|---|---|
| `--continue` | resume this project's newest session |
| `--name T` | title for the session (as `/name`) |
| `--trust-project` | load `AGENTS.md` / `CLAUDE.md`, project skills and project settings without asking |

### `jas run`

`jas run [flags] "task"`

| flag | does |
|---|---|
| `--json` | stream the event log as JSONL on stdout ([Event log](#event-log)) |
| `--name T` | title for the session |
| `--trust-project` | load project context without asking (no TTY: untrusted unless given) |
| `--max-tokens N` | stop with exit 7 before a request that would cross N total tokens |
| `--timeout D` | stop with exit 7 after wall-clock duration D (e.g. `30m`) |
| `--compact` | summarize older history when the context fills, as the chat does |

`jas run` prints diffs only on a terminal.

### `jas config`

```
jas config set   [--global|--in-profile|--project|--local] <key> <value>
jas config get   [--global|--in-profile|--project|--local] <key>
jas config unset [--global|--in-profile|--project|--local] <key>
jas config list
jas config init
```

`set` and `unset` write global by default. `get` without a scope prints the
effective value; with a scope, the value stored in that layer. `--in-profile`
writes `~/.jas/profiles/<active>/config.json`, where the active profile is the
one `JAS_PROFILE` or the `profile` key names; `JAS_PROFILE=ds jas config set
--in-profile model x` therefore targets the non-active profile `ds` (`config`
is a subcommand, so there is no `--profile` flag here). A profile file refuses
`provider`, `base_url`, `search_base_url` and `profile`. `list` shows
every effective value and its source. Endpoint keys are global-only and
project/local settings need trust: [Settings](#settings). `init` asks for provider, base URL
(custom only) and model, and writes the global file. Keys:
[Settings](#settings).

### `jas profile`

```
jas profile list [--json]
jas profile use <name>
jas profile new <name>
jas profile show [name]
```

`list` prints one line per profile, the active one marked `*`
(`* ds  4 skills`); `--json` prints `[{"name":"ds","active":true,"skills":4}]`.
`use` sets `profile` in the global config (an unknown name is a usage error).
`new` creates the empty `~/.jas/profiles/<name>/` (names: a-z, 0-9 and `-`,
at most 64 characters). `show` prints a profile's path, the settings in its
`config.json`, whether it has an `AGENTS.md`, and its skills; with no name it
shows the active one. Details: [Profiles](#profiles).

### `jas auth`

```
jas auth add <provider> [--env NAME]   # store a key (read without echo), or a reference to $NAME
jas auth list                          # names and sources, never values
jas auth rm <provider>
jas auth rotate <provider>             # replace a stored key
```

| flag | does |
|---|---|
| `--env NAME` | store a reference to the env var `NAME` instead of the key |

Keys live in `~/.jas/auth.json` (mode 0600). With `JAS_NO_PLAINTEXT_KEYS=1`,
jas refuses to store a literal key; only `--env` references are accepted.

### `jas sessions`

Lists the live jas sessions in this repo, linked worktrees included: title,
branch, files touched, last heartbeat.

| flag | does |
|---|---|
| `--prune` | delete stale session records |

### `jas memory`

Prints the memory report for this project: mode, the profile and project
memory dirs, index size against the cap and note counts. When the memory dir
is ignored (see [Memory](#memory)) the report ends with
`profile memory ignored: <reason>`. It reads the same settings a session
would, counting the project as trusted only when you already trusted it.

| flag | does |
|---|---|
| `--path` | print only the project memory dir (even with memory off) |

### `jas farm`

```
jas farm plan "<task>" [-o plan.yaml] [--allow-dirty] [--trust-project]
jas farm run <plan.yaml> [--json] [--allow-dirty] [--trust-project]
jas farm "<task>"                 # plan, approve, run in one terminal
jas farm status [<farm-id>]       # parts, tokens, check verdict (offline)
jas farm clean [<farm-id>] [--branches]
jas farm vars [--trust-project]   # each farm variable's value and source
```

| flag | does |
|---|---|
| `-o plan.yaml` | where `plan` writes the plan |
| `--allow-dirty` | plan or run on a dirty tree; uncommitted changes are not part of the farm |
| `--json` | (`run`) stream `farm.jsonl` events, only, on stdout |
| `--branches` | (`clean`) also delete the `jas/farm/<id>/*` branches |
| `--trust-project` | load the project's context and `.jas/farm.yaml` without asking |

Configuration: [Farm](#farm).

## Chat

The chat prints into your terminal's scrollback (no full-screen mode).
Under the input, a status line shows the active profile, model, approval level, context use,
token counts and the number of queued messages.

### Keys

| key | does |
|---|---|
| Enter | send; while a reply runs, **steer** it: the text reaches the model at its next step |
| Ctrl+Q | while a reply runs, queue a **follow-up**: sent as the next message once the run ends ok (several go together); if the run stops early it comes back into the input. Idle, same as Enter |
| Alt+Up | pull every queued steer and follow-up back into the input to edit |
| Alt+Enter, Shift+Enter, Ctrl+J | newline |
| Esc | stop the run (queued text comes back into the input); also closes an expanded tool output or a menu |
| Up / Down | recall earlier input (from the first or last line of the input) |
| Ctrl+L | clear the screen and reprint the banner; the session is kept |
| Ctrl+O | expand or collapse the latest tool output (Up/Down scroll it) |
| Ctrl+C | clear the input; on an empty input, stop a running `!` command; otherwise twice quits |
| Ctrl+1–9 / Alt+1–9 | show farm agent N |
| Ctrl+0 / Alt+0 | back to the main chat |
| `/` | open the command menu (Up/Down move, Tab or Enter complete, Esc hide) |

In a picker: type to filter, Up/Down move, Enter choose, Esc cancel. In the
`/model` picker, Ctrl+S also stores the choice as the default model for new
sessions (global config); in the `/profile` picker it stores the default
profile (`profile` in the global config) and switches to it.

On native Windows, Shift+Enter works even when the terminal collapses it to
Enter (jas probes the physical Shift key; not under WSL or SSH).

A paste of more than 10 lines or 1000 characters goes into the input as one
marker, `[paste #N +L lines]` (or `[paste #N C chars]`), so the cursor and the
rest of your message stay easy to edit. Backspace or Delete next to a marker
removes the whole paste. The marker turns back into the pasted text when you
send (also as a steer, follow-up, `/` command or prompt answer), and Up
recalls it as the marker. Smaller pastes go in as they are. The message shown
in the transcript is the full text the model gets.

### Commands

| command | does |
|---|---|
| `/help` | list commands and keys |
| `/new` | start a fresh conversation and clear the screen; model, thinking, approval level and always grants are kept |
| `/model [name]` | switch model; bare `/model` opens a picker. A model on another provider is refused: `/new` first |
| `/name [title]` | name the session (shown in `/resume` and the status line); bare `/name` shows it |
| `/resume` | continue a recent session in this project; always grants and level reset |
| `/compact [focus]` | summarize older messages to free context; `focus` steers the summary |
| `/reload` | reload `AGENTS.md` / `CLAUDE.md`, skills and project context into this session (same trust rules) |
| `/profile [name]` | switch profile (skills, `AGENTS.md`, settings) in this session; bare `/profile` opens a picker marking the active one. Live settings (`approve`, `model`, `hints`, `farm_bell`) apply now; other changed keys take effect on `/new`. Switching to the active profile just reloads |
| `/settings` | settings picker; closed-set keys get a value picker |
| `/settings <key> [value]` | show or set a setting (writes **local** unless `--global` / `--in-profile` / `--project` / `--local`; **global** in an untrusted project, where project and local writes are refused). `--in-profile` writes the chat's active profile's `config.json` (refused for `provider`, `base_url`, `search_base_url`, `profile` and `providers.*`). `approve`, `model`, `hints` and `farm_bell` apply to the current session; other keys take effect on `/new` |
| `/settings unset <key>` | remove a setting from local (or `--global` / `--in-profile` / `--project`; global by default in an untrusted project) |
| `/memory` | show the memory mode, the profile and project memory dirs, index size against the cap and note counts; says why when the dir is ignored. See [Memory](#memory) |
| `/session` | session file, tokens used, context use |
| `/thinking` | show or hide the model's thinking |
| `/approve [level]` | show or set the approval level: `never` \| `shell` \| `mutating` |
| `/allowed` | list and remove always grants |
| `/copy` | copy the last reply to the clipboard |
| `/farm "<task>"` | plan a farm from the conversation; approve, edit, replan or quit |
| `/farm run <plan.yaml>` | start an approved plan |
| `/farm` | farm dashboard: every agent, status, activity, tokens |
| `/farm attach <part\|N>` / `/farm back` | show an agent / return (as Ctrl+N / Ctrl+0) |
| `/farm pause\|resume\|drop <part\|N>` | hold an agent before its next request, release it, or drop it and its dependents |
| `/farm stop` | stop the farm gracefully; again to cancel now |
| `/skill:<name> [text]` | run a skill with optional text |
| `/quit` | save the session and quit |

`/new`, `/model`, `/name`, `/resume`, `/compact`, `/reload` and `/profile` wait
until no reply is running; `/new`, `/resume`, `/reload`, `/profile`,
`/settings`, `/skill:<name>` and a farm start also wait while a reload or
profile switch is in progress.

### Shell commands

Start a line with `!` to run it yourself in the project root, through the
platform shell (`sh -c`, or `cmd` on Windows) with your own environment:

| input | does |
|---|---|
| `!cmd` / `! cmd` | run `cmd` and show its output; the output goes to the agent in front of your **next** message (`` Ran `cmd` `` and the output in a code block) |
| `!!cmd` | run `cmd` and show its output; it never reaches the agent |

A `!` command never starts a reply, needs no approval and skips the deny
floor: you typed it. It runs even while a reply runs, one at a time; Ctrl+C
on an empty input stops it. Its output is redacted before it is shown or
sent; the agent's copy is capped at 32 KiB. Several `!` commands before one
message all go with it, in order (`(command output attached)` marks the
message). `!` commands run in the main chat only, not in a farm agent view.

Every finished `!` and `!!` command is written to the session log at once
(`shell_ran`, redacted), so quitting before your next message loses nothing.
`!!` entries are marked local and never reach the agent, `/resume` included.
`!` output the agent has not seen yet stays with its session: `/new` leaves it
behind, and `/resume` attaches it to that session's next message. `/new` and
`/resume` wait while a `!` command runs.

### Hints

When a command would help, the chat shows a one-line `tip:` in place of the
status line until your next key press. Each tip shows at most once per chat;
`/settings hints false` turns them off at once (`jas config set hints false`
from the next start).

| when | tip |
|---|---|
| you press Enter while a reply runs | Ctrl+Q queues for after the run instead of steering |
| your message has a link and `web_tools` is off | `/settings web_tools true` |
| queued text comes back into the input | Enter sends it, Ctrl+C clears it |
| a run hits the turn cap | `/settings max_turns 100` |
| context passes 80% | `/compact` |
| the first approval prompt | what `y`, `always` and free text do |
| three tool calls denied in a row | type why instead of `n` |
| a shell/grep/ls output is cut on screen | Ctrl+O expands it |
| the model's context window is a guess | `/settings models.<id>.context_window <n>` |
| a fresh chat in a project with earlier sessions | `/resume` or `jas --continue` |

### Approval prompts

`approve <tool>: <call>? [y/N/always]`:

| answer | does |
|---|---|
| `y` | allow this call once |
| Enter or `n` | deny |
| `always` | allow matching calls until jas quits ([always grants](#always-grants)) |
| any other text | deny, and pass the text to the model as steering |
| a `/command` | not an answer: the prompt stays open (run the command after) |
| Esc | deny and stop the run (a farm agent's prompt: deny only) |

A farm agent's prompt is labelled `[part]` and also takes `farm` (allow for
every part); `always` there covers that part only.

## Settings

Precedence, highest first: flag > `JAS_<KEY>` env var > local > project >
profile > global > built-in default.

| layer | file |
|---|---|
| global | `~/.jas/config.json` |
| profile (active profile) | `~/.jas/profiles/<name>/config.json` |
| project (shared, commit it) | `<root>/.jas/config.json` |
| local (personal, gitignore it) | `<root>/.jas/config.local.json` |

Project and local files sit in the repository, so a clone can ship either.
They join the project trust prompt (with `AGENTS.md`, project skills and
`.jas/farm.yaml`) and are ignored until the project is trusted: declined,
not asked yet, or no terminal without `--trust-project` (headless runs skip
them with `skipping untrusted .jas/config.json`). A trusted project's files
apply, including `approve`, `redact_skip_paths` and `provider`; a content
change asks again, except a change jas itself writes with `jas config set` or
`/settings` while the project's trust is remembered (`always`). `/reload` can
change the trust decision, but settings follow it only at the next start.

Endpoints are global-only: `base_url`, `search_base_url` and
`providers.<name>.base_url` are read only from `~/.jas/config.json`, flags and
env, never from a project or local file (warned and ignored even when
trusted), so a repository cannot send your API key or searches elsewhere.
`jas config set --project` / `--local` refuses them; `unset` still removes one.
`jas config get` / `list` show what a trusted session uses; `get --project`
or `--local` shows the file as written.

| key | default | values / notes |
|---|---|---|
| `profile` | `default` | active profile: `~/.jas/profiles/<name>/`; see [Profiles](#profiles) |
| `provider` | (custom) | `openrouter` \| `ollama` \| `custom`: the default provider |
| `model` | — | model id, plain or `provider/model` |
| `base_url` | provider preset | endpoint of the default provider |
| `approve` | `never` | `never` \| `shell` \| `mutating` |
| `max_turns` | 50 | model requests per user message before exit 4 |
| `max_tool_output` | 32768 | bytes of tool output kept per call |
| `elide_at` | 0.6 | fraction of the window at which old tool results are elided |
| `keep_tool_results` | 6 | most recent tool results never elided |
| `redact_skip_paths` | — | comma-separated paths whose content is not redacted |
| `compact_reserve_tokens` | 16384 | compact when the prompt passes window minus this (never below half the window) |
| `compact_keep_tokens` | 20000 | recent tokens kept verbatim when compacting |
| `compact_model` | session's model | model that writes the summary |
| `max_output_tokens` | 16000 | sent as `max_tokens` on every request; 0 omits it |
| `farm_bell` | true | ring the terminal bell when a chat farm needs you |
| `web_tools` | false | offer `web_fetch` + `web_search`; when on and `approve` is still the default, the level rises to `shell` |
| `search_base_url` | — | SearXNG root for `web_search`; otherwise DuckDuckGo lite |
| `hints` | true | one-time chat tips ([Hints](#hints)); `false` turns them off |
| `memory` | on | `on` \| `read` \| `off`: profile memory under `~/.jas/profiles/<name>/memory/` ([Memory](#memory)): the index goes in the system prompt, notes are readable, writes are asked every time; headless runs and farm sessions read only. `read` refuses writes; `off` removes it. `JAS_MEMORY` is the base above profile and global; project and local values can only narrow it (project `off` hides project index from the prompt and makes the store read-only; its notes stay readable on request) and apply only once the project is trusted. Not live: takes effect on `/reload`, `/new`, `/resume`, a profile switch or the next session |
| `models.<name>.context_window` | catalog, else 128000 | context window for a model the catalog does not know |
| `providers.<name>.base_url` | — | a named provider, addressed as `<name>/<model>` |

Every key `k` also has the env var `JAS_<K>` (e.g. `JAS_MODEL`). Other
environment variables:

| variable | does |
|---|---|
| `JAS_API_KEY` | API key for this run (CI, containers) |
| `JAS_HOME` | move `~/.jas` |
| `JAS_NO_PLAINTEXT_KEYS=1` | refuse to store literal keys in `auth.json` |

## Providers and models

A model ref is `model` or `provider/model`. The text before the first `/`
picks the provider when it names one (a preset — `openrouter`, `ollama` — or
a `providers.<name>` entry); otherwise the whole ref is a model id on the
default provider, so OpenRouter ids like `z-ai/glm-5.3` work as they are.
`openrouter/z-ai/glm-5.3` is the explicit form.

Avoid naming a provider after an OpenRouter vendor (`qwen`, `deepseek`, …):
with OpenRouter as the default, `qwen/qwen3-coder` would then go to
`providers.qwen`. jas warns on stderr when that happens.

The API key for a provider comes from its `auth.json` entry (literal key or
`$ENV` reference), else `JAS_API_KEY`. Setup per provider (OpenRouter,
ollama, vLLM and other local servers, OpenAI, Microsoft Foundry):
[providers.md](providers.md).

## Approvals

| level | prompts before |
|---|---|
| `never` (default) | nothing |
| `shell` | every `shell` call |
| `mutating` | every `shell`, `write` and `edit` call |

A denied call returns `status: denied` to the model and the run continues.
With no terminal, a gated call cannot be answered: jas logs
`approval: unavailable` and exits 5.

A `write` or `edit` into the active profile's `skills/` folder always
prompts, at every level and whatever grants you hold: every later session in
every project loads those skills. `always` allows it once and adds no grant;
with no terminal it exits 5 like any gated call.

A `write` or `edit` into the profile's `memory/` folder prompts the same way,
at every level and whatever grants you hold. Unlike skills, a memory write
that cannot be asked (a headless run, a farm worker or planner) is refused
before any prompt as a tool error, so memory never causes exit 5
([Memory](#memory)).

### Always grants

Grants last until jas quits: they survive `/new` but not `/resume` or
`--continue`, and are never saved. An `edit` or `write` grant covers the
whole tool. A shell grant covers commands with the same first two words
(`go test`, `git commit`), any arguments. No shell grant is offered for
commands with `;`, `&`, `|`, `$(`, a backtick, `>`, `<`, a newline or a
leading `VAR=`; one-word commands; programs given by path; a second word
that is a flag or path; shells, interpreters, wrappers (`env`, `sudo`,
`xargs`, `timeout`, …), `find` and `rm`. Such a call is allowed once.

**The `shell` tool is not sandboxed.** See
[Shell safety](../README.md#shell-safety-this-is-yolo) for bubblewrap and
Docker recipes.

## Tools

| tool | does | gated at |
|---|---|---|
| `read` | read a file (also listed skill directories) | — |
| `ls` | list a directory | — |
| `grep` | search file contents | — |
| `write` | create or overwrite a file (atomic) | `mutating` |
| `edit` | replace text in a file (atomic) | `mutating` |
| `shell` | run a command (`sh -c`; `cmd` on Windows), allowlisted env | `shell` |
| `web_fetch`, `web_search` | fetch a page / search the web (only with `web_tools=true`) | — |
| `suggest_farm` | chat only: show the drafted task and ask `plan this farm? [yes/no/change]` (change asks what should change and sends your answer to the agent, which revises the task and suggests it again; empty asks again); where a farm cannot run (untrusted project, no `check`, uncommitted changes, no commit yet) it tells the agent why and to ask you first | — |

File tools are confined to the project and a deny floor (secrets, `~/.jas`,
session files); the openings in `~/.jas` are the active profile's `skills/`
and `memory/` folders, which `read`, `ls` and `grep` may open and `write` and
`edit` may change (always with a prompt, see [Approvals](#approvals); memory
has extra rules, see [Memory](#memory)). That is how the
agent installs a personal skill; a project-only skill goes in
`.agents/skills/` as before. Read-only calls in a row run in parallel; each mutating call
waits for the ones before it. Output is redacted (keys, tokens) before the
model or the log sees it.

## Project context and skills

`AGENTS.md` (or `CLAUDE.md`) in the project root goes into the system
prompt after a one-time trust prompt (stored in `~/.jas/trust.json`; a
content change asks again). `--trust-project` skips the prompt. The same
prompt covers project skills (listed by name), `.jas/farm.yaml` and the
project and local settings files ([Settings](#settings)). For a skill only
its `SKILL.md` is hashed; files under its `scripts/` or `references/` are
read on demand and can change without a new prompt. Trust an older jas recorded carries over when
the project has only `AGENTS.md` / `CLAUDE.md`; with project skills,
`.jas/farm.yaml` or settings files it is asked once more.

User skills are read only from the active profile's `skills/` folder. A jas that
finds none there but some in `~/.agents/skills`, `~/.claude/skills` or
`<config dir>/jas/skills` prints a one-time notice naming them and the new
folder (remembered in `.skills-notice` in the profile directory).

Skills are [Agent Skills](https://agentskills.io) folders with a `SKILL.md`.
Scanned in this order; on a name clash the first wins:

| scope | directories |
|---|---|
| project (needs trust) | `<root>/.agents/skills/`, `<root>/.claude/skills/` |
| user | `~/.jas/profiles/<name>/skills/` (the active profile), then the built-in manual under `<UserCacheDir>/jas/manual/` (`~/.cache/jas/manual/` on Linux) |

Only names and descriptions go in the prompt (capped at 64 KB, project
skills kept before user skills; home paths written `~/…`, which read tools
expand); the model reads a full `SKILL.md` when a task matches. Project
skills are looked for at the project root (the git top level) only. `/skill:<name>` runs one directly.
`disable-model-invocation: true` hides a skill from the model. jas
reinstalls its built-in skills under `<UserCacheDir>/jas/manual/` at every start: `jas` (this manual) and `skill-creator` (how to write, improve and test a skill: where it goes, the frontmatter rules above, and a `jas run --json` trigger check). A same-named profile or project skill overrides either. `~/.agents/skills/`, `~/.claude/skills/` and `~/.config/jas/skills/` are no longer read.
A project skill symlinked outside the project is skipped; a user skill
symlinked outside the jas home loads with a warning, since read-only tools can
then open that directory.

## Profiles

A profile is a named bundle under `~/.jas/profiles/<name>/`: a `config.json`
(settings), an `AGENTS.md` (instructions) and a `skills/` folder. Use one per
context (work, data science, a client) and switch without touching the
project. The profile `default` always exists (created when missing; a
read-only jas home runs on an empty one). Manage them with
[`jas profile`](#jas-profile) and `jas config --in-profile`.

**Choosing.** `--profile` > `JAS_PROFILE` > `profile` in local config >
project config > global config > `default`. A project's `profile` counts only
once the project is trusted. An unknown name from the flag or env is a usage
error (exit 2) naming the profiles there are; from a config file it warns
(`profile "ds" (from …) not found; using default`) and runs on `default`.

**Settings.** The profile's `config.json` is one layer: flag > env > local >
project > **profile** > global > defaults ([Settings](#settings)). A profile
cannot set `provider`, `base_url`, `search_base_url`, `profile` or
`providers.*`; those keys are dropped with a warning, so endpoints stay in
`~/.jas/config.json` and a profile cannot choose itself.

**Instructions.** The profile's `AGENTS.md` needs no trust and goes into the
prompt before the project's file, so on a conflict the project's later text
wins; a missing file adds nothing.

**Skills.** User skills come only from the active profile's `skills/` plus
the built-in manual ([Project context and skills](#project-context-and-skills)).
The one-time notice about old skill folders is described there. Read-only
tools may open files under that `skills/` folder, and `write` and `edit` may
change them (after a prompt; a missing `skills/` is created), although the
rest of the jas home stays denied. The rest of the floor still applies inside
it: a skill's `.env` or `secrets/` stays denied, and so does a write through
a link out of `skills/`. The `shell` filter keeps the whole jas home denied. Symlinks work: a symlinked `$HOME`, `~/.jas` or profile
folder changes nothing, and `skills/` itself may link to a folder outside the
jas home (a dotfiles checkout; it loads with the outside-the-home warning). A
`skills/` that links to the jas home, its `profiles/` folder, another
profile's skills or a folder holding the jas home is ignored with a warning
(`profile skills … ignored: …`), so `auth.json`, `trust.json`, configs and
other profiles stay unreadable and unwritable. A link inside a skill that points into the
jas home stays denied.

**In the chat.** `/profile [name]` (bare: a picker marking the active one)
switches in place: the conversation is kept, the profile's `AGENTS.md` and
skills reload, live settings (`approve`, `model`, `hints`, `farm_bell`) apply
now and other changed keys wait for `/new`. The switch sticks for the chat
(not saved); Ctrl+S in the picker also stores it as the default. `/resume`
reopens a session on the chat's active profile, not the recorded one, and says
so (`session was on profile "ds"; continuing on "default"`) when they differ.

**Farm.** Workers run on the session's active profile, with its `AGENTS.md`
and skills. A worker keeps the profile it started with; in the chat, workers
started after a mid-farm `/profile` switch get the new profile's skills,
`AGENTS.md` and read access, but the farm's limits (`max_turns`, budget,
approval level) stay as they were when the farm started. A worker whose worktree's project
context does not match the trusted main checkout drops only the project's
`AGENTS.md` and skills; the profile's stay.

## Memory

Memory is a small store of notes the agent keeps for you between sessions:
corrections, working preferences, why a choice was made, where things live.
It is for what a later session cannot rediscover from the repo; project
state already in the tree is not memory. Nothing is stored in the repo.

**Layout.** One store per profile, under `~/.jas/profiles/<name>/memory/`:

```text
memory/
  MEMORY.md            index of the profile's notes
  <name>.md            notes that apply everywhere
  projects/<key>/
    MEMORY.md          index of this project's notes
    <name>.md          notes about this project only
```

`<key>` names the project root (the main checkout: worktrees of one
repository share a key). Every byte outside `A-Z a-z 0-9 . _` is
percent-encoded as `%XX` (uppercase hex), then `%2F` becomes `-`, so
`/home/me/app` is `-home-me-app`. A key over 200 bytes keeps its first 180
bytes, then `~` and the first 16 hex digits of the SHA-256 of the full path.
`jas memory --path` prints the project dir.

**Notes.** A note is a `.md` file directly in either dir, with frontmatter:

```markdown
---
name: prefers-table-tests
description: one line, up to 200 characters, saying when this note matters
type: feedback
---
Use table-driven tests; see [[test-layout]].
```

`name` is kebab-case and equals the file name without `.md`. `type` is
`user` (who you are), `feedback` (corrections and confirmed ways of working),
`project` (ongoing work, decisions) or `reference` (where things live).
Other frontmatter keys are allowed. `[[other-note]]` links to a note in the
same dir; the agent follows links on demand with `read`.

**Index.** Each `MEMORY.md` is a list, one line per note:
`- [Title](file.md) — one-line hook`. Both indexes go into the system prompt
at session start (profile first, then project), redacted, capped together at
200 lines or 25 KB, whichever comes first. The project's index always keeps at
least 50 lines or 6 KB, so a long profile index cannot push it out. A cut index ends with
`(memory index truncated at N lines; prune it)`. Notes are not injected: the
agent reads them with `read`, and the index tells it which ones exist. The
files are listed in the session's `context_files`.

**Setting.** `memory` is `on` (default), `read` or `off`.

| value | does |
|---|---|
| `on` | index in the prompt; the agent may read and, after you approve, write notes |
| `read` | index and notes readable; every write is refused |
| `off` | no memory section, no memory access |

Layering: `JAS_MEMORY` is the base, above the profile and global files. Project
and local values (`<root>/.jas/config.json`, `config.local.json`) can only
narrow it: `read` makes the session read-only, and `off` hides the project's
index from the prompt and makes the whole store read-only for that project.
The project's notes stay readable: the agent can still `read` them if you ask,
it just isn't told they exist. They never
turn a base `off` or `read` back on. Project settings apply only when the
project is trusted ([Settings](#settings)). The setting is not live: it
applies on `/reload`, `/new`, `/resume`, a profile switch and the next
session, not mid-turn. `--memory` is not a flag; use `jas config set memory …`
or `JAS_MEMORY`.

**Writes.** The agent changes memory with `write` and `edit`. Every memory
write asks you, at every `approve` level and whatever grants you hold; `always`
allows that one write and adds no grant. Before the prompt, jas refuses (a tool
error the agent sees; the turn goes on) a write that:

- happens in a headless `jas run`, a farm worker or the planner, or with
  `memory` set to `read`: memory is read-only there and never exits 5
- is outside the two dirs' top level, or is not a `.md` file
- is a whole-file `write` over an existing `MEMORY.md` (use `edit` to add or
  remove a line)
- is a note with missing or invalid frontmatter (`name`, `description`,
  `type`)
- contains a secret (the same redaction as tool output)

There are no deletes: to retract a note the agent rewrites it or removes its
index line. Remove files yourself with your shell.

**Guards.** The profile `memory/` folder is lifted past the jas home floor
with the same guards as the profile `skills/` ([Profiles](#profiles)): symlinks
work, but a `memory/` that resolves to, inside or above the jas home,
`~/.jas`, or the profile's skills dir is ignored, so `auth.json` and configs stay
unreadable and unwritable. An ignored dir gives a warning at start and a
reason in `/memory` and `jas memory` (`profile memory … ignored: …`); the
session runs without memory, as it does when `memory/` is a file. A `MEMORY.md`
that links out of its dir is not loaded, with a warning. The `shell` filter
keeps the whole jas home denied.

**Commands.** `/memory` in the chat and `jas memory [--path]` show the mode, both
dirs, index size against the cap and note counts.

## Farm

Experimental. Farm splits one big task into parts that run in parallel git
worktrees, merges them, and runs `check` as the verdict. It needs a `check`
command and a clean tree. On the merged tree `check` must leave committed
files unchanged: one that edits or commits a tracked file (a formatter, a
code generator) fails, its changes are undone, and `integrate_check` lists
them under `changed`, so the integrator can commit the change itself and a
pass always describes the saved branch. Untracked build output is fine. A
pass is reused for the verdict only while the tree is still that commit. Configuration layers: built-in defaults,
`~/.jas/farm.yaml`, then `<root>/.jas/farm.yaml` (trusted projects only).
A project with no `AGENTS.md`, project skills, `.jas/farm.yaml` or project settings never shows
the trust prompt, so it stays untrusted: start jas with `--trust-project` to
farm there. In the chat, start a farm with `/farm`, not `jas farm` in the
`shell` tool (no terminal for the plan picker and approvals).

A part's commit never includes untracked dependency or cache dirs
(`node_modules`, `.venv`, `venv`, `__pycache__`, `.pytest_cache`,
`.mypy_cache`, `.ruff_cache`, `.tox`, `.gradle`, `.next`, `.nuxt`,
`.svelte-kit`, `.turbo`, `.parcel-cache`), even with no `.gitignore`;
tracked files under them still commit. Other build output (`dist/`) is
committed unless ignored, so workers are told to add a `.gitignore` inside
their owned paths when they create it.

When the farm ends, a terminal asks `[m]erge`, `[k]eep` (branches only) or
`[d]iscard` (deletes the farm branches); a failed part asks retry, drop or
abort. Answer with the word or the start of one (`d`, `disc`, `discard`), in
any case; text that only begins with a choice, such as `don't`, is asked
again. An empty final answer, or no terminal, keeps. Merging an unmet farm
also needs the typed phrase `merge anyway`. Merging is refused, keeping the
branches, when the target branch has commits the check never ran on (it moved
during the farm, or the plan's `base` was older than its HEAD): the prompt
says so, `owner_decision` carries the note and `target_head`, and
`manifest.json` records the `checked_commit` and the `target_head` at the
verdict. Merge the branch yourself and run the check.

| key | default | does |
|---|---|---|
| `check` | detected | command whose exit code is the farm's verdict |
| `part_check` | `check` | run in each part's worktree before it counts as done; `""` turns it off. In its fix round a worker may answer `BLOCKED: <reason>` when the check needs other parts' work: the part is done and the merged `check` decides (`part_check_deferred` in `farm.jsonl`). Files the check writes outside the part's `owns` are undone, not blamed on the worker |
| `generated` | detected | `[{path, regenerate}]`: files a tool writes as a side effect (lockfiles). A worker's changes to one outside its `owns` are undone without a fix round (`generated_restored`); after the merge, `regenerate` runs once at the repo root of the merged tree and only that file is committed, before `check` (`generated_regenerated`; a failing command is logged and `check` decides). Unset: detected from the base commit's root — `package-lock.json` (`npm install --package-lock-only --no-audit --no-fund`, also for a root `package.json` with no JS lockfile), `yarn.lock` (`yarn install --mode=update-lockfile` with `.yarnrc.yml`, else `yarn install --ignore-scripts --non-interactive`), `pnpm-lock.yaml` (`pnpm install --lockfile-only`), `Cargo.lock` (`cargo update --workspace`), `uv.lock` (`uv lock`). `[]` turns it off; set in `~/.jas/farm.yaml`, `[]` also turns it off for every project without its own list |
| `vars` | detected | `check`, `test_globs`, `src_globs`, `build`, `lint` (see `jas farm vars`) |
| `split.max_workers` | 3 | parts running at once |
| `split.min_parts` | 2 | fewer and the planner refuses (exit 8) |
| `split.disjoint_files` | true | parts may not write the same files |
| `worktree.dir` | `<root>/.jas/worktrees` | where worktrees go (absolute path to move them) |
| `worktree.copy` | — | files copied into each worktree |
| `worktree.setup` | — | command run in each new worktree |
| `budget.per_worker_tokens` | 1500000 | token cap per part |
| `budget.farm_tokens` | 5000000 | token cap for the whole farm |
| `budget.max_output_tokens` | 16000 | `max_tokens` per request |
| `budget.integrator_reserve` | 0.2 | share of `farm_tokens` kept for the integrator |
| `budget.per_worker_timeout` | 30m | wall-clock cap per part |
| `budget.farm_timeout` | 2h | wall-clock cap for the farm |
| `budget.requests_per_minute` | — | rate limit across workers |
| `board.max_rounds` | 3 | board message rounds |
| `board.post_chars` / `board.deliver_chars` | 500 / 2000 | board message size caps |
| `on_failure` | `drop` | `drop` a failed part or `abort` the farm |
| `models.planner`, `models.integrator` | session model | models for those roles |
| `roles.<name>` | — | custom roles: `tools`, `writes`, `prompt`, `approve`, `model`, `budget` |

Full design:
[farm spec](superpowers/specs/2026-11-02-farm-design.md).

## Exit codes

| code | meaning |
|---|---|
| 0 | the model gave a final answer |
| 1 | provider failure after retries, unreadable config/auth/trust file, or internal error |
| 2 | usage error: bad flags, invalid setting, missing model or key |
| 3 | context full |
| 4 | turn cap (`max_turns`) hit |
| 5 | approval needed but no terminal to ask |
| 6 | a check run by jas failed, or required work is unmet (farm) |
| 7 | budget exceeded: `--max-tokens` / `--timeout` / farm budgets (`reason`: `tokens` or `timeout`) |
| 8 | the farm planner refused to split the task |
| 130 | interrupted (Ctrl+C / SIGTERM) |

Tool errors are results the model sees; they never set the exit code.

## Event log

Every session appends a versioned JSONL log at
`~/.jas/sessions/<id>/events.jsonl`; `jas run --json` streams the same
events to stdout. Each line has `v`, `id`, `parent_id`, `seq`, `ts` and
`type`. Adding a type or field is backward compatible; renaming or removing
one bumps `v`.

| type | payload |
|---|---|
| `session_started` | profile, model, provider, base_url, approve, root, context files (the profile `AGENTS.md` first when present; memory indexes included), skills, context window; optional title, memory |
| `session_resumed` | as `session_started`, on each reopen |
| `user_message` | content; `steer: true` for steering text |
| `turn_started` | — |
| `assistant_message` | text, content, final |
| `usage` | prompt_tokens, completion_tokens, cached_tokens |
| `tool_started` | tool, call_id, args, mutating |
| `approval` | call_id, tool, decision (`granted` \| `denied` \| `unavailable`); only when someone was asked, not for calls the approve level or a standing grant lets through |
| `tool_result` | call_id, tool, status (`ok` \| `error` \| `denied` \| `cancelled` \| `interrupted` \| `not_run`), output; optional diff |
| `elided` | count, tokens_before, tokens_after |
| `turn_ended` | status, exit_code, reason, turns, prompt_tokens, completion_tokens |
| `model_changed` | model, provider, base_url |
| `session_titled` | title |
| `compaction` | summary, first_kept_id, tokens_before, tokens_after, usage |
| `context_reloaded` | bytes, plus the reloaded context files and skills (`/reload`) |
| `profile_changed` | from, to, the reloaded context files and skills, settings deferred to `/new` (a profile switch in the chat) |
| `dream_proposed` | `jas dream` wrote a proposal: dream_id, counts, partial, report |
| `dream_applied` | `jas dream apply` changed memory: dream_id, counts |
| `dream_discarded` | `jas dream discard` dropped a proposal: dream_id |
| `dream_undone` | `jas dream undo` restored memory: dream_id |
| `injected` | text added to a tool-results message (farm board deliveries) |
| `shell_ran` | your `!` / `!!` command finished: command, output, exit code, error, cancelled, truncated, local (`!!`) |
| `budget_stop` | reason (`tokens` \| `timeout`); for tokens: pool, limit, used, want |
| `check_ran` | cmd, dir, exit_code, duration_ms, timed_out, output tail |
| `session_ended` | status, exit_code, turns, prompt_tokens, completion_tokens; reason on non-zero exits |

Field-level detail, reader rules and the resume rules: [event-log.md](event-log.md).

## Files

| path | holds |
|---|---|
| `~/.jas/config.json` | global settings |
| `~/.jas/auth.json` | API keys (0600) |
| `~/.jas/trust.json` | project trust decisions |
| `~/.jas/farm.yaml` | user farm config |
| `~/.jas/sessions/<id>/events.jsonl` | session event logs |
| `<root>/.jas/config.json`, `config.local.json` | project / local settings |
| `<root>/.jas/farm.yaml` | project farm config |
| `<root>/.jas/sessions/<id>.json` | live session records (`jas sessions`) |
| `<root>/.jas/worktrees/<farm-id>/part-<part>` | farm worktrees |
| `~/.jas/profiles/<name>/config.json` | a profile's settings (between global and project) |
| `~/.jas/profiles/<name>/AGENTS.md` | a profile's instructions |
| `~/.jas/profiles/<name>/skills/` | the active profile's skills |
| `~/.jas/profiles/<name>/memory/` | the profile's memory: `MEMORY.md`, notes, `projects/<key>/` ([Memory](#memory)) |
| `<UserCacheDir>/jas/manual/jas/` | jas's built-in manual skill |
