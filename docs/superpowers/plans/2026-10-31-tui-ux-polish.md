# Inline TUI UX Polish Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the inline chat readable and resume-friendly without leaving Bubble Tea inline mode. Owner priority: user vs agent turns must be obvious in scrollback. Then resume reprints the transcript, tools show while running, streaming no longer flashes blank, slash picker and input history match daily use, and the status line carries denser cues.

**Architecture:** Stay in the existing `tui/` package and `tea.Println` scrollback model from Plan 2 (`2026-10-31-inline-tui-chat.md`). Role chrome and turn gaps live in `Transcript` (+ `style.go`). Resume reprint walks `Session.Messages()` after open and prints through the same chrome helpers. Live tool rows extend `Transcript.Event` for `tool_started`. Streaming flash is fixed in the live → Println handoff in `run.go` / chat View. Slash listing reuses `picker`; input history is a small ring on the model. Status densifies `status.go` only. No alt-screen, no new packages, no new event types.

**Tech Stack:** Go 1.27, `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2` (versions already in `go.mod`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-30-interactive-mode-design.md` (look-and-feel; Plan 2 build). Issues: [#60](https://github.com/jasonli0226/jas/issues/60)–[#66](https://github.com/jasonli0226/jas/issues/66) (filed from the TUI UX review 2026-10-31 against `e793f7c`). Prior plan: `docs/superpowers/plans/2026-10-31-inline-tui-chat.md`.

## Global Constraints

- Single static binary, `CGO_ENABLED=0`, Linux/macOS/Windows. Every task ends green on `go test -race ./tui/ ./cmd/jas/` (and preferably `go test -race ./...`) and `GOOS=windows go vet ./...`.
- **Inline only.** No alt-screen, no fullscreen Bubble Tea mode, no theme system or color-scheme arms race. One-glyph role markers + lipgloss colours are enough.
- `jas run` and `jas run --json` keep their behaviour, output and exit codes. This plan adds no event types and does not change the approval ladder or redaction.
- Everything shown on screen still passes through `core.NewRedactingUI`; every logged payload through the redacting `EventLog`. Chrome prefixes are jas-owned strings, not model text.
- Thinking stays hidden by default (`· thought for Ns`). System / notice lines stay dim with `· `.
- Steering, `/model`, `/thinking`, grants and level rules from Plan 2 stay as they are.
- Commits use `<type>: <description>` (feat, fix, refactor, docs, test, chore).
- Do **not** invent skills-as-orchestrator, farm UI, follow-up queues, `@file` / `!cmd` / image paste, or pipeline YAML in this pass. TUI polish only.

## Review Focus

1. **Role confusion:** after several turns (especially when the model uses bold headings), a reader must still tell user lines from agent lines in scrollback and after copy/paste. (Task 1: `TestTranscript_UserChrome`, `TestTranscript_AssistantChrome`.)
2. **Resume blank:** `--continue` / `/resume` must reprint prior user, assistant and tool turns with the same chrome, not only `resumed id · N messages`. (Task 2: `TestChat_ResumeReprintsTranscript`.)
3. **Late / missing tool feedback:** a long tool must show a running row on `tool_started`, not only the final row on `tool_result`. (Task 3: `TestTranscript_ToolStartedShowsRunningRow`.)
4. **Streaming blank flash:** finished assistant blocks must not leave the live region empty before `Println` lands (cross-ref [#58](https://github.com/jasonli0226/jas/issues/58)). (Task 4: `TestChat_StreamingNoBlankFlash`.)

## Out of scope

- Fullscreen / alt-screen TUI, themes, left-border message boxes
- Follow-up queue, `@file` / `!cmd` / image paste
- Farm / worktree UI, session tree `/fork` UI
- Changing `jas run`, exit codes, approval levels, or redaction policy
- Skills orchestrating other skills or scenario `pipeline:` (separate design)

---

### Task 1: Role chrome + turn gaps (#60) — **do first**

**Issue:** https://github.com/jasonli0226/jas/issues/60

**Files:**
- Modify: `tui/transcript.go` (`User`, assistant block emit / `Flush` / turn boundaries)
- Modify: `tui/style.go` (`userStyle`, add `assistantStyle`; keep tool/system dim)
- Modify: `tui/run.go` if turn-gap or first-line chrome is applied at print time
- Test: `tui/transcript_test.go` (extend); touch `tui/chat_test.go` only if printed fixtures assert old unmarked assistant text

**Interfaces:**
- User marker stays `› `, styled **bold + cyan** (distinct from agent).
- Agent: first line of each assistant turn gets `● ` or `jas ` in a **muted accent** (e.g. magenta/white), not the user colour. Later lines of the same turn stay unmarked / indented consistently with today’s user multi-line indent pattern.
- Tools: keep `▸ `, faint.
- System / thinking / compacted / resumed notices: keep `· `, faint.
- Insert a **blank line between turns** (user ↔ assistant ↔ tool groups) so blocks don’t run together.
- Avoid full left borders or boxes (Println scrollback can’t rewrite; borders fight narrow terminals).

- [ ] **Step 1: Write failing tests**
  - `TestTranscript_UserChrome`: `User("hi")` after ANSI strip starts with `› hi`; style uses bold and a cyan-class colour (assert via lipgloss / known prefix + that `userStyle` is not equal to plain bold-only if you change it).
  - `TestTranscript_AssistantChrome`: feed a completed assistant paragraph through `Text`/`Flush`; after ANSI strip the first printed assistant line starts with `● ` or `jas `; a second turn gets the marker again; intra-turn continuation lines do not repeat the marker.
  - `TestTranscript_TurnGap`: user block then assistant flush → printed sequence has a blank line between the user block and the agent block.
  - Update any Plan 2 fixture that expected `["› hi", "Hello", …]` without agent chrome so expectations include the new marker.
- [ ] **Step 2: Run** `go test ./tui/ -run 'Transcript_(UserChrome|AssistantChrome|TurnGap)|Chat_SendStreams' -v`. Expect FAIL.
- [ ] **Step 3: Implement** styles and chrome in `transcript.go` / `style.go`; keep files under ~300 lines; all assistant text still goes through `cleanText` / markdown render as today.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `fix(tui): role chrome and turn gaps for readable transcript`

---

### Task 2: Resume transcript reprint (#61)

**Issue:** https://github.com/jasonli0226/jas/issues/61

**Depends on:** Task 1 (same chrome on live and resume).

**Files:**
- Modify: `tui/sessions.go` (after successful resume / `--continue`, before or with the `resumed …` notice)
- Possibly: `tui/transcript.go` helpers to render a historical `providers.Message` / tool row without streaming
- Test: `tui/chat_test.go` or `tui/sessions`-adjacent tests via test backend Resume

**Interfaces:**
- On `msg.resumed`, walk `msg.live.Session.Messages()` (or the replayed history the Live already holds) and `Println` user / assistant / tool turns using the **same** chrome as live chat.
- Keep the dim `resumed <id> · <n> messages` notice (after or before the reprint — pick one and test it; prefer notice after reprint so scrollback ends near the input).
- Seed input-history (Task 5) from prior user message texts when that task lands; for this task, at least collect user strings into a slice the model can adopt, or leave a clear TODO hook if history lands next — prefer seeding a `[]string` field now if cheap.
- Do not re-emit events into the log; display only.
- Generation / bridge rules unchanged: reprint is chat-owned Println, not bridge messages from an old gen.

- [ ] **Step 1: Write failing tests**
  - `TestChat_ResumeReprintsTranscript`: test backend with a saved session containing user `"hello"`, assistant `"hi there"`, and a completed tool result. `--continue` / Resume path → `Printed()` after ANSI strip contains user chrome for `hello`, agent chrome for `hi there`, and a `▸` tool row, plus the `resumed` notice.
  - `TestChat_ResumeEmptySessionStillNotices`: resume with no messages still prints `resumed … · 0 messages` and does not panic.
- [ ] **Step 2: Run** `go test ./tui/ -run 'ResumeReprint|ResumeEmpty' -v`. Expect FAIL.
- [ ] **Step 3: Implement** reprint in `sessions.go` using Transcript helpers; width from the model.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `fix(tui): reprint transcript on resume with role chrome`

---

### Task 3: Live tool_started rows (#62)

**Issue:** https://github.com/jasonli0226/jas/issues/62

**Files:**
- Modify: `tui/transcript.go` (`Event` `tool_started` branch — today it only records args)
- Modify: `tui/status.go` / chat status assembly optionally for `working · N tools`
- Test: `tui/transcript_test.go`, optionally `tui/chat_test.go`

**Interfaces:**
- On `tool_started`, print a **dim** running row (e.g. `▸ <tool> <subject> …` or `▸ <tool> <subject> running`) using the same subject rules as the final `toolRow`.
- On `tool_result`, print the final row as today (ok / failed / denied / cancelled). Prefer replacing the mental “running” line by printing the final row beneath it (Println can’t rewrite scrollback); document that two lines per tool is OK for v1, or only show running in `Live()` / status if you can do it without alt-screen — **owner preference: visible feedback during the call**. A dim running Println + final Println is acceptable.
- Optional status: while tools are in flight, append `working · N tools` (or keep `working…` and add the count).

- [ ] **Step 1: Write failing tests**
  - `TestTranscript_ToolStartedShowsRunningRow`: `Event("tool_started", {call_id, tool, args})` returns a row containing `▸` and the tool/subject; after ANSI strip it is non-empty before any `tool_result`.
  - `TestTranscript_ToolResultStillFinalRow`: matching `tool_result` still produces the existing outcome row (`ok` / `failed: …` etc.).
  - `TestToolRow_RunningTruncatesToWidth` (if running rows share `toolRow`): width 20 → ≤ 20 visible runes, ends in `…`.
- [ ] **Step 2: Run** `go test ./tui/ -run 'ToolStarted|ToolResultStill|RunningTruncat' -v`. Expect FAIL.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `fix(tui): show live tool rows on tool_started`

---

### Task 4: Streaming blank flash (#63)

**Issue:** https://github.com/jasonli0226/jas/issues/63 (elevates / completes [#58](https://github.com/jasonli0226/jas/issues/58))

**Files:**
- Modify: `tui/run.go`, `tui/chat.go` (View live region vs Println timing), possibly `tui/transcript.go` (`Live` / `Flush`)
- Test: `tui/chat_test.go`

**Interfaces:**
- When a block completes and is handed to `tea.Println`, keep that content in `Live()` (or an “pending print” buffer shown in View) until Println has been applied so the user never sees a blank gap between live stream and scrollback.
- Alternative acceptable approach: line-at-a-time prose flush that never clears live before print ack — pick one and test it.
- Cross-ref #58; close #58 when this lands if it fully covers that bug.

- [ ] **Step 1: Write failing test**
  - `TestChat_StreamingNoBlankFlash`: stream two completed markdown blocks; between Flush/Println of block 1 and appearance in `Printed()`, `View` (or `Live()` + pending) still contains block 1’s text — never both empty live and missing from Printed for that block.
- [ ] **Step 2: Run** `go test ./tui/ -run StreamingNoBlankFlash -v`. Expect FAIL.
- [ ] **Step 3: Implement** the hold-until-ack (or line-at-a-time) path without alt-screen.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `fix(tui): avoid blank flash between live stream and scrollback`

---

### Task 5: Fuzzy slash picker (#64)

**Issue:** https://github.com/jasonli0226/jas/issues/64

**Files:**
- Modify: `tui/commands.go` (completions / listing when `/` with no space), `tui/picker.go`, `tui/keys.go` / `tui/chat.go` as needed
- Test: `tui/commands_test.go`, `tui/picker_test.go`, `tui/chat_test.go`

**Interfaces:**
- When the input is `/` or `/partial` with **no space** (argument not started), show the existing fuzzy `picker` (or upgrade the plain completion list to the picker) over command + `skill:<name>` entries.
- Typing filters (case-insensitive substring, as picker already does); Up/Down move; Tab or Enter completes; Esc cancels back to the input.
- Keep unknown-command + `suggest` behaviour for submitted typos.
- Cap visible rows as today (picker max 10 / completions max 8 — align on one number and document it).

- [ ] **Step 1: Write failing tests**
  - `TestChat_SlashOpensPicker`: typing `/` idle opens a picker (or equivalent listing driven by picker) whose items include `help` and `model`.
  - `TestChat_SlashFilterAndTab`: input `/mo` filters to `model`; Tab/Enter completes to `/model` (or runs bare `/model` picker for models — match Plan 2 bare `/model` behaviour; completing the command name is enough).
  - `TestPicker_FilterAndChoose` remains green if already present.
- [ ] **Step 2: Run** `go test ./tui/ -run 'SlashOpens|SlashFilter|Picker_' -v`. Expect FAIL on new tests.
- [ ] **Step 3: Implement** by reusing `picker`, not a second UI.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `feat(tui): fuzzy slash command picker on /`

---

### Task 6: Up/Down input history (#65)

**Issue:** https://github.com/jasonli0226/jas/issues/65

**Files:**
- Modify: `tui/keys.go`, `tui/chat.go` (history ring on the model)
- Modify: `tui/sessions.go` to seed from resumed user messages (hook from Task 2)
- Test: `tui/chat_test.go`

**Interfaces:**
- While the input box is focused and no picker/approval is open, Up/Down walk a ring of prior submitted user texts (not slash-only noise unless you intentionally include commands — prefer all submitted lines including `/…`).
- Cap ~100 entries; consecutive duplicate submits collapse to one.
- Seed on resume from historical user messages (oldest→newest so Up gets the latest first).
- Empty input + Down is a no-op; browsing then editing creates a new draft that doesn’t corrupt the ring until submit.

- [ ] **Step 1: Write failing tests**
  - `TestChat_InputHistoryUpDown`: submit `a`, `b`; Up → `b`, Up → `a`, Down → `b`.
  - `TestChat_InputHistoryDedupeAndCap`: submitting the same text twice stores one entry; filling >100 still caps length at 100.
  - `TestChat_ResumeSeedsInputHistory`: resumed session with user msgs `one`,`two` → first Up shows `two`.
- [ ] **Step 2: Run** `go test ./tui/ -run 'InputHistory' -v`. Expect FAIL.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and expect PASS.
- [ ] **Step 5: Commit** `feat(tui): Up/Down input history seeded on resume`

---

### Task 7: Denser status line (#66)

**Issue:** https://github.com/jasonli0226/jas/issues/66

**Files:**
- Modify: `tui/status.go`, `tui/chat.go` (usage fields passed into status), optionally `tui/style.go` for ctx colour thresholds
- Test: `tui/status_test.go`

**Interfaces:**
- Extend the status line beyond `<model> · <level> · <ctx>% · <tokens> tok · <folder>`:
  - Show separate prompt/completion cues when known, e.g. `↑in ↓out` token counts from last turn or session totals (document which).
  - Colour the ctx% with simple thresholds (e.g. default / warn near compact reserve / hot when very high) via lipgloss; still truncate with `…` at narrow widths.
  - Thinking cue when a thought line is pending or ShowThinking is on (dim mark, not a second status row).
  - Optional: git branch when `Root` is a repo and read is cheap/cached — skip if it needs network or slows Init; OK to defer branch behind a build tag or best-effort `git rev-parse` with timeout. If skipped, note in the commit body.
- Keep `working…` / queued / press-Ctrl-C hints.

- [ ] **Step 1: Write failing tests**
  - `TestStatus_InOutAndCtx`: fixture with known prompt/completion and window → rendered line contains the new cues; at width 20 still ≤ 20 visible runes with `…`.
  - `TestStatus_CtxThresholdStyle`: high ctx% uses a distinct style from low ctx% (compare stripped vs styled, or expose a helper).
- [ ] **Step 2: Run** `go test ./tui/ -run 'Status_' -v`. Expect FAIL on new cases.
- [ ] **Step 3: Implement**.
- [ ] **Step 4: Run** `go test -race ./tui/ ./cmd/jas/` and `GOOS=windows go vet ./...`. Expect PASS.
- [ ] **Step 5: Commit** `feat(tui): denser status line with in/out and ctx cues`

---

## Done when

- [ ] Tasks 1–7 committed in order; #60–#66 can be closed with references to the commits (or left for the owner to close).
- [ ] `go test -race ./...` green; `CGO_ENABLED=0 go build ./cmd/jas` succeeds.
- [ ] Manual smoke (Linux terminal): several turns show distinct user vs agent chrome; `jas --continue` reprints history; a slow `shell` shows a running row; streaming doesn’t flash blank; `/` fuzzy picker works; Up recalls prior input; status shows denser cues.
