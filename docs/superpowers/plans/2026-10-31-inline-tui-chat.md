# Inline TUI Chat (Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `jas` with no arguments opens a pi-style inline chat. It has streaming replies, one-line tool rows, a status line, hidden thinking, steering, inline approvals with session `always` grants, slash commands (except `/compact`), and `jas --continue` / `/resume`.

**Architecture:** A new `tui/` package holds a Bubble Tea v2 model in inline mode. Finished transcript blocks are printed into scrollback with `tea.Println`. The live region holds the block still being written, any queued steering, the approval prompt or picker, the input box and the status line. The model reaches jas only through a `tui.Backend` interface that `cmd/jas` implements, so the chat runs in tests against the fake provider. Each core `Session` runs `Send` on its own goroutine. Its `core.UI` is a bridge that posts tagged messages into the program, and messages from an old session are dropped. Approvals go through a chat-level `core.Gate` (`tui.Gate`) that holds `core.Grants`, so grants outlive `/new`.

**Tech Stack:** Go 1.27, `charm.land/bubbletea/v2` v2.1.1, `charm.land/bubbles/v2` v2.2.1 (textarea), `charm.land/lipgloss/v2` v2.0.6. No Markdown or syntax-highlighting dependency.

**Spec:** `docs/superpowers/specs/2026-10-30-interactive-mode-design.md` (build step 3). Also read the "Carried into plan 2" list in `docs/superpowers/plans/2026-10-30-session-core.md`, because Task 1 and Task 8 close it.

## Global Constraints

- Single static binary, `CGO_ENABLED=0`, Linux/macOS/Windows. Every task ends green on `go test -race ./...` and `GOOS=windows go vet ./...`.
- `jas run` and `jas run --json` keep their behaviour, output and exit codes. This plan adds no event types.
- Everything shown on screen passes through `core.NewRedactingUI`, and every logged payload passes through the redacting `EventLog`. Nothing bypasses either.
- Approvals ask in exactly the situations `jas run` asks at the same level (never / shell / mutating). The prompt text is `approve <tool>: <summary>? [y/N/always]`.
- Thinking is hidden by default. At most one dim line is shown: `· thought for Ns`.
- Grants, the `/model` choice, the `/approve` level and `/thinking` last for the chat process and survive `/new`. `/resume` and `--continue` reset grants and the level to the configured ones. Nothing about grants is written to disk.
- Steering, `/model` and `/thinking` state live in the chat or the session, never in package globals (farm-readiness).
- Live provider tests use `z-ai/glm-*` or `deepseek/*` models only.
- Commits use `<type>: <description>` (feat, fix, refactor, docs, test, chore).

## Review Focus

1. **Late events from an old session**: a cancelled run's tool events can arrive after `/new` or `/resume`. They must not show up in the new transcript. (Task 6: `TestChat_DropsMessagesFromOldSession`.)
2. **Esc while an approval prompt is open**: the gate returns `Denied` with ctx's error and the prompt disappears. The next Enter must not answer a dead prompt. (Task 7: `TestChat_EscClosesApprovalPrompt`.)
3. **Multi-line paste**: a paste that contains newlines is inserted into the box and never submitted. (Task 6: `TestChat_PasteWithNewlinesDoesNotSubmit`.)
4. **Narrow terminal**: at width 20 the status line and tool rows are cut with `…` and never wrap into the input box. (Task 4: `TestToolRow_TruncatesToWidth`; Task 6: `TestStatus_TruncatesToWidth`.)
5. **Secrets in reasoning**: reasoning deltas are redacted before display, the same as text. (Task 2: `TestRedactingUI_RedactsReasoning`.)

---

### Task 1: Core follow-ups carried from plan 1

**Files:**
- Modify: `core/replay.go` (`flushResults`, `canonPath` callers in `ListSessions`)
- Create: `core/pathcase.go` (`samePath`)
- Modify: `core/session_test.go` (replace `TestSession_SteerNotCommittedAfterCancelAtFinal`)
- Test: `core/replay_test.go`, `core/pathcase_test.go`

**Interfaces:**
- Produces: `ReplayState.Interrupted` lists only the orphans of the **last** assistant message in the file. Orphans earlier in the file still get synthesized results in `Messages` but are not listed. `ListSessions` compares roots case-insensitively on windows and darwin.

- [ ] **Step 1: Write failing tests**
  - `TestReplay_OnlyTrailingOrphansAreInterrupted`: log user → assistant(tool_use c1) → user_message → assistant(tool_use c2), with no results for either. Expect `len(st.Interrupted)==1`, `st.Interrupted[0].ID=="c2"`, and `Messages` still holds synthesized results for both c1 and c2.
  - `TestSamePath_CaseRules`: `samePath("/A/b", "/a/B")` is `runtime.GOOS == "windows" || runtime.GOOS == "darwin"`, and `samePath("/a", "/a")` is true.
  - `TestSession_SteerAfterEscAtFinalIsNotCommitted`: fake turn `{Text: "done"}`. Use a log wrapper whose `Append` hook runs on `assistant_message`: it calls `cancel()` and then `s.Steer("late")`. Assert `res.Status == "ok"`, `res.Final == "done"`, that no message in `s.Messages()` contains `late`, and that `s.TakeSteering()` returns `["late"]`. Delete `TestSession_SteerNotCommittedAfterCancelAtFinal`.
