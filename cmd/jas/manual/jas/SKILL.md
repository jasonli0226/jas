---
name: jas
description: Manual for jas, the coding-agent harness this session runs in. Use when the user asks about jas itself — CLI subcommands and flags, chat commands and keys, config keys, auth, approval levels, the deny floor, exit codes, skills, or the farm workflow — or when a task needs jas configured (jas config set, jas auth add, jas farm plan/run).
---

# jas manual

jas is a tiny, fast coding-agent harness in Go: an inline chat (replies
stream into the terminal's own scrollback, no full-screen mode) plus a
CI-friendly one-shot runner. This file is the stable surface; a jas checkout
has the full docs in `README.md` and `docs/`.

## CLI

| command | what it does |
|---|---|
| `jas` | inline chat in the project root |
| `jas run "<task>"` | one-shot agent run (the CI path) |
| `jas --continue`, `jas --name <title>` | reopen, name a saved session |
| `jas sessions` | list this repo's sessions, across worktrees |
| `jas config get/set/unset/list`, `jas config init` | settings in `~/.jas/config.json` |
| `jas auth add <provider>` | store a key in `~/.jas/auth.json` (0600) |
| `jas farm …` | experimental parallel worktree runner (below) |

Shared flags: `--model <ref>` (`provider/model`), `--provider`, `--base-url`,
`--approve <level>`, `--json`, `--trust-project`. `JAS_*` env vars override
stored config; `JAS_HOME` moves `~/.jas`.

## Chat

Keys: Enter sends (typing while a reply runs steers it), Esc stops the run,
Ctrl-L clears the screen (keeps the session), Ctrl-C clears the input (twice
on an empty box quits), Alt/Shift+Enter is a newline, Up/Down recalls input,
`/` opens the command menu (Tab completes).
While a reply runs, a dim spinner line above the input shows the run in
progress (`working…`, or `thinking…` while the model reasons).
A start banner (version, key hints, loaded context files and skills; a
context file the trust decision declined is marked `not loaded`) shows
when the chat opens fresh and again on `/new`; resuming (`--continue`,
`/resume`) does not show it.

| command | what it does |
|---|---|
| `/model [name]` | switch model (bare form: picker; Ctrl+S also sets the default) |
| `/new` | fresh conversation and clear the screen (model, thinking, approval level and grants kept; reprints the start banner) |
| `/name [title]` | name this session, for `/resume` |
| `/resume` | continue a session here (grants and level reset) |
| `/compact [focus]` | summarize older messages to free context |
| `/session` | session file, tokens used, context use |
| `/thinking` | show or hide thinking |
| `/approve [level]` | `never` / `shell` / `mutating` |
| `/allowed` | list and remove always-grants |
| `/copy` | copy the last reply |
| `/skill:<name> [text]` | run a skill |
| `/quit` | save the session and quit |

## Config

`~/.jas/config.json` keys: `provider`, `model`, `base_url`, `approve`,
`max_turns`, `max_tool_output`, `redact_skip_paths`, `compact_reserve_tokens`,
`compact_keep_tokens`, `compact_model`, `compact_threshold`, and per-model
`models.<name>.context_window`.

## Approval and safety

- `--approve`: `never` (default) prompts nothing; `shell` prompts every shell
  call; `mutating` also prompts `write`/`edit`. A denied call returns
  `status: denied` to the model and the run continues. A gated call with no
  TTY exits 5.
- Deny floor, always on for file tools: `.env*`, `secrets/**`, `*.pem`,
  `id_rsa*`, `*.p12`, `.git-credentials`, `~/.aws/**`, `~/.ssh/**`,
  `~/.jas/**`.
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
`<root>/.claude/skills` (behind the project trust prompt), then user
`~/.agents/skills`, `~/.claude/skills`, and jas's own
`~/.config/jas/skills` (Windows/macOS: `%AppData%\jas`,
`~/Library/Application Support/jas`) — that last one holds this manual.
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
worktree; a failure gets one fix round. A clean merge whose `check` fails is
handed to the integrator. At the failure prompt, answer `retry`, `drop` or
`abort` (or a prefix); anything else is asked again. Part worktrees live in
`.jas/worktrees/<farm-id>/<part>` (git-excluded; file tools in the main
checkout are denied there); `worktree.dir` moves them. Budgets cover each part
and the whole farm; exit 7 when exhausted, 6 when a part or the check fails.
`--json` works throughout; `manifest.json` records the run. Experimental
until the farm-vs-`jas run` benchmark lands.