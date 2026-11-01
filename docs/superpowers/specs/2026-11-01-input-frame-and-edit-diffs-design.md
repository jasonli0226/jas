# Input frame and edit diffs (chat UI)

Date: 2026-11-01. Status: draft for approval.

## Intent

Bring two pieces of Claude Code's chat look to jas:

1. **Input frame**: horizontal rules above and below the input box, so the
   input reads as its own area apart from the transcript.
2. **Edit diffs**: when the agent edits or writes a file, show what changed as
   a diff with line numbers and `+`/`-` lines, both in the approval prompt
   (before the change, to judge it) and in the transcript (after it lands).

Why: today an edit shows as one row (`▸ edit core/budget.go ok`) and the
approval prompt as one line (`approve edit: core/budget.go? [y/N/always]`).
The user cannot see what they are approving or what changed without opening
the file.

## Goals

- Rules above and below the input, like Claude Code.
- A diff for `edit` and `write` in the approval prompt and in the transcript.
- Line numbers on every diff line; colour style **B** (chosen from a terminal
  swatch): muted red/green background on whole removed/added lines, plus a
  brighter background on the exact words that changed.

## Non-goals

- No alt-screen or fullscreen UI (AGENTS.md constraint). Everything stays
  inline scrollback plus the live region.
- No syntax highlighting inside diffs.
- No diffs for `shell` (a command that changes files is not diffed).
- No light-terminal theme (the palette is dark, like the rest of `tui/style.go`).

## Anti-goals

- The model must not pay for the diff: it is never added to the tool result
  text the model sees. `edit`/`write` outputs stay `edited <path>` /
  `wrote <path> (N bytes)`.
- `jas run`, `--json` and exit codes must not break. The event log change is
  additive only.
- Diffs must not leak secrets: diff text passes through the same redaction as
  every other printed or logged text.

## Part 1: input frame

```
● agent reply…

──────────────────────────────────────────
› your message
  second line
──────────────────────────────────────────
model · shell · 12% · ↑3k ↓1k · jas (main)
```

- A dim rule (`─`) the full terminal width directly above and directly below
  the input box. The status line stays below the bottom rule.
- The rules frame whatever occupies the input slot: the input itself, the `/`
  menu under it, or a picker (`/model`, `/resume`). Builder's call whether the
  `/` menu sits inside or below the bottom rule; inside is the default.
- The approval prompt and the queued-steering lines stay above the top rule.
- The blank line that currently separates the input from the transcript
  (`tui/chat.go` View) stays above the top rule.

## Part 2: edit diffs

### What the user sees

Approval (before the change):

```
edit core/budget.go
  182   	if arg == "" {
  183 - 		return fmt.Sprintf("%s%s %d lines]", elidedPrefix, tool, lines)
  183 + 		return fmt.Sprintf("%s%s %d lines; %s]", elidedPrefix, tool, lines, hint)
  184   	}
approve? [y/N/always]
```

Transcript (after the change lands):

```
▸ edit core/budget.go ok
  182   	if arg == "" {
  183 - 		return fmt.Sprintf(...)
  183 + 		return fmt.Sprintf(...)
  184   	}
```

### Rules

- **Tools covered**: `edit` and `write`.
  - `edit`: diff of the one replaced region.
  - `write` to a new file: every line shown as `+`.
  - `write` over an existing file: a real line diff against the old content.
- **Line numbers**: dim. A `-` line shows its old-file number, a `+` line its
  new-file number, a context line its new-file number.
- **Context**: 3 unchanged lines around each change (hunk), like `git diff`.
  Separate hunks are joined by a dim `⋮` row.
- **Colour (style B)**:
  - removed line background `#3f1d1d`, added line background `#1c3a24`,
    filling the full terminal width;
  - changed words within a paired `-`/`+` line on a brighter background:
    removed `#7a2a2a`, added `#2a6b3c`;
  - on 16-colour terminals the backgrounds degrade through lipgloss like the
    existing bands, and must stay distinguishable (red vs green); `NO_COLOR`
    leaves the `+`/`-` signs as the only marker.
  - Exact hex values are the swatch's; the builder may tune them for contrast
    but keeps the scheme.
- **Width**: each diff line is cut to the terminal width (with `…`) *before*
  it is styled, so the background ends cleanly at the edge.
- **Length**:
  - A diff is capped where it is computed (about 400 lines or 64 KB,
    builder may tune), with a `truncated` flag and a dim
    `… diff truncated` row when rendered. This one cap bounds the event,
    the `--json` record and the approval prompt.
  - Approval prompt: the diff up to that cap. The diff is printed into
    scrollback (`Println`, the way transcript blocks already are) and the
    `[y/N/always]` question stays in the live region, so a tall diff never
    pushes the question off screen.
  - Transcript: capped at about 30 diff lines, then a dim `… N more lines`.
