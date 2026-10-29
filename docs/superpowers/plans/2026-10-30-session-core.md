# Session Core and Resume Implementation Plan (interactive mode, plan 1 of 4)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `core` a multi-turn `Session` with steering, a replayable and lockable session file, and replay/resume, while `jas run` keeps its behavior and exit codes.

**Architecture:** `core.Run`'s loop moves onto a `Session` value that holds the message history; `Send` runs one user message to completion (or cancel) over that history. `events.jsonl` becomes the session file: new additive events make it replayable, reopening continues ids and fills `parent_id`, and an exclusive lock enforces the single-writer rule. `Replay` rebuilds messages from the file; `ResumeSession` continues it. No UI changes in this plan; the TUI (plan 2), compaction (plan 3) and customization (plan 4) build on these APIs.

**Tech Stack:** Go 1.27, stdlib, `golang.org/x/sys` (already a dependency) for Windows file locking.

**Spec:** `docs/superpowers/specs/2026-10-30-interactive-mode-design.md` (build-order steps 1 and 2; hazards 1, 2, 3, 6; sections "Logging and redaction", "Resume", "Typing while the agent works").

## Global Constraints

- `jas run` and `jas run --json`: same behavior and exit codes; the `--json` stream may only **gain** event types and fields, never lose or rename any.
- Every event goes through the existing `EventLog` (and its redactor); nothing writes to the session file directly.
- System prompt and earlier messages stay byte-identical across turns (prompt caching).
- Single writer per session file (design.md §3.2); fsync discipline unchanged (`AppendSync` for session lifecycle events).
- Platforms: Linux, macOS, Windows; `CGO_ENABLED=0`.
- Match existing style: `internal/assert` helpers, table tests where the package already uses them, comments in the existing density.

## Review Focus

1. **A crash mid-session** (killed process): the file ends without `session_ended`, possibly with a half-written last line. Replay must load everything before it, report one notice, and never fail. → Task 4 tests `TestReplay_TruncatedLastLine` and `TestReplay_NoSessionEnded`.
2. **A tool call with no `tool_result` event** (crash between `tool_started` and result): replay must not produce an orphan `tool_use`, which providers reject. → Task 4 test `TestReplay_OrphanToolUseGetsInterruptedResult`.
3. **Two jas processes on one session**: the second open must fail fast with `ErrSessionLocked`, not interleave writes. → Task 1 test `TestOpenEventLog_SecondOpenLocked`.
4. **Cancel (Esc) mid-turn, then a new message**: the session must stay usable, history consistent (cancelled tool calls have results), and the next `Send` must work. → Task 3 test `TestSession_CancelThenSendAgain`.
5. **Steering text that arrives while the model is finishing**: it must not be lost or silently dropped at end of turn. → Task 3 test `TestSession_SteerAtFinalStartsNewTurn`.

---

## File Structure

- `core/events.go` (modify): reopen continues `seq`/ids from the file, `parent_id` chain, locking, new event-type constants.
- `core/lock_unix.go`, `core/lock_windows.go` (create): `lockFile(f *os.File) error`.
- `core/wire.go` (create): JSON form of `providers.ContentBlock` used in events (`toWire`, `fromWire`).
- `core/session.go` (create): `Session`, `SessionConfig`, `TurnResult`, `NewSession`, `ResumeSession`, `Send`, `Steer`, `Close`, `Messages`.
- `core/loop.go` (modify): turn/stream/elide logic becomes `Session` methods; `Run` becomes a thin wrapper.
- `core/replay.go` (create): `Replay`, `ReplayState`, `ListSessions`, `SessionInfo`.
- `docs/design.md` (modify): event table.

---

### Task 1: Reopenable, locked event log with a `parent_id` chain

**Files:**
- Modify: `core/events.go:140-200` (`openEventLog`, `handle`)
- Create: `core/lock_unix.go`, `core/lock_windows.go`
- Test: `core/events_reopen_test.go`

**Interfaces:**
- Produces: `var ErrSessionLocked = errors.New("session is open in another jas process")`; `OpenEventLog` unchanged signature, new behavior: continues numbering, sets `parent_id`, holds an exclusive lock until `Close`.

- [ ] **Step 1: Write the failing tests** in `core/events_reopen_test.go`:
  - `TestOpenEventLog_ReopenContinuesIDs`: open, append 2 events, close; reopen, append 1; read lines; assert ids `e000001,e000002,e000003`, seqs `1,2,3`.
  - `TestOpenEventLog_ParentIDChain`: first event's `parent_id` is `null`; each later event's `parent_id` equals the previous line's `id`, including across a reopen.
  - `TestOpenEventLog_ReopenSkipsTruncatedLine`: write two valid lines plus `{"v":1,"id":"e000003"` (no newline) by hand; open and append; the new event has id `e000003` and `parent_id` `e000002`, and it starts on its own line (the file is valid JSONL from line 1 to the new line, the partial line excepted).
  - `TestOpenEventLog_SecondOpenLocked`: open a log, then `OpenEventLog` the same path again → `errors.Is(err, ErrSessionLocked)`; after the first `Close`, opening succeeds.

