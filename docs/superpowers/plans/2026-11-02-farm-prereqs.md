# Farm MVP Prerequisites Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Land the core pieces the farm MVP needs and that `jas run` uses on its own: a per-request output cap, token and time caps with exit 7, a jas-run `check`, opt-in headless compaction, and session records (session-awareness rollout step 1).

**Architecture:** Caps are a `core.TokenPool` (reservation-based, parent-chained so the farm can share one pool across workers) checked by `Session` before every request, plus deadline handling that maps `context.DeadlineExceeded` to exit 7. The check runner reuses the shell tool's process-group runner. Session records live in a new `sessions` package and are wired into chat and `jas run`.

**Tech Stack:** Go 1.27, stdlib, existing `core`/`tools`/`settings`/`cmd/jas` packages. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-02-farm-design.md` (Prerequisites 3, 5, 7; MVP cut; CI contract) and `docs/superpowers/specs/2026-11-02-session-awareness.md` (Record, Stale records, Security, Events, Rollout step 1). Plan B (`2026-11-02-farm-mvp.md`) builds on this one.

## Global Constraints

- `CGO_ENABLED=0`; `go test -race ./...` stays green; commits `type: description`.
- Do not change `jas run` behaviour, `--json` shape or existing exit codes when the new flags are unset (AGENTS.md).
- New exit codes (design §3 reserved rows): `6` = check run by jas failed / work unmet, `7` = budget exceeded (token or wall-clock cap). Never reuse 3/4/5.
- Exit 6/7 always carry a `reason` in the final event: 7 → `tokens` | `timeout`; 6 → `check_failed` (others come with farm).
- `max_output_tokens` default **16000**, sent as `max_tokens` on every request.
- Budget is checked **before** each request: estimated input + the request's output cap.
- Session record path: `<main checkout>/.jas/sessions/<session-id>.json` (main checkout via `git rev-parse --git-common-dir`), written atomically (temp + rename), `.jas/sessions/` added to `<git-common-dir>/info/exclude` on first use.
- Heartbeat every **15 s**. Stale = heartbeat older than **60 s** and (same host and pid not alive, or heartbeat older than **10 min**). `ended` records kept **60 s**.
- `touched` capped at **200** paths, then `touched_truncated: true`.
- `.jas/sessions/**` is denied to file tools.

## Review Focus

- **Cap hit mid-turn after tool calls ran:** the run must stop *before* the next request with exit 7, keep tool results already logged, and write `budget_stop` then `session_ended{exit_code:7, reason:"tokens"}` — pinned in Task 2.
- **`--timeout` while a shell tool is running:** the child process group must be killed and the run must end with exit 7 `timeout`, not 130 `interrupted` — pinned in Task 3.
- **Ctrl-C with `--timeout` set:** a user interrupt must still be 130, not 7 — pinned in Task 3.
- **Two jas processes in the same repo, one crashes:** the crashed record must not show as active once stale rules apply; the live one must keep its record — pinned in Task 6.
- **Repo without git (plain directory):** session records must still work under `<root>/.jas/sessions/` and never call git exclude — pinned in Task 6.

---

### Task 1: Per-request output cap (`max_tokens`)

**Files:**
- Modify: `providers/types.go` (Request), `providers/openai.go` (oaiRequest, encodeRequest)
- Modify: `settings/config.go`, `settings/run.go` (new key `max_output_tokens`)
- Modify: `core/session.go` (SessionConfig, request), `core/loop.go` (RunConfig → SessionConfig), `cmd/jas/run.go`, `cmd/jas/chat.go`
- Test: `providers/openai_test.go`, `settings/config_test.go`, `core/session_test.go`

**Interfaces:**
- Produces: `providers.Request.MaxTokens int` (0 = omit); `settings.Run.MaxOutputTokens int`; `core.SessionConfig.MaxOutput int` and `core.RunConfig.MaxOutput int` (0 = omit); const `settings.DefaultMaxOutputTokens = 16000`.

- [ ] **Step 1: Write failing tests**

```go
func TestOpenAI_RequestSendsMaxTokens(t *testing.T)   // Request{MaxTokens:16000} → body["max_tokens"] == 16000.0
func TestOpenAI_RequestOmitsZeroMaxTokens(t *testing.T) // MaxTokens 0 → key "max_tokens" absent from body
func TestConfig_MaxOutputTokensDefault(t *testing.T)  // Build() of defaults → Run.MaxOutputTokens == 16000; "0" rejected by validation
func TestSession_RequestCarriesMaxOutput(t *testing.T) // SessionConfig{MaxOutput: 123}; fake provider's Requests[0].MaxTokens == 123
```

Use the existing `TestOpenAI_RequestShape` server pattern for the body capture.

- [ ] **Step 2: Run** `go test ./providers/ ./settings/ ./core/ -run 'MaxTokens|MaxOutput'` — Expected: build failure / FAIL on missing fields.

- [ ] **Step 3: Implement.** `oaiRequest.MaxTokens int \`json:"max_tokens,omitempty"\``; `encodeRequest` copies it. Key `{Name: "max_output_tokens", Default: "16000", Validate: positiveInt, Flag: true}`. `Session.request` sets `req.MaxTokens = c.MaxOutput`. `Run` passes `RunConfig.MaxOutput` through. `cmd/jas` sets it from `e.cfg.MaxOutputTokens` in both run and chat. The compaction `summaryRequest` leaves it 0.

- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.

- [ ] **Step 5: Commit** `feat: send max_tokens on every request (max_output_tokens, default 16000)`

---

### Task 2: Token pool and `--max-tokens` (exit 7)

**Files:**
- Create: `core/tokens.go`
- Modify: `core/exit.go` (ExitBudget, ExitUnmet), `core/session.go` (check before request), `core/loop.go` (status const), `core/budget.go` (reuse `PromptOverhead`), `cmd/jas/run.go` (flag)
- Test: `core/tokens_test.go`, `core/budget_loop_test.go`, `cmd/jas/run_test.go`

**Interfaces:**
- Consumes: `SessionConfig.MaxOutput` (Task 1), `Budget.Count`, `PromptOverhead`.
- Produces:
  ```go
  const ExitUnmet = 6
  const ExitBudget = 7
  type TokenPool struct{ /* mutex-guarded */ }
  func NewTokenPool(name string, limit int, parent *TokenPool) *TokenPool // limit <= 0 = unlimited
  // Reserve holds n tokens in p and every ancestor, or reserves nothing and
  // returns *BudgetError naming the first pool that would overflow.
  func (p *TokenPool) Reserve(n int) (*Reservation, error)
  func (r *Reservation) Settle(used int) // replaces the held n with used in p and ancestors; idempotent
  func (p *TokenPool) Used() int          // settled + outstanding reservations
  type BudgetError struct{ Pool string; Limit, Used, Want int }
  func (e *BudgetError) Error() string
  // SessionConfig gains: Tokens *TokenPool // nil = no cap
  // event: budget_stop{reason:"tokens", pool, limit, used, want}
  // session_ended status "budget_exceeded", code 7, reason "tokens"
  ```

- [ ] **Step 1: Write failing tests**

```go
func TestTokenPool_ReserveSettle(t *testing.T)        // limit 100: Reserve(60) ok, Reserve(50) → *BudgetError{Pool:"w"}, Settle(30) then Reserve(50) ok, Used()==80
func TestTokenPool_ParentCapsChild(t *testing.T)      // parent 100, child 1000: child.Reserve(150) → BudgetError.Pool == parent name; nothing held in child
func TestTokenPool_ConcurrentNeverOvershoots(t *testing.T) // 20 goroutines Reserve(10) on limit 100: exactly 10 succeed (run under -race)
func TestRun_MaxTokensStopsBeforeRequest(t *testing.T)
// rig: first turn reads a file (Usage{PromptTokens: 900, CompletionTokens: 50}), MaxOutput 100,
// Tokens: NewTokenPool("run", 1000, nil) → exit 7, len(prov.Requests)==1,
// events contain budget_stop{reason:"tokens"} before session_ended{exit_code:7, status:"budget_exceeded", reason:"tokens"},
// tool_result for the read is logged.
func TestRunCmd_MaxTokensFlag(t *testing.T)           // parseRun(["--max-tokens","5000","task"]) → ra.maxTokens == 5000; "--max-tokens -1" → usage error (exit 2)
```

- [ ] **Step 2: Run** `go test ./core/ ./cmd/jas/ -run 'TokenPool|MaxTokens'` — Expected: FAIL (undefined).

- [ ] **Step 3: Implement.** Estimate per request = `Budget.Count(messages)` when a Budget exists, else `PromptOverhead(system, specs) + bytes/4` of messages; plus `MaxOutput`. In `Session.turn`, before `s.request`: `Reserve(estimate)`; on error log `budget_stop` and end `budget_exceeded`/`ExitBudget`/`"tokens"`. After the stream: `Settle(prompt+completion)` from usage, or `Settle(estimate)` when the provider reported no usage. `jas run --max-tokens N` (run-only flag, not a stored setting) builds `NewTokenPool("run", N, nil)`; 0/absent = no pool.

- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.

- [ ] **Step 5: Commit** `feat: token cap checked before each request (jas run --max-tokens, exit 7)`

---

### Task 3: Wall-clock cap `--timeout` (exit 7)

**Files:**
- Modify: `core/loop.go` (`classify`), `core/session.go` (ctx checks in `loop`/`Send`), `cmd/jas/run.go`
- Test: `core/loop_test.go`, `cmd/jas/run_test.go`

**Interfaces:**
- Produces: a session whose `ctx` ends with `context.DeadlineExceeded` ends `budget_exceeded`, `ExitBudget`, reason `"timeout"`, after a `budget_stop{reason:"timeout"}` event. `context.Canceled` stays `interrupted`/130. `jas run --timeout <duration>` (Go duration syntax; ≤0 or unparsable → exit 2).

- [ ] **Step 1: Write failing tests**

```go
func TestRun_DeadlineIsBudgetTimeout(t *testing.T)  // ctx with 50ms deadline, fake Turn{Block:true} → exit 7, session_ended{reason:"timeout"}
func TestRun_DeadlineKillsShell(t *testing.T)        // unix: shell "sleep 30" under 200ms deadline → returns < 5s, exit 7, tool_result status cancelled
func TestRun_CancelStillInterrupted(t *testing.T)    // context.WithCancel + cancel() → exit 130 even when a deadline is also set
func TestRunCmd_TimeoutFlag(t *testing.T)            // "--timeout 30m" → 30*time.Minute; "--timeout 0" and "--timeout soon" → usage error
```

- [ ] **Step 2: Run** `go test ./core/ ./cmd/jas/ -run 'Deadline|Cancel|Timeout'` — Expected: FAIL.

- [ ] **Step 3: Implement** a helper `ctxEnd(ctx) (status string, code int, reason string)` used wherever the loop currently returns `endInterrupted` on `ctx.Err() != nil`, and in `classify`. `jas run` wraps its ctx with `context.WithTimeout`.

- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.

- [ ] **Step 5: Commit** `feat: wall-clock cap for jas run (--timeout, exit 7)`

---

### Task 4: jas-run `check` (`core.RunCheck`)

**Files:**
- Create: `core/check.go`
- Modify: `tools/shell.go` (export the process-group runner it already uses)
- Test: `core/check_test.go` (unix build tag where the command is POSIX)

**Interfaces:**
- Produces:
  ```go
  type CheckSpec struct {
      Cmd     string
      Dir     string
      Env     []string      // allowlisted env, as for shell (tools.ShellEnv)
      Timeout time.Duration // 0 = none
      MaxOutput int         // bytes kept from the tail of output; 0 = DefaultMaxToolOutput
  }
  type CheckResult struct {
      ExitCode int  // -1 when it never started or timed out
      Duration time.Duration
      Output   string // tail, redacted by caller
      TimedOut bool
  }
  func RunCheck(ctx context.Context, s CheckSpec, run func(ctx context.Context, dir, cmd string, env []string, out io.Writer) (int, error)) CheckResult
  // tools.RunShell(ctx, dir, cmd string, env []string, out io.Writer) (exitCode int, err error) — the exported runner (Setpgid, group kill, WaitDelay)
  // event helper: func LogCheck(log EventLog, s CheckSpec, r CheckResult, redact func(string) string) error
  //   → check_ran{cmd, dir, exit_code, duration_ms, timed_out, output}
  ```
  `core` must not import `tools`; callers pass `tools.RunShell` as `run`.

- [ ] **Step 1: Write failing tests**

```go
func TestRunCheck_PassAndFail(t *testing.T)   // "true" → ExitCode 0; "exit 3" → ExitCode 3
func TestRunCheck_TimeoutKillsGroup(t *testing.T) // "sleep 30 & sleep 30", Timeout 200ms → TimedOut, ExitCode -1, returns < 5s
func TestRunCheck_KeepsOutputTail(t *testing.T)   // prints 10000 'x' then "END", MaxOutput 100 → len(Output) <= 100+len(marker), HasSuffix "END"
func TestLogCheck_Redacts(t *testing.T)           // output containing the redactor's exact key → logged output has it masked
```

- [ ] **Step 2: Run** `go test ./core/ -run 'Check'` — Expected: FAIL.
- [ ] **Step 3: Implement** by moving the shell tool's exec body into `tools.RunShell` (shell tool then calls it, behaviour unchanged) and writing `RunCheck` around it.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS, including existing `tools/shell_test.go`.
- [ ] **Step 5: Commit** `feat: core.RunCheck runs a project check command with timeout and tail capture`

---

### Task 5: Opt-in compaction for headless runs

**Files:**
- Modify: `core/loop.go` (RunConfig.Compact), `cmd/jas/run.go` (`--compact` flag)
- Test: `core/session_compact_test.go`, `cmd/jas/run_test.go`

**Interfaces:**
- Produces: `RunConfig.Compact *CompactConfig` (nil = off, today's behaviour); `jas run --compact` sets it from the same settings chat uses (`compact_reserve_tokens`, `compact_keep_tokens`, `compact_model`).

- [ ] **Step 1: Write failing tests**

```go
func TestRun_CompactConfigCompacts(t *testing.T)  // RunConfig{Compact: &CompactConfig{Reserve:16384, Keep:50}}, small window rig → a "compaction" event, exit 0
func TestRun_NoCompactByDefault(t *testing.T)     // same rig without Compact → no "compaction" event (existing behaviour: exit 3 when full)
func TestRunCmd_CompactFlag(t *testing.T)         // "--compact" sets ra.compact
```

- [ ] **Step 2: Run** — Expected: FAIL.
- [ ] **Step 3: Implement** the pass-through.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: jas run --compact enables auto-compaction headless`

