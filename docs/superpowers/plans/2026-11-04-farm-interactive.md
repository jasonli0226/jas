# Farm in the Chat Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Start, watch and steer a farm from the inline `jas` chat: live rows per agent, Ctrl/Alt+1–9 and Ctrl/Alt+0 to flip the window between the main chat and each agent, a `/farm` dashboard, and farm prompts routed to the owner.

**Architecture:** The `farm` package gains a non-blocking entry point (`StartFarm` → `*Handle`) with hooks for events, per-worker UIs, gates and questions; `RunFarm` becomes `StartFarm` + `Wait`, so the CLI and the chat share one supervisor. `cmd/jas` implements a new `tui.Farmer` port on the chat backend. The `tui` package holds the farm state, views and prompts; every farm call from the chat runs in a `tea.Cmd`, never in `Update`.

**Tech Stack:** Go, Bubble Tea v2 / bubbles v2 / lipgloss v2 (already deps), existing `farm`, `core`, `tui`, `cmd/jas` packages.

**Spec:** `docs/superpowers/specs/2026-11-04-farm-interactive-design.md` (refines the Interactive mode section of `docs/superpowers/specs/2026-11-02-farm-design.md`).

## Global Constraints

- No new dependencies. `CGO_ENABLED=0`. Keep `go test -race ./...` green.
- Inline only: no alternate screen, no fullscreen (AGENTS.md). A flip is a screen clear and reprint, like Ctrl+L (`tui/sessions.go` `clearView`).
- Never append to any event log synchronously from `Update` (AGENTS.md): farm hooks call `tea.Program.Send`, so every `Handle` method and every `Farmer` call from the chat runs inside a `tea.Cmd`.
- Same supervisor, logs and manifest as `jas farm run`; `jas farm status` must work on a chat-started farm.
- Ctrl+L (clear) and Ctrl+O (expand tool output) keep their meaning in every view.
- Everything printed is redacted and sanitized (`m.clean`, `Options.Redact`).
- Docs follow features (AGENTS.md): `README.md` and `cmd/jas/manual/jas/SKILL.md` change in the same commit as a user-visible change.
- Commit messages: `type: description` (feat, fix, refactor, docs, test, chore).
- Exact copy from the spec: prompt label `replay-fix ›`; strip markers `main chat: N new`, `farm needs you · ctrl+0`; row marker `⚠ needs approval`; quit confirm `a farm is running — stop it and quit? y/n`; queued steer `queued for <part>`; hand-back `[<part>] not delivered (part finished)`.

## Review Focus

1. **A farm event lands while the owner is mid-keystroke in a picker, the slash menu or the plan review** — the open prompt must keep the keys; rows and held counts update underneath. Test in Task 7.
2. **The terminal is resized while flipped to an agent** — the next redraw uses the new width; no line wraps in rows or strip. Test in Task 5.
3. **The agent the owner is viewing finishes or is dropped while they are typing a steer** — the draft stays in the input, and anything already queued comes back with the hand-back line. Test in Task 6.
4. **The farm ends (or asks the final choice) while the owner is in an agent view** — the summary is held, the strip shows `farm needs you · ctrl+0`, and the prompt opens only in the main chat after the main turn ends. Test in Task 7.
5. **`/new` or `/resume` while a farm runs** — rows survive, the end summary attaches to the session current at the end, and the old session's output is not mixed in. Test in Task 8.

---

### Task 1: Farm hooks — watcher, worker UI, gates

**Files:**
- Modify: `farm/progress.go`, `farm/run.go` (`RunOptions`, `startFarm`), `farm/supervisor.go` (fields), `farm/part.go` (`openWorker`, `gate`), `farm/integrate.go` (`runIntegrator`), `farm/worker.go` (`WorkerSession`), `cmd/jas/farm_run.go` (`farmSession`)
- Test: `farm/run_test.go`, `cmd/jas/farm_cmd_test.go`

**Interfaces:**
- Produces:
  - `type Watcher interface { FarmEvent(typ string, m map[string]any); WorkerEvent(part, typ string, m map[string]any) }` in `farm/progress.go`; `*Progress` implements it (its current formatting moves behind these two methods).
  - `RunOptions.Watch Watcher` — when set it replaces the default `Progress`; `JSON` still prints only `farm.jsonl`.
  - `RunOptions.WorkerUI func(part string) core.UI` — nil: quiet; copied into new field `WorkerSession.UI core.UI`. The integrator's part name is `"integrate"`.
  - `RunOptions.Gates func(part, approve string) (core.Gate, error)` — when set, `Supervisor.gate` uses it instead of `Approvals`.