- [ ] **Step 2: Run** `go test ./core/ -run 'OpenEventLog_' -v` — expect FAIL (ids restart at `e000001`, `parent_id` null, second open succeeds).

- [ ] **Step 3: Implement.**
  - `lockFile(f *os.File) error` — unix (`//go:build unix`): `syscall.Flock(int(f.Fd()), LOCK_EX|LOCK_NB)`, `EWOULDBLOCK` → `ErrSessionLocked`. Windows: `windows.LockFileEx(h, LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)` on **one byte at offset `1<<62`** (set in the `Overlapped`), so ordinary reads and appends by readers such as `ListSessions` are not blocked; `ERROR_LOCK_VIOLATION` → `ErrSessionLocked`. The lock is released by closing the file.
  - `openEventLog`: open as today, then `lockFile` (close the file on failure), then scan the existing content for the last line that parses as JSON with `id` and `seq`; seed `l.seq` and `l.lastID`. If the file does not end in `\n`, write one `\n` first so the next event starts a fresh line.
  - `handle`: set `ParentID` to `&l.lastID` when `lastID != ""`, then update `lastID` after a successful write.

- [ ] **Step 4: Run** `go test ./core/ -v -run 'OpenEventLog_|EventLog'` and `GOOS=windows go vet ./core/` — expect PASS and clean vet; existing event-log tests still pass (update any test that asserted `parent_id: null` on a non-first event).

- [ ] **Step 5: Commit** `feat: reopenable locked event log with parent_id chain`

---

### Task 2: Events that make a session replayable

**Files:**
- Create: `core/wire.go`
- Modify: `core/events.go:17-26` (constants), `core/loop.go` (`logAssistant`, `Run`)
- Test: `core/wire_test.go`, `core/loop_test.go` (add)

**Interfaces:**
- Produces:
  - constants `EvUserMessage = "user_message"`, `EvTurnStarted = "turn_started"`, `EvTurnEnded = "turn_ended"`, `EvSessionResumed = "session_resumed"`, `EvModelChanged = "model_changed"`.
  - `type wireBlock struct { Type string \`json:"type"\`; Text string \`json:"text,omitempty"\`; ToolUseID string \`json:"tool_use_id,omitempty"\`; Name string \`json:"name,omitempty"\`; Input json.RawMessage \`json:"input,omitempty"\`; IsError bool \`json:"is_error,omitempty"\` }`
  - `func toWire(bs []providers.ContentBlock) []wireBlock`, `func fromWire(ws []wireBlock) []providers.ContentBlock`.
  - Payloads: `user_message {content: []wireBlock, steer?: true}`; `assistant_message` keeps `text` and `final` and **adds** `content: []wireBlock` (text and tool_use blocks); `turn_started {}`; `turn_ended {status, exit_code, reason, turns, prompt_tokens, completion_tokens}` (turns/tokens for this user message); `session_resumed` = same fields as `session_started`.

- [ ] **Step 1: Write the failing tests.**
  - `TestWire_RoundTrip` (core/wire_test.go): text, tool_use with `Input {"path":"a"}`, tool_result with `IsError` round-trip through `json.Marshal(toWire(x))` → unmarshal → `fromWire` equal to `x`.
  - `TestRun_LogsReplayableEvents` (core/loop_test.go): fake provider turn 1 calls `ls`, turn 2 answers "done"; read the log; event types in order are `session_started, user_message, turn_started, assistant_message, …tool events…, assistant_message, turn_ended, session_ended`; `user_message.content[0].text` is the prompt; the first `assistant_message.content` holds the `tool_use` block with its id and input; `turn_ended.status == "ok"`.

- [ ] **Step 2: Run** `go test ./core/ -run 'Wire_|LogsReplayable' -v` — expect FAIL (undefined `toWire`; no `user_message`).

- [ ] **Step 3: Implement** the constants and `wire.go`; in `loop.go` log `user_message` (Append) after `session_started`, `turn_started` before the loop, `turn_ended` before `session_ended`, and `content: toWire(msg.Content)` in `logAssistant`. (Task 3 moves this into `Session`; doing it here first keeps the change reviewable.)

- [ ] **Step 4: Run** `go test ./... ` — expect PASS. Tests that compare an exact event-type sequence (e.g. in `core/loop_test.go`, `cmd/jas/run_test.go`) are updated to include the new events; no existing event or field disappears.

