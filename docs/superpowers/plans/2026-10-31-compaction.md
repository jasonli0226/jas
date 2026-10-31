# Compaction (Plan 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Long chats don't stop at "context window full". When context tokens exceed `window − compact_reserve_tokens`, jas summarizes older messages into a structured summary and keeps about the last `compact_keep_tokens` verbatim. `/compact [focus]` does the same on demand, and a resumed session replays from its latest compaction.

**Architecture:** Compaction lives in `core` as a `Session` capability, enabled by `SessionConfig.Compact` (nil means off). The chat turns it on and `jas run` doesn't, so `jas run` keeps its behaviour and exit codes. To record `first_kept_id`, the session needs the event ids of the messages it logs, so `EventLog` gains `AppendID`. The summary is a separate provider request with no tools. It is stored as a `compaction` event, and replay rebuilds history as the summary followed by every message from `first_kept_id` on.

**Tech Stack:** Go 1.27. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-30-interactive-mode-design.md`: decision 3 (Compaction), the last bullet of decision 4 (replay from the latest compaction), and build step 4.

## Global Constraints

- `CGO_ENABLED=0`, Linux/macOS/Windows. Every task ends with `go test -race ./...` and `GOOS=windows go vet ./...` passing.
- `jas run` and `jas run --json` keep their behaviour, output and exit codes. `jas run` does not enable compaction, and still ends with exit 3 "context window full".
- The trigger is context tokens > `window − compact_reserve_tokens`. Keep about the last `compact_keep_tokens` tokens. Cut only at user/assistant boundaries, never at a tool result.
- Settings and defaults: `compact_reserve_tokens` (16384), `compact_keep_tokens` (20000), `compact_model` (empty = session model).
- The summary is structured, with the sections Goal / Constraints & Preferences / Progress / Key Decisions / Next Steps / Critical Context, followed by read and modified files. It iterates on a previous summary. Tool results are clipped to 2000 chars in the summary input. A summary response that tries to call a tool is rejected.
- Stored as a `compaction` event `{summary, first_kept_id, tokens_before, tokens_after, usage}`. `tokens_after` is an additive field. The original messages stay in the file.
- Existing tool-result elision stays as the cheap first stage. Auto-compaction runs at the start of each turn (that is, between turns and before a new user message's first request). A provider context-overflow error (`providers.ErrContextLength`) triggers one compact-and-retry.
- Every payload goes through the redacting `EventLog`. The design.md event table documents `compaction`.
- Prompt caching: compaction is the one deliberate exception to byte-identical history.

## Review Focus

1. **Nothing to cut:** the history is one huge user message, or every candidate cut lands on a tool result. Compaction is skipped and the turn ends `context_full` with a clear reason, never a crash or loop. (Task 2: `TestCutIndex_NoValidCut`; Task 3: `TestSession_AutoCompactNothingToCutIsContextFull`.)
2. **Repeated compaction and resume:** a session compacted twice, then resumed, replays from the latest compaction only, and its summary request includes the previous summary. (Task 4: `TestReplay_UsesLatestCompaction`; Task 2: `TestSummaryRequest_IteratesOnPrevious`.)
3. **Esc during the summary request:** history is unchanged, no `compaction` event is logged, and the status is interrupted. (Task 3: `TestSession_CompactCancelledLeavesHistory`.)
4. **The summary model calls a tool:** rejected, history unchanged, the turn ends `context_full` with reason `compaction failed: summary tried to call a tool`. (Task 3: `TestSession_CompactRejectsToolCall`.)
5. **Overflow again after compacting:** at most one compact-and-retry per request, then `context_full`. (Task 3: `TestSession_OverflowRetriesOnce`.)

---

### Task 1: Event ids for live messages

**Files:**
- Modify: `core/events.go` (`EventLog`, `fileLog`, `redactingLog`), `core/ui.go` (`uiLog`), `core/session.go` (`addUser`, `logAssistant`, `ResumeSession`, a new `ids` field), `core/budget.go` (`Rebase`)
- Modify: test doubles that wrap `EventLog` and override `Append` (`grep -rn 'EventLog' --include=*_test.go`). Each must also override `AppendID`, so hooks still fire.
- Test: `core/events_test.go`, `core/session_test.go`, `core/budget_test.go`

**Interfaces:**
- Produces:
  - `EventLog` gains `AppendID(typ string, data any) (id string, err error)`. It works like `Append` and returns the written event's id, for example `e000007`. `redactingLog` and `uiLog` implement it with the same redaction and UI forwarding as `Append`.
  - `Session` keeps `ids []string` alongside `messages`, one per message. User and assistant messages hold their event id; tool-results messages hold `""`. `addUser` and `logAssistant` log with `AppendID`.
  - `ResumeSession` seeds `ids` from `st.MessageIDs`, padding or truncating to `len(st.Messages)`.
  - `func (b *Budget) Rebase()` forgets the last observation and the elision hysteresis (`obsTokens`, `obsMsgs`, `lastAfter` set to 0), so `Count` re-estimates after history is replaced.

- [ ] **Step 1: Write failing tests**
  - `TestEventLog_AppendIDReturnsWrittenID`: two `AppendID` calls return `e000001` and `e000002`. The file's events have those ids.
  - `TestSession_TracksMessageEventIDs`: a run with one tool call and then a final answer. The session's ids, read through an exported test hook `MessageIDs()` (add `func (s *Session) MessageIDs() []string`, which returns a copy), equal `[user_message id, assistant id, "", assistant id]` taken from the log file.
  - `TestResumeSession_SeedsIDs`: after replay and resume, `MessageIDs()` equals `st.MessageIDs` for user and assistant positions.
  - `TestBudget_RebaseForgetsObservation`: `Observe(5000, 3)` then `Rebase()`. `Count(msgs)` equals the pure byte estimate.
- [ ] **Step 2: Run** `go test ./core/ -run 'AppendID|TracksMessageEventIDs|SeedsIDs|Rebase' -v` and expect FAIL.
- [ ] **Step 3: Implement.** `logReq.done` carries `{id string; err error}`, and `handle` returns the id it wrote (`""` for a noop sync).
- [ ] **Step 4: Run** `go test -race ./...` and expect PASS, with existing hook tests still green.
- [ ] **Step 5: Commit** `feat: event ids for live session messages`

---

### Task 2: Compaction building blocks (pure)

**Files:**
- Create: `core/compact.go`
- Test: `core/compact_test.go` (package `core`, since these are unexported)

**Interfaces:**
- Produces:
```go
type CompactConfig struct {
	Reserve int    // compact when Count > threshold(window); compact_reserve_tokens
	Keep    int    // tokens kept verbatim; compact_keep_tokens
	Model   string // summary model; "" = session model
}
const (
	DefaultCompactReserve = 16384
	DefaultCompactKeep    = 20000
	summaryToolClip       = 2000
	summaryPrefix         = "[jas] The earlier conversation was compacted. Summary:\n\n"
)
func compactThreshold(window, reserve int) int // max(window-reserve, window/2)
func cutIndex(msgs []providers.Message, keepTokens int) int
func isSummary(m providers.Message) (summary string, ok bool)
func summaryMessage(summary string) providers.Message // user text: summaryPrefix + summary
func summaryRequest(model string, msgs []providers.Message, prev, focus string) providers.Request
func fileLists(prev string, msgs []providers.Message) (read, modified []string)
func withFileLists(summary string, read, modified []string) string
```

Rules:
- **`cutIndex`** returns the index of the first message kept verbatim, or 0 when there is nothing to compact.
  - Walk from the end and add up each message's estimated tokens (bytes/4, same estimate as `Budget`). The candidate is the smallest index where the sum from that index to the end is ≥ `keepTokens`. If the whole history is smaller than `keepTokens`, return 0.
  - Move the candidate **backwards** to the nearest index whose message is not a tool-results message (a user message with only `tool_result` blocks).
  - Index 0, or index 1 when `msgs[0]` is a previous summary, means nothing to compact, so return 0.
- **`summaryRequest`** builds a request with `Tools: nil` and this exact system text: `You summarize a coding-agent conversation so it can continue in a fresh context. Do not call tools. Reply with the summary only.`
  - It has one user message: `<conversation>\n` + the transcript + `\n</conversation>\n\n`.
  - When `prev` is non-empty, add `Previous summary to update:\n<previous-summary>\n` + prev + `\n</previous-summary>\n\n`.
  - Then these instructions:
    ```
    Write a structured summary with these sections:
    ## Goal
    ## Constraints & Preferences
    ## Progress
    ## Key Decisions
    ## Next Steps
    ## Critical Context
    Keep exact file paths, commands, error messages and identifiers. Be concise.
    ```
  - When `focus` is non-empty, add `\nPay special attention to: ` + focus.
  - Transcript lines, one per block:
    - user text: `[user]: <text>`
    - assistant text: `[assistant]: <text>`
    - tool use: `[tool call] <name>(<compact JSON input>)`
    - tool result: `[tool result]: <text cut to 2000 chars, then "…[clipped]">`
- **`fileLists`** collects `path` arguments from tool_use blocks in `msgs`: read, ls and grep add to read; write and edit add to modified. It also takes the lists parsed from `prev`'s `<read-files>` and `<modified-files>` blocks.
  - Both lists are de-duplicated and sorted, and a path that appears in modified is dropped from read.
- **`withFileLists`** appends `\n\n<read-files>\n<one path per line>\n</read-files>` and `\n<modified-files>\n…\n</modified-files>`, each only when non-empty. It first removes any such blocks already in `summary`.

- [ ] **Step 1: Write failing tests**
  - `TestCompactThreshold`: `(200000, 16384)` → 183616. `(20000, 16384)` → 10000.
  - `TestCutIndex_KeepsTailAndSkipsToolResults`: messages `[u0, a1(tool_use), r2(results), a3, u4, a5]` sized so the tail sum reaches keepTokens at index 2. The result is 1, moved back from the tool-results message to the assistant.
  - `TestCutIndex_NoValidCut`: a single huge user message → 0. `[summary, u1]` with keep larger than u1 → 0.
  - `TestSummaryRequest_Shape`: no tools, the exact system text, the tool result clipped at 2000 chars with `…[clipped]`, and the focus line present.
  - `TestSummaryRequest_IteratesOnPrevious`: with `prev="OLD"`, the user text contains `<previous-summary>\nOLD\n</previous-summary>`.
  - `TestFileLists_MergesAndPrefersModified`: prev lists `a.go` as read. The messages read `b.go` and edit `a.go`, so read is `[b.go]` and modified is `[a.go]`.
  - `TestWithFileLists_ReplacesOldBlocks`: applying the lists twice leaves one block of each.
- [ ] **Step 2: Run** `go test ./core/ -run 'CompactThreshold|CutIndex|SummaryRequest|FileLists|WithFileLists' -v` and expect FAIL.
- [ ] **Step 3: Implement** in `core/compact.go`, under 250 lines.
- [ ] **Step 4: Run** `go test -race ./core/` and expect PASS.
- [ ] **Step 5: Commit** `feat: compaction cut point, summary request and file lists`

---

### Task 3: Session compaction (manual, auto, overflow retry)

**Files:**
- Modify: `core/session.go` (`SessionConfig.Compact`, `turn`, a new `Compact` method), `core/events.go` (`EvCompaction = "compaction"`)
- Create: `core/session_compact.go`
- Test: `core/session_compact_test.go`

**Interfaces:**
- Consumes: Task 1 (`AppendID`, `ids`, `Budget.Rebase`) and Task 2 (everything in it).
- Produces:
```go
// SessionConfig
Compact *CompactConfig // nil: compaction off (jas run)