- `cmd/jas` `farmSession` uses `ws.UI` when non-nil, else `core.NewQuietUI()`.

- [ ] **Step 1: Write failing tests**
  - `TestRunFarm_WatcherSeesFarmAndWorkerEvents`: a recording `Watcher` on the happy-path rig sees `FarmEvent(EvPartStarted, …part=="a")` and `WorkerEvent("a", core.EvToolStarted, …)`; `r.stdout` has no progress lines.
  - `TestRunFarm_WorkerUIPerPart`: `WorkerUI` is called once per session with `"a"`, `"b"` (and `"integrate"` in the clean-merge-failing-check rig); the returned UI receives `Text` deltas from that part's fake provider.
  - `TestRunFarm_GatesHookPerPart`: `Gates` is called with each part id and the role's `approve`; `Approvals` nil.
  - `TestFarmSession_UsesWorkerUI` (cmd/jas): a `WorkerSession` with `UI` set builds a session whose config UI is that UI.
- [ ] **Step 2: Run** `go test ./farm ./cmd/jas -run 'Watcher|WorkerUI|GatesHook|UsesWorkerUI'` — Expected: FAIL (fields undefined).
- [ ] **Step 3: Implement.** `farmLog`/`workerLog` wrap with any `Watcher` (rename the internal log wrappers to take a `Watcher`); `Progress` keeps today's output exactly (existing `TestRunFarm_PrintsProgress` and `TestProgress_*` stay green).
- [ ] **Step 4: Run** `go test -race ./farm ./cmd/jas` — Expected: PASS.
- [ ] **Step 5: Commit** `refactor: farm watcher, per-worker UI and gate hooks`

---

### Task 2: `StartFarm` and the farm `Handle`

**Files:**
- Create: `farm/handle.go`, `farm/handle_test.go`
- Modify: `farm/run.go` (`RunFarm` = `StartFarm` + `Wait`), `farm/supervisor.go` (running-worker registry, per-part cancel, pause gate in `wrap`), `farm/part.go` (`finishDone` hand-back), `farm/events.go`

**Interfaces:**
- Consumes: Task 1 `Watcher`.
- Produces (all safe from any goroutine; each may log, so callers in the chat use a `tea.Cmd`):
  - `func StartFarm(ctx context.Context, opts RunOptions) (*Handle, error)` — preflight errors return synchronously (same codes as `RunFarm`); the farm then runs on its own goroutine.
  - `func (h *Handle) Wait() Result` with `type Result struct { Code int; Reason string; Branch string; Err error }`.
  - `func (h *Handle) Done() <-chan struct{}`.
  - `func (h *Handle) FarmID() string`, `func (h *Handle) Snapshot() Manifest` (a copy).
  - `func (h *Handle) Steer(part, text string) error` — `ErrPartNotRunning` when the part has no live session.
  - `func (h *Handle) Pause(part string) error`, `Resume(part string) error` — pause blocks that worker's next provider request (in the per-worker provider wrapper) until resumed; logs `owner_decision{part, choice:"pause"|"resume"}`.
  - `func (h *Handle) Drop(part string) error` — cancels the part's context and drops it and its dependents (the failure-drop path, `source:"owner"`).
  - `func (h *Handle) Stop(hard bool)` — `false`: as the first Ctrl-C of `jas farm run` (soft ctx); `true`: cancels `Hard`; a hard stop's `farm_ended` carries `"hard": true`.
  - `Watcher` gains `SteerUndelivered(part string, texts []string)`, called when `finishDone` takes queued steering (it already logs `choiceSteerUndelivered`).
- `RunOptions.Ask` keeps its signature; the chat supplies it in Task 9.