---

### Task 6: Session records (session-awareness rollout step 1)

**Files:**
- Create: `sessions/record.go`, `sessions/dir.go`, `sessions/alive_unix.go`, `sessions/alive_windows.go`
- Create: `cmd/jas/sessions_cmd.go`
- Modify: `core/exec.go` (touched hook), `cmd/jas/run.go`, `cmd/jas/chat.go`, `cmd/jas/main.go` (dispatch `sessions`), `cmd/jas/setup.go` (deny pattern)
- Test: `sessions/record_test.go`, `cmd/jas/sessions_cmd_test.go`, `core/exec_test.go`

**Interfaces:**
- Produces:
  ```go
  package sessions
  type Farm struct{ ID, Part string; Owns []string; TargetBranch string }
  type Record struct {
      V int; SessionID string; PID int; Host, Mode, Title, Branch, Worktree string
      Farm *Farm `json:",omitempty"`
      Touched []string; TouchedTruncated bool; ShellUsed bool
      Started, Heartbeat time.Time; State string; Log string
  } // JSON field names exactly as the session-awareness spec's example (snake_case)
  func Dir(root string) (string, error)            // <main checkout>/.jas/sessions; adds the git exclude line once
  func Register(dir string, r Record) (*Handle, error)
  func (h *Handle) Touch(rel string)               // dedups, caps at 200 → TouchedTruncated
  func (h *Handle) ShellUsed()
  func (h *Handle) SetState(state string)          // "active" | "idle"
  func (h *Handle) Start(ctx context.Context)      // heartbeat every 15s until ctx done
  func (h *Handle) Close() error                   // state "ended"; file deleted by later Prune after 60s
  func List(dir string, now time.Time) ([]Record, error) // excludes stale records
  func Prune(dir string, now time.Time) (int, error)
  func Stale(r Record, now time.Time, host string, alive func(pid int) bool) bool
  // core.Executor gains: Touched func(tool string, resolvedPath string) // called after a successful write/edit, and with tool "shell" for shell calls
  // event: session_registered{record_path}
  ```