- [ ] **Step 5: Commit** `feat: log user messages, full assistant content and turn boundaries`

---

### Task 3: `core.Session` with multi-turn `Send` and steering

**Files:**
- Create: `core/session.go`
- Modify: `core/loop.go` (loop/turn/streamFailed/elide/recordUsage/finish move to `*Session`; `Run` becomes a wrapper)
- Test: `core/session_test.go`

**Interfaces:**
- Consumes: Task 2 events and `toWire`.
- Produces:
  ```go
  type SessionConfig struct {
      Provider providers.Provider
      Model    string
      System   string
      Exec     *Executor
      Log      EventLog
      UI       UI
      MaxTurns int            // per user message; <=0 → DefaultMaxTurns
      Budget   *Budget
      Meta     map[string]any // merged into session_started / session_resumed
  }
  type TurnResult struct {
      Status string // "ok" | "context_full" | "turn_cap" | "approval_needed" | "interrupted" | "failed"
      Code   int    // the Exit* code jas run would exit with
      Reason string
      Turns  int    // model requests for this user message
      Usage  providers.Usage
      Final  string
  }
  func NewSession(c SessionConfig) (*Session, error)            // writes session_started (AppendSync)
  func (s *Session) Send(ctx context.Context, text string) (TurnResult, error)
  func (s *Session) Steer(text string)                          // safe from any goroutine
  func (s *Session) Messages() []providers.Message              // copy
  func (s *Session) SetModel(model string)                      // used by /model later; logged as a "model_changed {model}" event
  func (s *Session) Close(status string, code int) error        // writes session_ended (AppendSync) with session totals; idempotent
  ```
  `Run(ctx, RunConfig)` keeps its signature and results: `NewSession` → `Send(prompt)` → `Close(res.Status, res.Code)` → same `Summary`/`*ExitError` mapping as today.

- [ ] **Step 1: Write the failing tests** in `core/session_test.go` (fake provider, temp log, quiet UI):
  - `TestSession_SecondSendSeesHistory`: send "a" (fake answers "A"), send "b"; the second provider request's messages are `[user a, assistant A, user b]`; the log has one `session_started` and two `user_message`/`turn_started`/`turn_ended` groups.
  - `TestSession_TurnCapPerMessage`: `MaxTurns: 2`; first Send hits the cap (`Status "turn_cap"`), the next Send still runs (fresh cap) and returns `"ok"`.
  - `TestSession_CancelThenSendAgain`: provider turn blocks until ctx cancel after emitting a tool call; cancel; result `Status "interrupted"`; then `Send("next")` succeeds and the request it sends has a tool_result (cancelled) for every tool_use in history.
  - `TestSession_SteerDeliveredAfterToolResults`: turn 1 calls `ls`; `Steer("also X")` is called from the fake's `OnEmit`; the second request's last two messages are the tool-results message and `user "also X"`; log has `user_message` with `steer: true`.
  - `TestSession_SteerAtFinalStartsNewTurn`: fake answers final text and, via `OnEmit`, `Steer("more")`; `Send` does not return after the first final: it logs `turn_ended ok`, then `user_message(steer) / turn_started` and makes another request ending in `"more"`; the returned `TurnResult` is the last turn's.
  - `TestSession_CloseWritesSessionEndedOnce`: `Close` twice → exactly one `session_ended`.
  - Existing `core/loop_test.go` `Run` tests stay green unchanged except the event-sequence updates from Task 2.

- [ ] **Step 2: Run** `go test ./core/ -run 'Session_' -v` — expect FAIL (undefined `NewSession`).

- [ ] **Step 3: Implement.** Steering queue: `mu sync.Mutex; queue []string`. Delivery points inside `Send`'s loop: (a) right after the tool-results message is appended, drain the queue into one user message (texts joined with `"\n\n"`), log it as `user_message {steer: true}`; (b) when the model returns a final answer and the queue is non-empty, log `turn_ended`, drain into a steer `user_message`, log `turn_started`, reset the per-message turn counter and usage, and continue the loop. Per-message state (turn count, usage, final) lives in a local struct created per Send; session totals accumulate for `Close`. Replace `runState` with `*Session`; keep `classify`, `cancelUses`, `textOf` as they are.

- [ ] **Step 4: Run** `go test -race ./core/ ./cmd/jas/` — expect PASS.

- [ ] **Step 5: Commit** `feat: multi-turn core.Session with steering; Run wraps it`

---

### Task 4: Replay a session file and resume it

**Files:**
- Create: `core/replay.go`
- Modify: `core/session.go` (add `ResumeSession`)
- Test: `core/replay_test.go`