- [ ] **Step 1: Write failing tests** (fake providers, `newRunRig` style)
  - `TestStartFarm_WaitMatchesRunFarm`: happy path → `Result{Code:0, Reason:"ok", Branch:"jas/farm/<id>/integrate"}`.
  - `TestHandle_SteerReachesRunningWorker`: a part whose fake turns block on a channel; `Steer("a","use slog")` → the part's next request contains `use slog`; `Steer("zz", …)` → `ErrPartNotRunning`.
  - `TestHandle_PauseHoldsNextRequest`: after `Pause("a")` the fake provider records no new request for 200 ms; after `Resume("a")` the part finishes; events `owner_decision` pause then resume.
  - `TestHandle_DropDropsDependents`: plan a, b(after a); `Drop("a")` → manifest a and b `dropped`, `Result.Reason == "part_dropped"`.
  - `TestHandle_StopHardMarksFarmEnded`: `Stop(false)` then `Stop(true)` → `farm_ended{status:"interrupted", hard:true}`, code 130.
  - `TestHandle_UndeliveredSteeringHandedBack`: steer queued as the worker gives its final answer → watcher `SteerUndelivered("a", ["late"])`.
  - `TestFinalChoice_MergeRefusedOnDirtyMain` (spec, "What the farm inherits"): the main checkout has an uncommitted change when the owner answers merge → no merge, notice `main checkout has uncommitted changes; keeping the branches`, final choice `keep`.
- [ ] **Step 2: Run** `go test ./farm -run 'StartFarm|Handle_'` — Expected: FAIL.
- [ ] **Step 3: Implement.** Running workers in a `map[string]*worker` under a mutex, set in `openWorker`, cleared in `closeWorker`. Per-part `context.WithCancel` around each attempt. The pause gate lives in the existing per-worker provider wrapper (`s.wrap`), not in core. `RunFarm` keeps its exact behavior (all existing `farm` and `cmd/jas` farm tests unchanged).
- [ ] **Step 4: Run** `go test -race ./farm ./cmd/jas` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: farm Handle for steering, pausing, dropping and stopping a running farm`

---

### Task 3: The chat's farm port (`tui.Farmer`) in `cmd/jas`

**Files:**
- Create: `tui/farmer.go` (port types only), `cmd/jas/chat_farm.go`, `cmd/jas/chat_farm_test.go`
- Modify: `cmd/jas/chat.go` (backend gets a `Farmer`), `tui/backend.go` (`Backend` gains `Farmer() Farmer`; nil = farm unavailable)

**Interfaces:**
- Consumes: Task 1 hooks, Task 2 `StartFarm`/`Handle`.
- Produces in `tui/farmer.go`:

```go
type Farmer interface {
	// Available reports whether a farm can run here (trusted project, check resolves) and why not.
	Available() (ok bool, why string)
	Plan(ctx context.Context, task, model string) (FarmPlan, error)
	Replan(ctx context.Context, p FarmPlan, feedback string) (FarmPlan, error)
	Recheck(p FarmPlan, raw []byte) FarmPlan // after $EDITOR
	Approve(p FarmPlan) (planPath string, err error)
	Start(ctx context.Context, planPath, model string, level core.Level, h FarmHooks) (FarmRun, error)
}
type FarmPlan struct { ID string /* farm dir id, opaque to the chat */; Summary []string; Raw []byte; Refusal, Suggest string; Problems []string }
type FarmHooks struct {
	Event    func(typ string, m map[string]any)
	Worker   func(part string) core.UI
	Gate     func(part, approve string) (core.Gate, error)
	Ask      func(prompt string, choices []string) (string, bool)
	Undelivered func(part string, texts []string)
}
type FarmRun interface {
	FarmID() string
	Snapshot() farm.Manifest
	Steer(part, text string) error
	Pause(part string) error; Resume(part string) error; Drop(part string) error
	Stop(hard bool)
	Done() <-chan struct{}
	Wait() farm.Result
}
```

  `*farm.Handle` satisfies `FarmRun`. `Plan`/`Replan`/`Recheck`/`Approve` reuse `planWith`/`finishPlan` internals from `cmd/jas/farm_cmd.go` and `farm_review.go` (no second planner path); `Approve` writes the plan into the farm dir, not the repo. `Start` builds `RunOptions` as `runPlan` does, with `Watch`/`WorkerUI`/`Gates`/`Ask` from the hooks, the chat's `model` and `level`, `Stdout: io.Discard`.

- [ ] **Step 1: Write failing tests** (fake OpenAI server, temp repo with a farm `check`)
  - `TestChatFarm_AvailableNeedsTrustAndCheck`: untrusted → `false, "project not trusted"`; no check → `false, "no check …"`.
  - `TestChatFarm_PlanRefusal`: planner refusal → `FarmPlan.Refusal == "small_change"`, `Suggest` set.
  - `TestChatFarm_ApproveThenStart`: plan → approve → start with recording hooks → `Wait()` code 0; hooks saw `part_started` and a worker tool event; the plan file is under `<home>/farms/`, not the repo.
- [ ] **Step 2: Run** `go test ./cmd/jas -run ChatFarm` — Expected: FAIL.
- [ ] **Step 3: Implement** as above.
- [ ] **Step 4: Run** `go test -race ./cmd/jas ./tui` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: chat backend farm port`

