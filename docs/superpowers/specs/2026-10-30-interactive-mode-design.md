# jas interactive mode — design

Date: 2026-10-30. Status: draft for owner review.
Research behind it: `docs/research/pi-interactive.md` (pi 1.4.0, cited) and
`docs/research/pi-mono-audit.md`. Calls marked **(builder's/author's call)** were
not made by the owner and may be overturned.

## Intent

Today jas only does one-shot work: `jas run "task"`. The owner uses pi day to
day and wants jas to replace it. That needs a chat mode that **looks and
feels like pi**: an inline terminal UI with an input box, streaming replies,
compact tool rows and a status line. The owner keeps what jas does better
than pi (approvals, safety, speed) and drops what annoys them in pi (too much
thinking output on screen).

Interactive mode is also the base for the next big feature, **farm**:
starting several long-running agents (one per git worktree) from inside the
chat and watching them while chatting. Farm is not part of this design, but
nothing here may assume a single agent per process or a single session per
screen.

### What success looks like (owner's words)

jas becomes the daily driver instead of pi when it is:

- **fast**: (author's reading) starts instantly, typing never lags, streamed
  tokens appear immediately, no redraw flicker;
- **customizable**: own key bindings, own slash commands (saved prompts), and
  settings such as showing thinking; extensions later;
- **on the way to farm**: the core supports it next;
- **approvals that don't nag**: safe by default, but one "always" answer stops
  repeat prompts for the rest of the session.

## Goals

1. `jas` with no arguments opens an interactive chat in the current project.
   (When jas is not configured yet, the existing onboarding runs first, then
   the chat opens.) `jas run "task"` keeps working exactly as today.
2. Multi-turn conversations with history, in a pi-style **inline** TUI.
3. Thinking hidden by default.
4. Inline approvals with a session-scoped `always`.
5. Steering: typing while the agent works.
6. The v1 slash-command set below, including `/skill:name`.
7. Compaction so long chats don't stop at "context window full".
8. Sessions saved to disk and resumable (`/resume`, `jas --continue`).
9. Customization: key bindings, prompt templates, chat settings.

## Non-goals (this version)

- Farm, `/fork`, `/tree` / session branching UI. (The storage format must
  allow them later; see Session storage.)
- Extensions that run their own code, themes, `/share`, `/export`, `/settings`
  menu, `/reload`, logging in from the chat (`jas auth` exists).
- A fullscreen (alternate-screen) mode.
- Persisting `always` grants beyond the session.
- Image paste, `@file` autocomplete, `!cmd` passthrough. (Builder may add
  `@file` if cheap; not required.)
- Follow-up queue (pi's Alt+Enter). Only steering ships.

## Anti-goals (would make it a failure even if every feature works)

- **Nagging approvals.** Asking again for something the user already said
  `always` to, or asking more than `jas run` would at the same level.
- **Slowness.** Noticeable startup delay, input lag while a reply streams, or
  redrawing the whole screen per token.
- **Thinking spam.** Reasoning text on screen without the user asking for it.
- **Losing work.** A crash, Ctrl-C or cancelled turn that loses the
  conversation or leaves the session file unreadable.
- **Typing swallowed.** Any keystroke or line eaten by a stray reader (see
  Hazard 4).
- **Single-agent assumptions** that would force a rewrite for farm.

## Constraints

- Go, single static binary, `CGO_ENABLED=0`, Linux/macOS/Windows as today.
- Keep existing safety guarantees unchanged: path confinement, deny floor,
  redaction of tool output, events and UI, the approval ladder, project trust
  for `AGENTS.md` and project skills, fsync discipline of the event log.
- `jas run` and `jas run --json` behavior and exit codes stay as they are.
  The `--json` event stream may gain event types/fields (additive, as
  design.md §event contract allows) but must not lose or rename any.
- Prompt caching: the system prompt and earlier messages must stay
  byte-identical across turns (same rule as today), except where compaction
  deliberately replaces history.
- OpenRouter live tests use `z-ai/glm-*` models only.

## What the owner decided

### Look and feel

- **Inline, like pi** (owner picked option A over fullscreen). Output goes into
  the terminal's normal scrollback; the terminal's own scroll, search and copy
  work. A live region at the bottom holds the input box and status line. On
  quit, the conversation stays on screen.
- Transcript, top to bottom per turn:
  - user message;
  - thinking: **hidden by default**, shown as at most one dim line
    (`· thought for 4s`); `/thinking` toggles full display for the session and
    the `show_thinking` setting changes the default;
  - assistant text, streamed, with **light Markdown formatting** (owner's
    call): fenced code blocks shown boxed with syntax colouring; bold,
    headings and lists styled; other Markdown printed as-is. Each block
    (paragraph, list, code block) is printed into scrollback once it is
    complete; the block still being written shows in the live region. No heavy
    Markdown renderer dependency (startup speed);
  - each tool call as **one line** with a short result
    (`▸ read core/budget_test.go (212 lines)`, `▸ shell go test ./core ok`,
    errors in a distinct color). Full output is not printed by default
    (author's call: no expand UI in v1 since inline scrollback can't
    re-render old rows; full results stay in the session file).
- Status line (author's call): model · approval level · context used % ·
  tokens · project folder. Shows a working indicator while a turn runs and
  the queued-message count when steering is pending.

### Approvals

- Asked **inline in the chat**, the same situations as `jas run` at the
  current level (`never` / `shell` / `mutating`), prompt
  `approve <tool>: <summary>? [y/N/always]`.
- `always` lasts for the rest of the session only, never written to disk:
  - **shell**: allows later commands whose **first two words** match
    (`go test ./...` → `go test …`), but only for **simple** commands. Exact
    rules (author's call):
    - Words come from a quote-aware shell split of the command; the first two
      words must be **equal**, not prefixes (`go test` does not match
      `go testx`). A one-word command grants that word.
    - A command is not simple, so it always asks and can't create a grant, if
      its text contains anywhere (substring check, quoted or not) any of `;`,
      `&`, `|`, `$(`, a backtick, `>`, `<`, or a newline; or if it starts with
      an environment assignment (`FOO=bar go test` — an assignment such as
      `PATH=…` can change what runs).
    - On Windows (`cmd /c`) the same list plus `^` and `%`.
  - **edit / write**: allows that tool for the rest of the session (both are
    already confined to the project).
- **Grants live as long as the chat process** (owner's call): they survive
  `/new`, so starting a fresh conversation doesn't bring the prompts back.
  Quitting jas clears them. Resuming a session (`/resume`, `--continue`)
  starts with none, because grants are never stored in session files. The
  same goes for `/approve` level changes. (With farm later, sessions started
  from the chat get their own grants; builder may keep grants on a
  chat-level object, not on `core.Session`.)
- `/approve [level]` changes the level for the chat; `/allowed` lists the
  grants and removes one.
- **Any answer other than `y` or `always` denies the call.** If the answer
  is more than `n`/`no`/empty (e.g. "no, use X instead"), the text is also
  queued as a steering message, so nothing typed is lost (author's call).
  While an approval prompt is showing, Enter answers it.
- Answering the prompt uses the chat's own input (never a separate stdin
  reader).

### Typing while the agent works

- **Enter queues a steering message**, shown just above the input box. It is
  delivered to the model as soon as the current tool calls finish (after the
  tool results are added, before the next model request), not after the
  whole run.
- **If the model gives its final answer while messages are queued**, the
  queue is sent at once as the next user message and the agent continues
  (author's call); to the session this is a new user message, so it gets its
  own turn and its own turn cap (`max_turns` counts model requests per user
  message, as `jas run` does today).
- **While an approval prompt is showing, Enter answers the prompt.** Text
  queued before the prompt appeared stays queued.
- **Esc stops the run** and puts any queued text back in the input box.
- (Author's call, matching pi) **Ctrl-C clears the input box**; pressing it
  twice on an empty box within about a second quits. Ctrl-C does not stop a
  run; Esc does.
- Forking into a parallel agent: later, with farm (`/fork`).

### Slash commands (v1)

| Command | Does |
|---|---|
| `/new` | Start a fresh conversation (skills and project trust unchanged). |
| `/model [name]` | Switch model for the session; bare `/model` opens a picker. |
| `/skill:name [text]` | Run a skill explicitly; typing `/` lists commands and skills. |
| `/compact [focus]` | Compact now, optionally saying what to keep. |
| `/resume` | Pick a recent session in this project and continue it. |
| `/session` | Session file, tokens used, context %. |
| `/thinking` | Show or hide thinking for this session. |
| `/approve [level]` | Change the approval level for the session. |
| `/allowed` | List and remove this session's `always` grants. |
| `/copy` | Copy the last reply to the clipboard. |
| `/help`, `/quit` | Commands and keys; exit. |

- A mistyped or unknown `/command` shows "unknown command" and is **not** sent
  to the model (deliberately unlike pi), with the closest match suggested
  ("did you mean /model?") and the typed text left in the input box.
- `jas --continue` opens the most recent session for this project.
- User prompt templates appear as commands too (see Customization).

### Customization (v1)

- **Key bindings**: a user file remaps the chat keys (submit, newline, stop,
  clear, quit, picker navigation).
- **Prompt templates**: Markdown files that become `/name` commands; the file
  body (with any text typed after the command appended) is sent as the user
  message.
- **Settings**: chat settings live with the existing config (`jas config
  set …`), at least `show_thinking`.
- Extensions: later.

### Farm-readiness

The chat must not block farm. Concretely: session state lives in a core
`Session` value (not in globals or the UI); several sessions can exist in one
process; the UI renders events from a session it is given; the steering
queue lives in the session, not the UI.

## Decisions made by the author (owner may overturn)

1. **One file per session.** `events.jsonl` becomes replayable: it is both the
   audit log and the session file (no second message log). Matches design.md
   ("audit + resume").
2. **Lifecycle events.** `session_started` once when a session is created and
   `session_ended` when it closes; each user message adds a `user_message`
   event and the turn's events, framed by per-turn start/end events
   (names: builder's call, e.g. `turn_started` / `turn_ended` with status and
   reason). `jas run` writes one session with one turn, so its stream only
   gains events.
3. **Compaction** follows pi: trigger when context tokens exceed
   `window − 16384`; keep about the last 20k tokens; cut only at
   user/assistant boundaries, never at a tool result; structured summary
   prompt (Goal / Constraints & Preferences / Progress / Key Decisions /
   Next Steps / Critical Context, plus read and modified files), iterating
   on a previous summary; tool results clipped to 2000 chars in the summary
   input; a summary that tries to call a tool is rejected. Stored as a
   `compaction` event `{summary, first_kept_id, tokens_before, usage}`, where
   `first_kept_id` is the **event id** of the first message event kept
   verbatim; the original messages stay in the file. The two numbers are
   settings, not magic values: `compact_reserve_tokens` (default 16384) and
   `compact_keep_tokens` (default 20000). Runs on the **session model** by
   default; a `compact_model` setting may name a cheaper one. Existing
   tool-result elision stays as the cheap first stage. Auto-compaction runs
   between turns and before sending a new user message; a provider
   context-overflow error triggers one compact-and-retry.
4. **Resume.** `--continue` picks the newest session whose
   `session_started.root` matches the current project. `/resume` lists recent
   sessions for this project (first user message as the label) and replays
   the chosen file into a live session that keeps appending to it.
   - The **system prompt is rebuilt** from the current `AGENTS.md`, skills and
     trust decision, never replayed from the file, so content whose trust was
     revoked or whose hash changed can't come back. The conversation messages
     are replayed as stored. (The system prompt may still be logged for
     audit; it is not used for replay.)
   - Reopening a file appends a **`session_resumed`** event (model, root,
     context files and skills as now loaded) instead of a second
     `session_started`; `session_ended` is written each time the chat closes
     the session. The design.md event table is updated to say
     `session_started` is once per file and `session_ended` once per open.
   - The model recorded in the file is used on resume if it is on the current
     provider; otherwise the configured model, with a notice.
   - Replay must tolerate a crash: no final `session_ended`, and a truncated
     or unparseable last line (ignored with a notice, never fatal). It must not
     assume `session_ended` exists or appears once.
   - After compaction ships (build step 4), replay starts from the latest
     `compaction` event: the summary stands in for every message before its
     `first_kept_id`, then messages from that event onward are replayed.
5. **Prompt template locations**: `~/.jas/prompts/*.md` (user) and
   `<root>/.jas/prompts/*.md` (project; covered by the project trust
   decision, like project skills). These are read by jas itself, not by the
   model's tools, so the deny floor on `~/.jas/**` does not get in the way.
   Key bindings: `~/.jas/keybindings.json`.
6. **Model switching** applies to the session only and is recorded in the
   session file; it does not change stored config. In this version `/model`
   switches between models on the **current provider** only (same base URL
   and key), so no provider or redactor rebuilding mid-session. `/model` and
   `/thinking` choices, like grants, last for the chat process and survive
   `/new`.
7. **TUI library**: Bubble Tea, **inline mode** (no alt screen), printing
   finished transcript blocks above the live region into real scrollback;
   agent events delivered to the UI as messages from the session goroutine.
   v1 vs v2: builder evaluates (v2's better key handling may be needed for
   Shift+Enter newlines); otherwise v1.
8. **Thinking stream.** jas currently ignores reasoning deltas from the
   OpenAI-compatible stream. The provider must surface them (e.g. the
   `reasoning` / `reasoning_content` delta fields GLM and OpenRouter send) as a
   distinct stream event so the UI can time and optionally show them. Thinking
   is not sent back to the model in later turns and not logged in full by
   default (builder's call whether to log it).
   **Risk:** some reasoning models on OpenRouter work worse in multi-turn tool
   use when their reasoning is not sent back. Before step 3 ships, run the
   live `z-ai/glm-*` tests on a multi-turn tool task with and without sending
   reasoning back; if dropping it hurts, keep the reasoning in the message
   history (and the session file) for providers that need it, while still
   hiding it on screen.

## Logging and redaction

Every new event (`user_message`, full assistant content including tool
calls, steering, compaction summaries, `session_resumed`, turn events) is
written through the existing `EventLog`, which redacts each payload before it
reaches disk or the `--json` stream (`core.OpenEventLog(path, tee, redact)`).
No event may bypass it (design.md "session hygiene"). Replayed messages are
therefore the redacted text; that is accepted.

## Known hazards in today's code (must be fixed)

From `docs/research/pi-interactive.md` §10:

1. `events.jsonl` can't rebuild a conversation: the user prompt, assistant
   tool calls and the system prompt are not logged (`core/loop.go:76-82`,
   `:213`).
2. Event ids restart at `e000000` when a file is reopened for append
   (`core/events.go:148`, `:166`), which would duplicate ids and break any
   `parent_id` tree. Reopening must **continue `seq` and ids from the last
   valid line**. Fill `parent_id` with the previous entry on the active path
   (a linear chain is a valid tree) so `/fork` and `/tree` can come later.
   Two processes must never append to one file (design.md §3.2 single
   writer): opening a session file for append takes an **exclusive lock**
   (flock on unix, LockFileEx on Windows, held for as long as the session is
   open); if another jas holds it, `/resume` and `--continue` refuse with a
   clear message naming the session.
3. `session_started` / `session_ended` are written per `Run`.
4. The Approver reads stdin with its own reader and leaves it blocked after a
   cancel (`core/approve.go:90-104`); in a chat that reader would eat the next
   line. Approval answers must come through the chat input; the `Gate`
   interface can stay.
5. Ctrl-C cancels the whole process (`signal.NotifyContext` in
   `cmd/jas/main.go`); the chat needs a per-turn context.
6. Turn cap, context full and approval-needed come back as exit errors from
   `Run`; in the chat they are per-turn statuses and the session continues.

## Suggested build order

Each step is shippable and keeps `jas run` working:

1. **Core `Session`**: holds messages, log, budget and config; `Send(ctx,
   text)` runs the existing loop over held history; steering queue polled
   after tool results. `jas run` becomes "new session, one send". Fixes
   hazards 1-3, 6.
2. **Replay and resume**: rebuild a `Session` from `events.jsonl`;
   `--continue`.
3. **Inline TUI `jas` chat**: input box, streaming, tool rows, status line,
   thinking indicator, Esc/Ctrl-C, steering display, inline approvals with
   `always` grants, the slash commands except `/compact`. Fixes hazards 4-5.
4. **Compaction**: auto and `/compact`.
5. **Customization**: key bindings file, prompt templates, `show_thinking`.

## Left to the builder

- Exact names and fields of new event types (keep them additive and
  documented in design.md's event table).
- Visual details: colors, glyphs, spacing, status-line layout within the
  listed contents, picker UI for `/model` and `/resume`.
- Multi-line input keys (Shift+Enter or Alt+Enter for newline, if the
  terminal reports them) and paste handling, so a pasted multi-line block
  stays one message.
- Clipboard mechanism for `/copy` (e.g. OSC 52, with a fallback message).
- Where in the code the TUI lives (e.g. a `ui/` package as design.md
  sketches).
- How the model picker gets its list (config, catalog, provider API).
- Tests: the chat should be testable without a real terminal (drive the
  session and UI model with scripted input and the fake provider).
