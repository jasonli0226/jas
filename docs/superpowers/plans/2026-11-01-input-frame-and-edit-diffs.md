# Input Frame and Edit Diffs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Frame the chat input with rules above and below, and show `edit`/`write` changes as line-numbered `+`/`-` diffs in the approval prompt and the transcript.

**Architecture:** Tools report *what* changed as raw old/new text (`core.Change`); core redacts that text and turns it into a structured, capped `core.Diff` (one place for redaction, escaping and caps). The executor computes a lazy preview for the gate before approval, and after a successful call writes the diff to `tool_result` with a screen copy for the UI and a log copy that drops a `write` overwrite's removed lines. The TUI and the plain printer each render the structured diff.

**Tech Stack:** Go (stdlib only for diffing), Bubble Tea v2, lipgloss v2.

**Spec:** `docs/superpowers/specs/2026-11-01-input-frame-and-edit-diffs-design.md`

## Global Constraints

- Stay inline: no alt-screen / fullscreen (AGENTS.md).
- Do not break `jas run`, `--json` or exit codes; event-log changes additive only, `v` unchanged.
- The model never sees the diff: `edit`/`write` outputs stay `edited <path>` / `wrote <path> (N bytes)`.
- No new module dependencies; `CGO_ENABLED=0`; `go test -race ./...` green.
- Commits `type: description`.
- Colours (style B): removed line bg `#3f1d1d`, added line bg `#1c3a24`, removed changed-words bg `#7a2a2a`, added changed-words bg `#2a6b3c`; line numbers faint.
- 3 context lines per hunk; dim `⋮` row between hunks.
- Diff cap at compute time: 400 lines or 64 KB (`Truncated` flag). Transcript cap: 30 diff lines then dim `… N more lines`.
- No diff after `error`, `denied`, `cancelled`.
- Log keeps only added lines for a `write` over an existing file; screen gets the full diff.

## Review Focus

1. CRLF files: lines must not all show as changed, and no `\r` reaches the screen. → test in Task 2.
2. Empty file, new empty file, and a file with no trailing newline: no panic, correct line numbers. → tests in Task 2.
3. A PEM private key inside the changed region: every key line masked, line numbers unchanged. → test in Task 2.
4. A huge one-line (minified) file or a 10k-line rewrite: diff stops at the cap, no quadratic blow-up. → test in Task 2.
5. A very narrow terminal (width 10): rendering cuts lines without panicking. → test in Task 5.

---

### Task 1: Input frame

**Files:**
- Modify: `tui/chat.go` (`View`, around the `m.input.View()` / `m.statusLine()` appends)
- Test: `tui/chat_test.go`

**Interfaces:**
- Produces: `func (m *Model) rule() string` — dim `─` repeated `m.width` times. Task 5 places the approval diff above the top rule, not between the rules.

- [ ] **Step 1: Write the failing tests**

```go
func TestChat_InputFramedByRules(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	lines := strings.Split(c.view(), "\n")
	i := slices.IndexFunc(lines, func(l string) bool { return strings.HasPrefix(l, "› ") })
	rule := strings.Repeat("─", defaultWidth)
	assert.Eq(t, strings.TrimRight(lines[i-1], " "), rule)
	assert.Eq(t, strings.TrimRight(lines[i+1], " "), rule)
	assert.Contains(t, lines[i+2], "· ") // status line under the bottom rule
}

func TestChat_SlashMenuInsideRules(t *testing.T) {
	c := newTestChat(t, newTestBackend(t))
	c.typeText("/")
	lines := strings.Split(c.view(), "\n")
	rule := strings.Repeat("─", defaultWidth)
	last := len(lines) - 1 // status line
	assert.Eq(t, strings.TrimRight(lines[last-1], " "), rule)
	assert.Contains(t, strings.Join(lines[:last-1], "\n"), "/help")
}
```

(`slices` may need importing in the test file.)

- [ ] **Step 2: Run** `go test ./tui -run 'InputFramed|SlashMenuInside'` — expect FAIL (no rule lines).

