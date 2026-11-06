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
[Farm](#farm) · [Exit codes](#exit-codes) · [Event log](#event-log) ·
[Files](#files)

## Commands

| command | does |
|---|---|
| `jas` | inline chat in this project (needs a terminal); first run onboards |
| `jas run "task"` | one-shot task, no terminal needed (CI, scripts) |
| `jas config set\|get\|unset\|list\|init` | settings |
| `jas auth add\|list\|rm\|rotate` | API keys |
| `jas sessions [--prune]` | other jas sessions working in this repo |
| `jas farm …` | experimental parallel worktree runner |
| `jas --version` | print the version |

### Settings flags (chat, `jas run`, `jas farm plan|run`)

Each overrides its setting's env var and stored config for one run.

| flag | setting |
|---|---|
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
| `--trust-project` | load `AGENTS.md` / `CLAUDE.md` and project skills without asking |

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
jas config set   [--global|--project|--local] <key> <value>
jas config get   [--global|--project|--local] <key>
jas config unset [--global|--project|--local] <key>
jas config list
jas config init
```

`set` and `unset` write global by default. `get` without a scope prints the
effective value; with a scope, the value stored in that layer. `list` shows
every effective value and its source. `init` asks for provider, base URL
(custom only) and model, and writes the global file. Keys:
[Settings](#settings).

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
Under the input, a status line shows the model, approval level, context use,
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
| Ctrl+C | clear the input; twice on an empty input quits |
| Ctrl+1–9 / Alt+1–9 | show farm agent N |
| Ctrl+0 / Alt+0 | back to the main chat |
| `/` | open the command menu (Up/Down move, Tab or Enter complete, Esc hide) |

In a picker: type to filter, Up/Down move, Enter choose, Esc cancel. In the
`/model` picker, Ctrl+S also stores the choice as the default model for new
sessions (global config).

On native Windows, Shift+Enter works even when the terminal collapses it to
Enter (jas probes the physical Shift key; not under WSL or SSH).

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
| `/settings` | settings picker; closed-set keys get a value picker |
| `/settings <key> [value]` | show or set a setting (writes **local** unless `--global` / `--project`) |
| `/settings unset <key>` | remove a setting from local (or `--global` / `--project`) |
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

`/new`, `/model`, `/name`, `/resume`, `/compact` and `/reload` wait until
no reply is running.

### Hints

When a command would help, the chat shows a one-line `tip:` in place of the
status line until your next key press. Each tip shows at most once per chat;
`/settings hints false` (or `jas config set hints false`) turns them off from
the next start.

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
| Esc | deny and stop the run (a farm agent's prompt: deny only) |

A farm agent's prompt is labelled `[part]` and also takes `farm` (allow for
every part); `always` there covers that part only.

## Settings

Precedence, highest first: flag > `JAS_<KEY>` env var > local > project >
global > built-in default.

| layer | file |
|---|---|
| global | `~/.jas/config.json` |
| project (shared, commit it) | `<root>/.jas/config.json` |
| local (personal, gitignore it) | `<root>/.jas/config.local.json` |

| key | default | values / notes |
|---|---|---|
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
| `suggest_farm` | chat only: pre-fill `/farm "<task>"` for you to send; where a farm cannot run (untrusted project, no `check`) it tells the agent why and to ask you first | — |

File tools are confined to the project and a deny floor (secrets, `~/.jas`,
session files). Read-only calls in a row run in parallel; each mutating call
waits for the ones before it. Output is redacted (keys, tokens) before the
model or the log sees it.

## Project context and skills

`AGENTS.md` (or `CLAUDE.md`) in the project root goes into the system
prompt after a one-time trust prompt (stored in `~/.jas/trust.json`; a
content change asks again). `--trust-project` skips the prompt.

Skills are [Agent Skills](https://agentskills.io) folders with a `SKILL.md`.
Scanned in this order; on a name clash the first wins:

| scope | directories |
|---|---|
| project (needs trust) | `<root>/.agents/skills/`, `<root>/.claude/skills/` |
| user | `~/.agents/skills/`, `~/.claude/skills/`, `~/.config/jas/skills/` |

Only names and descriptions go in the prompt; the model reads a full
`SKILL.md` when a task matches. `/skill:<name>` runs one directly.
`disable-model-invocation: true` hides a skill from the model. jas
reinstalls its own `jas` skill under `~/.config/jas/skills/` at every start.

## Farm

Experimental. Farm splits one big task into parts that run in parallel git
worktrees, merges them, and runs `check` as the verdict. It needs a `check`
command and a clean tree. Configuration layers: built-in defaults,
`~/.jas/farm.yaml`, then `<root>/.jas/farm.yaml` (trusted projects only).
A project with no `AGENTS.md`, project skills or `.jas/farm.yaml` never shows
the trust prompt, so it stays untrusted: start jas with `--trust-project` to
farm there. In the chat, start a farm with `/farm`, not `jas farm` in the
`shell` tool (no terminal for the plan picker and approvals).

| key | default | does |
|---|---|---|
| `check` | detected | command whose exit code is the farm's verdict |
| `part_check` | `check` | run in each part's worktree before it counts as done; `""` turns it off |
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
| `session_started` | model, provider, base_url, approve, root, context files, skills, context window; optional title |
| `session_resumed` | as `session_started`, on each reopen |
| `user_message` | content; `steer: true` for steering text |
| `turn_started` | — |
| `assistant_message` | text, content, final |
| `usage` | prompt_tokens, completion_tokens, cached_tokens |
| `tool_started` | tool, call_id, args, mutating |
| `approval` | call_id, tool, decision (`granted` \| `denied` \| `unavailable`) |
| `tool_result` | call_id, tool, status (`ok` \| `error` \| `denied` \| `cancelled` \| `interrupted` \| `not_run`), output; optional diff |
| `elided` | count, tokens_before, tokens_after |
| `turn_ended` | status, exit_code, reason, turns, prompt_tokens, completion_tokens |
| `model_changed` | model, provider, base_url |
| `session_titled` | title |
| `compaction` | summary, first_kept_id, tokens_before, tokens_after, usage |
| `context_reloaded` | bytes, plus the reloaded context files and skills (`/reload`) |
| `injected` | text added to a tool-results message (farm board deliveries) |
| `budget_stop` | reason (`tokens` \| `timeout`); for tokens: pool, limit, used, want |
| `check_ran` | cmd, dir, exit_code, duration_ms, timed_out, output tail |
| `session_ended` | status, exit_code, turns, prompt_tokens, completion_tokens; reason on non-zero exits |

Field-level detail and the resume rules: [design §3](design.md).

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
| `<root>/.jas/worktrees/<farm-id>/<part>` | farm worktrees |
| `~/.config/jas/skills/jas/` | jas's built-in manual skill |
