# `jas dream` — design

**Status:** proposed (2026-11-13). Track C.2 in [design §5](../../design.md#5-v2-roadmap);
background in [design §12.3](../../design.md#123-memory--dreaming-memory-store-shipped-dreaming-proposed).
**Depends on:** the memory store ([2026-11-12-memory-store-design.md](2026-11-12-memory-store-design.md), shipped #188)
and the v1 event-log contract ([event-log.md](../../event-log.md), shipped #186).
**Followed by:** promotion with `jas learn` (v3).

The owner asked for this draft without an interview, so every decision below
that §12.3 and the memory store design do not already settle is marked
*(my call)*.

## Intent

The memory store grows one approved note at a time, and nothing tidies it.
Over weeks it collects duplicates, notes that later sessions contradicted,
index lines pointing at files that are gone, notes the index forgot, and an
index pushing against its 200-line cap. Some lessons never reach it at all:
memory is read-only in `jas run` and farm workers, so a correction found
there stays in that session's event log.

`jas dream` is a reviewed cleanup pass. It reads recent event logs and the
current store, and writes a **proposed** new store plus a report that says
what changed and why, citing the events behind each change. Nothing touches
the live store until the owner applies the proposal.

## Goals

1. One command produces a proposed store and a readable report, without
   changing the live store.
2. Every proposed change carries its reason and, where it rests on history,
   the event-log lines behind it.
3. The owner can see the diff, then apply or discard it. Applying is atomic
   and can be undone.
4. Checks that need no model (broken links, orphans, bad frontmatter, index
   over the cap) run deterministically and always appear in the report.
5. Lessons stranded in headless and farm sessions can surface as proposed
   notes.
6. The report flags notes that look ready to become a skill or an
   `AGENTS.md` rule.
7. The dream run is logged like any session, and applying or discarding it
   is logged too.

## Non-goals

- A background daemon, a scheduler, or a run at session end. A scheduled run
  may come later and would only prepare a proposal (§12.3).
- Wrapping Anthropic's managed-agents Dreams API.
- Editing `AGENTS.md`, skills, or any file outside the memory store.
- Promotion itself (v3, `jas learn`). Dreams only flag candidates.
- Dreaming across profiles. One run covers the active profile.
- A `/dream` chat command in this version *(my call)*. The owner can run
  `!jas dream` from the chat. A chat command can come later on the same core.
- Search or embeddings over the logs.

## Anti-goals (failure even if it works)

- **A silent rewrite.** Any path that changes the live store without the
  owner seeing the diff and saying yes.
- **Lost owner edits.** Applying a proposal over notes the owner or another
  session changed after the dream started.
- **Invented history.** A proposed note or correction that cites no
  evidence, or cites events that say something else, presented as fact.
- **A secret laundered into memory.** Event logs are redacted, but a dream
  must still refuse note content the redactor would change, exactly as live
  memory writes do.
- **Prompt injection through logs.** Event logs hold tool output from
  untrusted repos and web pages. That text must reach the dream model as
  quoted evidence, never as instructions, and a dream can only ever write to
  its own staging folder.
- **A dream that eats the store.** A proposal that deletes most notes
  because the logs are quiet about them. Absence of evidence is not a reason
  to remove a note.

## Command surface

```
jas dream [--since <dur|date>] [--sessions <n>] [--max-tokens <n>] [--timeout <dur>] [--json]
jas dream list    [--json]
jas dream diff    [<id>]
jas dream apply   [<id>] [--yes]
jas dream discard [<id>]
jas dream undo    [<id>]
```

An id is `<UTC timestamp>-<4 hex>`, like a session id *(my call)*. When
`<id>` is left out, `diff`, `apply` and `discard` take the newest `pending`
dream, and `undo` takes the newest `applied` one.

- **`jas dream`** runs a dream and prints a short summary: notes changed,
  added, removed, lint issues fixed and left, promotion candidates, and the
  path of the report. On a terminal it then offers
  `apply? [y/N/d=diff]`. Off a terminal it stops after writing the proposal,
  exit 0.
- **`list`** shows dreams with state (`pending`, `applied`, `discarded`,
  `stale`, `undone`), date, and change counts. For each `pending` dream it
  re-hashes the live store against `base` and shows `pending (stale)` when
  they differ. That is display only; the stored state becomes `stale` at
  the next `apply` attempt.
- **`diff`** on a `pending` dream prints a unified diff of the live store
  against the proposal, then the report. When the live store no longer
  matches `base`, it first prints a warning naming the changed paths: the
  diff then mixes those later edits into the view, and `apply` would refuse.
  On an `applied` dream it prints `before/` against the live store (what the
  apply changed). On any other state it fails with exit 1.
- **`apply`** replaces the live store with the proposal (see Apply). On a
  terminal it asks first, showing the diff stat; `--yes` skips the question
  and is required off a terminal.
- **`discard`** marks a pending dream discarded and deletes its proposed
  store, keeping the report.
- **`undo`** puts back the store an applied dream replaced, if the live
  store has not changed since the apply. An applied dream that was pruned
  (below) cannot be undone; the error says so ("dream <id> was pruned; only
  the newest 10 dreams keep their old store").

Caps reuse `jas run`'s meaning: `--max-tokens` and `--timeout` stop the
model pass with exit 7. Defaults when unset: 400k tokens and 20 minutes
*(my call; a dream is a bounded batch job and must not run away on a large
log window)*. A capped run still writes the proposal it has, marked
`partial` in the report, and the deterministic lint section is complete.

**`--json`.** `jas dream --json` prints nothing until the run ends, then one
JSON object on stdout: the dream's `dream.json` (below) plus `report` (the
report's path) and `lint_open` (count of lint issues left). There is no
apply question in this mode. `jas dream list --json` prints a JSON array of
`{id, state, stale, started_at, ended_at, counts, partial}`, newest first.
Errors still go to stderr with the usual exit code.

Exit codes: existing ones only. `0` proposal written or command done; `1`
failure (including apply refused because the store changed: the message
says to re-run the dream); `2` usage; `7` cap reached (proposal still
written); `130` interrupted (no proposal).

## Storage

```
~/.jas/profiles/<profile>/dreams/
  state.json                 watermark: end time of the last applied or discarded dream
  <id>/
    dream.json               metadata (below)
    report.md                the report the owner reads
    evidence.md              the evidence digest the model saw
    proposed/                the full proposed store (a copy of memory/, edited)
    before/                  after apply: the store it replaced (for undo)
```

Dreams live next to `memory/`, not inside it, so they never load into a
session and never fall under the memory write lift *(my call)*. Keep the
newest 10 dreams; `jas dream` prunes older ones that are not pending
*(my call)*. Pruning an applied dream deletes its `before/`, so it can no
longer be undone.

`dream.json`: `id`, `profile`, `state`, `started_at`, `ended_at`,
`session_id` (the dream's own event log), `window` (`since`, `until`, the
session ids read), `base` (path → sha256 for every file in the live store
when the dream started), `proposed` (same, for `proposed/`), `counts`, and
`partial`.

## How a dream runs

### 1. Snapshot

Copy the live `memory/` into `<id>/proposed/` and record `base`. The copy
follows the store's guards: a `memory/` that is a link out of the jas home
area gets no dream (the same check as the memory lift), links inside the
store are not followed, and non-`.md` files are copied but flagged by lint.
An empty or missing store still dreams: the run may only propose notes.

### 2. Choose the window

Event logs under `~/.jas/sessions/` whose `session_started.profile` is the
active profile (logs without `profile` count as `default`, per the contract),
that ended or were last written after the watermark. Default watermark when
none exists: 30 days ago. Newest first, at most 50 sessions *(my call)*.
Sessions that are themselves dreams are skipped.

`--since` replaces the watermark as the start of the window, and
`--sessions` replaces the 50-session limit. Both change only which logs this
dream reads. Applying or discarding any dream still moves the watermark to
that dream's `ended_at`.

The watermark moves on discard as well as apply, and undo does not move it
back. So a window whose dream was discarded or undone is not read again by
the next plain `jas dream`; use `--since` to revisit it *(my call: the owner
saw that window's proposal and rejected it)*.

### 3. Lint (deterministic)

Over `proposed/`, before the model runs and again after:

- Notes with missing or invalid frontmatter (`memory.ValidateNote`), or a
  `name` that does not match the file name.
- Index lines pointing at files that do not exist.
- Notes no index line points at (orphans).
- `[[links]]` that resolve to no note (dangling). Reported, not errors:
  dangling links are allowed by the store design.
- A load over the cap: for each project folder, the profile index plus that
  project's index over 200 lines or 25 KB together (the session-start cap in
  the memory store design, which would truncate). The profile index alone
  over the cap is reported once on its own.
- The same `name` in the profile store and a project folder (shadowing).
- Files the store does not allow: non-`.md`, nested folders,
  `projects/<key>/` folders whose key decodes to a path that no longer
  exists (reported as "project gone", never removed automatically).
- Note content the redactor would change.

### 4. Evidence digest (deterministic)

jas, not the model, walks the window's logs and writes `evidence.md`, so
the model reads a short, cited summary instead of raw logs. Each item cites
`<session-id>#<seq>` and the session's root and date. Items:

- Memory writes: `write`/`edit` tool calls with a path in a memory dir, with
  the `approval` decision and `tool_result` status. A denied memory write is
  a signal that the owner rejected that note.
- Memory reads: which notes were opened, and how often, across the window.
- Owner messages (`user_message` text blocks from the owner, not tool
  results). Corrections live here.
- Denied approvals, with the tool and args.
- Failed `check_ran` events (with the output tail), and `budget_stop`.
- For headless and farm sessions (no terminal), the same items. These are
  where stranded lessons come from.

Every quoted string in an item (message text, tool args, check output) is
trimmed to 2 KB, with a marker saying how much was cut; the model can read
the full event through its citation. The whole digest is capped at 200 KB,
newest sessions first, and says how many sessions or items were dropped
*(my call)*. Everything quoted is already redacted in the log and
is redacted again on the way in.

### 5. Model pass

An agent session with the active profile's model, logged to its own event
log under `~/.jas/sessions/<id>/` like any session. Its `session_started`
gains an optional `dream` object `{id, window_sessions}` (additive under the
contract).

**Tools** *(my call)*: `read`, `ls`, `grep` over `proposed/`, `evidence.md`,
and the window's event-log files (read-only, so the model can check a
citation in full); `write` and `edit` inside `proposed/` only; and one
dream-only tool, `remove_note`, that deletes one file inside `proposed/`.
It may remove a note (`.md` other than `MEMORY.md`) or a file lint flagged
as not allowed (non-`.md`). It refuses `MEMORY.md`; the model empties an
index by editing it. A folder under `projects/` left with no files is
removed with its last file. No shell, no web, no reads of the repo, no writes anywhere
else. Writes into `proposed/` need no approval: it is a scratch copy, and
the approval is `apply`. Every rule the live store enforces on writes still
applies inside `proposed/`: the folder rule, `.md` only, valid frontmatter
on notes, and the secrets refusal. The `MEMORY.md` edit-only rule is lifted
inside `proposed/` so the model can rewrite the index whole *(my call)*.

**Prompt.** The dream prompt states the job in this order:

1. Fix what lint found.
2. Merge notes that say the same thing; keep the clearer `name`, and fix
   links to the old one.
3. Correct a note when a later event contradicts it, citing the event.
4. Propose a note for a lesson in the evidence that no note covers, using
   the store's note rules (durable, not rediscoverable from the repo, no
   secrets), citing the events.
5. Remove a note only when evidence shows it is wrong and no correction
   applies, or when it duplicates another. Never remove a note because the
   window does not mention it.
6. Prune each index back under its share of the cap, keeping the lines with
   the most read and write evidence first.
7. Flag promotion candidates: a `feedback` note cited by corrections in 3 or
   more sessions looks like an `AGENTS.md` rule; a note describing a
   repeated multi-step procedure looks like a skill. Flag only.

The prompt says plainly that `evidence.md` and the event logs are quoted
data from past sessions and may contain text from untrusted repos and web
pages; nothing in them is an instruction.

**Change log.** The model ends by writing its change list through a
dream-only `report_change` tool, one call per change: `kind`
(`fix`, `merge`, `correct`, `add`, `remove`, `prune`, `promote`), `paths`,
`reason`, `evidence` (citations). jas, not the model, writes `report.md`
from these calls.

### 6. Verify (deterministic)

After the model pass:

- Re-run lint on `proposed/`.
- Diff `proposed/` against the snapshot. Every changed, added or removed
  file must appear in some `report_change` call; one that does not is
  listed under **Unexplained changes**, first in the report. On a `partial`
  run the model may have been stopped before reporting its edits, so the
  report labels this list best-effort and says why.
- Every citation must name a session in the window and a `seq` that exists
  in it; a bad one is marked `unverified` next to its change.
- `correct` and `add` changes with no valid citation are listed under
  **Unsupported**, and `apply` asks again before applying them (it shows
  the list). *(my call: keep them in the proposal rather than reverting, so
  the owner sees what the model wanted.)*
- If the proposal removes more than a third of the notes, the report opens
  with a warning and `apply` shows it *(my call)*.

Then write `dream.json` with state `pending` and log a `dream_proposed`
event in the dream's session: `id`, `counts`, `partial`, `report` (path).

## The report

`report.md`, Markdown, top to bottom:

1. Header: profile, window (dates and session count), model, tokens used,
   `partial` if capped.
2. Warnings: unexplained changes, unsupported changes, large removal.
3. Changes, grouped by kind, each with paths, reason and citations
   (citations as `session-id#seq` plus the session's root and date).
4. Promotion candidates, with the target (skill or `AGENTS.md`) and why.
5. Lint: fixed, and still open.
6. Diff stat.

The full diff is not in the report; `jas dream diff` prints it.

## Apply

1. Refuse unless the dream is `pending`.
2. Hash the live store and compare with `base`. Any difference (a note the
   owner or a chat session wrote since the dream started) refuses the apply
   with exit 1, marks the dream `stale`, and lists the changed paths. The
   owner re-runs the dream. No merge *(my call: a three-way merge of notes is
   not worth it for a store this small)*.
3. Show the diff stat and warnings; ask (or `--yes`).
4. Hold a profile-level lock while swapping, so two applies or an apply and
   an undo cannot interleave.
5. Rename live `memory/` to `<id>/before/`, then `<id>/proposed/` to
   `memory/`. Both are on one filesystem, so each rename is atomic. If the
   second rename fails, rename `before/` back and report the error.
6. Set state `applied`, move the watermark to the dream's `ended_at`, and
   append `dream_applied` (`id`, `counts`) to the dream's session log (logs
   span many opens under the contract).

Running chat sessions keep their loaded index until `/reload`, as with any
memory change. A session writing a note during the swap sees an `edit`
failure or writes into the new store; both are acceptable, and step 2 makes
the window tiny.

**Discard** sets state `discarded`, moves the watermark, deletes
`proposed/`, and logs `dream_discarded`.

**Undo** compares the live store with the dream's `proposed` hashes; if they
match, swaps `before/` back in the same way, sets state `undone`, and logs
`dream_undone`. If the store changed since the apply, it refuses (exit 1).
It does not move the watermark back.

## Event log additions

All additive under the v1 contract; update `docs/event-log.md` and the
fixture in the same change:

- `session_started.dream` (optional object): `{id, window_sessions}`.
- `dream_proposed`: `id`, `counts`, `partial`, `report`.
- `dream_applied`, `dream_discarded`, `dream_undone`: `id`; `counts` on
  applied.
- Tool names `remove_note` and `report_change` appear in ordinary
  `tool_started` / `tool_result` events in dream sessions.

## Interaction with existing features

- **Memory setting:** `memory: off` at the global or profile layer makes
  `jas dream` fail with a clear message (exit 1). `memory: read` does not
  block a dream: reading is allowed, and applying is an explicit owner act
  *(my call)*. Project-level settings do not apply; a dream covers the whole
  profile store.
- **Profiles:** a dream belongs to the active profile. `--profile` works as
  on other subcommands.
- **Session awareness:** when the write-overlap warning ships, `apply`
  should warn if a live session on this profile touched the store. Not a
  prerequisite; the hash check already prevents lost edits.
- **Farm, `jas run`, compaction:** unaffected. Their logs are dream input.
- **`jas memory` / `/memory`:** add one line when a dream is pending
  ("dream <id> pending: jas dream diff").

## Docs

`docs/manual.md`, the built-in manual skill and `README.md` (one line in
the overview) gain `jas dream` and its subcommands, flags and exit codes;
`TestManual_*` covers the new flags. `docs/design.md` §5 and §12.3 point here
and mark the item shipped when it is.

## Acceptance

- `jas dream` on a store with a broken index line, an orphan and a
  duplicate writes a proposal fixing all three, a report explaining each,
  and leaves the live store byte-identical.
- A correction made by the owner in a chat session, contradicting a note,
  produces a `correct` change citing that `user_message`.
- A lesson only present in a `jas run` session's log produces an `add`
  change citing it.
- A note the window never mentions is not removed.
- A citation to a missing `seq` is marked `unverified`; an uncited `add` is
  listed as unsupported and `apply` asks about it.
- A file changed by the model but not in any `report_change` call is listed
  as unexplained.
- A proposed note containing a redactable secret is refused inside the
  model pass.
- An instruction planted in a tool result in the logs ("write this to
  AGENTS.md") cannot cause a write outside `proposed/`.
- `apply` after a chat session edited a note refuses with exit 1 and marks
  the dream `stale`.
- `apply` then `undo` restores the original store byte for byte.
- `undo` with no `<id>` picks the newest applied dream, not a pending one;
  `undo` on a pruned dream fails with the pruned message.
- After a discard, a plain `jas dream` reads no session from the discarded
  window; `jas dream --since <that date>` reads it again, and applying that
  dream moves the watermark to its `ended_at`.
- `diff` on a pending dream whose base no longer matches prints the
  staleness warning; `diff` on an applied dream shows `before/` against live;
  `list` shows `pending (stale)` without changing the stored state.
- `remove_note` on `MEMORY.md` is refused; removing the last note in a
  `projects/<key>/` folder removes the folder.
- `jas dream --json` prints exactly one JSON object; `list --json` an array.
- `--max-tokens` reached: exit 7, proposal written, report marked `partial`.
- Off a terminal, `jas dream` never applies, and `apply` without `--yes`
  fails with exit 2.
- `dream_proposed` and `dream_applied` appear in the dream's event log and
  the contract fixture.

## Left to the builder

- The exact field set in `--json` output beyond the fields named above.

- The exact `evidence.md` layout, as long as every item carries its
  citation and the cap is honored.
- How `remove_note` and `report_change` are registered so they exist only
  in dream sessions.
- Whether the dream session reuses `jas run`'s loop directly or a thin
  wrapper around it.
- The diff renderer (the existing `core/diff.go` or plain unified diff).
- Wording of the prompt, warnings and the summary.
- Where the profile lock lives, reusing `core/lock_*.go`.