- [ ] **Step 3: Implement** in `View`: append `m.rule()` after the approval prompt slot and before the picker/input; append `m.rule()` after the input and its `/` menu (or after the picker), then the status line. Queued steering and the approval prompt stay above the top rule. Fix any existing view tests that assert exact line positions.

- [ ] **Step 4: Run** `go test -race ./tui` — expect PASS.

- [ ] **Step 5: Commit** `feat(tui): frame the input with rules above and below`

---

### Task 2: Diff engine in core

**Files:**
- Create: `core/diff.go`, `core/diff_test.go`

**Interfaces:**
- Produces (all in package `core`):

```go
// Change is what a mutating tool changed (or would change), as raw text.
type Change struct {
	Path     string
	Old, New string // whole file before/after; Old is "" for a new file
	// HideRemoved: removed lines are screen-only (a write over an existing
	// file, whose old content the model may never have read).
	HideRemoved bool
}

type DiffLine struct {
	Kind  string   `json:"k"`           // " " context, "-" removed, "+" added
	Old   int      `json:"o,omitempty"` // 1-based old line number ("-" and " ")
	New   int      `json:"n,omitempty"` // 1-based new line number ("+" and " ")
	Text  string   `json:"t"`           // redacted, escaped, tabs expanded
	Spans [][2]int `json:"s,omitempty"` // changed-word rune ranges [start,end) in Text
}

type Diff struct {
	Path      string       `json:"path"`
	Hunks     [][]DiffLine `json:"hunks"`
	Truncated bool         `json:"truncated,omitempty"`
}

func ComputeDiff(c Change, redact func(string) string) *Diff // nil when Old == New
func (d *Diff) AddedOnly() *Diff                              // copy without "-" lines
func (d *Diff) Lines() int                                    // total lines across hunks
func FormatDiffPlain(d *Diff) []string                        // "  183 - text" rows, "  ⋮" between hunks, "  … diff truncated" if Truncated
```

- Algorithm decisions (the implementer writes the bodies):
  - Split on `\n`; strip one trailing `\r` per line for display; a final empty element from a trailing newline is not a line.
  - Redact before diffing, line-preserving: replace each line inside a PEM private-key block (`-----BEGIN … PRIVATE KEY-----` to `-----END …-----`, or to end of text) with `[REDACTED:private_key]`, then apply `redact` to each line. `redact` may be nil (identity).
  - Then escape each line: tabs → 4 spaces, control and Cf runes → `\xNN`/`\uNNNN` as `Sanitize` does, but **keep spaces** (no `strings.Fields` collapse). Add `func sanitizeLine(s string) string` here.
  - Trim common prefix and suffix lines, LCS on the middle; if the middle exceeds 2,000,000 cells (old×new lines), treat the middle as all removed then all added.
  - Group into hunks with 3 context lines; merge hunks whose context overlaps.
  - Pair each run of `-` lines with the following `+` run index by index; for each pair, the changed span is what remains after the common rune prefix and suffix.
  - Stop adding lines at 400 lines or 64 KB of `Text`; set `Truncated`.