- Deny: `cmd/jas` adds `<root>/.jas/sessions/**` to `ExecPolicy.Deny`.

- [ ] **Step 1: Write failing tests**

```go
func TestStale_Rules(t *testing.T)
// table: heartbeat 30s ago, dead pid → false; 90s ago, same host, dead pid → true;
// 90s ago, same host, alive pid → false; 11min ago, alive pid → true; other host, 90s → false
func TestRegister_AtomicAndListed(t *testing.T)   // Register then List → one record, State "active"; file has no temp leftovers
func TestHandle_TouchCaps(t *testing.T)           // 250 distinct Touch → len(Touched)==200, TouchedTruncated
func TestPrune_RemovesStaleAndOldEnded(t *testing.T) // ended record 61s old removed; ended 30s old kept; stale removed; live kept
func TestDir_GitExcludeOnce(t *testing.T)         // git repo: Dir twice → "/.jas/sessions/" appears exactly once in .git/info/exclude
func TestDir_LinkedWorktreeUsesMainCheckout(t *testing.T) // git worktree add; Dir(worktree) == Dir(main)
func TestDir_NoGit(t *testing.T)                  // plain dir → <root>/.jas/sessions, no git invoked, no error
func TestExecutor_TouchedHook(t *testing.T)       // write ok → hook("write", path); denied write → no hook; shell → hook("shell", "")
func TestSessionsCmd_ListsAndPrunes(t *testing.T) // `jas sessions` prints one row per live record; `--prune` prints count removed
func TestFileTools_DenySessionsDir(t *testing.T)  // read of .jas/sessions/x.json → denied result
```