---

### Task 4: `/farm` start, plan review, live rows and dashboard

**Files:**
- Create: `tui/farm.go` (farm state, rows, dashboard), `tui/farm_cmd.go` (`/farm` command), `tui/farm_test.go`, test fake `tui/farm_fake_test.go`
- Modify: `tui/chat.go` (`Model.farm *farmState`, `View` adds rows), `tui/commands.go` (register `/farm`)

**Interfaces:**
- Consumes: Task 3 `Farmer`, `FarmPlan`, `FarmRun`.
- Produces: `type farmState struct { run FarmRun; parts []farmAgent; started time.Time; needs bool; held []string; ... }`, `type farmAgent struct { id, role, status, activity string; tokens int; num int; tr Transcript; printed []string; needsApproval bool }`; messages `farmEventMsg{typ string; m map[string]any}`, `farmDoneMsg{res farm.Result}`; `func (m *Model) farmRows() []string`, `func (m *Model) farmDashboard() []string`.

Behaviour (spec, Starting / Live rows / Dashboard):
- `/farm "<task>"` (also `/farm <task>` unquoted) → `Plan` in a `tea.Cmd` with a loader line `planning…`; the summary prints; a picker offers approve / edit / replan / quit (edit: `tea.ExecProcess($EDITOR)` on a temp copy, then `Recheck`; replan: next input line is the feedback). Refusal prints reason and `suggest` and pre-fills the input with the task.
- `/farm run <plan.yaml>` starts an approved plan.
- With `Farmer() == nil` or `Available()` false, `/farm` prints the why.
- Numbering: parts 1–9 in plan order; the integrator takes the next free number once it starts, else none (reachable via `/farm attach integrate`).
- Rows: one per agent, `N name status activity tokens`, then `farm <used> / <budget> tok · <elapsed> · check <state>     ctrl+1..9 flip · ctrl+0 back`; truncated with `ansi.Truncate`; when `m.height` cannot fit rows + input + 4, only the totals line shows.
- `/farm` with a farm: dashboard snapshot (spec text mockup columns); without: `no farm` or the last farm of this window, plus usage.

- [ ] **Step 1: Write failing tests** with a fake `Farmer`/`FarmRun` driven by test messages:
  - `TestFarm_StartApprovesAndShowsRows`: `/farm "fix x"` → picker → `a` → rows `1 a … running`, `2 b … queued`.
  - `TestFarm_RefusalPrefillsInput`: refusal → notice with reason; input value `fix x`.
  - `TestFarm_RowsCollapseWhenShort`: height 8 → view has the totals line and no part rows.
  - `TestFarm_DashboardColumns`: `/farm` → printed block contains `#  part`, `replay-fix`, `● running`.
  - `TestFarm_UnavailableExplains`: `Available()` false → notice with the why.