- **When not shown**: no diff after a call that ended `error`, `denied` or
  `cancelled`. The approval diff already showed the intent.
- **`jas run`** (plain printer): prints the diff under the tool line only
  when stdout is a terminal. Piped stdout (scripts, CI) is unchanged. Plain
  text; adding colour to the printer is optional.
- **Approval in `jas run --approve`** (the line-based `core.Approver`, on
  stderr): shows the diff above its `[y/N]` question, plain text.

### Where the diff comes from

- **Preview (approval)**: the gate has no `ExecPolicy`, so it cannot read
  files. The **executor** computes the preview before calling the gate: a
  mutating tool may implement an optional interface (for example
  `Previewer.Preview(ctx, ExecPolicy, args)`) that reads the file, applies
  the change in memory and diffs, without writing. The executor passes the
  preview to the gate; `Gate.Allow` gains a parameter for it (an internal
  interface, so `tui.Gate`, `core.Approver` and `AllowAll` change together).
  If the preview fails (file missing, `old_string` not found), the prompt
  shows no diff and the call fails normally when executed.
- **Result (transcript)**: the tool returns the diff on `core.Result`
  (`core/tools.go`), for example `Result.Diff`, so the executor never
  re-reads the file. `edit` already knows its region. The diff is never put
  in the model's tool_result block.
- **Structured, not pre-rendered**: the diff travels as data (hunks of
  lines with kind, old/new line numbers and changed-word spans) so the TUI
  and the plain printer render it their own way.

### What is logged vs shown (user decision)

- **Shown on screen**: the full diff (approval prompt and transcript).
- **Logged** (`events.jsonl`, `--json`): `tool_result` gains an optional
  `diff` field, but for a `write` over an existing file it keeps **only the
  added lines**; the old file's removed lines are screen-only, because they
  can hold content the model never read (for example a `.env`) that pattern
  redaction would miss. For `edit`, the removed text is `old_string`, which
  is already logged in `tool_started.args`, so the logged diff keeps both
  sides. A `write` to a new file has only added lines anyway.
- This means the screen copy must reach the TUI without passing through the
  log (today `core.NewUILog` writes an event to the log and then to the UI
  with the same payload). How to split it is the builder's call; the log
  must never receive removed lines of a `write` overwrite.
- **Event log schema**: additive, so `v` does not change (design.md §3).
  Update the event table in `docs/design.md`. `core/replay.go` ignores the
  new field (known-harmless; it reads only `output`/`status`).

### Safety

- **Redaction** runs on the whole old and new text *before* diffing, not
  per line or per word span. The existing per-string redaction
  (`walkRedact`, `core/events.go`) would see one line at a time, so a
  private-key body would leak and a token split by word highlighting would
  slip past its rule. Private-key blocks are masked line by line (each line
  of the block becomes a marker line) so line numbers stay correct. The
  approval-prompt diff gets the same treatment.
- **Escaping**: every rendered diff line (approval and transcript) goes
  through `core.Sanitize`-equivalent escaping, so control, ESC and bidi
  runes in a file cannot rewrite the prompt or the screen.

## Constraints

- Go, `CGO_ENABLED=0`, `go test -race ./...` green.
- Commits `type: description`.
- Keep the binary small and dependencies few (README and design.md stress a
  tiny, fast harness).

## Decided by the user

- Rules above and below the input.
- Full diff with line numbers and `+`/`-`, like Claude Code.
- Diff both in the approval prompt and in the transcript.
- Colour style B (muted line background + brighter changed words).
- A `write` overwrite's removed lines are shown on screen only; the log keeps
  just the added lines.

## Claude's calls (check these)

- 3 context lines; dim line numbers; `⋮` between hunks.
- Transcript cap of ~30 lines; approval shows up to the ~400-line/64 KB cap,
  printed to scrollback with the question kept live.
- `write` to a new file shows all `+`; overwrite shows a real diff.
- No diff after error/denied/cancelled.
- Diff stored in the event log (`tool_result.diff`, minus overwrite removed
  lines), so `--json` consumers get it; never sent to the model.
- The executor computes the approval preview and passes it to the gate.
- `jas run` prints diffs only when stdout is a terminal.
- Both parts (input frame and diffs) ship as one piece of work, input frame
  first since it is small and independent; the approval-diff rendering lands
  in the same series so the View layout changes once.

## Left to the builder

- Diff algorithm and whether to add a dependency. Preference: no new
  dependency; a small line diff (LCS/Myers) for `write` overwrites, and for
  word highlighting a common-prefix/suffix split of each paired line is
  enough. `edit` already knows its region, so its diff is the old/new
  strings at a known start line plus context.
- Exact type and interface names, and the structured diff's exact shape.
- How the screen-only diff reaches the TUI apart from the log.
- Whether resumed sessions reprint diffs (the transcript is rebuilt from
  messages, which do not carry the diff). Default: no diffs on resume.
- Tab expansion in diff lines.
