# Memory store — design

**Status:** proposed (2026-11-12). Track C.1 in [design §5](../../design.md#5-v2-roadmap);
background and layer model in [design §12.3](../../design.md#123-memory--dreaming-memory-store-and-dreaming-shipped).
**Depends on:** the v1 event-log contract ([event-log.md](../../event-log.md), shipped #186).
**Followed by:** `jas dream` (Track C.2), which reads this store plus the event logs.

## Intent

jas forgets everything between sessions except what the owner put in
`AGENTS.md` and skills. A correction ("don't run the live tests, they 403"),
how the owner likes to work, a deadline, or the reason plan A beat plan B gets
lost, and the next session repeats the mistake or asks again. The memory store
is a small, owner-visible place where the model writes those notes during a
session, and where the next session finds them.

**Memory belongs to the owner, not to a project** (owner, 2026-11-12). Most of
what is worth remembering is about the person and how they work, and holds
across every repo:

- **About the owner:** role, expertise, preferences ("explain Go internals
  briefly; they know the language").
- **Practices and feedback:** corrections and confirmed ways of working, with
  the reason ("squash-merge right after opening the PR; they don't wait for
  CI").
- **Project management:** goals, deadlines, who owns what, decisions and why —
  anything about ongoing work that the repo does not record.
- **References:** where things live outside the repo (dashboards, trackers,
  docs).

Some of those apply to one project only. They live in the same profile store,
in a per-project folder, so they load only in that project.

It is the draft pad between the transcript (the event log, already kept) and
the owner's handbook (`AGENTS.md`) and workflows (skills), which stay
owner-edited.

## Goals

1. A session starts with a short memory index already in its system prompt.
2. The model can read any note on demand with the tools it already has.
3. The model can add and change notes during a session. The owner approves
   every write.
4. Memory is per profile. Inside the profile, notes that only apply to one
   project are kept apart so they load only there.
5. The store is plain Markdown files the owner can read, edit, or delete by
   hand.
6. `jas dream` can later verify and clean the store against the event logs.

## Non-goals

- Automatic cleanup, summarization, or dedup. That is `jas dream`.
- Promoting notes to skills or `AGENTS.md` (v3, `jas learn`).
- Search, embeddings, or retrieval ranking. The index plus `read`/`grep` is
  the retrieval.
- Sharing memory with a team through the repo (see "Location" for why).
- Compaction. That stays in-session context management.
- A separate in-session scratch layer (layer 2 of §12.3). The live
  conversation is the scratch pad and the event log records it; a `scratch/`
  folder would only be another place for clutter to collect. Notes go
  straight into topic notes.

## Anti-goals (failure even if it works)

- **Memory as a prompt-injection channel.** Text that reaches every future
  session's system prompt must not be writable by a cloned repo or by an
  unseen tool call.
- **Memory that holds secrets.** A key written to a note would be injected into
  every later session and copied into its event log.
- **Memory that copies the repo.** Notes restating code structure or git
  history go stale and then get trusted. Guidance must steer away from this.
- **A noisy prompt.** Write prompts the owner learns to click through without
  reading defeat the review. Writes must be rare and the diff easy to read.
- **Breaking headless runs.** A `jas run` in CI must not stop with exit 5
  because the model tried to save a note.

## Location

```
~/.jas/profiles/<profile>/memory/
  MEMORY.md                      profile index: loaded in every session
  <note>.md                      notes about the owner, practices, references,
                                 work that spans projects
  projects/<key>/
    MEMORY.md                    project index: loaded only in that project
    <note>.md                    notes that only apply to that project
```

`<key>` names the project: the resolved repository root of the git common
dir (so every worktree of one repo, including farm worktrees, shares one
memory), or the resolved project root when it is not a git repo.
`sessions/dir.go`'s `gitCommonDir` already finds the common dir.

The path is encoded into one directory name that is readable and
**injective** (two different paths never share a key):

1. Percent-encode every byte that is not `A–Z a–z 0–9 . _`, including `-`
   and `%` themselves, as `%XX`.
2. Then replace each encoded `/` (`%2F`) with `-`.

So `/home/u/src/jas` → `-home-u-src-jas`, `/home/u/my-repo` →
`-home-u-my%2Drepo`, and `/home/u/my/repo` → `-home-u-my-repo`. Decoding
reverses both steps, so keys never collide. A key longer than 200 bytes keeps
its first 180 bytes plus `~` and the first 16 hex digits of the SHA-256 of the
full path.

**All memory lives in the profile** (owner, 2026-11-12). This replaces §12.3's
`<project>/.jas/memory/`. The owner's reason: memory is about the user and
how they work, not about a repo, so it belongs with the profile. It also
helps in other ways:

- A memory dir inside the repo can be committed. Anything committed there is
  injected into the system prompt of everyone who trusts the repo, and
  re-trusting would be needed on every note change if it joined the trust
  bundle. Keeping it under the jas home means only the owner's own approved
  writes ever reach the prompt.
- One write lift (one directory, one set of guards) instead of two, one of
  which would sit inside an untrusted tree.
- Worktrees share memory without copying it.

Because the store is owner-written and under the jas home, loading it needs no
project trust. An untrusted project still gets its project memory, since the
owner approved every line of it.

## Note format

Layers, as §12.3 names them: transcripts (the event log), topic notes (the
files below), and the index (`MEMORY.md`). Each note is one Markdown file
holding one fact:

```markdown
---
name: squash-merge-after-create
description: squash-merge each PR to main right after opening it; no CI wait
type: feedback
---

Squash-merge every PR to main as soon as it is created, without waiting for
CI or asking. Related: [[pr-titles]].

**Why:** the owner reviews before asking for the PR, and CI runs on main.
**How to apply:** after `gh pr create`, run `gh pr merge --squash`.
```

**Frontmatter** (required on every note; not on `MEMORY.md`):

| field | rule |
|---|---|
| `name` | kebab-case slug, equal to the file name without `.md`; unique within its folder |
| `description` | one line, the hook used to decide relevance; at most 200 characters |
| `type` | `user` (about the owner), `feedback` (corrections and confirmed practices), `project` (goals, deadlines, ownership, decisions), `reference` (where things live outside the repo) |

Other frontmatter keys are kept and ignored, so `jas dream` or the owner can
add more later.

**Body.** Free Markdown. A `feedback` or `project` note ends with a
`**Why:**` line and a `**How to apply:**` line; that is guidance, not checked.

**Links.** Notes link to each other as `[[name]]`. A link resolves to the
note with that `name` in the same project folder first, then in the profile
store. A link to a note that does not exist yet is allowed: it marks
something worth writing later. `jas dream` is what reports dangling links and
orphan notes; writes do not check links.

**Index.** `MEMORY.md` has one line per note and no frontmatter:
`- [Title](file.md) — hook`. Project index lines point into their own folder.

**Validation on write.** When `write` or `edit` would leave a note file (any
`.md` in a memory dir other than `MEMORY.md`) without valid frontmatter, the
call is refused before the approval prompt, with a tool error naming what is
missing or wrong ("memory note needs frontmatter: type must be one of user,
feedback, project, reference"). Files in a memory dir that are not `.md` are
refused too: the store holds notes only. Notes the owner edits by hand are
not checked at load time; a note with bad frontmatter is still readable.

**Where notes may go.** A write is allowed only directly in the profile
memory dir, or directly in `projects/<key>/` for the **active** project's
key. Any other folder (`projects/<other-key>/`, `projects/<made-up>/`, or a
deeper `sub/` folder) is refused before the prompt. Other projects' memory is
readable, not writable.

**`MEMORY.md` is append-and-edit only.** A `write` to an existing
`MEMORY.md` is refused ("change MEMORY.md with edit, not a whole-file
write"); only `edit`, or a `write` that creates it, is allowed. A whole-file
write from stale content would silently drop lines another session added,
while an `edit` fails loudly when its text has changed.

**No deletes.** jas has no delete tool and the shell keeps the jas home
denied, so the model cannot delete a note. To retract one, it rewrites the
note with the corrected fact (keeping its `name`) or, when the fact is simply
gone, edits its line out of `MEMORY.md`. A note with no index line is an
orphan: `jas dream` proposes removing orphans, and the owner can delete files
by hand. The guidance says this.

**Order of checks for `write` and `edit` in a memory dir:** resolve the path
and apply the lift guards; check the folder rule; for `edit`, apply the edit
to the current file in memory to get the result; check the result (`.md`
only, frontmatter on notes, the `MEMORY.md` rule, the secrets check below);
only then ask the owner. Every refusal is a tool error and the turn goes
on.

## Loading at session start

1. Read the profile `MEMORY.md`, then the project `MEMORY.md`, if present.
2. Cap the two indexes **together** at **200 lines or 25 KB**, whichever
   comes first, the profile index first and the project index taking what
   is left, but never less than 50 lines or 6 KB of it (the profile index is
   cut shorter to make room). When an index is cut, append one line to it:
   `(memory index truncated at N lines; prune it)`. This budget is separate
   from `MaxContextBytes` (64 KB, `AGENTS.md`), so the worst-case prompt
   growth is the 25 KB stated here.
3. Redact with the same redactor as `AGENTS.md`.
4. Add one "Memory" section to the system prompt, after the profile and
   project instructions. It holds:
   - the two indexes, each under a heading naming its directory,
   - the absolute paths of both memory dirs,
   - the writing guidance (below).
   The section is present even when both indexes are empty, so the model
   knows where to write. It is left out entirely when memory is off.
5. Record both index files in `session_started.context_files` (`path`,
   `loaded`, `sha256`), like `AGENTS.md`. Additionally add an optional
   `memory` object to `session_started` / `session_resumed`:
   `{profile_dir, project_dir, index_bytes, truncated}`. Both are additive
   under the event-log contract. Update `docs/event-log.md` in the same change.

Writes during a session do not change the system prompt (it would break the
provider's prompt cache and surprise the model). The new index loads on
`/reload`, `/new`, a profile switch, or the next session. `/reload` already
logs `context_reloaded` with `context_files`; memory files join that list.

## Reading notes

No new tool. Both memory dirs join `ExecPolicy.ReadRoots`-style read access
through the same lift mechanism as the profile `skills/` dir: read-only tools
(`read`, `ls`, `grep`) may open paths inside them even though they sit under
the jas home floor. Index lines point at note files; the model reads a note
when its hook looks relevant, and follows `[[name]]` links by opening
`<name>.md` (project folder first, then the profile store).

## Writing notes

The model uses the existing `write` and `edit` tools on paths inside the two
memory dirs.

**Path confinement.** Generalize the profile-skills lift (#173,
`ExecPolicy.ProfileSkills`, `profileLift`, `InProfileSkills`) to a small
set of lifted directories: the profile `skills/` and the profile `memory/`.
The project memory dir lives inside the profile memory dir, so one memory
lift covers both. Every guard the skills lift has applies unchanged: the
spelled path must be `<home>/profiles/<name>/memory`; a `memory/` symlink that
resolves to, inside, or above the jas home or `~/.jas` gets no lift; `Denied`
still checks every resolved path against the full floor, so a link inside the
store pointing at `auth.json` stays denied. A missing `memory/` is lifted only
when nothing on the way is a link elsewhere, so the first write can create it.

**Always ask.** Every `write`/`edit` inside a memory dir is wrapped with
`AskAlways`, like profile skills, at every approve level. The approval shows
the diff, as other writes do.

**Headless runs.** When no one can be asked (`jas run` without a terminal,
farm workers and the planner), memory is read-only: the index still loads, but a write into a
memory dir returns a tool error ("memory is read-only in this run: no one can
approve the write") with status `error`, and the turn continues. It does not
become `approval` `unavailable` and exit 5. This needs a policy check before
the approval gate, not a change to the gate.

An accepted cost: a correction discovered inside a farm worker or a CI run is
not captured as a note. It is still in that session's event log, which
`jas dream` reads, so it can surface as a proposed note later.

**Secrets.** Before asking, run the redactor over the content the write would
produce (for `edit`, the whole file after the edit). If redaction changes anything, refuse the write with a tool error
naming the reason ("memory notes cannot hold secrets; describe where the
secret lives instead"). The owner is never asked to approve a note with a
secret in it.

**Concurrency.** Two sessions on one profile can write the same index. `write`
and `edit` are already atomic (temp file + rename), and `edit` matches against
the current file, so the worst case is an `edit` that fails because the text
changed. That is acceptable. When session awareness's write-overlap warning
ships, it covers memory files like any other path. It is not a prerequisite.

## Writing guidance (system prompt text)

The Memory section tells the model, in about 15 lines:

- Save a note only when a future session could not rediscover it by reading
  the repo, and would otherwise repeat a mistake or ask again. Kinds worth
  saving: about the owner (role, expertise, preferences); practices and
  feedback (corrections and confirmed approaches, with the reason); project
  management (goals, deadlines, ownership, decisions and why); references
  (where things live outside the repo). Write relative dates as absolute.
- Do not save code structure, file lists, git history, or anything in
  `AGENTS.md`. Do not save secrets.
- Default to the profile store. Use the project folder only for a note that
  is meaningless outside this project.
- One fact per note file, in the format above: frontmatter with `name`,
  `description`, `type`; for `feedback` and `project`, end with **Why:** and
  **How to apply:**. Link related notes as `[[name]]`, freely, even to notes
  not written yet.
- After adding a note, add one line to the matching `MEMORY.md`:
  `- [Title](file.md) — hook`. Keep the index short; it is capped.
- Before adding a note, check for one that already covers it and update that
  instead. To retract a wrong note, rewrite it with the correct fact, or
  remove its line from `MEMORY.md` if the fact no longer applies; you cannot
  delete files.
- Change `MEMORY.md` with `edit` (add or remove one line), never a whole-file
  `write`.
- Treat recalled notes as what was true when written. Check a file, flag or
  function a note names before relying on it.
- Writes are shown to the owner for approval. Keep them few: one note plus
  its index line per fact.

## Owner controls

- **Setting `memory`** (`on` | `read` | `off`, default `on`) in
  `settings.Keys`, layered like other settings.
  - `on`: indexes load, notes are readable and (with approval) writable.
  - `read`: indexes load and notes are readable; every write into a memory
    dir is refused with a tool error, as in headless runs. For a sensitive
    repo whose details should not leak into notes used elsewhere.
  - `off`: no Memory section, no read or write lift, no `memory` event
    field.
  At project level only `read` and `off` take effect, and `off` there hides
  only that project's memory: the profile index still loads, read-only.
  Profile notes are about the owner, so a repo cannot remove them; only the
  global or profile layer turns memory fully off.
  Not a live setting: a change takes effect on `/reload`, `/new`, a profile
  switch, or the next session (it changes the system prompt). It is not
  added to `liveSettings`.
- **`/memory` chat command:** prints both dirs, each index's size against the
  cap (lines and bytes, and whether it was truncated), and the note count.
  Read-only; the owner edits notes with their editor.
- **`jas memory` CLI:** the same report, for scripts and the manual. A
  `--path` flag prints only the project memory dir, for `$EDITOR $(jas memory
  --path)/MEMORY.md`.

## Interaction with existing features

- **Profiles:** memory follows the active profile. `/profile` reloads it like
  context files and skills; `profile_changed.context_files` lists the new
  indexes.
- **Farm:** workers get the index read-only (headless rule). The planner too.
- **Resume:** a resumed session rebuilds its system prompt from current files,
  so it sees the current index, not the one from when it started. Same as
  `AGENTS.md` today.
- **Compaction:** unrelated. A compaction summary is not written to memory.
- **Event log:** no new event type. Memory writes are ordinary `tool_started`
  / `approval` / `tool_result` events with `path` in `args`; `jas dream` finds
  them by path prefix.

## Acceptance

- A note written and approved in one chat session appears in the next
  session's system prompt (index) and can be read (note).
- A write into a memory dir prompts at every approve level, including
  `never`.
- A `jas run` without a terminal that tries to write a note gets a tool error,
  continues, and exits 0 if the task otherwise succeeds.
- A note whose content contains a redactable secret is refused before any
  prompt.
- A note write with missing or invalid frontmatter (no `type`, a `name` that
  does not match the file name, an unknown `type`), or a non-`.md` file in a
  memory dir, is refused before any prompt. `MEMORY.md` needs no frontmatter.
- A note with hand-broken frontmatter still loads and reads.
- A `memory/` symlink to the jas home gets no lift; reading `auth.json`
  through a link inside the store is denied.
- An index over 200 lines or 25 KB is cut, marked truncated in the prompt and
  in `session_started.memory`.
- `memory: off` at profile level removes the section and the lift.
  `memory: off` at project level hides only the project index and makes the
  profile store read-only. `memory: read` refuses every write.
- A key for `/a/my-repo` and one for `/a/my/repo` differ; a path over 200
  bytes gets a stable hashed key.
- A write to `projects/<other-key>/`, a nested folder, or a whole-file
  `write` over an existing `MEMORY.md` is refused before any prompt.
- Every worktree of one repo resolves to the same project key.
- `docs/manual.md` documents the setting, `/memory`, `jas memory`, and the
  directory layout; `TestManual_*` stays green. `docs/event-log.md` documents
  the `memory` field and the fixture gains it.

## Left to the builder

- How the lift generalizes in code (a list of lifted dirs, or two named
  fields), as long as each keeps the #173 guards and tests.
- Exact wording of the guidance text and error messages.
- The frontmatter parser: reuse the YAML library already in `go.mod` (farm
  config) or a small `---` block reader, as long as unknown keys survive.
- Whether `/memory` and `jas memory` share one formatter.
- The exact encoder and decoder for `<key>`, as long as it follows the rule
  above (injective, readable, hashed tail past 200 bytes) and has a
  round-trip test.
