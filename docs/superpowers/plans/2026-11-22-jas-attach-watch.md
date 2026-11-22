# `jas attach` Phase 1 (Watch) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `jas attach [<farm-id> [<part>]]` shows a running farm's state and follows its events from another terminal, read from the farm's files; no socket (PR 1 of the attach design).

**Architecture:** The farm process holds a liveness lock (`<home>/run/<farm-id>.lock`) for its lifetime. A `farm.Follow` reads `farm.jsonl` and every `parts/<part>/events.jsonl`, and feeds decoded events to the existing `farm.Watcher` interface, so `farm.Progress` prints the same lines `jas farm run` prints. The CLI finds the farm, prints the `jas farm status` table, then runs the follower until the farm ends, its process dies, or Ctrl-C.

**Tech Stack:** Go; `core.LockPath`; existing `farm.Progress`, `farm.LoadManifest`, `printFarmStatus`.

**Spec:** `docs/superpowers/specs/2026-11-22-jas-attach-design.md` (sections "Finding a farm", "Phase 1: watching", "Phasing" PR 1).

## Global Constraints

- Inline output only: plain lines, no alt-screen, no cursor movement.
- No change to `jas run`, `jas farm run --json` stdout, `farm.jsonl` contents, or exit codes 6/7/8.
- Lock: `<jas home>/run/<farm-id>.lock` via `core.LockPath` (dir 0700, file 0600); pid in a sibling `<farm-id>.pid` (0600) — `LockPath` writes nothing, and on Windows a byte-range lock blocks writes from another handle. A starting farm retries `ErrLocked` 3 times, 100 ms apart, before failing. A probe takes the lock and releases it at once.
- "Running" = the lock is held. Not running + `farm_ended` in `farm.jsonl` = ended; not running without it = the process died.
- Ctrl-C detaches (exit 0); the farm is untouched.
- Docs in the same change: `docs/manual.md` (+ `make manual`), `cmd/jas/manual/jas/SKILL.md` subcommand summary, README command list; `TestManual_*` / `TestManualSkill_*` must pass.
- `CGO_ENABLED=0`; `go test -race ./...` green; commits `type: description`.

## Review Focus

1. A farm dir that exists but has no `manifest.json` yet (farm starting) must not crash attach: print `farm <id> is starting` instead of the table, then follow. Test in Task 3 (`TestAttach_StartingFarm`).
2. A malformed or unknown-type line in a log is skipped, never printed half or fatal. Test in Task 2 (`TestFollow_SkipsBadLines`).
3. Two farm ids sharing the given prefix → usage error naming both, exit 2. Test in Task 3 (`TestAttach_AmbiguousPrefix`).
4. The farm process dies while attached (lock released, no `farm_ended`) → attach prints `farm process ended` and exits 0 after draining what is on disk. Test in Task 2 (`TestFollow_ProcessDied`).
5. A large part log at attach time: only the last `Backlog` tool calls per part are printed, not the whole history. Test in Task 2 (`TestFollow_Backlog`).

---

### Task 1: Farm liveness lock

**Files:**
- Create: `farm/live.go`, `farm/live_test.go`
- Modify: `farm/run.go` (`startFarm` ~266: take the lock right after `dir` is made; store `release` on `farmRun`; `close()` ~323 releases it and removes the pid file)

**Interfaces:**
- Produces: `const runDirName = "run"`; `func LockFarm(home, id string) (release func() error, err error)` — takes `<home>/run/<id>.lock` with the retry rule, writes `<home>/run/<id>.pid`; release drops the lock and removes the pid file. `func FarmRunning(home, id string) bool` — probe.

- [ ] **Step 1: Write the failing tests** in `farm/live_test.go`:

```go
func TestLockFarm_RunningWhileHeld(t *testing.T)    // LockFarm → FarmRunning true; release → false; pid file holds os.Getpid() while held, gone after
func TestLockFarm_SecondHolderFails(t *testing.T)   // LockFarm twice for one id → second returns core.ErrLocked after its retries
func TestLockFarm_RetriesPastProbe(t *testing.T)    // core.LockPath held by the test, released after 100 ms in a goroutine → LockFarm succeeds
func TestFarmRunning_UnknownFarm(t *testing.T)      // no lock file → false, and the probe leaves no held lock
func TestLockFarm_Permissions(t *testing.T)         // run dir 0700, lock and pid files 0600 (skip on windows)
func TestStartFarm_HoldsLock(t *testing.T)          // with the existing handle test rig: StartFarm → FarmRunning(home, h.FarmID()) true; after Wait → false
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./farm -run 'TestLockFarm|TestFarmRunning|TestStartFarm_HoldsLock'`
Expected: build failure (`LockFarm` undefined).

