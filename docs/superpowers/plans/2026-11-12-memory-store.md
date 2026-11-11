# Memory Store Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A profile-owned memory store: a capped `MEMORY.md` index injected at session start, frontmatter topic notes read on demand, and owner-approved writes confined to `~/.jas/profiles/<name>/memory/`.

**Architecture:** A new dependency-free `memory` package holds the domain rules (project key, note format, index loading, write rules, prompt text, report). `core` gets a second profile lift (`ExecPolicy.ProfileMemory`, beside `ProfileSkills`) and a generic `Executor.WriteGuard` hook that runs before the approval gate. `cmd/jas` wires them: settings key, system prompt section, session metadata, reload/profile switch, farm workers, `/memory` and `jas memory`.

**Tech Stack:** Go (stdlib), `go.yaml.in/yaml/v3` (already in `go.mod`) for frontmatter, bubbletea TUI (existing).

**Spec:** `docs/superpowers/specs/2026-11-12-memory-store-design.md`

## Global Constraints

- Memory lives only under `~/.jas/profiles/<profile>/memory/`; project notes in `projects/<key>/`. Nothing in the repo.
- Index cap: both `MEMORY.md` files together at most **200 lines or 25 KB**; profile first; project keeps at least **50 lines or 6 KB**. Truncation line: `(memory index truncated at N lines; prune it)`.
- `<key>`: percent-encode every byte not in `A–Z a–z 0–9 . _` as `%XX` (uppercase hex), then `%2F` → `-`. Over **200** bytes: first **180** bytes + `~` + first **16** hex of SHA-256 of the full path.
- Note frontmatter: `name` (kebab-case, = file name without `.md`), `description` (one line, ≤ **200** chars), `type` ∈ `user`, `feedback`, `project`, `reference`. Unknown keys allowed.
- Every memory write asks at every approve level (`AskAlways`). Refusals happen before the prompt and are tool errors (status `error`); the turn continues.
- Headless runs, farm workers and the planner: memory read-only; never exit 5 because of memory.
- Setting `memory`: `on` | `read` | `off`, default `on`; not in `liveSettings`.
- Every #173 guard on the profile `skills/` lift applies unchanged to `memory/`; existing skills-lift tests stay green.
- `go test -race ./...` green, `CGO_ENABLED=0`; commits `type: description`.

## Review Focus

1. **Two sessions, one index:** session B `edit`s `MEMORY.md` after session A changed the same line → B gets the edit tool's no-match error, nothing is lost (Task 6 test `TestMemoryWrite_StaleEditFails`).
2. **Note path spelled through a symlinked `$HOME` / jas home** → still recognized as a memory write and still asks (Task 5 test `TestProfileMemory_SymlinkedHomeKeepsLift`).
3. **Owner hand-edits a note with broken frontmatter** → session still starts, index loads, `read` of the note works (Task 3 test `TestLoadIndexes_IgnoresNoteFrontmatter`).
4. **Model writes `MEMORY.md` in the project folder before the folder exists** → allowed (creating write), folder created (Task 4 test `TestGuard_CreatesProjectIndex`).
5. **Index file with CRLF line endings or no trailing newline** → line counting and the cap still exact (Task 3 test `TestLoadIndexes_CRLFAndNoTrailingNewline`).

---

### Task 1: Project key encoding

**Files:**
- Create: `memory/key.go`, `memory/key_test.go`
- Modify: `sessions/dir.go` (export the main-checkout lookup)

**Interfaces:**
- Produces: `memory.Key(root string) string`; `memory.DecodeKey(key string) (root string, ok bool)` (ok=false for a hashed key or a malformed one); `sessions.MainRoot(root string) string` — the main checkout of the repo holding root (the dir above a `.git` common dir), or root itself outside git. Refactor `sessions.Dir` to use it.

- [ ] **Step 1: Write failing tests** in `memory/key_test.go`:
  - `TestKey_Examples`: `Key("/home/u/src/jas") == "-home-u-src-jas"`, `Key("/home/u/my-repo") == "-home-u-my%2Drepo"`, `Key("/home/u/my/repo") == "-home-u-my-repo"`, `Key("/a/b c%") == "-a-b%20c%25"`.
  - `TestKey_RoundTrip`: for each of those plus `"/x/y_z.v1"`, `DecodeKey(Key(p))` returns `(p, true)`.
  - `TestKey_Injective`: table of path pairs that a `/`→`-` scheme would merge (`/a/b-c` vs `/a/b/c`, `/a-/b` vs `/a/-b`) give different keys.
  - `TestKey_LongPathHashed`: a 300-byte path gives a key of length `180+1+16`, contains `~`, is stable across calls, differs for two long paths sharing the first 250 bytes; `DecodeKey` returns `ok=false`.
  - In `sessions`, `TestMainRoot_WorktreeSharesMain`: a linked worktree's `MainRoot` equals the main checkout (reuse the worktree helper from the existing `Dir` tests).
