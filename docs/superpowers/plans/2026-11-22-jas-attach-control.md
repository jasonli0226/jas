# `jas attach` Phase 2 (Control) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** From an attached terminal, steer, pause, resume, drop and stop a farm's parts and answer its approvals (first answer wins), and let a farm with no TTY hold gated calls for an attached answer (PR 2 of the attach design).

**Architecture:** A farm-level approval **broker** becomes every part's `core.Gate`: it owns the "always" grants, gives each gated call a stable id, logs it, offers it to the host (TTY or chat) and to attached clients at once, and applies the first answer. The TTY host reads stdin through one persistent **line reader**, so a withdrawn prompt never swallows a line. `StartFarm` serves a Unix **socket** (`<home>/run/<farm-id>.sock`) speaking JSONL commands that map to `Handle` methods and `Broker.Answer`. `jas attach` (phase 1) gains a command line that talks to the socket.

**Tech Stack:** Go, `net` Unix sockets, existing `farm.Handle`, `core.Grants`, `core.SummarizeCall`, Bubble Tea chat gate (`tui/gate.go`).

**Spec:** `docs/superpowers/specs/2026-11-22-jas-attach-design.md` (Phase 2, Withdrawing a prompt, Unattended farms, Supervisor death and reconnect, Events, Tests). Phase 1 shipped in #226 (`farm/live.go`, `farm/follow.go`, `cmd/jas/attach_cmd.go`).

## Global Constraints

