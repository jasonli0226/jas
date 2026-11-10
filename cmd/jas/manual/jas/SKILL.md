---
name: jas
description: Manual for jas, the coding-agent harness this session runs in. Use when the user asks about jas itself — CLI subcommands and flags, chat commands and keys, config keys, auth, approval levels, the deny floor, exit codes, skills, or the farm workflow — or when a task needs jas configured (jas config set, jas auth add, jas farm plan/run).
---

# jas manual

jas is a tiny, fast coding-agent harness in Go: an inline chat (replies
stream into the terminal's own scrollback, no full-screen mode) plus a
CI-friendly one-shot runner. This file is the stable surface. For exact
flags, keys, defaults, exit codes and event fields, read
`references/manual.md` next to this file (the full manual, also
`docs/manual.md` in a jas checkout).

## CLI

| command | what it does |
|---|---|
| `jas` | inline chat in the project root |
| `jas run "<task>"` | one-shot agent run (the CI path) |
| `jas --continue`, `jas --name <title>` | reopen, name a saved session |
| `jas sessions` | list this repo's sessions, across worktrees |
| `jas config get/set/unset/list`, `jas config init` | settings in `~/.jas/config.json` (`--in-profile`: the active profile's `config.json`) |
| `jas profile list/use/new/show` | profiles: skills, `AGENTS.md`, settings under `~/.jas/profiles/<name>/` |
| `jas auth add <provider>` | store a key in `~/.jas/auth.json` (0600) |
| `jas farm …` | experimental parallel worktree runner (below) |

Shared flags: `--model <ref>` (`provider/model`; with OpenRouter as the
default, a provider named like a vendor such as `qwen` takes `qwen/…` refs and
jas warns, so write `openrouter/qwen/…` to stay on OpenRouter), `--provider`, `--base-url`,
`--approve <level>`, `--json`, `--trust-project`. `JAS_*` env vars override
stored config; `JAS_HOME` moves `~/.jas`.

## Chat

Keys: Enter sends (typing while a reply runs steers it), Ctrl+Q while a reply
runs queues a follow-up sent only after the run ends ok, Alt+Up pulls queued
messages back into the input (one-time `tip:` hints teach these; setting
`hints=false` hides them), Esc stops the run,
Ctrl-L clears the screen (keeps the session), Ctrl-C clears the input (on an
empty box it stops a running `!` command, else twice quits), `!cmd` runs a
shell command whose output goes to the agent with the next message (`!!cmd`
keeps it local; no approval, output redacted), Alt/Shift+Enter or Ctrl+J is a newline (native
Windows also probes Shift so Shift+Enter works when the terminal drops the
modifier), Up/Down recalls input, `/` opens the command menu (Tab completes).
While a reply runs, a dim spinner line above the input shows the run in
progress (`working…`, or `thinking…` while the model reasons).
A start banner (version, key hints, the active `[Profile]`, loaded context files and skills; a
context file the trust decision declined is marked `not loaded`; the active
profile's `AGENTS.md`, loaded before the project's, is listed first) shows
when the chat opens fresh and again on `/new`; resuming (`--continue`,
`/resume`) does not show it.

| command | what it does |
|---|---|
| `/model [name]` | switch model (bare form: picker; Ctrl+S also sets the default) |
| `/profile [name]` | switch profile (bare form: picker; Ctrl+S also sets the default profile) |
| `/new` | fresh conversation and clear the screen (model, thinking, approval level and grants kept; reprints the start banner) |
| `/name [title]` | name this session, for `/resume` |
| `/resume` | continue a session here (grants and level reset) |
| `/compact [focus]` | summarize older messages to free context |
| `/session` | session file, tokens used, context use |
| `/thinking` | show or hide thinking |
| `/approve [level]` | `never` / `shell` / `mutating` |
| `/allowed` | list and remove always-grants |
| `/copy` | copy the last reply |
| `/settings [--global\|--in-profile\|--project\|--local] [unset] <key> [value]` | show, set or unset a setting (default local; `--in-profile`: the active profile's `config.json`) |
| `/skill:<name> [text]` | run a skill |
| `/quit` | save the session and quit |

## Config

`~/.jas/config.json` keys: `provider`, `model`, `base_url`, `approve`,
`max_turns`, `max_tool_output`, `redact_skip_paths`, `compact_reserve_tokens`,
`compact_keep_tokens`, `compact_model`, `compact_threshold`, `farm_bell`, and per-model
`models.<name>.context_window`. Project (`<root>/.jas/config.json`) and local
(`config.local.json`) files apply only in a trusted project; endpoint keys
(`base_url`, `search_base_url`, `providers.<name>.base_url`) are global-only
and are never read from them.

Profiles: `--profile <name>` / `JAS_PROFILE` / the `profile` key pick
`~/.jas/profiles/<name>/` (default `default`). Its `config.json` sits between
global and project config (no endpoint keys, no `profile`), and its
`AGENTS.md` goes into the prompt before the project's. An unknown name from
the flag or env is a usage error; from config it warns and uses `default`.
`/resume` stays on the chat's active profile (the notice names the recorded
one when it differs); farm workers run on the active profile's `AGENTS.md`
and skills (workers started after a mid-farm `/profile` get the new profile's,
but the farm's `max_turns`, budget and approval level stay fixed).

## Approval and safety

- `--approve`: `never` (default) prompts nothing; `shell` prompts every shell
  call; `mutating` also prompts `write`/`edit`. A denied call returns
  `status: denied` to the model and the run continues. A gated call with no
  TTY exits 5.
- Deny floor, always on for file tools: `.env*`, `secrets/**`, `*.pem`,
  `id_rsa*`, `*.p12`, `.git-credentials`, `~/.aws/**`, `~/.ssh/**`,
  `~/.jas/**` (reads of the active profile's `skills/` folder are allowed,
  through symlinks too; a `skills/` linked into the jas home is ignored with
  a warning).
- The `shell` tool is not sandboxed: confinement and the deny floor do not
  filter it. Secrets are redacted from tool output and logs. Review untrusted
  work with `--approve=shell`, or run jas under bubblewrap/Docker.

## Exit codes

| code | meaning |
|---|---|
| 0 | model gave a final answer |
| 1 | provider failure after retries, corrupt config/auth, internal error |
| 2 | usage error (bad flags, invalid value, missing model/key) |
| 3 | context full |
| 4 | turn cap (`max_turns`) hit |
| 5 | approval required but no TTY to ask |
| 6 | farm unmet: the supervisor `check` failed, or a part failed/was dropped |
| 7 | budget exceeded (`--max-tokens`/`--timeout`, farm totals), after `budget_stop` |
| 8 | farm planner refused to split (suggests `jas run`) |
| 130 | interrupted (Ctrl-C / SIGTERM) |

## Skills

A skill is a directory holding a `SKILL.md` whose frontmatter has a `name`
and `description`. Scanned, in order: project `<root>/.agents/skills` and
`<root>/.claude/skills` (behind the project trust prompt), then user: the
active profile's `~/.jas/profiles/<name>/skills` and jas's built-in manual under `<UserCacheDir>/jas/manual` (this
manual). `~/.agents/skills`, `~/.claude/skills` and `~/.config/jas/skills`
are no longer read.
Only name and description reach the system prompt; the model reads the full
`SKILL.md` when a task matches. On a name clash the first found wins.
`disable-model-invocation: true` hides a skill from the model (it stays
runnable as `/skill:<name>`).

## Farm (experimental)

Splits one big task into parts that run in parallel git worktrees, then
merges them behind a supervisor-run `check`:

```sh
jas farm plan "<task>"    # draft a split plan; approve it on a TTY
jas farm run plan.yaml    # run the parts in parallel worktrees
jas farm "<task>"         # plan → approve → run, one terminal
jas farm status           # parts, tokens, check verdict (offline)
jas farm clean [--branches]
jas farm vars
```

Roles: `coder` writes its part, `tester` adds tests. A part is done only when
`part_check` (default: `check`; `part_check: ""` turns it off) passes in its
worktree; a failure gets one fix round, in which the worker may answer
`BLOCKED: <reason>` when the check needs other parts (the merged `check`
then decides). Writes outside `owns`, shell ones too, also go back for that
round. Root lockfiles (`generated`, detected by default) are undone in
part worktrees without a fix round and regenerated once on the merged tree
before `check`. A clean merge whose `check` fails is
handed to the integrator. A merged-tree `check` that edits or commits tracked
files fails (changes undone, files listed); untracked build output is fine.
At the failure prompt, answer `retry`, `drop` or
`abort` (or a prefix); at the final prompt, `merge`, `keep` or `discard`
(or a prefix: `d` discards, `don't` is asked again); anything else is asked
again. Merging is refused (branches kept) when the target branch moved
since the check. Part worktrees live in
`.jas/worktrees/<farm-id>/part-<part>` (git-excluded; file tools in the main
checkout are denied there); `worktree.dir` moves them. Without `--json`, progress prints
one line per part event and worker tool call (`[part] → shell go test …`). Budgets cover each part
and the whole farm; exit 7 when exhausted, 6 when a part or the check fails.

In the chat: never run `jas farm` through the `shell` tool (it has no
terminal for the plan picker and approvals). Call `suggest_farm`: it shows the task
and asks the owner `plan this farm? [yes/no/change]`, and never starts a farm; on change the
owner's next message says what to change: revise the task and call `suggest_farm` again; where a farm cannot run
(untrusted project, no `check`) it returns why: stop and ask the owner
instead of doing the task yourself. On uncommitted changes or a repository
with no commit, offer to commit (with the owner's ok), then suggest again. In a new or empty project, offer to set
up a `check` first (`check:` in `.jas/farm.yaml`, committed), then suggest
the farm again. A project
with no `AGENTS.md`, project skills, `.jas/farm.yaml` or project settings is only trusted with
`jas --trust-project`.
`/farm "<task>"` plans a farm (picker: approve, edit, replan, quit); while it runs, live rows under the input show each agent; `/farm`
prints the dashboard; `/farm run <plan.yaml>` starts an approved plan. One
farm per window; needs a trusted project, a `check` and a clean tree.
Ctrl+1..9 (Alt+1..9) shows agent N live; Ctrl+0 (Alt+0) returns to the main
chat, whose output is held meanwhile. `/farm attach <part|N>` / `/farm back`
do the same. In an agent view Enter steers it; Esc/Ctrl+C only clear the
input. `/farm pause|resume|drop <part|N>`, `/farm stop` (twice: cancel now);
quitting with a running farm asks first. Agent approvals open in any view as
`[part] approve …? [y/N/always/farm]`; failures and the final choice wait for
the main chat with no turn running (`farm needs you · ctrl+0`). Config key
`farm_bell` (default true) rings the bell for these. The end summary, and
any outcome you did not see (suggestion dismissed, planning failed or
refused, plan quit, farm started), is attached to the owner's next message; the farm survives `/new` and `/resume`.
`--json` works throughout; `manifest.json` records the run. Experimental.