- [ ] **Step 2: Run** `go test ./memory ./sessions` — expect FAIL (undefined).
- [ ] **Step 3: Implement** `Key`, `DecodeKey` per Global Constraints; `MainRoot` by lifting the `common`/`main` logic out of `Dir`.
- [ ] **Step 4: Run** `go test ./memory ./sessions` — PASS.
- [ ] **Step 5: Commit** `feat: add memory project keys`

### Task 2: Note frontmatter

**Files:**
- Create: `memory/note.go`, `memory/note_test.go`

**Interfaces:**
- Produces: `type Note struct{ Name, Description, Type string }`; `var Types = []string{"user", "feedback", "project", "reference"}`; `memory.ParseNote(content string) (Note, error)` (reads a leading `---` … `---` YAML block; unknown keys ignored); `memory.ValidateNote(fileName, content string) error` where fileName is the base name (`x.md`). Error texts start with `memory note needs frontmatter: ` followed by the specific reason.

- [ ] **Step 1: Write failing tests** `TestValidateNote` (table):
  - the spec's example note named `squash-merge-after-create.md` → nil.
  - no frontmatter → error containing `memory note needs frontmatter`.
  - missing `type` → `type must be one of user, feedback, project, reference`.
  - `type: idea` → same message.
  - `name: Foo_Bar` → error containing `kebab-case`.
  - `name` ≠ file name → error containing `must match the file name`.
  - `description` with a newline, or of 201 chars → error containing `description`.
  - extra key `status: retired` → nil.
- [ ] **Step 2: Run** `go test ./memory -run TestValidateNote` — FAIL.
- [ ] **Step 3: Implement** `ParseNote`, `ValidateNote` with `yaml.v3` into a `map[string]any`; kebab-case is `^[a-z0-9]+(-[a-z0-9]+)*$`.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat: add memory note frontmatter`

### Task 3: Index loading and prompt section

**Files:**
- Create: `memory/store.go`, `memory/store_test.go`, `memory/prompt.go`

**Interfaces:**
- Consumes: `Key` (Task 1).
- Produces:
  - `type Dirs struct{ Profile, Project string }` and `memory.DirsFor(profileDir, mainRoot string) Dirs` → `Profile = <profileDir>/memory`, `Project = <Profile>/projects/<Key(mainRoot)>`.
  - `type Index struct{ Path, Text string; Lines, Bytes int; Truncated bool; SHA256 string; Exists bool }` (Text is after the cap; Lines/Bytes before it).
  - `memory.LoadIndexes(d Dirs, hideProject bool) (profile, project Index, err error)` — missing files are `Exists=false`, not errors.
  - `memory.Section(d Dirs, profile, project Index, writable bool) string` — the system prompt section. Contains a `Memory` heading, both absolute dirs, each index under a heading naming its dir, and the guidance; when `!writable` the guidance says writes are not available in this session. Copy the guidance bullets from the spec's "Writing guidance" section verbatim.
  - Constants `MaxIndexLines = 200`, `MaxIndexBytes = 25 << 10`, `MinProjectLines = 50`, `MinProjectBytes = 6 << 10`.
- [ ] **Step 1: Write failing tests:**
  - `TestLoadIndexes_CombinedCap`: profile 180 lines + project 80 lines → profile cut to 150, project 50, both `Truncated`, each Text ends with `(memory index truncated at 150 lines; prune it)` / `… at 50 lines …`.
  - `TestLoadIndexes_ProfileOnlyGetsWholeBudget`: no project file → profile keeps 200 lines.
  - `TestLoadIndexes_ByteCap`: 30 KB profile, empty project → Text ≤ 25 KB plus the truncation line, cut on a line boundary.
  - `TestLoadIndexes_HideProject`: `hideProject=true` → project `Exists=false` even when the file is there.
  - `TestLoadIndexes_IgnoresNoteFrontmatter`: a note with broken frontmatter beside the index → no error (Review Focus 3).
  - `TestLoadIndexes_CRLFAndNoTrailingNewline`: CRLF file of 201 lines without final newline → `Lines == 201`, cut to 200 (Review Focus 5).
  - `TestSection_NamesDirsAndGuidance`: output contains both dirs, `[[name]]`, `never a whole-file`, `cannot delete`; with `writable=false` contains `not available in this session`.
- [ ] **Step 2: Run** `go test ./memory` — FAIL.
- [ ] **Step 3: Implement.** Budget algorithm: project reserve = `min(projectNeed, MinProject*)`; profile gets `Max − reserve`; project gets `Max − profileUsed`; apply per dimension (lines and bytes) and cut at whichever binds first.
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat: load memory indexes under one cap`