- [ ] **Step 2: Run** `go test ./core/ -run 'TrailingOrphans|SamePath|SteerAfterEsc' -v`. Expect FAIL. If the session test passes immediately, confirm the hook fires after the stream completes and before `Send` drains. That window is the point of the test.
- [ ] **Step 3: Implement.** `flushResults(final bool)` appends to `Interrupted` only when `final` is true, and `Replay` calls `flushResults(true)` after the scan loop. Add `samePath(a, b string) bool` in `core/pathcase.go`, which canonicalizes both and uses `strings.EqualFold` on windows and darwin. `ListSessions` uses it.
- [ ] **Step 4: Run** `go test -race ./core/` and expect PASS.
- [ ] **Step 5: Commit** `fix: trailing-only interrupted calls, case-insensitive session roots, steer-after-Esc test`

---

### Task 2: Reasoning stream

**Files:**
- Modify: `providers/types.go`, `providers/openai.go` (`oaiChunk`, `parseChunk`), `providers/fake/*.go` (`Turn`)
- Modify: `core/ui.go` (`UI`, `quietUI`, `printer`), `core/redact_ui.go`, `core/session.go` (`turn`)
- Test: `providers/openai_test.go`, `core/redact_ui_test.go`, `core/session_test.go`

**Interfaces:**
- Produces:
  - `providers.StreamEvent.Reasoning string`
  - `fake.Turn.Reasoning string`, emitted before `Text`
  - `core.UI` gains `Reasoning(delta string)`. `quietUI` and `printer` ignore it.
- Reasoning is never added to `providers.Message`, never sent back, and never logged. The live check in Task 10 decides whether that has to change.