- [ ] **Step 3: Implement `LockFarm`, `FarmRunning`, and wire `startFarm`/`close`.** A `startFarm` lock failure returns the error (the farm does not start); release on every later error path in `startFarm`.

- [ ] **Step 4: Run the tests**

Run: `go test ./farm`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add farm/live.go farm/live_test.go farm/run.go
git commit -m "feat: farms hold a liveness lock under run/"
```

### Task 2: Following a farm's logs

**Files:**
- Create: `farm/follow.go`, `farm/follow_test.go`

**Interfaces:**
- Consumes: `FarmRunning` (Task 1); `Watcher` (`farm/progress.go`); `EvFarmEnded` and log names (`farmLogName`, `parts/<part>/events.jsonl`).
- Produces:

```go
type Follow struct {
	Dir     string        // the farm dir
	Watch   Watcher
	Backlog int           // tool_started events replayed per part at start
	Poll    time.Duration // 0 = 250ms
	Running func() bool   // nil = always running
}
// Outcome: FollowEnded (farm_ended read), FollowDied (Running false, no farm_ended), FollowDetached (ctx done).
type FollowOutcome int
func (f *Follow) Run(ctx context.Context) (FollowOutcome, error)
```

- [ ] **Step 1: Write the failing tests** in `farm/follow_test.go`, using a temp farm dir and a recording `Watcher`; append lines to the files from the test while `Run` runs in a goroutine (Poll 10ms).

```go
func TestFollow_FarmAndPartEvents(t *testing.T) // appended farm.jsonl line → FarmEvent(typ, m); part line → WorkerEvent("p1", typ, m)
func TestFollow_ArgsAsRawMessage(t *testing.T)  // a tool_started line's "args" reaches the watcher as json.RawMessage (toolLine reads it that way)
func TestFollow_PartialLine(t *testing.T)       // half a line written, then the rest: one event, delivered once, after the newline
func TestFollow_NewPart(t *testing.T)           // parts/p2/events.jsonl created after Run started → its events delivered
func TestFollow_SkipsBadLines(t *testing.T)     // "not json\n" and {"type":42} lines: skipped; the next good line still delivered
func TestFollow_Backlog(t *testing.T)           // part log with 10 tool_started before Run, Backlog 3 → exactly the last 3 replayed, then live events; farm.jsonl history not replayed
func TestFollow_Ended(t *testing.T)             // farm_ended appended → FarmEvent for it, Run returns FollowEnded
func TestFollow_ProcessDied(t *testing.T)       // Running flips false; lines already on disk are delivered first; Run returns FollowDied
func TestFollow_Detach(t *testing.T)            // ctx cancel → FollowDetached, nil error
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./farm -run TestFollow`
Expected: build failure (`Follow` undefined).

- [ ] **Step 3: Implement `Follow.Run`.** Keep a byte offset per file; read from it, deliver only newline-terminated lines, keep the remainder for the next poll. Decode each line into `map[string]any`, re-encode `args` as `json.RawMessage`, take `type` (string, else skip). Part name is the `parts/<part>` directory name. Backlog: at start, scan each existing part log once, deliver its last `Backlog` `tool_started` events, then follow from its end; `farm.jsonl` starts at its end. Check `Running` once per poll after reading; on false, do one final read, then return `FollowDied`.

- [ ] **Step 4: Run the tests**

Run: `go test ./farm -run TestFollow` then `go test -race ./farm`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add farm/follow.go farm/follow_test.go
git commit -m "feat: follow a farm's logs into a watcher"
```

### Task 3: `jas attach` command and docs

**Files:**
- Create: `cmd/jas/attach_cmd.go`, `cmd/jas/attach_test.go`
- Modify: `cmd/jas/main.go` (dispatch `case "attach"`, usage text), `docs/manual.md`, `cmd/jas/manual/jas/SKILL.md`, `README.md`; run `make manual`