- [ ] **Step 2: Run** `go test ./tui -run TestFarm_` — Expected: FAIL.
- [ ] **Step 3: Implement.** Hook `Event` → `m.send(farmEventMsg)`; all `Farmer`/`FarmRun` calls in `tea.Cmd`s.
- [ ] **Step 4: Run** `go test -race ./tui` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: start a farm from the chat with live rows and a /farm dashboard` (README + manual: `/farm` usage).

---

### Task 5: Agent views and flipping

**Files:**
- Create: `tui/farm_view.go`, `tui/farm_view_test.go`
- Modify: `tui/keys.go` (flip keys before the picker branch, after Ctrl+L/Ctrl+O), `tui/chat.go` (`View` strip), `tui/farm_cmd.go` (`attach`, `back`)

**Interfaces:**
- Consumes: Task 4 `farmState`, `farmAgent`.
- Produces: `Model.viewing string` (`""` = main chat); messages `farmTextMsg{part, s string}`, `farmWorkerEventMsg{part, typ string; m map[string]any}` from the per-part UI (`Hooks.Worker` returns a bridge like `tui/bridge.go` keyed by part); `func (m *Model) flip(to string) tea.Cmd`; `func flipTarget(key string, agents []farmAgent) (part string, ok bool)` mapping `ctrl+1`…`ctrl+9`, `alt+1`…`alt+9` to agent numbers and `ctrl+0`/`alt+0` to `""`.

Behaviour (spec, Flipping):
- Every agent's text and events render through its own `Transcript` into `agent.printed` always; they reach the screen only while that agent is viewed.
- `flip`: `tea.Sequence(tea.ClearScreen, print(header, last blocks of the target that fit m.height))`; header `── <part> · <role> · <worktree> ──`; main chat redraws from `m.printed`.
- While viewing an agent, main-chat output (main transcript blocks and farm event lines) goes to `farm.held`; the strip shows `main chat: N new`; `flip("")` prints held blocks after the redraw.
- Strip under the rule: all agents, current highlighted.
- `/farm attach <part|N>` = flip; `/farm back` = flip(""). Unknown part → notice. Queued agents show `waiting for <deps>`.
- Ctrl+N with no farm → hint `no farm running`.

- [ ] **Step 1: Write failing tests**
  - `TestFlip_CtrlAndAltDigits`: `flipTarget` table for ctrl+1, alt+2, ctrl+0, alt+0, ctrl+9 with 2 agents (→ not ok).
  - `TestFlip_ShowsAgentAndHoldsMain`: flip to `a`; a main `textMsg` arrives → not in `Printed()` since flip, strip has `main chat: 1 new`; `ctrl+0` → held block printed.
  - `TestFlip_AgentOutputOnlyWhileViewed`: `farmTextMsg{b,…}` while viewing `a` → not printed; flip to `b` → in redraw.
  - `TestFlip_ResizeRedrawsAtNewWidth` (Review Focus 2): flip, `WindowSizeMsg{Width:40}`, flip again → no printed line wider than 40 cells.
  - `TestFlip_AttachCommandAndUnknownPart`.
- [ ] **Step 2: Run** `go test ./tui -run TestFlip_` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./tui` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: flip the chat between the main session and farm agents` (README + manual: keys, `/farm attach|back`).

---

### Task 6: Steering, Esc/Ctrl+C, stop and quit

**Files:**
- Modify: `tui/keys.go` (`onEnter`, `onCtrlC`, `esc`), `tui/farm_view.go`, `tui/farm_cmd.go` (`stop`, `pause`, `resume`, `drop`), `tui/run.go` (`quit`)
- Test: `tui/farm_view_test.go`

**Interfaces:**
- Consumes: Task 2 `Steer`/`Pause`/`Resume`/`Drop`/`Stop`, `Hooks.Undelivered`; Task 5 `viewing`.
- Produces: `farmUndeliveredMsg{part string; texts []string}`.

Behaviour (spec, Steering; Stopping and leaving; review R2):
- In an agent view the input prompt is `<part> ›`; Enter → `Steer` in a `tea.Cmd`; the queued line reads `queued for <part>`; a done/failed agent refuses with a hint; slash commands still go to the chat.
- Esc and the first Ctrl+C in an agent view clear the input only; Ctrl+C on an empty input within the double-press window → quit flow.
- Quit (`/quit`, Ctrl+D, double Ctrl+C) with a running farm → `a farm is running — stop it and quit? y/n`; `y` → `Stop(false)`, wait `Done`, then the normal quit; `n` → nothing.
- `/farm stop` → `Stop(false)`; a second while draining → `Stop(true)`.
- `/farm pause|resume|drop <part|N>`.
- Undelivered texts → hand-back line and the text restored to the input (appended to any draft).

- [ ] **Step 1: Write failing tests**
  - `TestSteer_RoutesToViewedAgent`: viewing `a`, type `use slog`, Enter → fake run got `Steer("a","use slog")`; view shows `queued for a`.
  - `TestSteer_DoneAgentRefused`.
  - `TestEscAndCtrlC_NeverStopAgents`: viewing `a`, Esc and one Ctrl+C → no `Drop`/`Stop`/main cancel; input empty.
  - `TestQuit_ConfirmsWithRunningFarm`: double Ctrl+C → confirm prompt; `n` keeps running; `y` → `Stop(false)` then quit.
  - `TestFarmStop_SecondIsHard`.
  - `TestSteer_HandBackWhileTyping` (Review Focus 3): viewing `a` with draft `half`, `farmUndeliveredMsg{a,["late"]}` → notice `[a] not delivered (part finished)`, input contains `half` and `late`.
- [ ] **Step 2: Run** `go test ./tui -run 'Steer_|EscAndCtrlC|Quit_Confirms|FarmStop'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./tui` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: steer, pause, drop and stop farm agents from the chat` (README + manual).