- [ ] **Step 1: Write the failing tests** in `core/diff_test.go` (package `core_test`):
  - `TestComputeDiff_EditMiddleLine`: old `"a\nb\nc\nd\ne\nf\ng\nh\n"`, new with `d`→`D`. Assert one hunk of 8 rows: context `a`,`b`,`c` (old=new=1,2,3), `{"-",4,0,"d"}`, `{"+",0,4,"D"}`, context `e`,`f`,`g` (5,6,7); the `-` and `+` lines' `Spans` are both `[[0,1]]`.
  - `TestComputeDiff_NewFileAllAdded`: old `""`, new `"x\ny\n"` → one hunk, two `+` lines numbered 1, 2.
  - `TestComputeDiff_EqualIsNil`: same text → `nil`.
  - `TestComputeDiff_CRLF`: old `"a\r\nb\r\n"`, new `"a\r\nB\r\n"` → only line 2 differs; no `Text` contains `\r`.
  - `TestComputeDiff_NoTrailingNewlineAndEmpty`: old `"a"`, new `"a\nb"` → one `+` line 2; old `""`, new `""` → nil.
  - `TestComputeDiff_TwoHunksFarApart`: change lines 2 and 40 of a 50-line file → `len(Hunks) == 2`.
  - `TestComputeDiff_PrivateKeyMaskedLinePreserving`: new file with line 1 `x`, lines 2–5 a PEM block, line 6 `y` → lines 2–5 all `Text == "[REDACTED:private_key]"`, line 6 numbered 6.
  - `TestComputeDiff_RedactAppliedPerLine`: `redact` replacing `"sk-secret"` → no `Text` contains it.
  - `TestComputeDiff_EscapesControlKeepsIndent`: new line `"\tx\x1b[31m"` → `Text == "    x\\x1b[31m"`.
  - `TestComputeDiff_CapsLinesAndBytes`: new file of 10,000 lines → `Lines() == 400`, `Truncated`; new file of one 1 MB line → total `Text` bytes ≤ 64<<10 + one line's cap, `Truncated` (cut an over-long single line at the byte cap).
  - `TestComputeDiff_LargeRewriteFallback`: 3,000 distinct old lines vs 3,000 distinct new lines finishes (test with `-timeout 10s`) and has 3,000 `-` lines before `+` lines (capped at 400 total).
  - `TestDiff_AddedOnlyDropsRemoved`: from the middle-line edit, `AddedOnly()` keeps `" "` and `"+"` lines, no `"-"`.
  - `TestFormatDiffPlain`: the middle-line edit renders `"     4 - d"` and `"     4 + D"` style rows (`%6d %s %s`), hunks separated by `"  ⋮"`.

- [ ] **Step 2: Run** `go test ./core -run 'ComputeDiff|Diff_|FormatDiffPlain'` — expect build FAIL (undefined).

- [ ] **Step 3: Implement** `core/diff.go` per the interfaces and algorithm decisions. Named constants: `diffContext = 3`, `maxDiffLines = 400`, `maxDiffBytes = 64 << 10`, `maxLCSCells = 2_000_000`.

- [ ] **Step 4: Run** `go test -race ./core` — expect PASS.

- [ ] **Step 5: Commit** `feat(core): structured, redacted and capped line diffs`

---

### Task 3: Tools report changes and previews

**Files:**
- Modify: `core/tools.go` (`Result`, new `Previewer`)
- Modify: `tools/files.go` (`Write.Call`, `Edit.Call`)
- Test: `tools/files_test.go`

**Interfaces:**
- Consumes: `core.Change` (Task 2).
- Produces:

```go
// in core/tools.go
type Result struct {
	Output  string
	IsError bool
	Change  *Change // display only: never sent to the model or logged as output
}

// Previewer is implemented by mutating tools that can say what a call would
// change without changing anything. A nil Change with a nil error means
// nothing to show.
type Previewer interface {
	Preview(ctx context.Context, p ExecPolicy, args json.RawMessage) (*Change, error)
}
```

- `Edit` and `Write` implement `Previewer`. Refactor each `Call` so the read/validate/compute part is shared with `Preview` (one unexported helper per tool returning the resolved path, old content, new content and an error `Result`); `Call` then writes, `Preview` does not.
- `Edit`: `Change{Path: a.Path, Old: content, New: updated}` (`HideRemoved` false: `old_string` is already logged in `tool_started.args`).
- `Write`: new file → `Change{Path, Old: "", New: content}`; existing file → read it first, `Change{Path, Old: old, New: content, HideRemoved: true}`. An unreadable old file (not regular, too big for `readFileCtx`) → no `Change`, write proceeds as today.
- Outputs (`edited <path>`, `wrote <path> (N bytes)`) unchanged.

