# Read dedup: skip re-sending a file still in context (#217)

Status: draft, awaiting owner approval. Short design. Revised after a review (2026-11-22).

## Intent

Agents re-read files that are still in their context. Each repeat adds the
whole file to every later prompt, and a jas run's token cap counts the
prompt again on every turn, so repeats push reviews into `--max-tokens`.
The reviewer checklist already forbids re-reading and reviewers ignore it.
The `read` tool should enforce it instead.

## Goals

- A `read` call whose output is byte-identical to an earlier `read` result
  with the same arguments that the model can still see returns a short note
  instead of the content.
- The note is sent only when the earlier output is intact in the session's
  current messages. Elision can still stub that earlier result after the
  note (see Elision after a note); the note's wording makes no claim that
  goes stale, and the next identical read sends the content.
- Measurable: the saving shows in the event log.

## Non-goals

- `ls`, `grep` and other read-only tools (owner: `read` only).
- Reads through `shell` (`cat`, `sed -n`).
- Diffs or partial content when a file changed since the earlier read: a
  changed file is sent in full.

## Anti-goals

- A note sent when the earlier content is already gone (compacted or
  elided away). That is worse than a repeat: the model loses the file.
- A loop where the model keeps asking and keeps getting notes.

## Approaches

1. **State in the read tool** (the issue's proposal): remember path, range
   and a content hash per session; clear it on compaction and on elision.
   Needs session state plumbed into a stateless tool and invalidation kept
   in sync with two history rewrites (compaction, `Budget.MaybeElide`).
2. **Derive it from the visible history** (picked): after `read` runs, look
   back through the session's current messages for an earlier `read`
   tool_use with the same arguments whose tool_result is intact (not an
   elided stub) and whose text equals the new output. Compaction, elision
   and resume are handled by construction; nothing to invalidate, no hash.
   The file is still read each time, which is cheap.
3. Prompt rule only: already in the reviewer checklist and ignored.

Owner picked 2 (2026-11-22).

## Design

- **Match.** Same tool (`read`), the same file after resolving both paths
  with the policy (`ExecPolicy.ResolveRead`, so `core/session.go`,
  `./core/session.go` and a differently cased spelling on a
  case-insensitive system match), the same range (`offset`, `limit`
  compared as decoded; `{"path":"a"}` and `{"path":"a","offset":0}` are
  the same), and the new output equals the earlier tool_result's text
  exactly. Only results that are not errors count. The comparison is with
  the text the model saw, after redaction (`Executor.Filter`).
- **Visible.** The earlier result must be in the session's current
  messages when the call runs (inside `Exec.Run`, before the next request
  is built), and not an elided stub (`elidedPrefix`). After compaction the
  earlier read is gone from the history, so it cannot match.
- **Note.** The tool_result text becomes a one-line note that says where
  the earlier read is, e.g.
  `unchanged since your read of core/session.go 7 turns ago (same range)`.
  It does not claim the content is still in context, since elision may
  stub it later.
  "Turns ago" counts assistant messages between the two calls in the
  current history.
- **Escape hatch.** If the matched earlier result is itself a note, the
  call gets the full content. So a model that does not trust the note gets
  the file on its next identical call, and a repeat costs at most one note.
  (A note's text is never equal to real output, so this falls out of "find
  the latest intact non-note result"; the rule is: when the latest earlier
  identical call was answered with a note, send the content.)
- **Elision after a note.** `s.elide()` runs at the end of the turn, after
  the results are pushed, so it can stub the earlier read the note pointed
  at. The model then sees the note and an `[elided: read <path> N lines]`
  stub. Accepted: the stub says the content is gone, and the next identical
  read sends it in full (the earlier result is a stub, and the latest call
  was a note). Pinning noted results in `MaybeElide` was rejected: it would
  break the stable prefix elision keeps for prompt caching.
- **Where.** In the Executor's result path, before the tool_result event
  is logged, so replay and resume rebuild the same history the model saw.
  The Executor needs read access to the session's current messages; the
  session provides it (for example a hook set on the Executor, like
  `Filter`). Chat, `jas run` and farm workers all get it, since they all
  run tools through a Session.
- **Event.** `tool_result` for a deduplicated call logs the note as
  `output` and adds `unchanged: true`, so review logs can count the saving.
  `tool_result` keeps every existing field; `--json` output and exit codes
  are unchanged.

## Docs

- `docs/manual.md`: the `read` tool's entry says repeats of an unchanged,
  still-visible read return a note, and the repeat after a note returns the
  content. `make manual` for the skill copy.
- `docs/event-log.md`: `tool_result` gains the optional `unchanged` field.

## Tests

- Same read twice, file unchanged: second result is the note, `unchanged`
  logged.
- File changed between reads (an `edit` in between): full content.
- Different range: full content.
- Earlier result elided by `MaybeElide`: full content.
- Note issued, then `MaybeElide` stubs the earlier read: the history shows
  the stub, and a further identical read returns full content.
- `./path` and `path` (and, on a case-insensitive system, another case):
  the second read is a note.
- Two identical reads in one parallel batch: both get full content (the
  first is not in the history yet).
- After compaction: full content.
- Third identical read after a note: full content.
- Error result earlier (missing file): no note.
- Resume a session whose log holds a note: replayed history has the note.

## Left to the builder

- The exact hook shape on the Executor and the note's wording.
- Measuring: compare prompt tokens and `unchanged` counts in review logs
  before and after, against the baseline in the review-loop notes.