---

### Task 7: Farm approvals, prompts and the bell

**Files:**
- Create: `tui/farm_prompt.go`, `tui/farm_prompt_test.go`
- Modify: `tui/gate.go` (a labelled per-part gate sharing one prompt queue), `tui/run.go` (`onApproval` accepts farm approvals with no main run), `settings/config.go` (`farm_bell`), `cmd/jas/chat.go` (pass `farm_bell` into `tui.Options.FarmBell bool`)

**Interfaces:**
- Consumes: Task 3 `FarmHooks.Gate`/`Ask`.
- Produces: `func (g *Gate) ForPart(part string, level core.Level) core.Gate` — same prompt UI, label `[<part>] `, grants per part plus a farm-wide grant set (`always for this farm`); `farmAskMsg{prompt string; choices []string; reply chan<- string}`; `tui.Options.FarmBell bool`; settings key `{Name: "farm_bell", Default: "true", Validate: oneOf("true","false")}`.

Behaviour (spec, Prompts while flipping; review R1; R3):
- Approvals (main or farm) open immediately in any view; the draft is stashed and restored after the answer; pending approvals queue oldest first; the agent's row shows `⚠ needs approval` until answered.
- `Ask` (failure, final choice) opens only in the main chat and only when no main run is active; until then the strip shows `farm needs you · ctrl+0`; answers use `farmAsk`-style matching (whole word or prefix; else asked again).
- Bell: write `"\a"` once when an approval or ask opens, unless `FarmBell` is false.

- [ ] **Step 1: Write failing tests**
  - `TestFarmApproval_TakesInputAndRestoresDraft` (R1): viewing `a` with draft `half`; approval from `b` → prompt labelled `[b]`; `y` → reply `y`; input back to `half`.
  - `TestFarmApproval_QueueOldestFirst`.
  - `TestFarmApproval_WhileSlashMenuOpen` (Review Focus 1): slash menu open; approval arrives → approval prompt wins, menu returns after.
  - `TestFarmAsk_WaitsForMainTurnAndView` (Review Focus 4): main run active and viewing `a` → no prompt, strip `farm needs you · ctrl+0`; run ends + `ctrl+0` → prompt open.
  - `TestFarmBell_OncePerPromptAndOff`.
  - `TestConfig_FarmBellKey` (settings).