- [ ] **Step 1: Write the failing tests**
  - `TestEdit_ResultCarriesChange`: after a successful edit, `res.Change.Old`/`New` equal the file before/after, `HideRemoved == false`, `res.Output == "edited f.txt"`.
  - `TestWrite_NewFileChange`: `Change.Old == ""`, `New` is the content, `HideRemoved == false`.
  - `TestWrite_OverwriteChangeHidesRemoved`: `Change.Old` is the old content, `HideRemoved == true`.
  - `TestEdit_PreviewDoesNotWrite`: `Preview` returns the same `Change` a `Call` would, and the file on disk is unchanged.
  - `TestEdit_PreviewNotFoundIsError`: `old_string` missing → non-nil error, nil `Change`.
  - `TestWrite_PreviewDoesNotWrite`: new path → not created by `Preview`.

- [ ] **Step 2: Run** `go test ./tools -run 'Change|Preview'` — expect FAIL.

- [ ] **Step 3: Implement** per the interfaces above.

- [ ] **Step 4: Run** `go test -race ./tools ./core` — expect PASS (existing edit/write tests unchanged).

- [ ] **Step 5: Commit** `feat(tools): edit and write report changes and previews`

---

### Task 4: Executor: diff on tool_result, screen/log split, gate preview

**Files:**
- Modify: `core/exec.go` (`Executor`, `runMutating`, `emitResult`)
- Modify: `core/tools.go` (`Gate`, `AllowAll`)
- Modify: `core/ui.go` (`uiLog`)
- Modify: `core/approve.go` (`Approver.Allow`)
- Modify: `tui/gate.go` (`Gate.Allow`, `approvalMsg`)
- Modify: `cmd/jas/run.go`, `cmd/jas/chat.go` (set `Executor.Redact`)
- Test: `core/exec_test.go`, `core/approve_test.go`, `tui/gate_test.go`

**Interfaces:**
- Consumes: `ComputeDiff`, `(*Diff).AddedOnly`, `FormatDiffPlain` (Task 2); `Result.Change`, `Previewer` (Task 3).
- Produces:

```go
// core/tools.go
type Gate interface {
	// preview, when non-nil, returns the diff the call would make; a gate
	// calls it only when it is about to ask.
	Allow(ctx context.Context, t Tool, args json.RawMessage, preview func() *Diff) (Decision, error)
}

// core/exec.go
type Executor struct {
	// ...existing fields...
	Redact func(string) string // applied to diff text before diffing; nil is identity
}

// core/ui.go — implemented by uiLog only
type screenAppender interface {
	// AppendScreen logs logData and shows screenData to the UI.
	AppendScreen(typ string, logData, screenData any) error
}

// tui/gate.go
approvalMsg struct {
	prompt string
	diff   *core.Diff // nil: nothing to show
	reply  chan string
}
```

- `runMutating`: build `preview` as a closure that, if `s.tool` is a `Previewer`, calls `Preview(ctx, r.e.Policy, s.call.Input)` and returns `ComputeDiff(*change, r.e.Redact)`; errors or nil change → nil. Pass it to `gate.Allow`.
- `emitResult`: when `s.status == statusOK` and `s.res.Change != nil`, compute `d := ComputeDiff(*s.res.Change, r.e.Redact)`; if non-nil, the screen payload gets `"diff": d` and the log payload `"diff": d` or `d.AddedOnly()` when `HideRemoved`. If `r.e.Log` is a `screenAppender`, call `AppendScreen(EvToolResult, logPayload, screenPayload)`; else `Append(EvToolResult, logPayload)`. `Filter` must not drop `Change`.
- `Approver.Allow`: when prompting, if `preview != nil` and it returns a diff, write `FormatDiffPlain(d)` lines (each + `"\n"`) to `Out` before the `approve …? [y/N] ` line.
- `tui.Gate.Allow`: call `preview` (if non-nil) only on the path that sends `approvalMsg`, and set `diff`.
- `AllowAll.Allow` and every test fake gate: add the parameter.
- `cmd/jas`: set `Redact: e.red.Redact` on both executors.