**Interfaces:**
- Consumes: `farm.FarmRunning`, `farm.Follow`/`FollowOutcome` (Tasks 1–2); `farm.NewProgress`, `farm.LoadManifest`, `printFarmStatus`, `farmIDArg`, `settingsHome`, `newRedactor` (existing).
- Produces: `func attachCmd(ctx context.Context, args []string) error`.

Behavior (the spec's "Finding a farm" and "Phase 1"):
- No argument: running farms = farm dirs whose lock is held. One → attach. Several → list `id  started  parts running` and return a usage error (exit 2) asking for an id. None → print `no farm is running (see jas farm status)`, exit 0.
- `<farm-id>`: exact or unique prefix among farm dirs; ambiguous → usage error naming the matches (exit 2); none → usage error (exit 2).
- Running farm: print the `printFarmStatus` table (or `farm <id> is starting` when there is no manifest yet), then `Follow{Backlog: 3, Running: FarmRunning(...), Watch: progress}`; on `FollowEnded` print nothing extra (Progress prints the farm_ended line), on `FollowDied` print `farm process ended`; exit 0 in all three outcomes. Ctrl-C (ctx) → exit 0.
- Ended or dead farm at attach time: print the table, then `farm ended: <reason>` (from `farm_ended`) or `farm process ended without finishing`, exit 0.
- `<farm-id> <part>`: the part must exist in the manifest (else usage error, exit 2). The watcher also prints, for that part only, `[part] says: <reply text>` for `assistant_message` (first 300 runes, one line) and `[part] -> <status>: <first output line>` for `tool_result` (first 160 runes).
- The table uses the existing `jas farm status` columns (ruling: the chat dashboard lives in `tui` and is a Bubble Tea view; the CLI table is the inline equivalent).

- [ ] **Step 1: Write the failing tests** in `cmd/jas/attach_test.go`, with a temp `JAS_HOME`, fixture farm dirs (manifest + logs), and the lock taken with `farm.LockFarm` for "running" fixtures:

```go
func TestAttach_NoneRunning(t *testing.T)     // only ended farms → output "no farm is running", nil error
func TestAttach_SeveralRunning(t *testing.T)  // two locked farms, no arg → exit code 2, both ids in the message
func TestAttach_AmbiguousPrefix(t *testing.T) // ids 20261122-aa1 and 20261122-aa2, arg "20261122-aa" → exit 2 naming both
func TestAttach_EndedFarm(t *testing.T)       // unlocked farm with farm_ended{reason:"ok"} → table + "farm ended: ok", nil error
func TestAttach_DeadFarm(t *testing.T)        // unlocked, no farm_ended → "farm process ended without finishing"
func TestAttach_StartingFarm(t *testing.T)    // locked, no manifest → "is starting", then follows; release the lock → returns nil with "farm process ended"
func TestAttach_FollowsRunning(t *testing.T)  // locked farm; append a part tool_started line → its progress line printed; append farm_ended → returns nil
func TestAttach_PartDetail(t *testing.T)      // "<id> p1": assistant_message for p1 prints "[p1] says: …"; one for p2 does not
func TestAttach_UnknownPart(t *testing.T)     // "<id> nope" → exit 2
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./cmd/jas -run TestAttach`
Expected: build failure (`attachCmd` undefined).

- [ ] **Step 3: Implement `attachCmd`, dispatch and docs.**
  - `docs/manual.md`: a `jas attach` section (usage, the three forms, exit codes 0/2, Ctrl-C detaches, reads files only, what "running" means) and the command in the command list.
  - `SKILL.md`: one line in the subcommand summary.
  - `README.md`: `jas attach` line in the command list.
  - `make manual`.

- [ ] **Step 4: Run the full suite**

Run: `make test` then `CGO_ENABLED=1 go test -race ./farm ./cmd/jas`
Expected: all `ok`, including `TestManual_*` and `TestManualSkill_*`.

- [ ] **Step 5: Commit**

```bash
git add cmd/jas/attach_cmd.go cmd/jas/attach_test.go cmd/jas/main.go docs/manual.md cmd/jas/manual/jas/SKILL.md cmd/jas/manual/jas/references/manual.md README.md
git commit -m "feat: jas attach watches a running farm from its files (#156)"
```