- [ ] **Step 2: Run** `go test ./tui ./settings -run 'FarmApproval|FarmAsk|FarmBell|FarmBellKey'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./tui ./settings ./cmd/jas` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: route farm approvals and questions in the chat, with a bell` (README + manual: `farm_bell`).

---

### Task 8: End-of-farm summary and window lifetime

**Files:**
- Modify: `tui/farm.go`, `tui/keys.go` (`onEnter` attaches the summary), `tui/sessions.go` (`/new`, `/resume` keep `m.farm`)
- Test: `tui/farm_test.go`

**Interfaces:**
- Consumes: Task 4 `farmDoneMsg`, Task 2 `Result`, `Snapshot`.
- Produces: `func farmSummary(m farm.Manifest, res farm.Result) string` (parts and statuses, diffstat, verdict, branch, final choice); `Model.farmNote string` (pending note for the next user message).

Behaviour (spec, Lifetime; When the farm ends):
- On `farmDoneMsg`: print the summary (held if in an agent view); set `farmNote`. The next user message to the main session is sent as `farmNote + "\n\n" + text`; the transcript shows only the user's text plus a dim `(farm summary attached)`. No turn starts on its own.
- `/new` and `/resume` keep `m.farm`, rows and `farmNote`; the note goes to whichever session is current.
- The last farm stays in `m.farm` (done) for `/farm` until another starts; nothing persists across restarts.

- [ ] **Step 1: Write failing tests**
  - `TestFarmEnd_SummaryAttachedToNextMessage`: `farmDoneMsg` → no `startRun`; next Enter `review it` → fake session's first user message starts with the summary.
  - `TestFarmEnd_SurvivesNewSession` (Review Focus 5): farm running, `/new` → rows still in view; done → note attaches to the new session.
  - `TestFarmSummary_Fields`.
- [ ] **Step 2: Run** `go test ./tui -run 'FarmEnd_|FarmSummary'` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./tui` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: attach the farm summary to the next chat message`

---

### Task 9: `suggest_farm`

**Files:**
- Create: `cmd/jas/suggest_farm.go`, `cmd/jas/suggest_farm_test.go`
- Modify: `cmd/jas/chat.go` (`configProv` registry adds the tool only when `Farmer().Available()`), `tui/chat.go` (message `suggestFarmMsg{task string}` pre-fills the input)

**Interfaces:**
- Produces: tool `suggest_farm` with schema `{"task": string (required)}`; result text `suggested to the owner: /farm "<task>"`; a callback `func(task string)` from backend to TUI that sends `suggestFarmMsg`.
- Tool description (exact): `Suggest splitting a large task into a farm of parallel agents. Use only for big, decomposable work (several packages or layers); the owner decides. Draft a self-contained task description; it pre-fills the owner's input as /farm "<task>". Never use for small or single-package changes.`

- [ ] **Step 1: Write failing tests**
  - `TestSuggestFarm_RegisteredOnlyWhenAvailable`.
  - `TestSuggestFarm_PrefillsInput`: tool call → TUI input value `/farm "migrate logging"`; nothing starts.
- [ ] **Step 2: Run** `go test ./cmd/jas ./tui -run SuggestFarm` — Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: suggest_farm tool pre-fills /farm in the chat` (README + manual).

---

### Task 10: Docs and the acceptance run

**Files:**
- Modify: `README.md` (farm section: chat usage, keys, prompts, bell), `cmd/jas/manual/jas/SKILL.md`, `AGENTS.md` (Current focus: interactive farm shipped; benchmark next), `docs/research/farm-dogfood-2026-11.md` (run 4)

- [ ] **Step 1:** Check README and manual cover: `/farm "<task>"`, `/farm run`, `/farm` dashboard, `attach|back|stop|pause|resume|drop`, Ctrl/Alt+1–9 and +0, Esc/Ctrl+C in agent views, approvals and prompts, `farm_bell`, `suggest_farm`.
- [ ] **Step 2: Run** `go test -race ./... && go vet ./... && gofmt -l .` — Expected: PASS, no files listed.
- [ ] **Step 3: Acceptance run** (spec, Acceptance; owner at the terminal): in `jas` chat on a real two-part task, use a `suggest_farm` pre-fill, approve, flip to each agent and back, steer one, `/farm`, answer an approval while flipped elsewhere, answer keep, ask the main agent about the result. Record findings as "Run 4" in `docs/research/farm-dogfood-2026-11.md`.
- [ ] **Step 4: Commit** `docs: interactive farm in README, manual and dogfood notes`

---

## Build record (2026-11-04)

**Status:** all 10 tasks built on branch `feat/farm-interactive` (worktree
`~/projects/jas-farm-interactive`), test-first; final whole-branch review by
a fresh reviewer found 4 Critical and 8 Important issues, all fixed with
regression tests (`tui/farm_review_test.go`, `farm/handle_test.go`);
`go test -race ./...` green. **Not merged.** Next: the owner's acceptance run
([`docs/farm-acceptance-run.md`](../../farm-acceptance-run.md)), then a PR.

**Open question for the owner:** the final prompt matches prefixes either
way round (as the CLI does), so `dunno` or `don't` count as **d**iscard,
which deletes branches. Tighten (e.g. whole word for discard) before merge?

**Rulings made during the build** (decision — why — cost if wrong):