- [ ] **Step 1: Write the failing tests**
  - `TestExec_EditResultEventHasDiff` (core): run an `edit` through the executor with a recording log; the `tool_result` event has a `diff` with one `-` and one `+` line; the provider-facing result block text is `edited <path>` (no diff text).
  - `TestExec_WriteOverwriteLogDropsRemovedScreenKeeps`: wrap a recording log in `NewUILog` with a recording UI; overwrite a file; the logged `diff` has no `"-"` lines, the UI event's `diff` has them.
  - `TestExec_NoDiffOnError`: edit with missing `old_string` → `tool_result` has no `diff` key.
  - `TestExec_PreviewPassedToGate`: a fake gate records `preview()`; for an edit it returns a diff with the would-be change, and the file is unchanged at that moment.
  - `TestExec_DiffRedacted`: `Executor.Redact` masking `"sk-secret"`; written content containing it → no diff `Text` contains it (screen or log).
  - `TestApprover_PrintsDiffBeforeQuestion` (core): `Out` buffer holds a `"- "`/`"+ "` row before `"approve edit:"`.
  - `TestGate_ApprovalCarriesDiff` (tui): the `approvalMsg` sent has non-nil `diff`; an ungated call never calls `preview` (count calls).

- [ ] **Step 2: Run** `go test ./core ./tui -run 'Diff|Preview|PrintsDiff|ApprovalCarries'` — expect FAIL.

- [ ] **Step 3: Implement** per the interfaces. Update `docs/design.md` event table: `tool_result` payload gains optional `diff` (`path`, `hunks` of `{k,o,n,t,s}`, `truncated`; a `write` over an existing file logs added lines only). Note in the same row that replay ignores it.

- [ ] **Step 4: Run** `go vet ./... && go test -race ./...` — expect PASS.

- [ ] **Step 5: Commit** `feat(core): diffs on tool results and approval previews`

---

### Task 5: TUI diff rendering (transcript and approval)

**Files:**
- Create: `tui/diff.go`, `tui/diff_test.go`
- Modify: `tui/style.go` (four bg colours), `tui/transcript.go` (`Event` `tool_result` case), `tui/run.go` (`onApproval`)
- Test: `tui/transcript_test.go`, `tui/chat_test.go`

**Interfaces:**
- Consumes: `core.Diff`, `core.DiffLine` (Task 2); `approvalMsg.diff` (Task 4); `rule()` placement (Task 1).
- Produces:

```go
// renderDiff renders d in style B, at most maxLines diff rows (0 = no cap),
// each row cut to width (with "…") before it is styled.
func renderDiff(d *core.Diff, width, maxLines int) []string
// diffFromEvent decodes data["diff"] (a map after the redacting UI's JSON
// round trip) into a *core.Diff; nil when absent or malformed.
func diffFromEvent(data map[string]any) *core.Diff
```

- Row layout: `fmt.Sprintf("%6d %s %s", num, kind, text)` with the number faint; `-`/`+` rows padded to `width` on their line bg; `Spans` ranges on the brighter bg; context rows unstyled apart from the faint number; `⋮` row between hunks; `… N more lines` (faint) when cut by `maxLines`, `… diff truncated` when `d.Truncated`.
- Transcript: on `tool_result` with status `ok` and a diff, return the tool row followed by `renderDiff(d, t.Width, transcriptDiffLines)` in the same tool group (`transcriptDiffLines = 30`).
- Approval: `onApproval` prints `renderDiff(msg.diff, m.width, 0)` into scrollback (via `m.print`, before setting `m.approval`) so the question stays in the live region above the top rule.