### Task 4: Write rules

**Files:**
- Create: `memory/guard.go`, `memory/guard_test.go`

**Interfaces:**
- Consumes: `Dirs` (Task 3), `ValidateNote` (Task 2).
- Produces:
  - `type Mode int` with `ModeOn`, `ModeRead`, `ModeOff`; `memory.ParseMode(s string) (Mode, error)`.
  - `memory.ResolveMode(base, project string) (mode Mode, hideProject bool)` — base is the merged global+profile value, project the merged project+local value ("" = unset). Rules: base `off` → Off; base `read` → Read; base `on`: project `read` → Read; project `off` → Read with hideProject; project `on`/"" → On. Project `off`/`read` never turn a base `off` on.
  - `type Guard struct{ Dirs Dirs; Mode Mode; Headless bool; Redact func(string) string }`.
  - `func (g Guard) Holds(resolved string) bool` — resolved lies in `Dirs.Profile`.
  - `func (g Guard) Check(tool, resolved string, existed bool, newContent string) error` — nil when not `Holds`. Order: mode/headless (`memory is read-only in this run: no one can approve the write` when Headless; `memory is read-only in this session (memory: read)` for ModeRead); folder rule (only `Dirs.Profile/*` or `Dirs.Project/*`, no deeper; message names the allowed dirs); `.md` only; `MEMORY.md` with `tool=="write" && existed` → `change MEMORY.md with edit, not a whole-file write`; notes → `ValidateNote`; secrets: `Redact(newContent) != newContent` → `memory notes cannot hold secrets; describe where the secret lives instead`.
- [ ] **Step 1: Write failing tests** `TestGuard_Check` (table, one row per rule above, each asserting the exact message prefix), plus:
  - `TestGuard_OtherProjectRefused`: `projects/<other-key>/x.md` refused; `projects/<key>/sub/x.md` refused.
  - `TestGuard_CreatesProjectIndex`: `write` of `projects/<key>/MEMORY.md` with `existed=false` → nil (Review Focus 4).
  - `TestGuard_EditIndexAllowed`: `edit` on existing `MEMORY.md` → nil.
  - `TestGuard_OutsideMemoryIgnored`: a project file → nil.
  - `TestResolveMode` table covering all twelve combos (base on|read|off × project ""|on|read|off).