- [ ] **Step 2: Run** `go test ./sessions/ ./core/ ./cmd/jas/ -run 'Stale|Register|Touch|Prune|Dir_|Sessions'` — Expected: FAIL.

- [ ] **Step 3: Implement.** `alive_unix.go`: `syscall.Kill(pid, 0)` (nil or EPERM = alive). `alive_windows.go`: `windows.OpenProcess` + `GetExitCodeProcess == STILL_ACTIVE`. `cmd/jas` registers in `runCmd` and in the chat backend at session start (mode `run`/`chat`), wires `Executor.Touched` to `Handle.Touch` with root-relative paths / `ShellUsed`, sets `idle` while the chat waits for input, closes on exit; a registration failure prints one warning line and the session continues (records are advisory).

- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.

- [ ] **Step 5: Commit** `feat: per-repo session records and jas sessions (session awareness step 1)`

---

### Task 7: Docs

**Files:**
- Modify: `docs/design.md` §3 exit-code table (6 and 7 move from "reserved" to emitted by `jas run`; 8 stays reserved), §10 settings list (`max_output_tokens`); `README.md` (`--max-tokens`, `--timeout`, `--compact`, `jas sessions`)

- [ ] **Step 1:** Update the docs to match Tasks 1–6 (flag names, defaults, exit codes, event names `budget_stop`, `check_ran`, `session_registered`).
- [ ] **Step 2: Run** `go test -race ./... && go vet ./...` — Expected: PASS.
- [ ] **Step 3: Commit** `docs: caps, check runner, headless compaction, session records`
