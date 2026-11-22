# Read Dedup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `read` call whose output equals the latest identical, still-visible `read` result returns a one-line note instead of the content (#217).

**Architecture:** The Executor gets `RunWithHistory(ctx, history, calls)`; for each `read` result, after `Filter` and before the `tool_result` event is logged, it looks back through `history` (the session's current messages) for the latest earlier `read` of the same resolved file and range, and swaps the output for a note when that earlier result is intact and byte-identical. The Session calls `RunWithHistory` with `s.messages`. No state is kept anywhere: compaction, elision and resume are handled because only the current history is consulted.

**Tech Stack:** Go, existing `core` package; tests with `providers/fake` and `internal/assert`.

**Spec:** `docs/superpowers/specs/2026-11-22-read-dedup-design.md`

## Global Constraints

- `read` only; `ls`, `grep`, `shell` unchanged.
- Note text: `unchanged since your read of <path> <N> turns ago (same range)` (`1 turn ago` for N = 1); `<path>` is the path as the current call spelled it. It must not claim the content is still in context.
- `tool_result` event of a deduplicated call: `output` is the note, plus `unchanged: true`. All existing fields stay; `--json` output and exit codes unchanged.
- Dedup happens before the `tool_result` event is logged, so replay rebuilds the note.
- Keep `go test -race ./...` green; `CGO_ENABLED=0`. Commits `type: description`.
- Docs in the same change: `docs/manual.md` (`read` row, line ~433), `docs/event-log.md` (`tool_result`, line 252), then `make manual`.

## Review Focus

1. A path that fails to resolve now (file deleted, path outside root) must never produce a note: the call's own result is an error, and errors are never deduplicated. Test in Task 1 (`TestExec_ReadDedupErrorNotDeduped`).
2. An earlier `read` whose tool_use input is malformed or has extra keys must be skipped, not panic. Test in Task 1 (`TestExec_ReadDedupIgnoresBadEarlierArgs`).
3. Offset spellings `0`, `1` and absent read the same lines and must match each other; `limit` `0` and absent likewise. Test in Task 1 (`TestExec_ReadDedupOffsetSpellings`).
4. A long session where the earlier read was compacted away must send content. Test in Task 2 (`TestSession_ReadDedupAfterCompact`).
5. Truncated output (file over the output limit) read twice with the same range is byte-identical and may be noted; that is correct, since the same truncated text is visible. No extra test: covered by output equality.

---

### Task 1: Executor read dedup

**Files:**
- Create: `core/readdedup.go`
- Modify: `core/exec.go` (`Run`, `run` struct, `emitResult`)
- Test: `core/exec_dedup_test.go` (package `core_test`)

**Interfaces:**
- Produces: `func (e *Executor) RunWithHistory(ctx context.Context, history []providers.Message, calls []providers.ContentBlock) ([]providers.ContentBlock, error)` — same contract as `Run`; `Run(ctx, calls)` becomes `RunWithHistory(ctx, nil, calls)`.
- Produces (unexported, `core/readdedup.go`): `const readUnchangedPrefix = "unchanged since your read of "`; `func (p ExecPolicy) dedupRead(history []providers.Message, call providers.ContentBlock, res Result) (Result, bool)` — returns the note result and true when deduplicated.

- [ ] **Step 1: Write the failing tests** in `core/exec_dedup_test.go`. Build history by hand: an assistant message with a `tool_use` block (`ToolUseID`, `Name: "read"`, `Input`) followed by a user message with its `tool_result` block, then an assistant message holding the current call (the session pushes it before running tools). Use a temp root with a file `a.go` and an Executor like `newRig`'s (`tools.ReadOnly()`, `AllowAll`, an event log).

```go
func TestExec_ReadDedupSameRead(t *testing.T)            // earlier result == file content; result Text == "unchanged since your read of a.go 1 turn ago (same range)"; tool_result event has unchanged == true and output == the note
func TestExec_ReadDedupTurnsAgo(t *testing.T)            // two assistant messages between: "... 2 turns ago (same range)"
func TestExec_ReadDedupChangedFile(t *testing.T)         // earlier result text differs from current content: full content, no "unchanged" field
func TestExec_ReadDedupDifferentRange(t *testing.T)      // earlier {"path":"a.go"}, now {"path":"a.go","offset":2}: full content
func TestExec_ReadDedupResolvedPath(t *testing.T)        // earlier {"path":"./a.go"}, now {"path":"a.go"}: note naming "a.go"
func TestExec_ReadDedupOffsetSpellings(t *testing.T)     // earlier {"path":"a.go","offset":1,"limit":0}, now {"path":"a.go"}: note
func TestExec_ReadDedupElidedEarlier(t *testing.T)       // earlier result "[elided: read a.go 3 lines]": full content
func TestExec_ReadDedupAfterNote(t *testing.T)           // read 1 content, read 2 note, now read 3: full content
func TestExec_ReadDedupErrorNotDeduped(t *testing.T)     // earlier result IsError for a missing file, file now exists: full content; and a current error is never a note
func TestExec_ReadDedupIgnoresBadEarlierArgs(t *testing.T) // earlier tool_use Input `{"path":` (invalid): full content, no panic
func TestExec_ReadDedupParallelBatch(t *testing.T)       // empty history, two identical reads in one Run: both full content
func TestExec_RunHasNoDedup(t *testing.T)                // Run (no history) on the same call: full content
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./core -run 'TestExec_ReadDedup|TestExec_RunHasNoDedup'`
Expected: build failure, `RunWithHistory` undefined.

- [ ] **Step 3: Implement `RunWithHistory` and `dedupRead`.** Store `history` on the `run` struct; in `emitResult`, after `Filter` and only when `s.status == statusOK`, call `r.e.Policy.dedupRead(r.history, s.call, s.res)`; on true set `s.res` to the note and add `logged["unchanged"] = true`. `dedupRead`:
  - Only `call.Name == "read"` and `!res.IsError`.
  - Key a read by `(resolved path, offset, limit)`: decode `{path, offset, limit}`, resolve with `p.ResolveRead`, normalize `offset < 1` to 1 and `limit < 0` to 0. Compare resolved paths with `samePath` (`core/pathcase.go`).
  - Walk `history` backwards over `tool_result` blocks; map each `ToolUseID` to its `tool_use` block. The **latest** earlier result whose call has the same key decides: if its text starts with `readUnchangedPrefix` or `elidedPrefix`, or it `IsError`, return content; if its text equals `res.Output`, return the note; otherwise content. Skip earlier calls whose args fail to decode or resolve.
  - N = number of assistant messages in `history` after the one holding the matched `tool_use`.

- [ ] **Step 4: Run the tests**

Run: `go test ./core -run 'TestExec_'` then `go test ./core`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add core/readdedup.go core/exec.go core/exec_dedup_test.go
git commit -m "feat: dedup a read whose output is still in the history (#217)"
```

### Task 2: Session wiring, compaction, elision, resume

**Files:**
- Modify: `core/session.go:479` (`c.Exec.Run(ctx, uses)` → `c.Exec.RunWithHistory(ctx, s.messages, uses)`)
- Modify: `docs/manual.md` (`read` row ~433), `docs/event-log.md:252`; run `make manual`
- Test: `core/session_dedup_test.go` (package `core_test`)

**Interfaces:**
- Consumes: `Executor.RunWithHistory` from Task 1; note prefix text from Global Constraints.

- [ ] **Step 1: Write the failing tests** using `newRig`/`newSession` with a file `a.go` in `r.root` and fake turns calling `{Name: "read", Args: `{"path":"a.go"}`}`.

```go
func TestSession_ReadDedupSecondRead(t *testing.T)    // turns: read, read, text "done"; Requests[2] last tool_result text starts with "unchanged since your read of a.go"
func TestSession_ReadDedupAfterCompact(t *testing.T)  // read, then s.Compact(ctx, ""), then read: full content in the next request
func TestSession_ReadDedupNoteThenElided(t *testing.T) // Budget{Window: 500, ElideAt: 0.48, Keep: 1} (as core/budget_loop_test.go:38); read a.go, read a.go (note), read b.go (elides the first a.go), read a.go: last result is full content
func TestSession_ReadDedupResume(t *testing.T)        // after a noted read, close; core.Replay(r.logPath) messages hold the note text for that call
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./core -run TestSession_ReadDedup`
Expected: FAIL (second read returns full content).

- [ ] **Step 3: Switch the session call to `RunWithHistory(ctx, s.messages, uses)`; update docs.**
  - `docs/manual.md` `read` row: add "a repeat of an unchanged read of the same file and range, while that result is still in the history, returns a one-line note instead; the next identical read returns the content".
  - `docs/event-log.md` `tool_result`: "optional `diff`, `unchanged` (true: a repeated `read` whose output was replaced by a note, #217)".
  - `make manual`.

- [ ] **Step 4: Run the full suite**

Run: `make test` and `CGO_ENABLED=1 go test -race ./core ./tui ./farm`
Expected: all `ok`; `TestManual_*` pass.

- [ ] **Step 5: Commit**

```bash
git add core/session.go core/session_dedup_test.go docs/manual.md docs/event-log.md cmd/jas/manual/jas/references/manual.md
git commit -m "feat: sessions dedup repeated reads; document it (#217)"
```
