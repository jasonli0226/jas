# Skill from a conversation — design

**Status:** draft, under discussion (2026-11-18). Not planned yet.
**Builds on:** the built-in `skill-creator` skill (#185), hidden skills
(`disable-model-invocation`, readable since #205), the profile read lifts in
`core/policy.go`, and the v1 event-log contract ([event-log.md](../../event-log.md)).
**Related, not in scope:** `jas learn` (research → skill, design §12.2),
hooks (design: "later").

## Intent

The owner often works through a standard operating procedure (SOP) once in a
chat (reviewing a project, updating its command docs) and then wants to run
the same procedure again later with one command. The way to keep it is a
skill. Two things get in the way today:

1. Turning the chat into a skill relies on what is still in the model's
   context. After compaction, the early steps survive only as a summary, so
   the exact commands, their order, and which attempts failed are lost.
2. A skill written from one project tends to copy that project's details
   (its test command, its doc paths, its module names). The owner wants the
   procedure, not a record of project A, so the same skill can run on
   project B.

Most of these SOPs should not cost context in every session, and the owner
starts them on purpose, so they belong in **hidden** skills
(`disable-model-invocation: true`), run with `/skill:<name>`.

## Which mechanism for which case

Recorded here because the discussion settled it, and the skill-creator text
should steer by it.

| Mechanism | Who starts it | What runs | Use it for |
|---|---|---|---|
| Hook (not in jas yet) | the harness, on an event | a fixed script | "every time X, do Y", deterministic |
| Hidden skill | the owner, `/skill:<name>` | the model following steps | an SOP the owner triggers that needs judgement |
| Visible skill | the model, when a task matches | the model following steps | know-how the model should pick up unprompted |
| `AGENTS.md` | always loaded | rules | short rules for every task |
| Memory note | always indexed | facts | things learned |
| Script / make target | anyone | code | fully mechanical steps; a skill may call it |

An SOP made from a chat is usually a hidden skill. Mechanical steps in it can
become a script under the skill's `scripts/`.

## Goals

1. In a chat, "turn what we just did into a skill" produces a skill that
   describes the general procedure, works on another project, and is hidden
   by default.
2. The source can be the whole session, not just what survived compaction:
   the model may read the current session's own event log.
3. An older task works the same way: the owner `/resume`s that session and
   asks there. No session picker, no reading of other sessions.
4. The generated skill works out project-specific values on the project it
   runs in and tells the owner what it found before acting.
5. The owner sees, before anything is written, what was generalized and
   what was dropped.

## Non-goals

- Reading other sessions' logs, or a picker over past sessions.
- A new chat command (`/learn`) or a separate built-in skill; "make this a
  skill" already reaches `skill-creator`.
- Hooks.
- `jas skill hide|show` or a `skills.hidden` setting (agreed as wanted,
  designed separately).

## Anti-goals

- A skill that silently carries project A's paths, names or commands into
  project B.
- A lift that widens access to the jas home beyond one file: `auth.json`,
  config and other sessions stay denied.
- A skill that acts on a guessed project value without saying what it
  guessed.

## Approaches considered

- **A. Extend `skill-creator`** with a from-a-conversation path. Text only.
  Works from what is in context, so compaction loses detail.
- **B. A new hidden built-in skill** for the same job. Two overlapping
  skills, and a name to remember.
- **C. A `/learn` command** that feeds the full log to the model, plus a
  deterministic check for project-A paths. Most robust, most code.
- **Chosen: A plus a read lift for the current session's log.** It recovers
  what compaction drops (C's main benefit) with a small policy change,
  keeps one entry point, and needs no new command.

## Design

### 1. `skill-creator`: the from-a-conversation path

A new section in `cmd/jas/manual/skill-creator/SKILL.md` (and, if long, a
`references/from-conversation.md`). When the user asks to turn the
conversation (or "what we just did") into a skill, the model:

1. **Recovers the steps.** It uses the conversation in context. When the
   context was compacted, or the steps are unclear, it reads the current
   session's log (section 2), at the path jas gives it (how it learns the
   path is open question 2). It greps the log for `tool_use` /
   `tool_result` events instead of reading it whole, since the log can be
   large.
2. **Sorts what happened** into three groups:
   - general steps: kept, written as instructions;
   - project-specific facts (paths, commands, module names, URLs): removed,
     and replaced with a step that finds the equivalent ("find the project's
     test command, for example a Makefile target or the package scripts");
   - dead ends and fixes for one-off mistakes: dropped, unless the lesson
     generalizes ("check X before Y"), which becomes a step.
3. **Adds a discovery step at the start** of the generated skill: find the
   project-specific values, list them to the user, then go on. The user can
   correct them in one reply. (Goal 4: work it out, then tell before
   acting.)
4. **Defaults:** the active profile's `skills/` (the SOP is meant for any
   project) and `disable-model-invocation: true`. The user can ask for a
   project skill or a visible one instead.
5. **Checks for leftovers before showing anything:** the draft names no
   path, command or identifier that exists only in the source project. The
   model checks the draft against the facts list from step 2 and fixes what
   it finds, so a leak never reaches the owner's approval.
6. **Shows the draft** with three short lists next to it: what it
   generalized (fact → discovery step), what it dropped, with why, and
   anything from the step 5 check it was unsure about. Writing then goes
   through the existing approval for profile skills.
7. **Tests it** as today (`references/testing.md`).

The rest of `skill-creator`'s workflow (one round of questions only when the
conversation lacks examples, check for an existing skill, name rules) stays
as it is.

### 2. Read lift: the current session's own log

- Read-only tools (`read`, `ls`, `grep`) may open **one file**: the
  running session's `~/.jas/sessions/<id>/events.jsonl`. It works like the
  `skills/` and `memory/` lifts in that it passes the jas home floor, but it
  is **read-only**: `write` and `edit` stay refused, so the log cannot be
  forged.
- Everything else under `~/.jas` stays denied: other sessions, `auth.json`,
  config, other profiles.
- The path is in the system prompt (or a skill can be told where to find
  it), written as `~/.jas/sessions/<id>/events.jsonl` like other home paths.
- **Redaction:** the log is already redacted on write (event-log contract,
  "never log a secret value"), so the lift exposes no secret the model was
  not already shown in redacted form. Tool output read back through the
  lift goes through the normal output redaction too.
- **Resume:** `/resume` reopens and appends to the same file, so after
  resuming an old session, its whole history is readable (goal 3).
- Updated on `/new`, `/resume` and a profile switch, the same way the
  read roots are swapped today (`Session.SetReadRoots` and friends).
- Applies in chat. *(My call:)* `jas run` and farm workers get it too: it is
  their own log, read-only, and redacted. If that seems wider than needed,
  limiting it to chat is a one-line change.

### 3. Docs

`docs/manual.md` (and the copy in the manual skill): the deny-floor section
names the new read exception; the skills section describes turning a
conversation into a skill. `README.md` if the overview mentions the deny
floor exceptions. The built-in `jas` manual skill in the same commit
(AGENTS.md rule).

## Constraints

- Approval ladder, deny floor and redaction stay as they are, apart from
  the one read-only file.
- No change to `jas run`, `--json` or exit codes.
- Session logging rule (AGENTS.md): nothing new logs from `Update`.
- `CGO_ENABLED=0`; `go test -race ./...` green.

## Tests (for the builder)

- Policy: `ResolveRead` allows the current session's `events.jsonl`;
  refuses another session's log, `~/.jas/auth.json` and the sessions folder
  listing beyond that file; `Resolve` (write/edit) refuses the log.
- Lift moves with `/new`, `/resume` and a profile switch (old log refused,
  new one allowed).
- End to end with the fake provider: the scripted user message carries a
  fake secret, a later scripted `read` of the session's own log goes
  through the lift, and the tool result the provider receives holds the
  redaction marker, not the secret. Temp `HOME`/`JAS_HOME`, as the other
  `runJas` tests use, so no real data is read.
- Built-in skills still validate (`TestBuiltinSkills_Valid`); manual tests
  pass.

## Open, to discuss next

1. Whether the log lift also applies to `jas run` and farm workers, or chat
   only (current call: all, see section 2).
2. Whether the log path goes in every system prompt (costs a line always)
   or only appears when `skill-creator` asks for it (needs a way to learn
   the session id, for example a `session_info` field or a tiny tool).
3. Whether a deterministic leak check (scan the draft for paths that exist
   in the source project) is worth adding later, instead of relying on the
   model's own check.
4. `jas skill hide|show` / `skills.hidden`: its own short design.

## Left to the builder

- Exact wording of the skill-creator section and whether it needs a
  `references/` file.
- How the lift is represented in `ExecPolicy` (a field next to
  `ProfileSkills` / `ProfileMemory`, read-only variant of a lift).
