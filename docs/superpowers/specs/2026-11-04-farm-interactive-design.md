# Farm in the chat — design

Date: 2026-11-04 (Asia/Tokyo). Status: **draft, owner-approved direction.**
Refines the "Interactive mode" section of
[`2026-11-02-farm-design.md`](2026-11-02-farm-design.md) (the farm spec):
where the two disagree, this document wins. Calls marked **(author's call)**
were not made by the owner and may be overturned.

## Intent

The owner wants to talk to the jas agent as usual and, when a task splits,
kick off a farm from that same chat. They then watch and steer each part's
agent in its own worktree **without leaving the window**: one keystroke flips
the view to an agent, another flips back; a dashboard command shows every
agent at a glance. The model is t3-code or Claude Code's multi-agent views,
kept inside jas's inline terminal UI.

Why now: three dogfood runs (`docs/research/farm-dogfood-2026-11.md`) showed
that `jas farm` from a second terminal is opaque and awkward. The owner chose
to build the interactive front end **before** the farm-vs-solo benchmark
gate. This amends the farm spec's Validation rule "no new farm features until
the MVP has been measured": interactive mode is exempt by owner decision
(2026-11-04); the benchmark stays next after it, and the README keeps farm
marked experimental.

## Scope

This update: start a farm from the chat, live rows, one-keystroke flipping
between the main chat and each agent, steering a flipped-to agent, a `/farm`
dashboard, and the in-chat prompts (plan review, approvals, failures, final
choice).

Next update (recorded, not designed here): **farm from GitHub issues** — jas
reads issues (via `gh`) and plans one part per issue or issue group, each in
its own worktree (the farm spec's acceptance scenario 3, driven from issue
numbers). Owner's idea: a skill that teaches jas to read issues and start
that farm.

## Goals

1. From the main chat, a farm starts after the owner approves a plan inline;
   the main agent may *suggest* a farm but never starts one itself.
2. While it runs, one live row per agent sits under the input box, with a
   totals line.
3. **Ctrl+1 … Ctrl+9** flips the window to that agent; **Ctrl+0** flips back
   to the main chat. Each flip clears the screen and redraws that view's
   recent transcript, then keeps streaming it live. (owner's choice of keys)
4. In an agent's view, the input box talks to that agent (steering).
5. `/farm` prints a dashboard of every agent into the chat.
6. Same supervisor, logs, manifest, exit codes and safety rules as
   `jas farm run`; only the front end differs.

## Non-goals

- Seeing two agents side by side (owner: flipping matters more).
- `jas farm attach` from another terminal (still issue #12; not needed now).
- Several farms at once in one chat **(author's call: one at a time)**.
- GitHub issues (next update).
- Changing the farm's planning, budgets, roles or verdict.

## Anti-goals

- **A fullscreen / alternate-screen UI.** jas stays inline (AGENTS.md); the
  flip is a screen clear and redraw, like Ctrl+L today. Discussed and
  rejected for this update: an optional fullscreen mode like pi's default.
- The chat agent starting a farm without the owner approving the plan.
- Any agent seeing another agent's view or the owner's steering of another
  agent.
- Losing an owner message: text typed to an agent that finishes before
  reading it comes back to the input box (farm spec, "No silent loss").

## How it behaves

The text mockups below are the reference.

### Starting

- `/farm "<task>"` in the chat, or the main agent's `suggest_farm` tool
  (registered in every chat and checked at call time, so a `check` added
  mid-chat counts and an unavailable farm returns its reason instead of the
  agent running `jas farm` through `shell` (owner, 2026-11-07; was: registered
  only when a trusted project resolves a `check`); the agent drafts a task; jas pre-fills the input with `/farm "<task>"` for the
  owner to edit and send). `/farm run plan.yaml` runs an approved plan.
- The planner runs; its plan summary prints into the chat with
  `[a]pprove [e]dit [r]eplan [q]uit`, as in the CLI. A refusal prints its
  reason and offers to run the task in the chat instead (farm spec).
- On approval the farm starts in the background of the chat process. The
  main chat stays usable: the owner can keep talking to the main agent
  **(owner confirmed)**.
- `suggest_farm` ships in this update (owner, grilling Q2).

### Lifetime **(owner, grilling Q1 and Q9)**

- The farm lives in the chat process. Quitting or a crash ends it; worktrees
  and branches are kept, and `jas farm resume` stays on the roadmap. The
  quit confirm (below) guards the normal case.
- The farm belongs to the **window**, not the main session: it keeps running
  across `/new` and `/resume`, its live rows stay, and `/model` in the main
  chat does not change the farm's model.
- "The last farm" means the last farm in this jas window (process); it is
  not remembered across restarts.

### When the farm ends **(owner, grilling Q3 and Q6)**

A short summary (parts and statuses, diffstat, verdict, branch, final choice)
prints into the main chat and is **attached to the owner's next message** to
the main session current at that time, so the main agent can answer "review
what the farm did". It never starts a main-agent turn by itself. There is no
live farm-status tool for the main agent; `/farm` covers watching.

### What the farm inherits from the chat **(author's calls)**

- **Model:** the chat's current model for planner, workers and integrator,
  unless `.jas/farm.yaml` names one (`models:`, role `model`).
- **Approval level:** the chat's current level, unless a role sets
  `approve`.
- **Main agent edits during a farm** are allowed (the farm works from `base`
  in its own worktrees). The final **merge** needs a clean main checkout; if
  it is dirty, merge is refused with a one-line reason and keep is offered.
- With no farm running, Ctrl+N is a no-op with a hint, and `/farm` shows the
  last farm of this window (or "no farm") plus usage.

### Live rows (main chat)

Under the input, re-rendered in place, height = agents + 1:

```
1 replay-fix         ● running  → shell go test ./core/...   212k
2 frontmatter-tests  ✓ done     part_check passed            170k
  integrate          · queued
farm 382k / 5M tok · 2m14s · check pending     ctrl+1..9 flip · ctrl+0 back
```

Numbers are the flip keys. Parts are numbered in plan order (1–9); the
integrator, once running, takes the next free number **(author's call)**.
Agents without a number (more than nine; or the integrator when 1–9 are
taken) are reachable through `/farm attach` (the planner's `max_workers`
default is 3, so this is rare). Rows truncate, never wrap. An agent waiting
on an approval shows `⚠ needs approval` in its row. A row appearing
mid-farm (the integrator) grows the block by one line; a terminal too short
for every row collapses the block to the totals line. Finished events (part done or failed, board posts) also print
once into the main transcript, as the progress lines of `jas farm run` do.

### Flipping

- **Ctrl+N** (N = 1–9): clear the screen, print a header
  (`── replay-fix · coder · .jas/worktrees/<farm>/<part> ──`), redraw that
  agent's recent transcript (as much as fits the screen, rendered exactly
  like the main chat: messages, tool calls, diffs), then stream it live.
  The bottom shows a one-line strip of all agents with the current one
  highlighted.
- **Ctrl+0**: back to the main chat, redrawn the same way from its own
  transcript, live rows restored.
- **Alt+1 … Alt+0** do the same **(author's call)**: many terminals send no
  distinct Ctrl+digit unless they support the kitty keyboard protocol or
  `modifyOtherKeys`. `/farm` and the first-farm hint name the keys that work.
- Done, failed and queued agents can be flipped to: done and failed ones
  show their final transcript read-only; queued ones show "waiting for …".
- Terminal scrollback is shared, so scrolling up after flips shows a mix of
  views; each flip's header marks where a view begins. Accepted cost of
  staying inline.
- **Command form:** `/farm attach <part|N>` flips like Ctrl+N and
  `/farm back` like Ctrl+0, for terminals where neither Ctrl+digit nor
  Alt+digit reaches jas. (Replaces the farm spec's attach routing.)
- **Main-chat output is held while the owner is in an agent view** (owner,
  grilling Q4): main-agent replies and farm event lines are kept, the strip
  shows `main chat: N new`, and Ctrl+0 prints them. Approval prompts are
  the exception (below).

### Steering a flipped-to agent

- The input prompt shows the agent's name (`replay-fix ›`). Enter sends the
  text to that agent through `Session.Steer` — these are the owner's words,
  so user role is right (farm spec).
- Steering a done or failed agent is refused with a one-line hint (done
  agents are never re-woken; use retry on failure).
- Text queued for an agent that finishes first is handed back to the input
  box with `[part] not delivered (part finished)` (farm spec).
- A steer is delivered when the agent's current tool call finishes (e.g.
  after a 45 s `go test`); until then the input line shows
  `queued for replay-fix`. A steer queued for an agent that is dropped takes
  the same "not delivered" path back to the input box.
- **Esc and Ctrl+C never stop a farm agent** (owner, grilling Q7; review
  R2). In an agent view, Esc and Ctrl+C clear the input; a second Ctrl+C on
  an empty input starts the quit confirm. They do not cancel the main
  agent's turn either, which the owner cannot see from there. Stopping an
  agent is explicit: `/farm pause <part>` or `/farm drop <part>`.
- Slash commands in an agent view act on the farm/chat, not the agent
  **(author's call)**. `/farm pause|resume|drop|attach` accept a part name
  or its number.

### Dashboard: `/farm`

Prints a snapshot into the chat (it does not update in place):

```
farm 20261104-e414 · fix #48 and #51 · 3m02s · 401k / 5M tok
 #  part               role   status     last activity              tokens  check
 1  replay-fix         coder  ● running  edit core/replay_test.go   231k    —
 2  frontmatter-tests  coder  ✓ done     part_check passed (1s)     170k    pass
    integrate          —      · queued   waits for 1
 board  2 posts · frontmatter-tests: done (3 files, +224 −14)
 ctrl+1..9 flip · /farm pause|resume|drop <part> · /farm stop
```

Subcommands from the farm spec stay: `/farm pause|resume|drop <part>`, plus
`/farm stop` (graceful stop, as the first Ctrl-C of `jas farm run`)
**(author's call: an explicit command, because Ctrl-C in the chat already
cancels the main agent's turn)**.

### Prompts while flipping

- **Approvals** from any agent (main or farm) show immediately wherever the
  owner is, labelled `[frontmatter-tests] shell: go test …? y/n/always`;
  other agents keep running (owner confirmed). Choices: yes · no · always
  for this part · always for this farm (farm spec).
- **An open approval takes the input** (owner, review R1): any draft in the
  input box is set aside, the answer goes only to the approval, and the
  draft comes back afterwards with its label (`replay-fix ›`). Several
  pending approvals queue, oldest first, one prompt at a time.
- **Failures** (retry · drop · abort) and the **final choice** (merge · keep
  · discard, with the farm spec's typed confirm for an unmet farm) appear in
  the main chat, **once the main agent's turn has ended** (owner, grilling
  Q8); the farm keeps running everything that does not depend on the
  answer. In an agent view the strip shows `farm needs you · ctrl+0`.
- **Bell** (owner, R3): when an approval, failure or final-choice prompt
  appears, jas rings the terminal bell once (most terminals turn it into a
  dock bounce, tab badge or notification). Never with `--json`; off with the
  config key `farm_bell` (`jas config set farm_bell false`) **(author's call
  on the key: a per-user preference, so it lives in config, not
  farm.yaml)**.
- Farm workers have no "ask the user" tool; a stuck worker posts `blocked`
  to the board (shown in its row and in `/farm`). Approvals, failures and
  the final choice are the only times the farm waits for the owner.
- Answers use the same matching as the CLI (whole word or prefix; anything
  else is asked again).

### Stopping and leaving

- `/farm stop`: graceful stop (no new parts, running requests finish,
  `farm_ended{status: interrupted}`), then the final choice. A second
  `/farm stop` while draining cancels hard and is logged as
  `farm_ended{status: interrupted, hard: true}` **(author's call)**.
- Quitting the chat (`/quit`, Ctrl-D, double Ctrl-C) while a farm runs asks
  `a farm is running — stop it and quit? y/n`; yes stops it gracefully and
  quits. The farm's worktrees and branches are kept, as on an interrupt.

## Constraints

- **Same supervisor as `jas farm run`.** The chat calls the same `farm`
  package entry points; no second scheduler. The farm's own logs
  (`farm.jsonl`, `board.jsonl`, `manifest.json`, per-part `events.jsonl`)
  are written exactly as in the CLI, so `jas farm status` and the dogfood
  analysis work on chat-started farms.
- **Inline Bubble Tea v2** (`tui/`), no alternate screen. The flip reuses the
  existing clear-and-reprint path (`clearView`, Ctrl+L) and the existing
  transcript renderer, so an agent view looks like the main chat.
- **Keys:** Ctrl+L (clear) and Ctrl+O (expand tool output) keep their
  meaning in every view.
- **Security and safety rules carry over unchanged:** write limits, the
  deny floor and `.jas/worktrees/**` denial for the main chat's own tools,
  redaction of everything printed, trust decided once for the main root.
- Docs follow features (AGENTS.md): README farm section, the built-in manual
  (`cmd/jas/manual/jas/SKILL.md`), and the farm spec's Interactive mode
  section updated in the same change.

## Acceptance **(owner, grilling Q5)**

A live, chat-started farm on a real two-part task (like dogfood run 3:
#48 + #51 style) in one window, using every feature once: start from chat
(including a `suggest_farm` pre-fill), flip to each agent and back, steer
one mid-run, open `/farm`, answer an approval while flipped elsewhere,
answer the final keep prompt, then ask the main agent about the result.
Findings are appended to `docs/research/farm-dogfood-2026-11.md`.

## Decisions

Owner: interactive mode before the benchmark; one window, one keystroke to
flip (not terminal tabs, not side by side); Ctrl+1–9 / Ctrl+0; a `/farm`
dashboard; main chat stays usable during a farm; approvals follow the
owner, failures and the final choice live in the main chat; GitHub issues
next update; stay inline (no fullscreen). From the 2026-11-04 grilling and
review: farm dies with the chat (Q1); `suggest_farm` in (Q2); end summary
attached to the next message, no status tool (Q3, Q6); main-chat output held
while flipped (Q4); acceptance run (Q5); Esc/Ctrl+C never stop a worker
(Q7, R2); farm prompts wait for the main turn (Q8); the farm belongs to the
window (Q9); approvals take the input (R1); bell on by default (R3).

Author's calls (overturnable): model and approval level inherited from
the chat; main-agent edits allowed during a farm, merge needs a clean tree;
Alt+digit fallback; one farm per chat;
integrator numbering; done/failed agents viewable read-only; `/farm stop`
instead of a Ctrl-C meaning; slash commands act on the farm; confirm before
quitting with a running farm.

## Left to the builder

- How an agent's events reach the chat (the supervisor already wraps each
  worker's event log for progress lines; a per-agent buffer or re-reading
  the log on flip both work) and how much history a flip redraws.
- Exact live-row and dashboard columns and colours.
- How `suggest_farm` is registered and its prompt wording.
- Whether the plan review prompt is the chat's picker or a typed answer.
- Test approach (the existing `tui` tests drive the model with fake
  backends; farm tests use fake providers).
