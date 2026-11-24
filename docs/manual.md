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
[Profiles](#profiles) · [Memory](#memory) · [Dreams](#dreams) · [Farm](#farm) · [Exit codes](#exit-codes) · [Event log](#event-log) ·
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
| `jas dream [flags]` | consolidate the profile's memory into a reviewed proposal ([Dreams](#dreams)) |
| `jas dream list\|diff\|apply\|discard\|undo` | review, apply, drop or revert a dream ([Dreams](#dreams)) |
| `jas farm …` | experimental parallel worktree runner |
| `jas attach [<farm-id> [<part>]]` | watch a running farm from another terminal ([`jas attach`](#jas-attach)) |
| `jas --version` | print the version: the release tag; `git describe` for `make build`; the Go-stamped module version for a plain `go build`/`go install`; `dev` when there is none (e.g. `go run`) |

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
| `--max-tokens N` | stop with exit 7 before a request that would cross N total tokens (cached prompt tokens count at `cached_token_weight`) |
| `--timeout D` | stop with exit 7 after wall-clock duration D (e.g. `30m`) |
| `--compact` | summarize older history when the context fills, as the chat does |
| `--wrap-up` | at the turn cap (`max_turns`) or `--max-tokens`, send one last request with tools off asking for the final answer, instead of stopping without one; an answer exits 0. Tool calls in that reply are not run, and the run exits 4 or 7 at the cap as it would have. The token wrap-up starts early, when the room left after the next request would drop below one output cap (`max_output_tokens`, or 32000 when none is set), so the answer gets a full output cap; it needs room for 1000 output tokens, else exit 7 as usual. A token wrap-up reply cut off at its output cap exits 7 (reason `tokens`), not 9 |

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
jas profile new <name> [--extends <parent>]
jas profile show [name] [--resolved]
```

`list` prints one line per profile, the active one marked `*`
(`* ds  4 skills`); a profile with a parent ends `(extends p)`, and one whose
`profile.yaml` does not parse `(bad profile.yaml)`. `--json` prints
`[{"name":"ds","active":true,"skills":4}]`, with `"extends":"p"` or
`"error":"<message>"` added when they apply. `list` never resolves a chain, so
one broken profile does not fail it. `use` sets `profile` in the global config
(an unknown name is a usage error).
`new` creates the empty `~/.jas/profiles/<name>/` (names: a-z, 0-9 and `-`,
at most 64 characters); with `--extends <parent>` it also writes a
`profile.yaml` of `extends: <parent>` (mode 0600), and the parent must exist
(`default` always does). `show` prints a profile's path, its `extends` and
`replace_agents` when `profile.yaml` sets them, the settings in its
`config.json`, whether it has an `AGENTS.md`, and its own skills; with no name
it shows the active one. `show --resolved` instead resolves the parent chain
(a chain that does not resolve is a usage error, exit 2) and prints the chain,
each merged setting and the profile it comes from, the exact instructions
text the prompt gets, the effective skills with their profile, and the skills
a profile excludes. Details: [Profiles](#profiles).

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
`profile memory ignored: <reason>`, and when a dream is waiting for review
with `dream <id> pending: jas dream diff` (see [Dreams](#dreams)). It reads the same settings a session
would, counting the project as trusted only when you already trusted it.

| flag | does |
|---|---|
| `--path` | print only the project memory dir (even with memory off) |

### `jas farm`

```
jas farm plan "<task>" [-o plan.yaml] [--allow-dirty] [--trust-project]
jas farm run <plan.yaml> [--json] [--allow-dirty] [--trust-project] [--approval-wait <duration>] [--review]
jas farm "<task>" [--review]      # plan, approve, run in one terminal
jas farm status [<farm-id>]       # parts, tokens, check verdict (offline)
jas farm clean [<farm-id>] [--branches]
jas farm vars [--trust-project]   # each farm variable's value and source
```

| flag | does |
|---|---|
| `-o plan.yaml` | where `plan` writes the plan |
| `--allow-dirty` | plan or run on a dirty tree; uncommitted changes are not part of the farm |
| `--json` | (`run`) stream `farm.jsonl` events, only, on stdout |
| `--approval-wait <duration>` | (`run`, no TTY) hold gated calls this long for a `jas attach` client to answer, even with none attached; overrides `budget.approval_wait` |
| `--review` | (`run`, `jas farm "<task>"`) run the read-only reviewer after the integrator; `--review=false` turns a configured `review: true` off for this run. Never changes the plan, so its approval stands. `jas farm plan` does not take it |
| `--branches` | (`clean`) also delete the `jas/farm/<id>/*` branches |
| `--trust-project` | load the project's context and `.jas/farm.yaml` without asking |

`status` and `clean` take a farm id or a unique id prefix; without one they
use the latest farm. `clean` also removes the farm's lock file
(`~/.jas/run/<farm-id>.lock`) unless the farm is still running; then it
keeps the file and says so on stderr.

`status` reads `manifest.json`, which the farm keeps current while it runs:
a part's (and the integrator's) tokens update after each model request, a part paused from `/farm`
or attach shows `paused`, and the last line reads `final: none yet` until
the farm has an outcome. Token counts there (per part and `total_tokens`), in
plan review and in the run summary are weighted like the farm budgets: cached
prompt tokens count at `cached_token_weight`. With a review it also prints `review: <status>  open <n>`
and one line per open finding (`R1 [missed] <plan_ref> <where>: <text>`).

Configuration: [Farm](#farm).

### `jas attach`

```
jas attach                        # the one running farm
jas attach <farm-id>              # a farm by id or unique id prefix
jas attach <farm-id> <part>       # also that part's (or integrate's, review's) replies and tool results
```

Watch and steer a farm from another terminal: started by `jas farm run` or by
the chat's `/farm`. Attach prints the `jas farm status` table, the last 3 tool
calls of each part, then one line per event as the farm runs (the same lines
`jas farm run` prints). It reads the farm's files under
`~/.jas/farms/<farm-id>/`; a farm is running while its process holds
`~/.jas/run/<farm-id>.lock`. To change the farm it also talks to the running
farm over a Unix socket (below).

- Ctrl-C detaches; the farm keeps running.
- When the farm ends, attach prints its end line and exits. If the farm
  process dies, attach prints `farm process ended` and exits.
- A farm that is not running shows its table and how it ended
  (`farm ended: <reason>`, or `farm process ended without finishing`).
- With no argument and no running farm, attach says so and exits 0; with
  several running it lists them and exits 2. An unknown or ambiguous id, an
  unknown part or extra arguments exit 2.

#### Command line

While the farm runs, attach reads commands from its own stdin, one per line.
Only a terminal stdin is read: run in the background (`jas attach <id> &`) or
with stdin from a pipe or file, attach watches only, does not connect for
control (it does not count as an attached client) and never reads stdin.
Output from the farm is sanitized and redacted before it is printed.

| command | does |
|---|---|
| `steer <part> <text>` | send `text` to a running part (or `integrate`) as the owner's next message |
| `pause <part>` / `resume <part>` / `drop <part>` | as the chat's `/farm pause`, `resume`, `drop` |
| `stop [hard]` | stop the farm (`hard` cancels running calls at once) |
| `status` | print the status table again |
| `y` \| `n` \| `a` \| `always` \| `farm` `[<approval-id>]` | answer a gated call; the id defaults to the oldest pending approval |

An unknown command prints the command list and sends nothing. Replies print
nothing on success (except `stop`, which prints `ok`) and the error text on
failure; a part the plan does not have prints `unknown part "<part>"`. A steer that its part
finished without reading comes back as
`[<part>] not delivered (part finished): <text>`; if the client that sent it
has left, the farm's own output (`jas farm run` or the chat) prints that line.

Answers: `y`/`yes`, `n`/`no`, `a`/`always` (that tool for that
part only, as the chat's `always`) and
`farm` (a standing grant for that tool for every part of the farm). `farm`
has no single-letter form. After an `always` or `farm` answer, every attached client
prints what it granted, as the farm's own terminal does:
`[<part>] always: shell: ls (covers any arguments)`, or `allowed once` when
the call can't get a standing grant.

#### Approvals

A part's gated call becomes a pending approval with a stable id. Attach
prints each one, those pending when it connects and those that open later:

```
[backend-api] approve [a3] shell: npm install zod (y/n/a/always/farm)
```

Attach shows the tool and its one-line summary (the command, or the path
for a write or edit), not the diff: only the farm's own terminal and the
chat show a write's preview. When the summary is not enough to decide,
answer `n`, or answer from the farm's own terminal.

The first answer wins, whether from the farm's own terminal (chat
`/farm` approvals) or any attach client. A late answer prints
`already answered (<answer>, by <source>)`; an id that is not pending prints
`unknown approval`. When another answer settles an approval, attach prints
`approval [<id>] closed: <answer> (by <source>)`; an answer this client gave
reads `by you`, there and in `already answered`. The chat's approval level is
fixed when the farm starts; attach does not change it. On `jas farm run`'s
terminal, a prompt another answer settles is withdrawn (its line ends, then
`[<part>] [<id>] answered from attach: <answer>` when attach answered it); an
answer typed just after another one won prints
`[<part>] [<id>] already answered (<answer>, by <source>)`.

A farm with no terminal (`jas farm run` without a TTY) holds a gated call
for an answer for up to 10 minutes. With `budget.approval_wait` 0 (the
default) it holds only while an attach client is connected: with nobody
attached a gated call is refused at once (`approval_needed`, as before), and
when the last attached terminal leaves, held calls are refused (`by:
no_client`). `budget.approval_wait` or `--approval-wait <duration>` (which
overrides it) replaces the 10 minutes with its value and holds gated calls
even with nobody attached, so a client can attach later and answer. A negative
`--approval-wait` is a usage error. A held call is refused when the window
ends (`by: timeout`). The part's own timeouts still apply while it is held.
A soft stop (attach `stop`, or the first Ctrl-C) refuses every held call at
once (`by: stop`) and any later gated call on that farm, so a held approval
never keeps a stopping farm alive; a farm with a terminal or chat keeps
prompting.

#### Socket and protocol

A running farm listens on `~/.jas/run/<farm-id>.sock` (mode 0600). It is
unavailable on Windows (the farm serves no socket and logs nothing) and when
the path is too long for a Unix socket (the farm then logs
`attach_unavailable`); either way attach only watches, printing once
`control unavailable (watching only)`. Attach does the same when it cannot
connect. If the socket closes while the farm still runs, attach keeps
watching; commands then print the same notice.

The protocol is JSON lines. A client sends
`{"id","cmd","part","text","hard","approval_id","answer"}` (`cmd`: `hello`,
`steer`, `pause`, `resume`, `drop`, `stop`, `approve`) and the farm replies
`{"reply":{"id","ok","error",...}}` with the same id; `hello` is first (any
other command before it is refused with `hello first`) and returns
`farm_id`, `protocol` (1), `client` (this client as `by` names it, such as
`attach:c1`) and the pending approvals. The farm also pushes
`{"notice":{...}}` lines (`approval_requested`, `approval_closed`,
`approval_granted` with its `text`, `steer_undelivered`). A reply to a line that is not valid JSON has an empty
`id`.

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
| Ctrl+L | clear the screen and reprint the banner; the session and the terminal scrollback are kept |
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
| `/new` | start a fresh conversation and clear the screen and the terminal scrollback (where the terminal supports it); model, thinking, approval level and always grants are kept |
| `/model [name]` | switch model; bare `/model` opens a picker. A model on another provider is refused: `/new` first |
| `/name [title]` | name the session (shown in `/resume` and the status line); bare `/name` shows it |
| `/resume` | continue a recent session in this project; always grants and level reset |
| `/compact [focus]` | summarize older messages to free context; `focus` steers the summary |
| `/reload` | reload `AGENTS.md` / `CLAUDE.md`, skills and project context into this session (same trust rules) |
| `/profile [name]` | switch profile (skills, `AGENTS.md`, settings) in this session; bare `/profile` opens a picker marking the active one. Live settings (`approve`, `model`, `hints`, `farm_bell`) apply now; other changed keys take effect on `/new`. Switching to the active profile just reloads. A switch prints a new `[Profile]` row |
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
| the first approval prompt | what `y`, `a`/`always` and free text do |
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
| `a` or `always` | allow matching calls until jas quits ([always grants](#always-grants)) |
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
| `cached_token_weight` | 0.1 | share of a cached prompt token (as the provider reports `cached_tokens`) charged against `--max-tokens` and farm budgets, above 0 and at most 1; 1 charges in full. Requests still reserve the full prompt first, so no cap is crossed. Farms use the host's value |
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
| `read` | read a file (also skill directories, hidden skills included). A repeat of a read of the same file and range whose output is unchanged, while that earlier result is still in the history (not compacted or elided), returns a one-line note (`unchanged since your read of <path> N turns ago (same range)`) instead; the next identical read returns the content | — |
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
skills kept before user skills); the model reads a full `SKILL.md` when a
task matches. Home paths in the prompt, here and in the memory index
headings, are written `~/…`: the file tools expand them, as they do `~\…` on
Windows, and the prompt tells the model to pass `~/` paths through as
written. Project
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

**Inheritance.** A profile may extend one parent: `profile.yaml` in its folder
takes `extends: <parent>`, `replace_agents: true` and `exclude: [<skill>, …]`
(unknown keys are an error). The chain runs root first and holds at most 3
profiles. `extends: default` works without a `default/` folder. A missing
parent, a loop or a longer chain is an error (exit 2). The chain is read at
start, `/profile` and `/reload`; a chain that no longer resolves fails and the
chat keeps its current profile. `jas profile new --extends`, `list` and
`show --resolved` are under [`jas profile`](#jas-profile).

- Instructions: each profile's `AGENTS.md` goes in, root first, then the
  project's. `replace_agents: true` drops the parents' `AGENTS.md` text; it
  affects instructions only.
- Skills: project, then the profile, its parent, … the root, then the built-in
  manual; the first skill of a name wins. `exclude` is cumulative along the
  chain and hides a skill from the catalog only. It is not a security
  boundary: the files stay readable. A `skills/` symlinked into another
  profile is still ignored; to share skills, use `extends`. A `write` or
  `edit` into a parent's skills is always asked, and the prompt names the
  owner (`in profile base's skills, shared with every profile that extends it`).
- Settings: the child wins key by key; `models.<id>` merges per model id. A
  child cannot remove an inherited key; set the default explicitly.
  `jas config set|unset --in-profile` and `/settings --in-profile` act on the
  active profile's own file, as do scoped reads. When a parent also sets the
  key they add `(overrides profile base)` on set or
  `(now inherited from profile base)` on unset.
- Memory is never inherited; the `memory` mode setting is.
- `/reload` re-reads the chain's `AGENTS.md` and skills; settings wait for
  `/new`.

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

**Writes.** The agent changes memory with `write` and `edit`. In a chat it
offers a note in the turn you correct it, state a preference, confirm a
decision or say where something lives outside the repo, and drops it if you
decline. Every memory
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
dirs, index size against the cap and note counts. When the profile has a
pending dream, the report ends with `dream <id> pending: jas dream diff`
(see [Dreams](#dreams)).

## Dreams

`jas dream` reads your recent sessions and the profile's memory store and
proposes a cleaned-up store: lint fixes, merged duplicates, corrections and
new notes, each cited to the session events behind it. It never changes the
live `memory/`; you review the proposal and apply it.

`jas dream [--since D] [--sessions N] [--max-tokens N] [--timeout D] [--json] [--profile NAME]`

| flag | default | does |
|---|---|---|
| `--since D` | the watermark | read sessions since D: a duration back from now (`36h`, `7d`), a date (`2026-11-01`, local midnight) or an RFC 3339 time |
| `--sessions N` | 50 | read at most the N most recently written sessions |
| `--max-tokens N` | 400000 | stop the model pass with exit 7 before a request that would cross N total tokens (cached prompt tokens count at `cached_token_weight`) |
| `--timeout D` | `20m` | stop the model pass with exit 7 after wall-clock duration D |
| `--json` | off | print nothing until the run ends, then one JSON object: `dream.json` plus `report` (its path) and `lint_open`; no apply question |
| `--profile NAME` | the active profile | dream for that profile's store |

**How it runs.** jas copies `memory/` into a temporary workspace (removed when
the run ends), lints it, and picks the window: event logs of this profile
(logs without a profile count as `default`) written after the watermark,
newest first, skipping other dreams' sessions. It writes a cited evidence
digest (capped at 200 KB, each quoted string trimmed to 2 KB) and runs one
model session, logged like any other with a `dream` object on
`session_started`. That session's tools are `read`, `ls` and `grep` over the
workspace (the copied store, the digest and copies of the window's logs),
`write` and `edit` inside the copied store only, `remove_note` and
`report_change`. There is no shell, no web, no approval prompt and no access
to the repo or the live store. Inside the copy every memory write rule holds
(top-level `.md` notes or `projects/<key>/`, valid frontmatter, no secrets)
except that `MEMORY.md` may be rewritten whole. jas then lints again,
checks every change against the model's `report_change` calls and every
citation against the logs, and writes the report.

**Memory setting.** `memory: off` in the global or profile config (or
`JAS_MEMORY=off`) makes `jas dream` fail with exit 1; `read` does not block
it. Project settings do not apply: a dream covers the whole profile store. A
`memory/` the store's guards ignore ([Memory](#memory)) gets no dream.

**Output.** The run prints a summary (notes changed, added and removed, lint
fixed and open, promotion candidates, the report's warnings and its path).
On a terminal it then asks `apply? [y/N/d=diff]`: `d` prints the diff and
asks again, `y` shows the diff stat and applies (asking once more when the
report has warnings). Off a terminal it stops after writing the proposal.

**Reviewing a dream.** Subcommands work on the active profile (or
`--profile NAME`) and take an optional `<id>`; an invalid id is a usage
error (exit 2).

| command | does |
|---|---|
| `jas dream list [--json]` | one row per dream: id, state, counts. `--json` prints an array of `dream.json` objects, each with `stale` |
| `jas dream diff [id]` | a `pending` dream: a warning first if memory changed since it started, then the diff live to proposed, then the report. An `applied` dream: the diff of the store it replaced (`before/`) to the live store. Any other state: exit 1 |
| `jas dream apply [id] [--yes]` | shows the diff stat and the warnings (unexplained changes, a large removal, unsupported changes with their reasons), asks `apply? [y/N]`, swaps the proposal in and logs `dream_applied`. `--yes` skips the question; off a terminal it is required (exit 2 without it) |
| `jas dream discard [id]` | drops a pending proposal, keeps its report, moves the watermark and logs `dream_discarded` |
| `jas dream undo [id]` | puts back the store an applied dream replaced and logs `dream_undone`; exit 1 when that store was pruned or the live store changed since the apply |

Default ids: `diff`, `apply` and `discard` use the newest `pending` dream;
`undo` uses the newest `applied` one. With none, the command exits 1 with
"no pending dream" (or "no applied dream"). Stale is decided at apply time
and shown in `list`: `list` shows a pending dream whose live store differs from the one it
started from as `pending (stale)` without saving anything, `diff` prints a
warning line first, and `apply` then refuses (exit 1) and records the dream
as `stale`. The subcommands call no model, so they need no API key and work
with `memory: off`; only the run needs both.
The events go to the dream's own session log.

**Exit codes.** `0` proposal written (or the subcommand done); `1` failure (including memory off);
`2` usage (including `apply` off a terminal without `--yes`); `130` interrupted, and no proposal is written (Ctrl+C at the
apply question also exits 130 and leaves the dream pending). When the model
pass is stopped early, the proposal it has so far is still written and
marked `partial`, and the exit code says why: `7` the `--max-tokens` or
`--timeout` cap, `4` the turn cap (`max_turns`), `3` the context is full.

**Storage.** Dreams live beside the store, never inside it:

```text
~/.jas/profiles/<name>/dreams/
  state.json        watermark: end time of the last applied or discarded dream
  <id>/
    dream.json      id, profile, state, started_at, ended_at, session_id,
                    window (since, until, sessions), base and proposed
                    (path -> sha256), counts, partial, and the
                    warnings apply shows (unexplained paths, unsupported
                    changes, large removal)
    report.md       the report you read
    evidence.md     the evidence digest the model saw
    proposed/       the full proposed store
    before/         after apply: the store it replaced (for undo)
```

An id is `<UTC timestamp>-<4 hex>`. A dream's state is `pending`,
`applied`, `discarded`, `stale` or `undone`. Each run keeps the newest 10
dreams that are not pending and deletes older ones. Nothing is written under
`dreams/` until the model pass ends, so a failed or interrupted run leaves
no `<id>/`.

**Watermark.** With no `--since`, the window starts at the watermark, or 30
days ago when there is none. Applying or discarding a dream moves the
watermark to that dream's `ended_at`; undo does not move it back, so a
discarded or undone window is not re-dreamed; use `--since` to revisit it.
`--since` and `--sessions` change only which logs this dream reads.

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

A failed part's line gives its reason and the first line of the error
(`[a] failed: error (start worker session: ...)`); `part_ended` in
`farm.jsonl` carries the full text as `detail`.

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
| `budget.farm_tokens` | 5000000 | token cap for the whole farm; this cap and `per_worker_tokens` count cached prompt tokens at the host's `cached_token_weight` ([Settings](#settings)) |
| `budget.max_output_tokens` | 16000 | `max_tokens` per request |
| `budget.integrator_reserve` | 0.2 | share of `farm_tokens` kept for the integrator |
| `budget.per_worker_timeout` | 30m | wall-clock cap per part |
| `budget.farm_timeout` | 2h | wall-clock cap for the farm |
| `budget.requests_per_minute` | — | rate limit across workers |
| `budget.approval_wait` | 0 | without a TTY, hold gated calls this long (replacing the 10 minute default) for an attach client, even with none attached; 0 holds them up to 10 minutes, only while a client is attached |
| `board.max_rounds` | 3 | board message rounds |
| `board.post_chars` / `board.deliver_chars` | 500 / 2000 | board message size caps |
| `on_failure` | `drop` | `drop` a failed part or `abort` the farm |
| `models.planner`, `models.integrator` | session model | models for those roles |
| `models.reviewer` | `models.integrator`, then the session model | model for the reviewer |
| `review` | false | run the reviewer after the integrator (all config layers; `--review` / `--review=false` overrides it per run). The chat's `/farm` gets its review from this key |
| `roles.<name>` | — | custom roles: `tools`, `writes`, `prompt`, `approve`, `model`, `budget`. `tools` names built-in tools, only `read`, `grep`, `ls`, `write`, `edit` and `shell`; a role naming any other is unavailable (`unknown tool "<name>"`) and a plan using it is refused (exit 2) |

### Reviewer

An optional read-only check of the integrated tree against the approved plan,
after `check` passes: `review: true` in `farm.yaml` or `--review` on a run.
It costs tokens on every run, so it is off unless you ask, and the planner
never turns it on. It changes no plan and no approval hash.

The built-in role `reviewer` has `read`, `grep` and `ls` and no write or
shell tool; a `roles.reviewer` override that adds `write`, `edit` or `shell`
is a config error, and a plan part with `role: reviewer` is refused. Round 1
reads the plan and the integrated diff from the base (the redacted diff up to
64 KiB inline, past that a `git diff --stat`, itself capped at 64 KiB, and it
reads the files itself). It reports with `board_post`; only the reviewer can
post these two topics, and parts cannot:

| topic | fields |
|---|---|
| `finding` | `kind` (`missed`, `drift` or `unsure`), `plan_ref` (`task`, a part id, `interface:<name>` or `contracts`), `where` (`path` or `path:line`; optional for `missed`), `text`, `id` (round 2 only) |
| `review_done` | `summary`, `round`; closes the round |

The supervisor numbers findings `R1`, `R2`, ... in post order. With no
`missed` or `drift` finding the review is met (`unsure` findings are only
recorded). Otherwise the integrator gets one fix round listing them, `check`
runs again, and a fresh reviewer round 2 re-reports (with `id`) each finding
still open and any new `drift`. A `missed` or `drift` finding left open means
exit 6, `reason: review_unmet`; merging then needs the typed `merge anyway`
phrase. A reviewer that ends without `review_done` is an unfinished review,
also `review_unmet`. The review is skipped (`skipped`) when a part failed,
was paused or was dropped, or `check` failed before round 1. A failed
`check` after the fix round is `check_failed`. Round 1 draws on the work
pool and round 2 may use what is left of `budget.integrator_reserve`; a
drained pool is exit 7.

In `farm.jsonl` (and `--json`): `review_started` (`round`, `base`, `head`);
`review_ended` (`round`, `status` `done` or `unfinished`, `findings` with
`missed`, `drift` and `unsure` counts, `open`, and `detail` when
unfinished); `farm_ended` gains `review_status`, `review_open` and, when set,
`review_detail`, only when a review was requested. `manifest.json` gains
`review`: `requested_by` (`flag` or `config`), `status` (`met`, `unmet`,
`unfinished` or `skipped`), `rounds` (`round`, `session_id`, `log`, `tokens`,
`findings`), `open` (finding ids not cleared) and `detail`. The chat
dashboard shows a `reviewer` row under the integrator.

Full design:
[farm spec](design/2026-11-02-farm-design.md),
[reviewer](design/2026-11-24-farm-reviewer-design.md).

## Exit codes

| code | meaning |
|---|---|
| 0 | the model gave a final answer |
| 1 | provider failure after retries, unreadable config/auth/trust file, or internal error |
| 2 | usage error: bad flags, invalid setting, missing model or key, a profile chain that does not resolve (missing parent, loop, more than 3) |
| 3 | context full (`jas dream` still writes its partial proposal) |
| 4 | turn cap (`max_turns`) hit (`jas dream` still writes its partial proposal) |
| 5 | approval needed but no terminal to ask |
| 6 | a check run by jas failed, or required work is unmet (farm: `check_failed`, `part_failed`, `review_unmet`) |
| 7 | budget exceeded: `--max-tokens` / `--timeout` / farm budgets (`reason`: `tokens` or `timeout`; token caps count cached prompt tokens at `cached_token_weight`); `jas dream` still writes its partial proposal |
| 8 | the farm planner refused to split the task |
| 9 | the final reply was cut off at `max_output_tokens` (finish reason `length`); the cut-off text is still printed. Raise `max_output_tokens` (`jas dream` still writes its partial proposal) |
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
| `session_started` | profile, model, provider, base_url, approve, root, context files (the profile `AGENTS.md` first when present; memory indexes included), skills, context window; optional title, memory, dream (`id`, `window_sessions`: the session `jas dream` runs), `profile_hash`, `profile_chain` |
| `session_resumed` | as `session_started`, on each reopen (with `profile_hash`, `profile_chain`) |
| `user_message` | content; `steer: true` for steering text |
| `turn_started` | — |
| `assistant_message` | text, content, final, finish_reason |
| `usage` | prompt_tokens, completion_tokens, cached_tokens |
| `tool_started` | tool, call_id, args, mutating |
| `approval` | call_id, tool, decision (`granted` \| `denied` \| `unavailable`); only when someone was asked, not for calls the approve level or a standing grant lets through |
| `tool_result` | call_id, tool, status (`ok` \| `error` \| `denied` \| `cancelled` \| `interrupted` \| `not_run`), output; optional diff |
| `elided` | count, tokens_before, tokens_after |
| `turn_ended` | status, exit_code, reason, turns, prompt_tokens, completion_tokens |
| `model_changed` | model, provider, base_url |
| `session_titled` | title |
| `compaction` | summary, first_kept_id, tokens_before, tokens_after, usage |
| `context_reloaded` | bytes, plus the reloaded context files and skills (`/reload`); `profile_hash`, `profile_chain` |
| `profile_changed` | from, to, the reloaded context files and skills, settings deferred to `/new` (a profile switch in the chat); `profile_hash`, `profile_chain` |
| `dream_proposed` | `jas dream` wrote a proposal: dream_id, counts, partial, report |
| `dream_applied` | `jas dream apply` changed memory: dream_id, counts |
| `dream_discarded` | `jas dream discard` dropped a proposal: dream_id |
| `dream_undone` | `jas dream undo` restored memory: dream_id |
| `injected` | text added to a tool-results message (farm board deliveries) |
| `shell_ran` | your `!` / `!!` command finished: command, output, exit code, error, cancelled, truncated, local (`!!`) |
| `budget_stop` | reason (`tokens` \| `timeout`); for tokens: pool, limit, used (cached prompt tokens at `cached_token_weight`; raw counts are on `usage`), want |
| `wrap_up` | reason (`turn_cap` \| `tokens`), max_tokens: `--wrap-up` asked for the final answer at the turn cap, or before the token cap leaves less than one output cap; a cut-off `tokens` reply ends at the token cap (exit 7) |
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
| `~/.jas/run/<farm-id>.lock`, `.pid` | held by a running farm (`jas attach` checks it); the pid file is advisory, for finding the process |
| `<root>/.jas/config.json`, `config.local.json` | project / local settings |
| `<root>/.jas/farm.yaml` | project farm config |
| `<root>/.jas/sessions/<id>.json` | live session records (`jas sessions`) |
| `<root>/.jas/worktrees/<farm-id>/part-<part>` | farm worktrees |
| `~/.jas/profiles/<name>/config.json` | a profile's settings (between global and project) |
| `~/.jas/profiles/<name>/AGENTS.md` | a profile's instructions |
| `~/.jas/profiles/<name>/skills/` | the active profile's skills |
| `~/.jas/profiles/<name>/memory/` | the profile's memory: `MEMORY.md`, notes, `projects/<key>/` ([Memory](#memory)) |
| `~/.jas/profiles/<name>/dreams/` | `jas dream` proposals, reports and the watermark ([Dreams](#dreams)) |
| `<UserCacheDir>/jas/manual/jas/` | jas's built-in manual skill |