- [ ] **Step 1: Write failing tests**
  - `TestParseChunk_Reasoning`: `{"choices":[{"delta":{"reasoning":"a"}}]}` → `[{Reasoning:"a"}]`, and `{"choices":[{"delta":{"reasoning_content":"b"}}]}` → `[{Reasoning:"b"}]`. If both fields are present, `reasoning` wins.
  - `TestSession_ForwardsReasoningToUI`: fake turn `{Reasoning: "hmm", Text: "A"}` with a recording UI → reasoning deltas joined are `"hmm"`, text is `"A"`, and the history's assistant message has exactly one text block, `"A"`.
  - `TestRedactingUI_RedactsReasoning`: redactor masks `sk-secret`. `Reasoning("key sk-secret")` reaches the inner UI as `"key [REDACTED]"` (use the redactor's real mask string).
- [ ] **Step 2: Run** `go test ./providers/ ./core/ -run 'Reasoning' -v`. Expect a compile failure, then FAIL.
- [ ] **Step 3: Implement.** Add `Reasoning` and `ReasoningContent` delta fields to `oaiChunk`. In `turn`, call `c.UI.Reasoning(ev.Reasoning)` when it is non-empty. The `Accumulator` ignores reasoning. Update every `core.UI` implementation in the repo, including test doubles (`grep -rn 'Text(delta string)'`).
- [ ] **Step 4: Run** `go test -race ./...` and expect PASS.
- [ ] **Step 5: Commit** `feat: surface reasoning deltas as a separate stream event`

---

### Task 3: Approval gating and session grants

**Files:**
- Modify: `core/approve.go` (extract the gating and summary helpers)
- Create: `core/grants.go`, `core/shellwords.go`
- Test: `core/grants_test.go`, `core/shellwords_test.go`

**Interfaces:**
- Produces:
  - `func Gated(level Level, t Tool) bool`: the logic in `Approver.gated`, which now calls it.
  - `func SummarizeCall(tool string, args json.RawMessage) string`: the existing `summarize`, exported.
  - `func ShellWords(cmd string) (words []string, ok bool)`: a quote-aware split that handles `'…'`, `"…"` and `\` escapes outside single quotes. `ok` is false on unbalanced quotes.
  - `func NewGrants(windows bool) *Grants`, safe for concurrent use:
    - `Covers(tool string, args json.RawMessage) bool`
    - `Add(tool string, args json.RawMessage) (grant string, ok bool)`. `ok` is false when the call can't be granted: a non-simple or unparseable shell command, or a tool other than shell, edit or write.
    - `List() []string`, sorted, for example `["edit", "shell: go test"]`.
    - `Remove(grant string) bool`

Rules, verbatim from the spec:
- The shell grant key is the first two words of the command (or one word if there is only one), formatted `shell: <w1> <w2>`, and taken from the args' `command` field. A later command is covered only if its first two words are **equal** to the key's words.
- A command isn't simple if its text contains `;`, `&`, `|`, `$(`, a backtick, `>`, `<` or a newline anywhere, or if its first word matches `^[A-Za-z_][A-Za-z0-9_]*=`. With `windows` true, `^` and `%` also make it non-simple.
- A non-simple command is never covered and never grantable.
- edit and write grant the whole tool (key `edit` / `write`).

- [ ] **Step 1: Write failing tests**
  - `TestShellWords`: table. `go test ./...` → `[go test ./...]`. `git commit -m "a b"` → `[git commit -m a b]`. `echo 'x` → ok=false.
  - `TestGrants_ShellFirstTwoWords`: `Add(shell, {"command":"go test ./..."})` gives grant `"shell: go test"`. Then `Covers` is true for `go test -run X ./core` and false for `go testx`, `go build` and `go test ./... | tee out`.
  - `TestGrants_NotSimpleNeverGrants`: each of `a;b`, `a && b`, `a | b`, `echo $(x)`, ``echo `x` ``, `a > f`, `a < f`, `"a\nb"` and `FOO=bar go test` gives `Add` ok=false and `Covers` false.
  - `TestGrants_WindowsExtras`: `NewGrants(true)` rejects `echo ^x` and `echo %PATH%`, and `NewGrants(false)` accepts both.
  - `TestGrants_EditWholeTool`: after `Add("edit", …)`, `Covers("edit", any)` is true and `Covers("write", any)` is false. `List()` returns `["edit"]`, and after `Remove("edit")`, `Covers` is false.
  - `TestGated_MatchesApprover`: for every level × {read, write, edit, shell}, `Gated` equals what `jas run` gated before. Use a table copied from today's `Approver.gated` behaviour.
- [ ] **Step 2: Run** `go test ./core/ -run 'ShellWords|Grants|Gated' -v` and expect FAIL.
- [ ] **Step 3: Implement** the above. Read `command` with `json.Unmarshal` into `struct{ Command string }`. Bad JSON can't be granted.
- [ ] **Step 4: Run** `go test -race ./core/` and expect PASS.
- [ ] **Step 5: Commit** `feat: session approval grants with simple-command shell rules`

---

### Task 4: Transcript rendering (light Markdown, tool rows, thinking line)

**Files:**
- Create: `tui/markdown.go`, `tui/transcript.go`, `tui/style.go`
- Modify: `go.mod`, `go.sum` (add the three charm modules at the versions in Tech Stack)
- Test: `tui/markdown_test.go`, `tui/transcript_test.go`

**Interfaces:**
- Produces (all pure, with no Bubble Tea types):
  - `type Transcript struct { ShowThinking bool; Width int }` plus unexported state. All methods return the finished, styled blocks to print, in order:
    - `User(text string) []string`
    - `Text(delta string) []string`
    - `Reasoning(delta string, now time.Time) []string`
    - `Event(typ string, data map[string]any, now time.Time) []string`
    - `Flush(now time.Time) []string`, called at the end of a run; prints the partial block and any pending thinking line
    - `Live() string`, the rendered partial block for the live region
  - `func toolRow(tool string, args map[string]any, status, output string, width int) string`

Rendering rules:
- **Blocks:** a block ends at a blank line outside a code fence. A fenced block runs from its opening fence line to the closing fence line, and only the closing fence completes it.
- **Styling:** `#`-headings are bold, and `- ` / `* ` / `1. ` list items get a bullet and hanging indent. `**x**` becomes bold with the markers removed. Everything else prints as is.
- **Code blocks** are drawn in a lipgloss rounded border. Inside, string literals, `//` and `#` line comments, and numbers get three distinct colours. The highlighting is language-agnostic and built in.
- **Thinking line:** the first `Text` or tool event after reasoning prints `· thought for Ns`, dim. N is the whole seconds from the first reasoning delta, at least 1. With `ShowThinking`, the reasoning text prints as dim blocks instead, followed by the same line.
- **Tool rows** print on `tool_result`, using the args saved from the matching `tool_started` (keyed by `call_id`). The format is `▸ <tool> <subject> <outcome>`:

  | tool | subject | outcome when ok |
  |---|---|---|
  | read | path | `(N lines)` |
  | ls | path, or `.` | `(N entries)` |
  | grep | pattern | `(N matches)` |
  | shell | command | `ok` |
  | write / edit | path | `ok` |
  | other | `SummarizeCall` | `ok` |

  Here N is the number of output lines. For status error the outcome is `failed: <first output line>`, and denied and cancelled print as `denied` and `cancelled`. Rows for error, denied and cancelled are red. A row longer than `Width` is cut with `…`.

- [ ] **Step 1: Write failing tests**
  - `TestMarkdown_SplitsBlocksAcrossDeltas`: feed `"para one\n"`, `"\npara"`, `" two"` one at a time. Only `"para one"` completes, and `Live()` contains `para two`.
  - `TestMarkdown_FenceHoldsBlankLines`: `"```go\na\n\nb\n```\n"` completes one block that contains both `a` and `b`, and nothing completes before the closing fence.
  - `TestMarkdown_BoldAndLists`: the rendered `"- **x** y"` contains `x` without `**`, and starts with a bullet glyph after ANSI stripping. (Strip with `ansi.Strip` from `github.com/charmbracelet/x/ansi`, which lipgloss already depends on.)
  - `TestTranscript_ThinkingHiddenByDefault`: `Reasoning("plan", t0)` then `Text("Hi\n\n", t0+3s)` → printed blocks after ANSI stripping are `["· thought for 3s", "Hi"]`.
  - `TestTranscript_ThinkingShownWhenEnabled`: with `ShowThinking`, the reasoning text block comes before the thought line.
  - `TestToolRow_Formats`: table of the rows above, for example `read` with output of 212 lines gives `▸ read core/budget_test.go (212 lines)`, and an error shell call gives `▸ shell go test ./core failed: exit status 1`.
  - `TestToolRow_TruncatesToWidth`: at width 20, the visible rune count is ≤ 20 and the row ends in `…`.
- [ ] **Step 2: Run** `go test ./tui/ -v` and expect FAIL.
- [ ] **Step 3: Implement.** Run `go get` for the three modules and keep each file under 300 lines.
- [ ] **Step 4: Run** `go test -race ./tui/` and expect PASS. Run `CGO_ENABLED=0 go build ./...` and expect it to succeed.
- [ ] **Step 5: Commit** `feat: tui transcript with light markdown, tool rows and thinking line`

---

### Task 5: Backend interface, session bridge and test backend

**Files:**
- Create: `tui/backend.go`, `tui/bridge.go`, `tui/backend_test.go` (the test backend, `package tui`)

**Interfaces:**
- Produces:
```go
// Backend is everything the chat needs from jas's wiring; cmd/jas implements it.
type Backend interface {
	NewSession(model string, ui core.UI, gate core.Gate) (*Live, error)
	Resume(path string, ui core.UI, gate core.Gate) (*Live, []string, error) // notices
	Sessions(exclude string) ([]core.SessionInfo, error)                     // this project, newest first
	Models() []string
	Window(model string) int
	SkillNames() []string
	SkillPrompt(name, text string) (string, bool) // redacted user message; false if unknown
}

// Live is one open session.
type Live struct {
	Session *core.Session
	ID, Path, Model string
	Budget *core.Budget
	Close func(status string, code int) error // session_ended, then closes the log
}
```
  - `type bridge struct{ gen int; send func(tea.Msg) }` implements `core.UI`. It posts `textMsg{gen, s}`, `reasoningMsg{gen, s}` and `eventMsg{gen, typ, data map[string]any}`. Data that isn't a map is dropped.
  - `type testBackend` (test-only), built on `fake.Provider`, a temp-dir `core.OpenEventLog`, `tools.ReadOnly()` and the given gate. Later tasks use `newTestBackend(t, turns ...fake.Turn) *testBackend`.

- [ ] **Step 1: Write failing test** `TestBridge_TagsGeneration`: a bridge with gen 3 and a recording `send`. `Text("a")`, `Event("tool_started", map…)` and `Event("x", 5)` post exactly two messages, both with gen 3.
- [ ] **Step 2: Run** `go test ./tui/ -run Bridge -v` and expect FAIL.
- [ ] **Step 3: Implement** the bridge and the test backend. The backend's `NewSession` wraps the log with `core.NewUILog(log, ui)` and builds a `core.Executor{Gate: gate}`.
- [ ] **Step 4: Run** `go test -race ./tui/` and expect PASS.
- [ ] **Step 5: Commit** `feat: tui backend interface and session event bridge`

---

### Task 6: Chat model: input, runs, steering, keys, status line

**Files:**
- Create: `tui/chat.go` (model, Update dispatch), `tui/run.go` (run lifecycle), `tui/status.go`, `tui/keys.go`
- Test: `tui/chat_test.go`, `tui/status_test.go`

**Interfaces:**
- Consumes: `Transcript` (Task 4), `Backend`, `Live`, `bridge` (Task 5).
- Produces:
```go
type Options struct {
	Level    core.Level
	Model    string
	Root     string // project folder shown in the status line
	Windows  bool
	Continue bool   // resume the newest session for this project at start
	Now      func() time.Time
}
func New(b Backend, o Options) *Model            // *Model implements tea.Model
func Run(ctx context.Context, b Backend, o Options, in io.Reader, out io.Writer) error
```
  - The model's `gen` increments on every new or resumed session, and messages whose gen doesn't match are ignored.
  - `runDoneMsg{gen int; res core.TurnResult; err error}`.
  - `Model.Printed() []string` (test hook): every block handed to `tea.Println`, in order.

Behaviour:
- **Start:** `Init` opens a session through `Backend.NewSession`. With `Continue`, it calls `Backend.Sessions("")` and resumes the newest one. If there is none, it prints `no saved session for this project; starting a new one` and opens a new session.
- **Enter while idle:** trimmed empty input does nothing. Input starting with `/` goes to Task 8. Anything else prints the user block, starts `Send` on a goroutine with a fresh cancellable context, and posts `runDoneMsg` when it returns.
- **Enter while running:** calls `Live.Session.Steer(text)` and adds the text to the queued list shown above the input box. A `user_message` event with `steer: true` clears the list.
- **Alt+Enter and Shift+Enter** insert a newline. Request `tea.KeyboardEnhancements` in `View` so Shift+Enter is reported where the terminal supports it. A `tea.PasteMsg` goes into the textarea verbatim.
- **Esc while running:** cancels the run. When `runDoneMsg` arrives, it puts the texts from `TakeSteering()`, joined with `\n`, in front of the current input and clears the queued list. Esc while idle does nothing.
- **Ctrl-C:** clears non-empty input. On an empty box, it quits if the previous Ctrl-C was under 1s ago (`Options.Now`); otherwise the status shows `press Ctrl-C again to quit`.
- **Quit:** if a run is active, cancel it and wait for `runDoneMsg`. Then call `Live.Close("ok", 0)`, print `session <id> saved · jas --continue to resume`, and send `tea.Quit`.
- **Run end:** `runDoneMsg` calls `Transcript.Flush`. A status other than ok prints a dim notice: `interrupted`, `turn cap reached: <reason>`, `context window full: start a /new chat` or `failed: <reason>`. A non-nil err prints `event log error: <err>` in red.
- **Status line:** `<model> · <level> · <ctx>% · <tokens> tok · <folder base>`. Append ` · working…` while running and ` · N queued` when steering is pending. ctx% is the last usage prompt_tokens ÷ `Budget.Window`. tokens is the session total of prompt and completion tokens, formatted `12.3k`. The line is cut to the width with `…`.
- **View** (top to bottom): `Transcript.Live()`, the queued list (each line `↳ <text>`, dim), the approval prompt or picker (Tasks 7 and 8), the textarea, the status line.

- [ ] **Step 1: Write failing tests.** Each one drives `m.Update` with messages directly, using the test backend.
  - `TestChat_SendStreamsAndPrints`: turn `{Text: "Hello\n\nWorld"}`. Submit `hi` and pump bridge messages until `runDoneMsg`. `Printed()` after ANSI stripping is `["› hi", "Hello", "World"]`.
  - `TestChat_SteerWhileRunning`: turn 1 makes a tool call whose `OnEmit` blocks until the test submits `also`. Afterwards the queued list is shown, it clears when the steer `user_message` arrives, and the second request's messages contain `also`.
  - `TestChat_EscRestoresQueuedText`: a blocking turn. Submit `go`, steer `x` and `y`, then press Esc. After `runDoneMsg`, the input value is `"x\ny"`.
  - `TestChat_PasteWithNewlinesDoesNotSubmit`: `PasteMsg{"a\nb"}` leaves the input as `"a\nb"` and starts no run.
  - `TestChat_CtrlCClearsThenQuits`: with input `abc`, Ctrl-C leaves the input empty. A second Ctrl-C under 1s returns a quit command (check with `cmd() == tea.QuitMsg{}` after resolving the batch), and `Live.Close` ran once.
  - `TestChat_DropsMessagesFromOldSession`: a `textMsg{gen: m.gen-1}` changes nothing printed and nothing live.
  - `TestStatus_Format`: model `glm`, level shell, 5000 of window 10000, 12345 tokens, root `/x/jas` → `glm · shell · 50% · 12.3k tok · jas`.
  - `TestStatus_TruncatesToWidth`: at width 20, ≤ 20 visible runes ending in `…`.
- [ ] **Step 2: Run** `go test ./tui/ -run 'Chat_|Status_' -v` and expect FAIL.
- [ ] **Step 3: Implement.** The model posts bridge and run messages through an unexported `send func(tea.Msg)`. `Run` builds `tea.NewProgram(New(...), tea.WithContext(ctx), tea.WithInput(in), tea.WithOutput(out))` with no alt screen, and sets `send` to `p.Send` before calling `p.Run()`. Tests use a helper `newTestChat(t, b)`: it sets `send` to a buffered channel, and `pump(t, m)` feeds that channel into `Update` until a `runDoneMsg` arrives. Commands returned by `Update` run synchronously in the helper.
- [ ] **Step 4: Run** `go test -race ./tui/` and expect PASS.
- [ ] **Step 5: Commit** `feat: inline chat model with streaming, steering and status line`

---

### Task 7: Inline approvals

**Files:**
- Create: `tui/gate.go`
- Modify: `tui/chat.go` (approval prompt state, Enter routing)
- Test: `tui/gate_test.go`, `tui/chat_test.go`

**Interfaces:**
- Consumes: `core.Gated`, `core.SummarizeCall`, `core.Grants` (Task 3).
- Produces:
  - `func NewGate(level core.Level, windows bool, send func(tea.Msg)) *Gate`, which implements `core.Gate`.
  - `(*Gate).SetLevel(core.Level)`, `(*Gate).Level() core.Level`, `(*Gate).Reset(core.Level)` (clears grants and sets the level), `(*Gate).Grants() *core.Grants`.
  - `approvalMsg{prompt string; reply chan<- string}`.

Behaviour of `Allow`:
1. If `!core.Gated(level, t)`, return `Granted`.
2. If the grants cover the call, return `Granted`.
3. Otherwise post an `approvalMsg` whose prompt is `approve <tool>: <SummarizeCall>? [y/N/always]`, and wait for the reply or `ctx.Done()`. On ctx done, return `Denied, ctx.Err()`. One prompt is open at a time; serialize with a mutex.
4. The reply is lowercased and trimmed:
   - `y` / `yes` → `Granted`.
   - `always` → `Grants.Add`, then `Granted`. If `Add` returns ok=false, print the notice `this command can't get an always grant; allowed once`.
   - `n`, `no` or empty → `Denied`.
   - Any other text → `Denied`, and the original text is queued with `Session.Steer`. The model does that queuing when it sends the reply, because it holds the session.

On the chat side, while a prompt is open: Enter sends the input text (cleared afterwards) to `reply` and closes the prompt. Esc cancels the run, which unblocks the gate through ctx, and closes the prompt. Text queued before the prompt appeared stays queued.

- [ ] **Step 1: Write failing tests**
  - `TestGate_AlwaysStopsRepeatPrompts`: at level shell, the first `Allow(shell, "go test ./...")` posts a prompt. Reply `always` → `Granted`. A second `Allow(shell, "go test ./core")` returns `Granted` without posting.
  - `TestGate_UngatedNeverPrompts`: at level shell, `Allow(write, …)` returns `Granted` and posts nothing.
  - `TestGate_CtxCancelUnblocks`: cancel while waiting → `Denied` and `context.Canceled`.
  - `TestChat_ApprovalAnswerWithTextSteers`: a fake turn calls shell at level shell, and the user answers `no, use make test`. The tool result is denied and the queued list shows `no, use make test`.
  - `TestChat_EscClosesApprovalPrompt`: with a prompt open, Esc leads to `runDoneMsg` with status interrupted, the prompt is gone, and the next Enter with `y` starts a new run instead of answering anything.
- [ ] **Step 2: Run** `go test ./tui/ -run 'Gate_|Approval' -v` and expect FAIL.
- [ ] **Step 3: Implement.** The test backend passes the chat's gate to `core.Executor`.
- [ ] **Step 4: Run** `go test -race ./tui/` and expect PASS.
- [ ] **Step 5: Commit** `feat: inline approvals with session always grants`

---

### Task 8: Slash commands and picker

**Files:**
- Create: `tui/commands.go` (parse, dispatch, suggest), `tui/picker.go`
- Test: `tui/commands_test.go`, `tui/picker_test.go`

**Interfaces:**
- Consumes: `Backend` (Task 5), `Gate` (Task 7), `Transcript.ShowThinking` (Task 4).
- Produces:
  - `func parseCommand(input string) (name, arg string, ok bool)`: `"/model glm"` → `("model", "glm", true)`. `"/skill:review fix it"` → `("skill:review", "fix it", true)`.
  - `func suggest(name string, known []string) string`: the known name at the smallest Levenshtein distance ≤ 2, or the one with `name` as its prefix. Ties go alphabetical, and no match gives `""`.
  - `type picker struct{ title string; items []pickItem; filter string; cursor int }` with `pickItem{label, value string}`. Typing filters by case-insensitive substring, Up and Down move, Enter returns the value, Esc cancels. At most 10 rows are visible.

Commands. Those marked **idle** print `wait for the reply or press Esc first` while a run is active.

| command | does |
|---|---|
| `/help` | prints the commands and keys |
| `/quit` | quits, as in Task 6 |
| `/new` (idle) | `Live.Close("ok",0)` then `NewSession(m.model, …)`. Grants, level, model and thinking are kept. |
| `/model [name]` (idle) | With a name: `Session.SetModel(name)`, `Live.Budget.Window = Backend.Window(name)`, and `m.model = name`. Bare: a picker over `Backend.Models()`. |
| `/thinking` | toggles `Transcript.ShowThinking` and prints `thinking shown` or `thinking hidden` |
| `/approve [level]` | With a level: `core.ParseLevel` then `Gate.SetLevel`. Bare: prints the current level and `never \| shell \| mutating`. |
| `/allowed` | a picker over `Gate.Grants().List()`. Enter removes the chosen grant and prints `removed <grant>`. With no grants it prints `no always grants`. |
| `/session` | prints the path, `in <p> · out <c> tokens` and `<ctx>% of <window>` |
| `/copy` | `tea.SetClipboard(last final reply)`, then prints `copied <n> chars (OSC 52)`. With no reply yet it prints `nothing to copy`. |
| `/resume` (idle) | a picker over `Backend.Sessions(m.live.Path)`, labelled `<started local time> <first message>`. On choice: `Live.Close`, `Backend.Resume`, `Gate.Reset(Options.Level)`, `m.model = live.Model`, print the notices, then print `resumed <id> · <n> messages`. A locked session prints the error and keeps the current session. |
| `/skill:<name> [text]` (idle) | `Backend.SkillPrompt` and, if ok, send that text as the user message. Shows `› /skill:<name> <text>` as the user block. |

- **Unknown command:** print `unknown command /<name>`, adding ` — did you mean /<s>?` when `suggest` finds one. Keep the input text unchanged and send nothing.
- **Listing:** typing `/` as the first character shows, below the input box, the commands and the `skill:<name>` entries whose names start with the typed text (up to 8). Tab completes the first entry.

- [ ] **Step 1: Write failing tests**
  - `TestParseCommand` and `TestSuggest`: `suggest("modle", …)` → `model`, `suggest("res", …)` → `resume`, and `suggest("zzz", …)` → `""`.
  - `TestChat_UnknownCommandKeepsInput`: submit `/modle`. Printed contains `unknown command /modle — did you mean /model?`, the input is still `/modle`, and the backend saw no `Send`.
  - `TestChat_NewKeepsGrantsAndModel`: after `/model other` and a shell always grant, `/new` leaves the new `Live.Model == "other"` and the grant still listed.
  - `TestChat_ResumeResetsGrants`: after a grant, `/resume` (pick the first entry) clears `Gate.Grants().List()` and sets the level back to `Options.Level`.
  - `TestChat_IdleOnlyCommandsRefusedWhileRunning`: `/new` during a blocked run prints `wait for the reply or press Esc first`, and the session ID is unchanged.
  - `TestChat_SkillCommandSendsPrompt`: test backend skill `review` → the provider's last user message equals `SkillPrompt("review", "fix it")`.
  - `TestPicker_FilterAndChoose`: items `a-one`, `b-two`; typing `two` then Enter returns `b-two`'s value.
- [ ] **Step 2: Run** `go test ./tui/ -run 'Command|Suggest|Picker|Chat_(Unknown|New|Resume|IdleOnly|Skill)' -v` and expect FAIL.
- [ ] **Step 3: Implement.** Keep the dispatch as a `map[string]func(*Model, string) tea.Cmd`. Because the idle-only commands are refused during a run, `Session.SetModel` and `Session.Messages` never run while `Send` does. That settles the thread-safety question carried over from plan 1 without adding a mutex, so add a sentence saying so to the `Session` doc comment.
- [ ] **Step 4: Run** `go test -race ./tui/` and expect PASS.
- [ ] **Step 5: Commit** `feat: chat slash commands, picker and skill invocation`

---

### Task 9: `jas` and `jas --continue` wiring

**Files:**
- Create: `cmd/jas/setup.go` (shared setup taken out of `runCmd`), `cmd/jas/chat.go` (`chatCmd`, `chatBackend`)
- Modify: `cmd/jas/run.go` (use the setup), `cmd/jas/main.go` (dispatch, usage), `cmd/jas/onboard.go`, `settings/models.go` (`CatalogModels() []string`, sorted), `README.md`, `docs/design.md` (CLI section)
- Test: `cmd/jas/chat_test.go`, `cmd/jas/run_test.go` (unchanged, must stay green)

**Interfaces:**
- Consumes: `tui.Run`, `tui.Backend`, `tui.Live`, `tui.Options`; `core.Replay`, `core.ResumeModel`, `core.ResumeSession`, `core.ListSessions`.
- Produces:
  - `type env struct` with every value `runCmd` builds today before opening the log: cfg, file, level, key, root, home, red, policy, pc, skillList, interactive and provider. Also `func prepare(ctx context.Context, flags map[string]string, trust bool) (*env, error)`. Both `runCmd` and `chatCmd` use it.
  - `type chatBackend struct{ e *env; current string }`, which implements `tui.Backend`:
    - `NewSession`: a new session dir, `core.OpenEventLog(path, nil, red.Redact)`, then `core.NewUILog(log, core.NewRedactingUI(ui, red.Redact))`. It builds an executor with the given gate and a budget from `settings.ContextWindow(model, …)`, and the same Meta as `jas run`.
    - `Resume`: opens the log **first** (the lock). `core.ErrSessionLocked` becomes `fmt.Errorf("session %s is open in another jas process: %w", id, err)`. Then `Replay`, then `ResumeModel(st, cfg.BaseURL, cfg.Model)`, then `ResumeSession`. Notices are `st.Notices` plus the model notice. The system prompt comes from `e.pc.system`, never from the file.
    - `Sessions(exclude)`: `core.ListSessions(dir, root, 20)` minus `exclude`.
    - `Models()`: the union of `file.ModelNames()`, `cfg.Model` and `settings.CatalogModels()` when the provider is openrouter, sorted and deduplicated.
    - `SkillNames`, `SkillPrompt`: over `e.pc.skills` and also the skills with `DisableModelInvocation`. The prompt is exactly:
      ```
      Use the "<name>" skill for this request. Its instructions (<location>):

      <SKILL.md content, redacted>

      <text, if any>
      ```
- **Dispatch:** `jas` with no args, or args starting with `-` other than `--version`, goes to `chatCmd`. The chat flags are the setting flags plus `--continue` and `--trust-project`. With no `config.json`, onboarding runs first (today's `onboardOrUsage` flow) and then the chat. If stdin or stdout isn't a terminal: `usageErr("the chat needs a terminal; use jas run \"task\"")`. `chatCmd` calls `tui.Run(ctx, backend, opts, os.Stdin, os.Stdout)`.

- [ ] **Step 1: Write failing tests**
  - `TestChatCmd_NeedsTerminal`: `dispatch(ctx, nil)` with a config present and a non-terminal stdin exits `core.ExitUsage`, and stderr contains `the chat needs a terminal`.
  - `TestChatBackend_ResumeLockedSession`: hold `core.OpenEventLog` on a saved session. `Resume` errors with `errors.Is(err, core.ErrSessionLocked)`, and the message contains the session id.
  - `TestChatBackend_ResumeUsesFileModelOnSameBaseURL`: a saved session with model `z-ai/glm-x` and the same base URL → `Live.Model == "z-ai/glm-x"`, with no notices.
  - `TestChatBackend_SessionsExcludesCurrent`: two saved sessions, excluding one → one returned.
  - `TestChatBackend_SkillPromptRedacts`: a skill containing the API key → the prompt holds the redaction mask, not the key.
  - `TestRunCmd_*` (existing): unchanged and still passing.
- [ ] **Step 2: Run** `go test ./cmd/jas/ -run 'ChatCmd|ChatBackend' -v` and expect FAIL.
- [ ] **Step 3: Implement.** Move code into `prepare` with no behaviour change to `jas run`. Update the `usage` text with `jas [--continue] [flags]` and README's quick start.
- [ ] **Step 4: Run** `go test -race ./...`, `GOOS=windows go vet ./...` and `CGO_ENABLED=0 go build ./cmd/jas`. Expect all to succeed.
- [ ] **Step 5: Commit** `feat: jas opens the inline chat; --continue resumes this project's last session`

---

### Task 10: Live checks and manual smoke

**Files:**
- Create: `providers/live_test.go` (`//go:build live`)
- Modify: `docs/superpowers/specs/2026-10-30-interactive-mode-design.md` (record the reasoning outcome under decision 8)

- [ ] **Step 1: Write the live test** `TestLive_GLMMultiTurnToolsWithoutReasoning`, which skips unless `OPENROUTER_API_KEY` is set. It uses model `z-ai/glm-4.6` (or the newest `z-ai/glm-*` in `settings/catalog.json`) and the real `providers.OpenAI`. Over at most 6 requests, the model must call a stub `lookup` tool twice in sequence, with consecutive user messages in between, then answer with both looked-up values. Assert the final text contains both values and that no request fails.
- [ ] **Step 2: Run** `go test -tags live ./providers/ -run Live -v -count=3`.
  - **Expected: PASS 3/3.** Record "dropping reasoning: no degradation seen" under decision 8 of the spec.
  - **If it fails:** record the failure, open an issue titled `reasoning replay needed for GLM tool loops`, and don't widen this plan.
- [ ] **Step 3: Manual smoke** in a real terminal on Linux. Check each item and note the results in the PR body:
  - `jas` opens and replies stream.
  - A tool row appears.
  - Esc stops a run.
  - Ctrl-C twice quits, and the transcript stays in scrollback.
  - `jas --continue` resumes the conversation.
  - `/model` picker and `/approve shell` then `always` work.
  - A pasted multi-line block stays one message.
- [ ] **Step 4: Commit** `test: live GLM multi-turn check and reasoning decision`