- [ ] **Step 1: Write the failing tests**
  - `TestRenderDiff_RowsAndNumbers`: plain text (`ansi.Strip`, trailing spaces trimmed) of the middle-line diff from Task 2 is `"     3   c"`, `"     4 - d"`, `"     4 + D"`, `"     5   e"` (plus the rest of the context).
  - `TestRenderDiff_StyleB`: under `lipgloss` truecolor, the `-` row contains `bgSeq(diffDelBg)` and the span `D` contains `bgSeq(diffAddWordBg)`.
  - `TestRenderDiff_CapsLines`: a 100-line diff with `maxLines = 30` → 30 rows then `… 70 more lines`.
  - `TestRenderDiff_NarrowWidth`: width 10 → no panic, every row ≤ 10 runes.
  - `TestRenderDiff_TruncatedNote`: `Truncated` → last row `… diff truncated`.
  - `TestTranscript_EditResultShowsDiff`: `tool_result` event with a `diff` map (built via `json` round-trip of a `core.Diff`) → row `▸ edit f.go ok` followed by diff rows.
  - `TestTranscript_ErrorResultNoDiff`: status `error` with a diff key → only the row.
  - `TestChat_ApprovalPrintsDiffAboveQuestion`: drive an edit that needs approval (pattern of the existing approval chat tests) → the printed blocks contain a `- ` row and the view's prompt line still shows `[y/N/always]`.

- [ ] **Step 2: Run** `go test ./tui -run 'RenderDiff|ResultShowsDiff|ResultNoDiff|ApprovalPrintsDiff'` — expect FAIL.

- [ ] **Step 3: Implement** per the interfaces; colours from Global Constraints as named vars in `tui/style.go` (`diffDelBg`, `diffAddBg`, `diffDelWordBg`, `diffAddWordBg`). Check `TestBands_DistinctOnANSI16` style: add the two line bgs to it so they stay distinct on 16 colours.

- [ ] **Step 4: Run** `go test -race ./tui` — expect PASS.

- [ ] **Step 5: Commit** `feat(tui): render edit diffs in the transcript and approval prompt`

---

### Task 6: `jas run` printer diffs and docs

**Files:**
- Modify: `core/ui.go` (`NewPrinter`, `printer.Event`)
- Modify: `cmd/jas/run.go` (printer construction)
- Modify: `README.md` (Chat section bullet; run note)
- Create: `core/ui_test.go` (no printer tests exist yet)
- Test: `cmd/jas/run_test.go`

**Interfaces:**
- Consumes: `FormatDiffPlain` (Task 2); `diff` on `tool_result` (Task 4).
- Produces: `func NewPrinter(w io.Writer, diffs bool) UI` — with `diffs`, an ok `tool_result` carrying a `diff` prints `FormatDiffPlain` rows under the tool line. The payload arrives as a map (redacting UI), so decode it the same way as `diffFromEvent` (put a shared `func DiffFromPayload(v any) *Diff` in `core/diff.go` and have `tui.diffFromEvent` call it).

- [ ] **Step 1: Write the failing tests**
  - `TestPrinter_DiffsWhenEnabled`: printer with `diffs=true` given a `tool_result` map with a diff prints a `"     4 + D"` row.
  - `TestPrinter_NoDiffsWhenDisabled`: same event, `diffs=false` → no diff rows.
  - `TestCLI_RunPipedOutputHasNoDiff` (e2e, stdout is a pipe): a fake-OpenAI turn that writes a file with `--approve=never` → stdout has no `" + "` diff row.

- [ ] **Step 2: Run** `go test ./core ./cmd/jas -run 'Printer_|RunPipedOutput'` — expect FAIL.

- [ ] **Step 3: Implement**; in `cmd/jas/run.go` pass `isTerminal(int(os.Stdout.Fd()))` as `diffs`. README: add to the Chat list "Edits and writes show a diff (line numbers, `+`/`-`) in the approval prompt and under the tool row", and one line under the run flags that `jas run` prints diffs only on a terminal.

- [ ] **Step 4: Run** `go vet ./... && go test -race ./... && CGO_ENABLED=0 go build ./...` — expect PASS.

- [ ] **Step 5: Commit** `feat: show diffs in jas run on a terminal; document diffs`