- Socket `<jas home>/run/<farm-id>.sock`, mode 0600 (dir 0700, as the lock), removed on farm end; same user only, any connection gets full control. On Windows the socket is not served (attach stays watch-only there; `net` Unix sockets exist on Windows 10+, but the builder may gate it off — record the ruling).
- Protocol: JSONL, one request per line, one reply per request (echoing the client's `id`), plus pushed notices. Protocol version `1` in the `hello` reply.
- Answers: `yes`, `no`, `always` (this part), `farm` (this farm) — the chat's words (`tui/gate.go` `classifyReply`); "always" grants never outlive the farm and never cover a must-ask call (`core.MustAsk`).
- First answer wins; a later answer gets `already answered (<answer>, by <source>)`; an unknown id gets `unknown approval`; neither changes anything.
- Unattended (no host asker): with zero clients and `approval_wait` 0 → refuse at once (`core.Unavailable` → `approval_needed`, as today). Otherwise hold up to the window: `approval_wait` when > 0, else 10m. Refuse when the window ends (`by: "timeout"`) or, with `approval_wait` 0, when the last client leaves (`by: "no_client"`). The part's ctx (per-worker and farm timeouts) still applies: ctx done → `core.Denied, ctx.Err()`, the existing timeout handling.
- With a host asker (interactive TTY or chat): no window — wait as today; clients may also answer.
- Events: part log `approval_requested {approval_id, call_id, tool, summary}` before anyone is asked; part log `approval_closed {approval_id, answer, by}` when closed (ruling: `core` logs `approval` from a bare `Decision`, with no channel for id/by, so the broker logs `approval_closed` beside it instead of extending `approval`). `farm.jsonl`: `owner_decision` with `source: "attach"` for every attach command; `attach_connected` / `attach_disconnected {client}`. `summary` = `core.SummarizeCall` of the redacted args.
- New setting: farm.yaml `budget.approval_wait` (duration, default `0`); flag `jas farm run --approval-wait <d>` overrides it.
- Unchanged with no client: `jas farm run --json` stdout, `farm.jsonl` event sequence, exit codes (part logs gain `approval_requested` + `approval_closed` per gated call — the intended diff).
- Chat: farm calls from a `tea.Cmd`/goroutine only; never log synchronously from `Update` (AGENTS.md).
- Docs in the same change set: `docs/manual.md` (attach commands, `--approval-wait`, `budget.approval_wait`, events), `docs/event-log.md`, `SKILL.md`, `make manual`; `TestManual_*` must pass.

## Review Focus

1. A client that sends a line longer than 1 MiB, or invalid JSON, gets an error reply (or is disconnected) and the farm is unaffected. Test in Task 4 (`TestAttachServer_BadInput`).
2. A client connected but never sending `hello` does not count as present (no hold). Test in Task 4 (`TestAttachServer_PresenceNeedsHello`).
3. A must-ask call (profile memory/skills write) answered `farm` from attach is granted once, not recorded as a standing grant. Test in Task 2 (`TestBroker_MustAskNoGrant`).
4. Two parts requesting at once: ids are distinct and each answer reaches only its own request. Test in Task 2 (`TestBroker_ConcurrentRequests`).
5. The farm ends while a client is connected: the socket file is removed and the client sees EOF, then attach prints the end line. Test in Task 4 (`TestAttachServer_RemovedOnEnd`) and Task 6 (`TestAttach_ControlFarmEnds`).

---

### Task 1: TTY line reader

**Files:**
- Create: `farm/linereader.go`, `farm/linereader_test.go`

**Interfaces:**
- Produces:

```go
// LineReader reads one input (stdin) on a single goroutine for the farm's
// lifetime; prompts take lines from it instead of reading the input.
type LineReader struct{ /* … */ }
func NewLineReader(in io.Reader, out io.Writer) *LineReader
// Ask prints prompt and returns the next line read while this prompt is the
// current one. ctx done: returns ctx.Err() and the prompt stops being current.
// Asks are serialized (one prompt on screen at a time).
func (l *LineReader) Ask(ctx context.Context, prompt string) (string, error)
// Note prints a line under the prompt lock (e.g. a withdrawal line).
func (l *LineReader) Note(line string)
```

A line read while no prompt is current is dropped with the note `(ignored: no question is waiting)`; EOF makes every current and later `Ask` return `io.EOF`.

- [ ] **Step 1: Write the failing tests** (`io.Pipe` as input):

```go
func TestLineReader_Answer(t *testing.T)            // Ask, write "y\n" → "y"
func TestLineReader_CancelThenNext(t *testing.T)    // Ask A, cancel A; Ask B; write "n\n" → B gets "n", A got context.Canceled
func TestLineReader_LateLineNotPassedOn(t *testing.T) // Ask A, cancel; write "y\n" before B is asked → dropped with the note; Ask B then "n\n" → "n"
func TestLineReader_Serialized(t *testing.T)        // two concurrent Asks: second prompt printed only after the first returns
func TestLineReader_EOF(t *testing.T)               // close input → Ask returns io.EOF
```

- [ ] **Step 2: Run** `go test ./farm -run TestLineReader` → build failure.
- [ ] **Step 3: Implement.** One reader goroutine sends lines on a channel; `Ask` takes the prompt lock, prints, then selects on that channel and ctx.
- [ ] **Step 4: Run** `go test -race ./farm -run TestLineReader` → PASS.
- [ ] **Step 5: Commit** `feat: a persistent line reader for farm prompts`

### Task 2: Approval broker

**Files:**
- Create: `farm/broker.go`, `farm/broker_test.go`

**Interfaces:**
- Consumes: `LineReader` only through the host asker (Task 5 wires it).
- Produces:

```go
type Answer int
const (AnswerNo Answer = iota; AnswerYes; AnswerAlways; AnswerFarm)
func ParseAnswer(s string) (Answer, bool)   // y/yes, n/no/"", always/a, farm/f (case-insensitive)
func (a Answer) String() string             // "yes" | "no" | "always" | "farm"

type ApprovalRequest struct {
	ID, Part, CallID, Tool, Summary string
	Diff *core.Diff // display only; nil when none
}
// HostAsker asks the farm's own terminal or chat; it must stop asking when
// ctx is done. nil = no host (unattended).
type HostAsker func(ctx context.Context, req ApprovalRequest) (Answer, error)

type Notice struct {
	Type    string // "approval_requested" | "approval_closed"
	Request ApprovalRequest
	Answer  string // closed: answer or "refused"
	By      string // closed: "host" | "attach:<client>" | "timeout" | "no_client"
}

type BrokerOptions struct {
	Level   core.Level
	Host    HostAsker
	Wait    time.Duration // budget.approval_wait / --approval-wait; 0 = hold only while attached
	Windows bool          // core.NewGrants
	Redact  func(string) string
	After   func(time.Duration) <-chan time.Time // nil = time.After (tests inject)
}
func NewBroker(o BrokerOptions) *Broker
func (b *Broker) Gate(part, approve string, log core.EventLog) (core.Gate, error)
func (b *Broker) Answer(id string, a Answer, by string) (ok bool, msg string) // msg: "", "already answered (yes, by host)", "unknown approval"
func (b *Broker) Pending() []ApprovalRequest
func (b *Broker) Subscribe() (<-chan Notice, func())  // buffered; slow subscribers drop notices, never block a gate
func (b *Broker) SetClients(n int)
const defaultApprovalWindow = 10 * time.Minute
```

Ids: `a1`, `a2`, … per farm. Grant handling as `tui/gate.go` `partGate.decide` (part grants per gate, farm grants shared; must-ask never recorded). The hold rules are in Global Constraints.

- [ ] **Step 1: Write the failing tests** (fake `core.Tool`s from existing farm tests; a recording `EventLog`; injected `After`):

```go
func TestBroker_NotGatedAuto(t *testing.T)            // level never → AutoGranted, no events
func TestBroker_HostAnswers(t *testing.T)             // host returns AnswerYes → Granted; part log: approval_requested{approval_id:"a1",summary} then approval_closed{answer:"yes",by:"host"}
func TestBroker_ClientWinsHostWithdrawn(t *testing.T) // host blocks; Answer("a1", AnswerNo, "attach:c1") → Denied; host's ctx is cancelled; later Answer → ok=false, msg "already answered (no, by attach:c1)"
func TestBroker_UnknownID(t *testing.T)               // Answer("a9",…) → ok=false, "unknown approval"
func TestBroker_AlwaysCoversPart(t *testing.T)        // answer always → next identical call AutoGranted for that part, asked again for another part
func TestBroker_FarmCoversAll(t *testing.T)           // answer farm → identical call in another part AutoGranted
func TestBroker_MustAskNoGrant(t *testing.T)          // core.AskAlways(tool) answered farm → Granted; next call asked again
func TestBroker_NoHostNoClientRefuses(t *testing.T)   // Host nil, clients 0, Wait 0 → Unavailable at once; approval_requested then approval_closed{answer:"refused",by:"no_client"}
func TestBroker_HeldWhileAttached(t *testing.T)       // Host nil, SetClients(1) → blocks; Answer yes → Granted
func TestBroker_LastClientLeaves(t *testing.T)        // held; SetClients(0) → Unavailable, by "no_client"
func TestBroker_WindowElapses(t *testing.T)           // held, injected After fires → Unavailable, by "timeout"
func TestBroker_WaitHoldsWithoutClients(t *testing.T) // Wait 5m, clients 0 → held; Answer yes → Granted
func TestBroker_TimeoutWins(t *testing.T)             // held; part ctx deadline passes → Denied, context.DeadlineExceeded (not Unavailable)
func TestBroker_ConcurrentRequests(t *testing.T)      // two parts ask at once: ids a1,a2 distinct; answering a2 resolves only part 2
func TestBroker_SummaryRedacted(t *testing.T)         // args holding a secret the Redact func masks → approval_requested summary masked
func TestBroker_Notices(t *testing.T)                 // Subscribe sees requested then closed, in order
```

- [ ] **Step 2: Run** `go test ./farm -run TestBroker` → build failure.
- [ ] **Step 3: Implement `farm/broker.go`.**
- [ ] **Step 4: Run** `go test -race ./farm -run TestBroker` → PASS.
- [ ] **Step 5: Commit** `feat: a farm approval broker with stable ids and first answer wins`

### Task 3: Supervisor uses the broker

**Files:**
- Modify: `farm/part.go:536-545` (`gate`), `farm/part.go:387`, `farm/integrate.go:248`, `farm/run.go` (`RunOptions`), `farm/supervisor.go:60-65`, `farm/approvals.go` (delete what the broker replaces; keep `Approvals.Ask`'s job via `LineReader` — see Task 5)
- Test: `farm/supervisor_test.go` or `farm/broker_farm_test.go`

**Interfaces:**
- Consumes: `NewBroker`, `Broker.Gate` (Task 2).
- Produces: `RunOptions.Broker *Broker` (nil → `NewBroker(BrokerOptions{Level: core.Never})`, i.e. today's no-TTY behavior); `RunOptions.Approvals` and `RunOptions.Gates` are removed; `func (s *Supervisor) gate(part, approve string, log core.EventLog) (core.Gate, error)`; `func (h *Handle) Broker() *Broker`.

- [ ] **Step 1: Write the failing tests** with the existing run rig (fake provider, a role at `approve: shell`, a scripted shell call):

```go
func TestFarm_GatedCallNoClientUnchanged(t *testing.T) // no host, no client: part ends approval_needed as today; farm.jsonl types equal the pre-change fixture list; part log has approval_requested + approval_closed{by:"no_client"}
func TestFarm_BrokerAnswerReachesPart(t *testing.T)    // SetClients(1); wait for Pending() non-empty; Answer yes → the shell call runs, part ok
```

- [ ] **Step 2: Run** → FAIL (no `RunOptions.Broker`).
- [ ] **Step 3: Implement.** Pass the part's log to `gate` at both call sites.
- [ ] **Step 4: Run** `go test ./farm` → PASS (existing approval tests updated to construct a `Broker`).
- [ ] **Step 5: Commit** `refactor: farm parts gate through the approval broker`

### Task 4: Attach socket server

**Files:**
- Create: `farm/attachproto.go` (wire types, shared with the client), `farm/attachsrv.go`, `farm/attachsrv_test.go`
- Modify: `farm/handle.go` (`StartFarm` starts the server after `startFarm`; `Handle.run` stops it before `close`), `farm/live.go` (`func sockPath(home, id string) string`)

**Interfaces:**
- Consumes: `Handle.Steer/Pause/Resume/Drop/Stop`, `Broker.Answer/Pending/Subscribe/SetClients`.
- Produces (`attachproto.go`):

```go
const AttachProtocol = 1
type AttachRequest struct {
	ID   string `json:"id"`
	Cmd  string `json:"cmd"` // hello|steer|pause|resume|drop|stop|approve
	Part string `json:"part,omitempty"`
	Text string `json:"text,omitempty"`
	Hard bool   `json:"hard,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	Answer     string `json:"answer,omitempty"`
}
type AttachMessage struct { // one per line from the server
	Reply  *AttachReply  `json:"reply,omitempty"`
	Notice *AttachNotice `json:"notice,omitempty"`
}
type AttachReply struct {
	ID string `json:"id"`; OK bool `json:"ok"`; Error string `json:"error,omitempty"`
	FarmID string `json:"farm_id,omitempty"`; Protocol int `json:"protocol,omitempty"`
	Pending []ApprovalRequest `json:"pending,omitempty"` // hello only
}
type AttachNotice struct {
	Type string `json:"type"` // approval_requested|approval_closed|steer_undelivered
	Request *ApprovalRequest `json:"request,omitempty"`
	Answer, By, Part string `json:"…"`
	Texts []string `json:"texts,omitempty"`
}
func SockPath(home, id string) string
```

Server: one goroutine per connection; lines capped at 1 MiB (`bufio.Scanner` buffer); a connection counts for `SetClients` only after `hello`; client name `c<N>`. Commands log `owner_decision {part, choice, source:"attach"}` (choice = cmd; approve: `approve:<answer>`). Steer bookkeeping: the server wraps the host `Watcher`; `SteerUndelivered(part, texts)` sends texts the server queued for a still-connected client back to that client as `steer_undelivered` and passes the rest to the host watcher.

- [ ] **Step 1: Write the failing tests** (real socket in a short temp dir — Unix socket paths are limited to ~104 bytes; fake `Handle`-like via the run rig or an interface):

```go
func TestAttachServer_SocketMode(t *testing.T)        // file exists, 0600, while running
func TestAttachServer_Hello(t *testing.T)             // reply ok, protocol 1, farm id, pending list
func TestAttachServer_Steer(t *testing.T)             // steer → reaches the part's next request; owner_decision source attach in farm.jsonl
func TestAttachServer_SteerNotRunning(t *testing.T)   // unknown part → ok=false, "part not running"
func TestAttachServer_PauseResumeDrop(t *testing.T)   // each maps to Handle; replies ok
func TestAttachServer_Approve(t *testing.T)           // pending a1; approve yes → reply ok; second client's approve → "already answered (yes, by attach:c1)"
func TestAttachServer_Notices(t *testing.T)           // client gets approval_requested and approval_closed notices
func TestAttachServer_PresenceNeedsHello(t *testing.T) // connected without hello → no hold (unattended gated call refused at once)
func TestAttachServer_DisconnectReleasesHold(t *testing.T) // held while client connected; close conn → refused no_client; attach_disconnected logged
func TestAttachServer_SteerUndeliveredToSender(t *testing.T) // part finishes before reading → sender gets steer_undelivered; host watcher not called for those texts
func TestAttachServer_BadInput(t *testing.T)          // invalid JSON → error reply; 2 MiB line → connection closed; farm keeps running
func TestAttachServer_RemovedOnEnd(t *testing.T)      // after Wait: socket file gone; client read → EOF
func TestAttachServer_StaleSocket(t *testing.T)       // a leftover socket file (no listener) is replaced at start
```

- [ ] **Step 2: Run** `go test ./farm -run TestAttachServer` → build failure.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./farm` → PASS.
- [ ] **Step 5: Commit** `feat: farms serve attach commands on a unix socket`

### Task 5: Hosts — `jas farm run`, chat, approval wait

**Files:**
- Modify: `cmd/jas/farm_run.go:81-110` (build `LineReader` once for stdin; host asker from it when interactive; every farm stdin question — `farmAsk` in `cmd/jas/farm_ask.go` — reads through the same `LineReader`; `--approval-wait`), `farm/config.go` (`budget.approval_wait`), `cmd/jas/chat_farm.go:244-270` (broker with chat host asker), `tui/gate.go` (`func (g *Gate) AskFarm(ctx context.Context, part string, t core.Tool, args json.RawMessage, preview func() *core.Diff) (string, error)` — the prompt half of `partGate.Allow`; on ctx done it sends a message that removes the queued/open prompt; the reply text is classified by `farm.ParseAnswer`), `tui/farm_cmd.go:310-334` (drop `Gate` hook; grants move to the broker), `tui/farm_prompt.go` (withdrawal: drop the prompt, print `[part] [a3] answered from attach: yes`)
- Test: `cmd/jas/farm_run_test.go`, `tui/gate_test.go`, `farm/config_test.go`

**Interfaces:**
- Consumes: `NewLineReader`, `LineReader.Ask/Note` (Task 1), `NewBroker`, `HostAsker`, `Notice` (Task 2), `RunOptions.Broker` (Task 3).
- Produces: flag `--approval-wait <duration>`; config `Budget.ApprovalWait time.Duration`.

Host asker text for the TTY: `[part] approve [a3] shell: npm install zod? [y/N/always/farm] `. When a notice closes a request the TTY is showing with `by` ≠ host, the host prints `[part] [a3] answered from attach: <answer>`.

- [ ] **Step 1: Write the failing tests:**

```go
func TestConfig_ApprovalWait(t *testing.T)          // farm.yaml budget.approval_wait: 5m → 5m; absent → 0; "x" → error
func TestFarmRun_ApprovalWaitFlag(t *testing.T)     // --approval-wait 3m overrides config 5m in RunOptions.Broker
func TestFarmRun_TTYWithdrawnNextPrompts(t *testing.T) // spec test: TTY prompt open → answered via Broker.Answer → withdrawal line printed; next approval prompts and receives its own typed answer
func TestFarmRun_JSONNoClientUnchanged(t *testing.T)   // spec CI regression: --json, no TTY, no client → stdout and farm.jsonl types identical to the pre-change golden list; exit code unchanged
func TestGate_AskFarmWithdrawn(t *testing.T)        // tui: AskFarm open, ctx cancelled → prompt removed from the model; a later AskFarm gets its own reply
func TestChatFarm_AttachAnswerClosesPrompt(t *testing.T) // chat host + Broker.Answer from a client → chat prompt withdrawn, notice line shown
```

- [ ] **Step 2: Run** the new tests → FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run** `go test -race ./farm ./tui ./cmd/jas` → PASS.
- [ ] **Step 5: Commit** `feat: farm hosts answer through the broker; --approval-wait`

### Task 6: Attach client commands, docs

**Files:**
- Create: `cmd/jas/attach_ctl.go`, `cmd/jas/attach_ctl_test.go`
- Modify: `cmd/jas/attach_cmd.go` (when running: connect to `farm.SockPath`, send `hello`, print pending approvals, start the command line; on socket EOF while the lock is still held, keep watching), `docs/manual.md`, `docs/event-log.md`, `cmd/jas/manual/jas/SKILL.md`; `make manual`

**Interfaces:**
- Consumes: wire types (Task 4), `ParseAnswer` (Task 2).
- Produces: client command syntax (mirrors the chat's `/farm` words):

```
steer <part> <text>      pause <part>     resume <part>     drop <part>
stop [hard]              status           y|n|always|farm [<approval-id>]   (id defaults to the oldest pending)
```

Pending approval line: `[backend-api] approve [a3] shell: npm install zod (y/n/always/farm)`. Replies print `ok` silently (nothing) or the error text; `steer_undelivered` prints `[part] not delivered (part finished): <text>`; `status` re-prints the farm status table.

- [ ] **Step 1: Write the failing tests** (a real farm with the run rig, or a fake server speaking the protocol):

```go
func TestAttach_ControlSteer(t *testing.T)       // input "steer p1 use slog" → server receives steer{part:p1,text:"use slog"}
func TestAttach_ControlApproveDefaultID(t *testing.T) // pending a1, input "y" → approve{a1,yes}
func TestAttach_ControlAlreadyAnswered(t *testing.T)  // server replies already answered → printed
func TestAttach_ControlUndelivered(t *testing.T) // steer_undelivered notice → "[p1] not delivered (part finished): use slog"
func TestAttach_ControlFarmEnds(t *testing.T)    // server closes, farm_ended in log → end line, exit 0
func TestAttach_ControlUnknownCommand(t *testing.T) // "frobnicate" → help line, nothing sent
func TestAttach_WatchOnlyWithoutSocket(t *testing.T) // locked farm, no socket file → watch works as phase 1, command line says "control unavailable"
```

- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement; write docs** (manual: attach command line, approvals and the hold rules, `--approval-wait`, `budget.approval_wait`; event-log: `approval_requested`, `approval_closed`, `attach_connected`, `attach_disconnected`, `owner_decision` source `attach`).
- [ ] **Step 4: Run** `make test` and `CGO_ENABLED=1 go test -race ./...` → all `ok`, including `TestManual_*`.
- [ ] **Step 5: Commit** `feat: jas attach controls a running farm (#156)`