- [ ] **Step 2: Run** `go test ./memory -run 'TestGuard|TestResolveMode'` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat: add memory write rules`

### Task 5: Profile memory lift in core

**Files:**
- Modify: `core/tools.go` (ExecPolicy field), `core/policy.go`, `core/deny.go`
- Test: `core/policy_memory_test.go`

**Interfaces:**
- Produces: `ExecPolicy.ProfileMemory string` (spelled `<home>/profiles/<name>/memory`; "" disables); `func (p ExecPolicy) InProfileMemory(resolved string) bool`; `func (p ExecPolicy) ProfileMemoryIgnored() string`. `Resolve`, `ResolveRead`, `ReadScope`, `Denied`, `DenyMatcher` treat it exactly like `ProfileSkills`.
- Approach: turn `profileLift()` into `profileLift(spelled, leaf string)` and add `lifts() []lift` (skills, memory). `denyMatcher.lift` becomes `[]lift` with a `holdsAny`. `profileWrite` tries each lift. `readRoot` sets `liftHome` when the read root is inside any lift. Keep the doc comments' rules; update wording from "skills" to "profile skills or memory".

- [ ] **Step 1: Write failing tests** mirroring the existing `ProfileSkills` tests for memory (find them with `grep -n ProfileSkills core/*_test.go`):
  - `TestProfileMemory_WriteLifted`: `Resolve(<memory>/x.md)` succeeds; `InProfileMemory` true.
  - `TestProfileMemory_ReadLifted`: `ResolveRead` of an existing note succeeds when `ProfileMemory` and the memory dir in `ReadRoots` are set.
  - `TestProfileMemory_EnvStillDenied`: `<memory>/.env` denied for read and write.
  - `TestProfileMemory_LinkToAuthDenied`: a symlink inside memory to `<home>/auth.json` denied.
  - `TestProfileMemory_LinkToJasHomeIgnored`: `memory` → symlink to jas home → no lift, `ProfileMemoryIgnored` non-empty.
  - `TestProfileMemory_MissingDirLifted`: no `memory/` yet → write lifted.
  - `TestProfileMemory_SymlinkedHomeKeepsLift` (Review Focus 2).
  - `TestProfileMemory_SkillsUnaffected`: with both set, a skills write still lifts and a memory path is not `InProfileSkills`.
- [ ] **Step 2: Run** `go test ./core -run TestProfileMemory` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./core` — all PASS, including every existing skills-lift test.
- [ ] **Step 5: Commit** `feat: lift the profile memory dir like profile skills`

### Task 6: Write guard hook in the executor

**Files:**
- Modify: `core/exec.go` (`Executor`, `runMutating`, `profileSkillWrite`)
- Test: `core/exec_memory_test.go`

**Interfaces:**
- Consumes: `InProfileMemory` (Task 5); `Previewer` / `Change` (existing).
- Produces: `Executor.WriteGuard func(tool, resolved string, existed bool, newContent string) error` (nil = no check). In `runMutating`, for `write`/`edit` whose resolved path is `InProfileMemory`: compute the change through the tool's `Previewer` (`Change.New` is the whole file after; `existed` from `os.Stat` of resolved); call `WriteGuard`; on error settle `statusError` with the error text and return **before** the gate (no `approval` event). Otherwise wrap with `AskAlways`, as `profileSkillWrite` does now (rename it `profileWrite(s) bool` covering both lifts). If the preview itself fails, refuse with `memory write could not be checked: <err>`.
- [ ] **Step 1: Write failing tests** with a fake gate that records calls and a `WriteGuard` stub:
  - `TestMemoryWrite_GuardRefusesBeforeGate`: guard error → `tool_result` status `error`, output = guard text, gate not called, no `approval` event, turn continues (`stopErr` nil).
  - `TestMemoryWrite_AlwaysAsks`: guard nil-error, gate level `never` (AllowAll) still sees a must-ask tool (`MustAsk` true).
  - `TestMemoryWrite_EditPassesWholeFile`: `edit` on a 3-line note → guard receives the full post-edit file and `existed=true`.
  - `TestMemoryWrite_StaleEditFails`: file changed between two edits → the stale edit returns the edit tool's no-match error and the file still holds the other session's change (Review Focus 1).
  - `TestMemoryWrite_NonMemoryUnchanged`: a project write never calls the guard.
- [ ] **Step 2: Run** `go test ./core -run TestMemoryWrite` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./core ./tools` — PASS.
- [ ] **Step 5: Commit** `feat: check memory writes before the approval gate`

### Task 7: Wire memory into sessions

**Files:**
- Modify: `settings/config.go` (key), `cmd/jas/setup.go` (policy, meta, guard), `cmd/jas/context.go` (prompt section, context files), `cmd/jas/chat.go` and `cmd/jas/profile_chat.go` (reload, profile switch, worker profile), `cmd/jas/farm_run.go`, `cmd/jas/farm_cmd.go` (headless)
- Test: `cmd/jas/memory_test.go`

**Interfaces:**
- Consumes: Tasks 1–6.
- Produces:
  - `settings.Keys` entry `{Name: "memory", Default: "on", Options: []string{"on", "read", "off"}, Validate: oneOf(...)}`.
  - `env` gains `mem memoryState` with `dirs memory.Dirs`, `mode memory.Mode`, `hideProject bool`, `profile, project memory.Index`. Computed in setup from `settings.Layers`: base = Profile layer value, else Global; project = Local value, else Project; `memory.ResolveMode`.
  - Policy: `policy.ProfileMemory = <ProfileDir>/memory` and `dirs.Profile` appended to `ReadRoots` unless mode Off.
  - `Executor.WriteGuard` = `memory.Guard{Dirs, Mode, Headless: !interactive, Redact: e.red.Redact}.Check` adapter; farm workers and planner always `Headless: true`.
  - System prompt: `memory.Section(...)` appended as the last `BuildSystem` section of both `system` and `userSystem`; omitted when Off. Redact index text like `AGENTS.md`.
  - `context_files`: each existing index appended as `{path, loaded: true, sha256}`.
  - `sessionMeta` adds `"memory": {"profile_dir", "project_dir", "index_bytes", "truncated"}` when not Off (`project_dir` "" when hidden; `index_bytes` = bytes injected; `truncated` = either index cut).
  - `/reload` and `/profile` recompute `mem` (new profile → new dirs) and pass memory files in `context_files`; `SetProfileMemory(dir string)` on `core.Session` beside `SetProfileSkills`; `workerProfile` carries `profileMemory`.
- [ ] **Step 1: Write failing tests** (use the existing `cmd/jas` test env helpers):
  - `TestMemory_IndexInSystemPrompt`: profile and project `MEMORY.md` present → system prompt contains both texts and both dirs; `session_started` has `memory.index_bytes > 0` and both files in `context_files`.
  - `TestMemory_OffRemovesEverything`: global `memory=off` → no section, no `memory` field, write to the memory dir is denied by policy.
  - `TestMemory_ProjectOffHidesProjectOnly`: project config `memory=off` → profile text present, project text absent, writes refused as read-only.
  - `TestMemory_HeadlessWriteIsToolError`: `jas run` (non-interactive) where the fake model writes a note → tool_result `error` with `read-only in this run`, run exits 0.
  - `TestMemory_WorktreesShareKey`: two worktrees of one repo → same `project_dir`.
  - `TestMemory_ReloadPicksUpNewNote`: write index after start, `/reload` → new text in prompt, `context_reloaded.context_files` lists it.
  - `TestMemory_FarmWorkerReadOnly`: worker session's guard has `Headless: true`.
- [ ] **Step 2: Run** `go test ./cmd/jas -run TestMemory` — FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` — PASS.
- [ ] **Step 5: Commit** `feat: load and guard profile memory in sessions`

### Task 8: `/memory` and `jas memory`

**Files:**
- Create: `memory/report.go`, `cmd/jas/memory_cmd.go`
- Modify: `tui/commands.go`, `tui/backend.go`, `cmd/jas/chat.go` (Backend impl), `cmd/jas/main.go` (dispatch)
- Test: `memory/report_test.go`, `cmd/jas/memory_cmd_test.go`, `tui/commands_test.go`

**Interfaces:**
- Consumes: `Dirs`, `Index`, `Mode` (Tasks 3–4).
- Produces: `memory.Report(d Dirs, profile, project Index, mode Mode) string` — for each store: dir, `N/200 lines, X/25 KB`, `truncated` when cut, note count (`.md` files other than `MEMORY.md`), and the mode. `Backend.MemoryReport() string`. `/memory` (help text `show memory dirs and index size`) prints it. `jas memory` prints it; `jas memory --path` prints only `Dirs.Project`.
- [ ] **Step 1: Write failing tests:** `TestReport_Counts` (2 notes + index → `2 notes`, `truncated` shown only when cut); `TestMemoryCmd_Path` (stdout = project dir + `\n`); `TestCommand_Memory` (tui prints the backend report).
- [ ] **Step 2: Run** `go test ./memory ./cmd/jas ./tui -run 'Report|MemoryCmd|Command_Memory'` — FAIL.
- [ ] **Step 3: Implement** (reuse `Report` for both entry points).
- [ ] **Step 4: Run** — PASS.
- [ ] **Step 5: Commit** `feat: add /memory and jas memory`

### Task 9: Docs and contract

**Files:**
- Modify: `docs/manual.md`, `docs/event-log.md`, `core/testdata/eventlog/v1.jsonl`, `core/eventlog_contract_test.go`, `docs/design.md` (§5 checkbox, §12.3 status), then `make manual`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write failing test** `TestEventLogFixture_MemoryField` in `core/eventlog_contract_test.go`: the fixture's `session_started` has a `memory` object with keys `profile_dir`, `project_dir`, `index_bytes`, `truncated`.
- [ ] **Step 2: Run** — FAIL.
- [ ] **Step 3:** Add the `memory` field to the fixture's `session_started` and `session_resumed`; document it under `session_started` in `docs/event-log.md` (optional, additive). Manual: a "Memory" section (layout, note format, `memory` setting values and layering, `/memory`, `jas memory`, headless read-only, the lift's guards next to the skills paragraph); `memory` in the settings table; `/memory` in commands; `jas memory` in subcommands. Design §5: mark Memory store shipped with the PR number. Run `make manual`.
- [ ] **Step 4: Run** `go test -race ./...` — PASS (including `TestManual_*`).
- [ ] **Step 5: Commit** `docs: document the memory store`