**Interfaces:**
- Consumes: Task 1 lock/ids, Task 2 payloads, Task 3 `SessionConfig`.
- Produces:
  ```go
  type ReplayState struct {
      Messages []providers.Message
      Model    string   // last session_started/session_resumed/model_changed model
      Root     string   // session_started.root
      Notices  []string // e.g. "ignored unreadable line 41"
  }
  func Replay(path string) (ReplayState, error)                    // error only if the file can't be opened or has no session_started
  func ResumeSession(c SessionConfig, st ReplayState) (*Session, error) // writes session_resumed (AppendSync); Messages = st.Messages; System and Model come from c (the caller rebuilds the system prompt from current trust — never from the file)
  type SessionInfo struct {
      Path         string
      ID           string    // directory name
      Started      time.Time // session_started ts
      FirstMessage string    // first user_message text, one line, ≤80 runes
  }
  func ListSessions(sessionsDir, root string, limit int) ([]SessionInfo, error) // newest first by file mtime; only sessions whose root equals root (symlink-resolved)
  ```
  Replay rules: `user_message` → user message from `content`; `assistant_message` → assistant message from `content` (events without `content`, from older jas versions, are skipped and add one notice); `tool_result` events after an assistant message → one user message of tool_result blocks in the assistant's tool_use order, `IsError = status != "ok"`; a tool_use with no result → synthesized result with `cancelledOutput`-style text `"interrupted"` and `IsError: true`; unparseable lines → notice, skipped; a missing `session_ended` is normal.

- [ ] **Step 1: Write the failing tests** in `core/replay_test.go` (generate files by running a real `Session` with the fake provider where possible; hand-write lines only for crash cases):
  - `TestReplay_RoundTripMatchesSession`: run a session with two Sends (one using a tool); `Replay(path).Messages` deep-equals `sess.Messages()`.
  - `TestReplay_TruncatedLastLine`: valid session + a partial final line → messages from the valid part, one notice, no error.
  - `TestReplay_NoSessionEnded`: a file cut right after a `turn_ended` replays fine.
  - `TestReplay_OrphanToolUseGetsInterruptedResult`: file ends after an `assistant_message` with a tool_use and its `tool_started`; replayed messages end with a user message holding a tool_result for that id, `IsError: true`.
  - `TestResumeSession_ContinuesFile`: replay, `OpenEventLog` the same path, `ResumeSession`, `Send("next")`; the provider request contains the replayed history then `next`; the file has one `session_started`, one `session_resumed`, ids continue without duplicates.
  - `TestListSessions_FiltersByRootNewestFirst`: three session dirs (two for root A, one for B, different mtimes) → for A, two entries, newest first, `FirstMessage` set.

- [ ] **Step 2: Run** `go test ./core/ -run 'Replay_|ResumeSession_|ListSessions_' -v` — expect FAIL (undefined `Replay`).

- [ ] **Step 3: Implement** `Replay` with a line scanner (`bufio.Scanner` with a buffer large enough for big tool outputs, e.g. 16 MiB max token, so a long line is not mistaken for a broken one), decoding each line into a struct holding the header fields plus the payload fields used above; `ListSessions` reads only up to the first `user_message` of each file.

- [ ] **Step 4: Run** `go test -race ./...` and `GOOS=windows go vet ./...` — expect PASS / clean.

- [ ] **Step 5: Commit** `feat: replay and resume session files`

---

### Task 5: Document the event contract

**Files:**
- Modify: `docs/design.md` (event table near "Event types (v1 contract)")

- [ ] **Step 1:** Add rows for `user_message`, `turn_started`, `turn_ended`, `session_resumed`, `model_changed`; note `assistant_message.content`; change `session_started` to "once per file, first" and `session_ended` to "once each time the session is closed; absent after a crash"; state that `parent_id` is the previous event's id (linear chain) and that ids continue across reopen; state that a session file is locked while open.
- [ ] **Step 2:** Run `go test ./...` (docs only; sanity) — PASS.
- [ ] **Step 3: Commit** `docs: session events and session file contract`

---

## Later plans (not in this plan)

- **Plan 2 — Inline TUI chat** (spec step 3): reasoning stream in the provider (+ the GLM live test on sending reasoning back), Bubble Tea inline UI, input/steer queue display, Esc/Ctrl-C, approvals through the chat input with `always` grants (shell split rules), slash commands except `/compact`, `jas` with no args, `jas --continue` / `/resume` using `ListSessions` + `Replay` + `ResumeSession`, light Markdown.
- **Plan 3 — Compaction** (spec step 4), including replay from the latest `compaction` event.
- **Plan 4 — Customization** (spec step 5).
