# Event log contract (v1)

**Status:** published 2026-11-12. Track A.1 in [design §5](design.md#5-v2-roadmap).
**Version:** `v: 1`, the version jas writes today. This document describes the
current format; it does not change it.

The session event log is jas's receipt: the audit trail, the input to resume,
the stream `jas run --json` prints, and the future RPC event stream. Farm,
`jas attach`, RPC clients, and `jas dream` read it. This file is the contract
they read against. If the code and this file disagree, it is a bug in one of
them; fix it in the same change.

## Scope

In scope:

- `~/.jas/sessions/<id>/events.jsonl`, one file per session.
- The `jas run --json` stdout stream, which carries the same bytes as the file.

Out of scope (separate formats, not covered by this version):

- The farm's own run events and `board.jsonl`.
- Session-awareness records in `<project>/.jas/sessions/`.
- The RPC command set. Only its event half is this log.
- Screen-only data the TUI receives (for example an `edit`'s full diff).

## File

- Path: `~/.jas/sessions/<id>/events.jsonl`. `<id>` is
  `<UTC YYYYMMDDTHHMMSS>-<6 hex>`, for example `20261112T091500-a1b2c3`.
- Modes: directory `0700`, file `0600`.
- Encoding: UTF-8 JSON Lines. One event per line, each line ends with `\n`.
- One writer: an open session holds an exclusive lock on the file. A second
  jas process that tries to open it fails (`session is open in another jas
  process`). Readers do not need the lock.
- A file is append-only. Events are never rewritten or removed.
- One file can span many opens: `session_started` once, then a
  `session_resumed` for each reopen.

## Event header

Every line is one JSON object. These six fields come first, in this order,
on every event:

| field | type | meaning |
|---|---|---|
| `v` | int | schema version of this event (`1`) |
| `id` | string | unique within the file; today `e` + zero-padded `seq` (`e000042`). Treat it as opaque |
| `parent_id` | string or `null` | `id` of the event written just before this one; `null` only on the file's first event |
| `seq` | int | starts at `1` and strictly increases across reopens; a failed write can leave a gap (below) |
| `ts` | string | write time, RFC 3339 in UTC with up to nine fractional digits, trailing zeros dropped (`2026-11-12T09:15:00.1234Z`, `2026-11-12T09:15:00Z`); parsers must not assume a fixed width |
| `type` | string | one of the types below |

The payload fields follow at the top level of the same object, not nested.
A header field always wins over a payload field with the same name. Readers
must not depend on the order of payload fields.

`parent_id` is a linear chain today. The planned session tree (§5) will
branch it; a reader that follows `parent_id` instead of file order will keep
working.

**Failed writes.** When a write fails, its `seq` is not reused: the failed
write may have landed whole except for its newline, and reusing the number
would then give two events the same `id`. The next event starts on a new line
with the next `seq`, and its `parent_id` is the last event that was written
successfully. So `seq` can skip a number, and `parent_id` stays correct.

**Reopening.** jas refuses to append to a non-empty file that holds no
complete event, rather than start again at `seq` 1 with a `null`
`parent_id` mid-file.

## Compatibility rules

These rules are the contract. Everything else in this file can grow.

**Backward compatible (no version change):**

- Adding an event type.
- Adding an optional field to any event.
- Adding a value to an enum (`status`, `decision`, `reason`, …).

**Breaking (bumps `v`):**

- Removing or renaming an event type or a field.
- Changing a field's JSON type or meaning.
- Making an optional field required, or a required one optional.
- Changing the resume rules below.
- Making file order stop being a valid replay order. Branching through
  `parent_id` (the session tree) is additive as long as file order still
  replays correctly.

**Readers must:**

1. Ignore event types they do not know.
2. Ignore fields they do not know.
3. Treat an enum value they do not know as opaque, not as an error.
4. Check `v` on every line, and never misread an event whose `v` is newer
   than the reader supports. jas's own replay refuses the whole file. A tool
   that tails a file may instead stop at that line and keep what it read
   before it. A file can mix versions: an older session reopened by a newer
   jas gets newer events appended.
5. Skip a line that does not parse as a JSON object with a `type`, and keep
   going. A crash can leave a partial last line, and a failed write can leave
   a torn one mid-file. Report skipped lines; do not fail the whole file.
   Blank lines are not events and are skipped silently.
6. Accept lines up to 16 MiB. A longer line may be skipped as unreadable.
7. Expect a file with no `session_ended`: that means jas crashed or was
   killed.

**Writers must:**

1. Write `v` on every event.
2. Start each event on its own line, even after a torn write.
3. Never log a secret value. Every string in a payload goes through redaction
   before it is written (design §9), and the old content of an overwritten
   file is never logged.

## Shared shapes

**Content block** (`content` in `user_message` and `assistant_message`):

| field | type | when |
|---|---|---|
| `type` | string | always: `text`, `tool_use`, `tool_result` |
| `text` | string | text blocks; result text |
| `tool_use_id` | string | `tool_use`, `tool_result` |
| `name` | string | `tool_use` |
| `input` | object | `tool_use` with valid JSON arguments |
| `input_raw` | string | `tool_use` whose arguments were not valid JSON, kept verbatim instead of `input` |
| `is_error` | bool | `tool_result`, when true |

Empty fields are omitted.

**Context file:** `{path, loaded, sha256}`. **Skill:** `{name, scope, location}`.

**Diff** (optional `diff` on `tool_result`): `{path, hunks, truncated}`.
`hunks` is a list of hunks; each hunk is a list of lines
`{k, o, n, t, s}`: `k` is `" "`, `"-"` or `"+"`; `o` and `n` are 1-based old
and new line numbers (omitted when not set); `t` is the redacted line text;
`s` is a list of `[start, end)` rune ranges of changed words. A `write` over
an existing file logs added lines only. An `edit` logs no diff.

## Event types

"Required" fields are always present. "Optional" fields may be missing.

### Session lifecycle

**`session_started`** — once per file, first event.

- Required: `model`, `profile`, `provider`, `base_url`, `approve`, `root`,
  `context_files` (list of context file), `skills` (list of skill),
  `context_window` (int), `window_known` (bool), `web_tools`.
- Optional: `title` (`jas run --name`), `record_path`, `memory` (below), and
  the farm additions below.
- Logs from before profiles shipped lack `profile`. Readers treat it as
  missing, not as an error.

*Memory* (optional `memory` object, on `session_started` and
`session_resumed` when the session has memory; absent when memory is off):

| field | type | meaning |
|---|---|---|
| `profile_dir` | string | the profile's memory dir |
| `project_dir` | string | the project's memory dir; `""` when project memory is hidden |
| `index_bytes` | int | bytes of both `MEMORY.md` indexes that went into the prompt |
| `truncated` | bool | an index was cut to the cap |

The `MEMORY.md` files that were loaded also appear in `context_files`.
Readers that do not know `memory` ignore it.

*Farm additions* (optional `session_started` fields, only in farm sessions):

| field | on | meaning |
|---|---|---|
| `farm_id` | planner, workers | the farm run |
| `role` | planner | `planner` |
| `part_id` | workers | the part this worker builds |
| `base_sha` | workers | commit the worker's worktree started from |
| `plan_sha256` | workers | hash of the approved plan |
| `project_context` | workers | whether the project's trusted context was loaded |

Farm can add fields here without touching the core list above.

**`session_resumed`** — once per reopen, after replay. Same fields as
`session_started`.

**`session_ended`** — once each time the session closes; missing after a
crash.

- Required: `status`, `exit_code`, `turns`, `prompt_tokens`,
  `completion_tokens`. Totals cover this open only, not earlier ones.
- Optional: `reason` on a non-zero exit.

**`session_titled`** — `title` (one line, at most 80 runes; `""` clears it).
The latest wins over `session_started.title`.

**`model_changed`** — `model` (bare id); optional `provider`, `base_url`.
Never carries a key.

**`profile_changed`** — `from`, `to`, `context_files`, `skills`, `deferred`
(settings keys that wait for `/new`). Chat only.

**`context_reloaded`** — `bytes` (system prompt size), plus `context_files`
and `skills` after `/reload` or a profile switch.

### Turns

**`turn_started`** — no payload. Starts each user message, including a steer
delivered after the final answer.

**`user_message`** — `content` (list of content block); optional
`steer: true` for steering text.

**`assistant_message`** — `text`, `content` (list of content block), `final`
(bool; true on the answer).

**`usage`** — `prompt_tokens`, `completion_tokens`, `cached_tokens`. After
each provider response.

**`turn_ended`** — `status`, `exit_code`, `reason`, `turns` (model requests
in this user message), `prompt_tokens`, `completion_tokens`.

`status` values (both `turn_ended` and `session_ended`): `ok`,
`context_full`, `turn_cap`, `approval_needed`, `interrupted`, `failed`,
`budget_exceeded`. `exit_code` follows the
[manual's exit codes](manual.md#exit-codes).

### Tools

**`tool_started`** — `tool`, `call_id`, `args` (object), `mutating` (bool).
Written before the call runs, after any `approval`. A denied or cancelled call
has a `tool_result` and no `tool_started`.

**`approval`** — `call_id`, `tool`, `decision` (`granted`, `denied`,
`unavailable`). Only when someone was asked, or no one could be. Calls let
through by the approve level or a standing grant log no `approval`.

**`tool_result`** — `call_id`, `tool`, `status`, `output`; optional `diff`.
`status`: `ok`, `error`, `denied`, `cancelled`, `interrupted`, `not_run`. The
last two are written only on resume (below).

### Context management

**`elided`** — `count`, `tokens_before`, `tokens_after`. Old tool results were
replaced with stubs.

**`compaction`** — `summary`, `first_kept_id` (the `id` of the first event
kept after the summary), `tokens_before`, `tokens_after`, `usage`
(`{prompt_tokens, completion_tokens}`). Chat only.

**`injected`** — `text` added to a tool-results message. Farm board
deliveries add `board_seqs`.

### Owner and run controls

**`shell_ran`** — the owner's `!` or `!!` command finished. `command`,
`output`, `exit_code`; optional `error` (could not run), `cancelled`,
`truncated`, `local` (`!!`: never sent to the model). Chat only.

**`budget_stop`** — `reason` (`tokens` or `timeout`). For `tokens`, also
`pool`, `limit`, `used`, `want`.

**`check_ran`** — `cmd`, `dir`, `exit_code`, `duration_ms`, `timed_out`,
`output` (redacted tail).

## Ordering and durability

Within one user message, the order is:

```
turn_started
user_message
( elided?  usage?  assistant_message
  ( approval?  tool_started?  tool_result )*
  injected? )*
turn_ended
```

`budget_stop`, `compaction`, `model_changed`, `session_titled`,
`context_reloaded`, `profile_changed`, `shell_ran` and `check_ran` can appear
between or inside turns.

fsync points:

- `session_started` / `session_resumed`: fsynced.
- A mutating call's `tool_started`: fsynced **before** the call runs.
- After each turn's tool results, and at `turn_ended` and `session_ended`.

Other events are written but may not be on disk yet when jas crashes. A
reader tailing a live file can see them before they are fsynced.

## Resume rules

These are part of the contract. Replay and any other consumer that rebuilds a
conversation must follow them.

1. A `tool_started` with no matching `tool_result` is **never** run again. It
   is answered with `interrupted` and the text "interrupted: jas stopped while
   this call was running; it may have partly run. Inspect the current state
   before retrying."
2. A `tool_use` block with no `tool_started` never ran. It is answered with
   `not_run` and the text "not run: jas stopped before this call started; it
   had no effect."
3. On reopen, jas appends those `tool_result` events right after
   `session_resumed`, so a later replay finds real results and does not make
   them up again.
4. Model, provider, and base URL come from the latest `session_started`,
   `session_resumed`, or `model_changed` that carries each one. Profile comes
   from the latest `session_started`/`session_resumed` `profile` or
   `profile_changed` `to`. Title comes from the latest `session_titled`, else
   `session_started.title`.
5. A `compaction` replaces every message before `first_kept_id` with its
   `summary`.
6. `!` (not `!!`) `shell_ran` events after the last `user_message` are
   pending: the next message carries them to the model.
7. Two user messages in a row with no model answer between them merge into
   one.

This is at-most-once for tool calls, not atomicity. A crash during a shell
command can leave it half done; the result text says so.

## Keeping this file honest

- `core/eventlog_contract_test.go` fails when a `core.Ev*` constant has no
  `**`type`**` entry under "Event types", or an entry names a type that no
  longer exists.
- `core/testdata/eventlog/v1.jsonl` is a v1 log holding every event type.
  The same test file checks its header chain and that `Replay` reads it with
  no notices and the expected state. Change the fixture only for an additive
  change; anything else is a version bump.
- [design §3](design.md#event-log--resume) points here for field lists. The
  [manual](manual.md#event-log) keeps its short user-facing table.