- Task 2: Ruling: TestHandle_UndeliveredSteeringHandedBack steers during part_check, not "as the worker gives its final answer" — core.Session delivers a steer that arrives mid-reply (it continues the turn), so only a steer after Send returns is undelivered — cost if wrong: one test's trigger point.
- Task 2: Ruling: the dirty-main merge refusal also applies to jas farm run (CLI), including --allow-dirty farms — the spec's rule is about the owner's checkout, not the front end; git merge alone only refuses conflicting dirty files — cost if wrong: a CLI owner who kept edits must commit/stash before merge.
- Task 3: Ruling: tui.Farmer gains Discard(p) and FarmPlan gains Task — the chat must close a planner it never approves (quit/q) and re-offer the task on refusal — cost if wrong: two extra port members.
- Task 3: Ruling: a farm from the chat needs a clean working tree, as jas farm plan without --allow-dirty — spec is silent on dirty-at-start; the CLI's safe default (uncommitted edits would silently not be part of the farm) — cost if wrong: owner commits/stashes before /farm.
- Task 3: Ruling: farmPreflight returns warnings instead of printing them (CLI prints them; chat shows them in the plan summary) — printing to stderr would corrupt the inline chat — cost if wrong: none for CLI output (same lines).
- Task 4: Ruling: farmDoneMsg comes from a goroutine waiting on run.Wait, not a tea.Cmd — a command blocking for the whole farm ties up the runtime (and hangs the synchronous test harness) — cost if wrong: none.
- Task 4: Ruling: rows collapse when agents+1+input+4 chrome+3 transcript rows exceed the height — keeps some transcript visible; the plan's "+4" alone collapsed only when the live region overflowed — cost if wrong: threshold tweak.
- Task 7: Ruling: every approval (main or farm) sets the input and any open picker aside and restores them after the answer; TestChat_ApprovalClosesPicker becomes TestChat_ApprovalSetsPickerAside — R1 says the input comes back; the farm plan review picker must survive an approval — cost if wrong: a restored /allowed picker may list grants from before an "always" answer (reopen refreshes).
- Task 7: Ruling: farm approvals offer y/N/always/farm ("farm" = always for this farm) — spec's choices yes · no · always for this part · always for this farm — cost if wrong: answer word.
- Task 7: Ruling: onRunDone only clears a main-session approval; a farm part's open approval survives the main turn ending — otherwise the worker would wait on a prompt that vanished — cost if wrong: none.
- Task 7: Ruling: farm_bell is a config key (jas config set farm_bell false), default true; bell rings for farm approvals and farm questions only, not main-session approvals — R3 scope is "the farm needs you" — cost if wrong: one condition.
- Task 8: Ruling: farmSummary takes the part order as a third argument — the manifest's parts are a map, so a stable order must come from the plan — cost if wrong: signature only.
- Task 8: Ruling: /new and /resume return the window to the main chat first — they clear and redraw the main chat; leaving the view on an agent would hold the banner — cost if wrong: none.
- Task 9: Ruling: suggest_farm reaches the chat as a UI event (tui.EvSuggestFarm) through the session's own UI, not a new backend callback — the bridge already carries session events to Update — cost if wrong: none.
- Task 9: Ruling: a suggestion never overwrites a draft (or a stashed input); it is printed as 'suggested: /farm …' instead — the spec says the owner edits or deletes it — cost if wrong: one condition.
- Task 10: Ruling: the acceptance run (owner at the terminal) happens after the final branch review and before merge, not inside Task 10 — it needs the owner live; the review may change code it exercises — cost if wrong: order only.
- Final: Ruling: review #12 (held output longer than a screen) regraded Important and fixed by printing every held block on Ctrl+0 — spec Q4 says "Ctrl+0 prints them" — cost if wrong: a long redraw.

**Deferred minors** (from the final review; not fixed):

- paused parts show as running in rows/dashboard
- per-part tokens update only when a part ends
- /farm stop ends without a final choice (matches CLI interrupted path)
- Available() does config file I/O in Update
- unquoted /farm tasks starting with a subcommand word run the subcommand
- suggest_farm %q escapes newlines/quotes in the pre-filled task
- "farm needs you · ctrl+0" on the totals line in the main chat while a turn runs
- planning can't be cancelled; quit during review leaves the planner open