type CompactResult struct {
	Messages     int // messages replaced by the summary
	TokensBefore int
	TokensAfter  int
	Usage        providers.Usage
}
var ErrNothingToCompact = errors.New("nothing to compact")
func (s *Session) Compact(ctx context.Context, focus string) (CompactResult, error)
```

Behaviour of `Compact`:
1. Error if `c.Compact == nil` or `c.Budget == nil`.
2. Find the previous summary with `isSummary(messages[0])`. Then `cut := cutIndex(messages, Keep)`. If `cut == 0`, return `ErrNothingToCompact`.
3. The summarized range is `messages[start:cut]`, where start is 1 when there was a previous summary and 0 otherwise. Stream `summaryRequest(model, range, prev, focus)` with model `Compact.Model`, or the session model when that is empty.
   - Stream errors are returned wrapped as `compaction failed: <err>`. A cancelled ctx returns `ctx.Err()`.
   - Any tool call in the response returns `errors.New("compaction failed: summary tried to call a tool")`.
   - Empty summary text returns `compaction failed: empty summary`.
4. On success:
   - Build `summary := withFileLists(text, fileLists(prev, range))`.
   - Set `messages = [summaryMessage(summary)] + messages[cut:]` and `ids = [compaction event id] + ids[cut:]`.
   - Call `Budget.Rebase()` and add the usage to the session totals (not to Budget.Observe).
   - Log `AppendID(EvCompaction, {"summary", "first_kept_id": old ids[cut], "tokens_before", "tokens_after", "usage": {"prompt_tokens", "completion_tokens"}})`.
   - `tokens_before` is `Budget.Count` before the change and `tokens_after` is `Budget.Count` after `Rebase`.
   - If `old ids[cut]` is `""`, which can happen for a session resumed from an old file, return `compaction failed: kept message has no event id` without changing anything.
5. Reasoning deltas from the summary stream go nowhere. Text deltas don't go to `UI.Text`.

Auto and overflow behaviour in `turn`:
- Run `s.elide()` first (it already exists). Then, at the start of `turn` and before the existing `WouldExceed` check: if `c.Compact != nil` and `Budget.Count(messages) > compactThreshold(Window, Reserve)`, call `Compact(ctx, "")`.
  - `ErrNothingToCompact` falls through to the existing checks.
  - A cancelled ctx returns interrupted.
  - Any other error returns `context_full` with reason `<err>`.
- When the stream fails with `errors.Is(err, providers.ErrContextLength)` and `c.Compact != nil` and this request hasn't been retried: call `Compact(ctx, "")`, and on success redo the request once.
  - A second overflow, or a failed compaction, ends the turn `context_full` with reason `context window full after compaction` or `<err>`.

- [ ] **Step 1: Write failing tests.** Use the fake provider. `fake.Turn.Err` already lets a turn fail, for example with `providers.ErrContextLength`, and `fake.Provider.Requests` records every request. Use a small window (for example 2000) and reserve and keep values that force the trigger.
  - `TestSession_CompactReplacesHistory`: after 3 exchanges, `Compact(ctx, "")` returns a result.
    - History is `[summary, …kept]`. The summary text starts with the summary prefix and contains the fake's summary reply.
    - The log has one `compaction` event whose `first_kept_id` equals the event id of the first kept message, and whose `tokens_after` is less than `tokens_before`.
  - `TestSession_AutoCompactBeforeRequest`: a long history over the threshold, then `Send`. The provider sees the summary request first (no tools), then a normal request whose messages start with the summary message.
  - `TestSession_AutoCompactNothingToCutIsContextFull`: a single huge user message gives status `context_full` and no compaction event.
  - `TestSession_CompactCancelledLeavesHistory`: the summary turn blocks, then ctx is cancelled. History is unchanged, there is no compaction event, and `Send` returns interrupted.
  - `TestSession_CompactRejectsToolCall`: the summary turn returns a tool call. History is unchanged and the reason is `compaction failed: summary tried to call a tool`.
  - `TestSession_OverflowRetriesOnce`: the first normal request fails with `ErrContextLength`, the fake answers the summary request, and the retried request succeeds, giving status ok. A second variant overflows twice and gets `context_full` with reason `context window full after compaction`.
  - `TestSession_CompactOffKeepsContextFull`: with `Compact == nil` over the window, the status stays `context_full`. This is `jas run`'s behaviour.
- [ ] **Step 2: Run** `go test ./core/ -run 'Compact|Overflow' -v` and expect FAIL.
- [ ] **Step 3: Implement** in `core/session_compact.go`, keeping `turn`'s changes small. Map the existing `ErrContextLength` handling in `streamFailed` onto the retry.
- [ ] **Step 4: Run** `go test -race ./...` and expect PASS.
- [ ] **Step 5: Commit** `feat: session compaction with auto trigger and overflow retry`

---

### Task 4: Replay from the latest compaction

**Files:**
- Modify: `core/replay.go` (`replayLine` gets `Summary` and `FirstKeptID` with json `summary` and `first_kept_id`; the `replayer` handles `EvCompaction`), `docs/design.md` (event table row for `compaction`)
- Test: `core/replay_test.go`

**Interfaces:**
- Consumes: `summaryMessage` (Task 2), `EvCompaction` (Task 3).
- Produces: on a `compaction` event, the replayer:
  1. Flushes pending results.
  2. Finds `first_kept_id` in `st.MessageIDs`.
  3. Sets `Messages = [summaryMessage(summary)] + Messages[idx:]` and `MessageIDs = [compaction event id] + MessageIDs[idx:]`.
  - If the id isn't found, it adds the notice `ignored compaction at line N: first kept message not found` and keeps the history.

- [ ] **Step 1: Write failing tests**
  - `TestReplay_CompactionRoundTrip`: a session compacts through `Session.Compact` and continues one more turn, then closes. `Replay(...).Messages` equals the live `Messages()`.
  - `TestReplay_UsesLatestCompaction`: two compactions in one file. The replayed history starts with the second summary, and `MessageIDs[0]` is the second compaction's id.
  - `TestReplay_CompactionUnknownKeptID`: a hand-written `compaction` event with `first_kept_id: "e999999"` keeps the full history and adds the notice.
  - `TestResumeSession_AfterCompactionCanCompactAgain`: resume a compacted file, then `Compact`. The second summary request's user text contains `<previous-summary>`.
- [ ] **Step 2: Run** `go test ./core/ -run 'Compaction|AfterCompaction' -v` and expect FAIL.
- [ ] **Step 3: Implement**, and add the design.md row: `| compaction | summary, first_kept_id, tokens_before, tokens_after, usage | when history is compacted (chat only) |`.
- [ ] **Step 4: Run** `go test -race ./...` and expect PASS.
- [ ] **Step 5: Commit** `feat: replay sessions from their latest compaction`

---

### Task 5: Settings, chat wiring and `/compact`

**Files:**
- Modify: `settings/config.go` (keys), `settings/run.go` (fields on the built `Run`), `cmd/jas/chat.go` (backend sets `Compact`), `cmd/jas/setup.go` if the env carries cfg
- Modify: `tui/commands.go` (`/compact`), `tui/transcript.go` (compaction row), `tui/run.go` (status after compaction), `tui/backend_test.go` (test backend sets `Compact`), `README.md`, `docs/design.md` (settings list)
- Test: `settings/config_test.go`, `cmd/jas/chat_test.go`, `tui/commands_test.go`, `tui/transcript_test.go`

**Interfaces:**
- Consumes: `core.CompactConfig`, `Session.Compact`, `ErrNothingToCompact`, `EvCompaction` and its payload (Tasks 2–4).
- Produces:
  - Settings keys (not flags):
    - `compact_reserve_tokens`, default `16384`, `positiveInt`.
    - `compact_keep_tokens`, default `20000`, `positiveInt`.
    - `compact_model`, default empty, any value.
  - `settings.Run` gains `CompactReserve`, `CompactKeep` and `CompactModel`.
  - The chat backend's sessions (new and resumed) get `Compact: &core.CompactConfig{Reserve, Keep, Model}`. `runCmd` leaves it nil.
  - `/compact [focus]` is idle-only, like `/new`.
    - It prints `compacting…` (dim) and runs `Session.Compact(ctx, focus)` on a goroutine with a cancellable ctx. Esc cancels it, like a run.
    - On success it prints `compacted <n> messages · <before> → <after> tok`, with token counts formatted like the status line.
    - `ErrNothingToCompact` prints `nothing to compact yet`. Other errors print `compaction failed: <err>` (redacted and sanitized).
  - Transcript: a `compaction` event prints the dim row `· compacted earlier messages (<before> → <after> tok)`. This covers auto-compaction during a run.
  - Status line: on a `compaction` event, set the last prompt tokens to `tokens_after`, so ctx% drops right away.

- [ ] **Step 1: Write failing tests**
  - `TestKeys_CompactDefaults`: `Resolve(nil, env, File{}).Build()` gives 16384, 20000 and `""`. `compact_keep_tokens=0` is rejected.
  - `TestChatBackend_EnablesCompaction` and `TestRunCmd_NoCompaction`: the chat backend's session config has a non-nil `Compact` with the configured values. The `jas run` path's is nil. Expose the config through a small test seam on `chatBackend`, the same way existing chat tests inspect meta.
  - `TestChat_CompactCommand`: on a test-backend session with three exchanges and a small window, `/compact keep the API notes` makes the summary request contain `Pay special attention to: keep the API notes`. Printed output then contains `compacted ` and the status ctx% has dropped.
  - `TestChat_CompactNothingYet`: a fresh session → `nothing to compact yet`.
  - `TestChat_CompactRefusedWhileRunning`: → `wait for the reply or press Esc first`.
  - `TestTranscript_CompactionRow`: the event `{tokens_before: 150000, tokens_after: 18000}` → `· compacted earlier messages (150.0k → 18.0k tok)`.
- [ ] **Step 2: Run** `go test ./settings/ ./cmd/jas/ ./tui/ -run 'Compact' -v` and expect FAIL.
- [ ] **Step 3: Implement.** Remove `/compact` from the unknown-command path, and add it to `/help` and the `/` completion list.
- [ ] **Step 4: Run** `go test -race ./...`, `GOOS=windows go vet ./...` and `CGO_ENABLED=0 go build ./cmd/jas`. Expect all to pass.
- [ ] **Step 5: Commit** `feat: compaction settings, chat wiring and /compact`
