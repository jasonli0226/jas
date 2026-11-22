# `jas attach`: reach a running farm from another terminal

Status: draft, awaiting owner approval (2026-11-22); builder check done. Issue #156; design §5;
farm spec "Next version".

## Intent

The owner is coding elsewhere while a farm runs: in another terminal
(`jas farm run`, possibly detached) or from a chat (`/farm`) in another
worktree. A part hits a wall that needs a human call. The owner wants to
look at it and unblock it — steer it at a turn boundary, answer its
approval — without stopping the farm and without going back to the
terminal that started it (docs/use-cases.md §7, design §5).

Control is the point; watching serves it.

## Goals

1. **Phase 1, watch:** `jas attach` shows a live dashboard of a farm and
   follows its events; `jas attach <farm> <part>` follows one part in more
   detail. Works for farms started by `jas farm run` and by the chat.
2. **Phase 2, control:** from an attached terminal, steer, pause, resume,
   drop, stop, and answer approvals.
3. **Approvals for unattended farms:** a farm with no TTY can hold a gated
   call for an attached terminal to answer, instead of refusing at once.
4. CI behavior, `--json`, exit codes and the approval ladder are unchanged
   when nobody attaches.

## Non-goals

- Answering the farm's failure questions (retry / drop / abort) from attach
  (owner, 2026-11-22: approvals only; failure questions later, in their own
  design, because drop takes dependents with it and abort ends the farm).
  Attached terminals *see* failure events; the farm's own TTY or the chat
  still answers them, and unattended farms keep using `on_failure`.

- Live token-by-token text. Watching shows logged events (tool calls,
  results, whole replies), not streaming deltas.
- Crash-resume of a farm (`farm resume`, take-over): separate post-MVP item.
  "Resume" in this design always means unpausing a paused part.
- Attach to a plain `jas run` or chat session (non-farm).
- An approval inbox across farms (farm spec "Next version"): one farm per
  attach.
- Remote (non-local) access, multi-user access.

## Anti-goals

- A fullscreen / alt-screen view. Inline scrollback only (AGENTS.md).
- Two terminals both deciding the same approval, or a reply landing on a
  different request than the one it answered.
- Owner text silently lost (steering that never reaches a part and never
  comes back).
- A CI farm that slows down or hangs because attach exists.

## Constraints

- Inline Bubble Tea `Println` scrollback; no alt-screen (AGENTS.md).
- Do not break `jas run`, `jas farm run --json`, exit codes (farm 6/7/8),
  approval levels, deny floor, redaction.
- Chat: every farm call from the chat runs in a `tea.Cmd` or goroutine,
  never in `Update`; never append to the session log synchronously from
  `Update` (AGENTS.md).
- `CGO_ENABLED=0`; `go test -race ./...` green; tests use no live model
  calls (fake provider).
- Docs follow features: `docs/manual.md` (+ `make manual`), README overview,
  `cmd/jas/manual/jas/SKILL.md` (new subcommand), `docs/event-log.md` for new
  events.

## Approaches

- **A. Files now, socket later for everything.** Phase 1 tails the logs;
  phase 2 moves the event stream onto a socket too. Two event transports,
  the second replacing the first.
- **B. Files for watching, socket for commands only (picked).** Watching is
  always read from the farm's logs on disk; the socket carries small
  request/reply commands and presence. Reconnect and replay are free (the
  files are the history), a dead farm is still readable, the protocol stays
  small, and phase 1 needs only one small change to the farm process (a
  liveness lock, below). Cost: no live token text.
- **C. One socket for events and commands** (design §5 as first written).
  Live tokens, but reconnect needs snapshot + buffer, nothing works once the
  supervisor is gone, and the most new code in the farm process.

Owner picked B (2026-11-22).

## Design

### Finding a farm

- Farms live under `<jas home>/farms/<farm-id>/` (`farm.jsonl`,
  `manifest.json`, `parts/<part>/events.jsonl`), found as `jas farm status`
  finds them.
- `jas attach` with no argument: if exactly one farm is running, attach to
  it; if several, list them (id, plan name, started, parts running) and exit
  2 asking for an id; if none, say so and exit 0 — (builder: the exit code
  for "none running" follows `farm status` conventions).
- `jas attach <farm-id>`; `jas attach <farm-id> <part>`. A unique id prefix
  is accepted.
- **Liveness (my call, builder check).** Nothing records today whether a
  farm is still running (`farm status` picks the newest). PR 1 adds a lock
  file `<jas home>/run/<farm-id>.lock`, held by the farm process for its
  lifetime with the existing `core.LockPath` (a held OS lock, released on
  exit or crash). `LockPath` writes nothing: the farm writes its pid into
  the file after taking the lock, for display. Attach probes liveness by
  taking the lock and releasing it at once; so that a probe never makes a
  starting farm fail, the farm retries `ErrLocked` briefly (for example 3
  times, 100 ms apart) before treating it as fatal. "Running" means
  the lock is held; otherwise the farm has ended or died, and attach shows
  its final state from the files and exits. A crashed farm and a finished
  one differ by whether `farm.jsonl` has `farm_ended`. In PR 2 the socket
  sits beside the lock; the lock stays the liveness test.

### Phase 1: watching (from files)

- On attach: print a dashboard built from `manifest.json` (same columns as
  the chat's farm dashboard: part, state, current tool, tokens, last board
  post / approval), then follow `farm.jsonl` and every part's
  `events.jsonl`, printing one line per event of interest, prefixed
  `[part]`: tool started (as the chat's progress lines), tool result status,
  approval requested / decided, part state changes, board posts, farm
  events. New parts' logs are picked up as they appear.
- Replay on attach: the dashboard is the current state; the scrollback shows
  only events after attach, plus the last N lines per part (builder: N, a
  small number such as 3).
- `jas attach <farm> <part>`: the same, but for that part also prints
  assistant replies (text, truncated to the terminal's taste — builder) and
  tool results (redacted output as logged, truncated).
- Following: poll or watch the files (builder: polling at ~250 ms is
  acceptable; event lines are appended whole by the log writer). Lines that
  are incomplete at read time are retried, never printed half.
- Ctrl-C detaches; the farm keeps running. The dashboard re-prints on
  `status` (phase 2 command line) or every N state changes (builder).
- When the farm ends while attached: print the farm's end line and exit 0.
- Inline only; plain lines, no cursor tricks beyond what the chat already
  does.

### Phase 2: control (over the socket)

**Socket.** The supervisor (in `jas farm run` and in the chat) listens on
`<jas home>/run/<farm-id>.sock`, mode 0600, directory 0700, for the farm's
lifetime; removed on farm end. A stale socket file (connect refused) is
treated as "not running" and removed by the next farm or attach that finds
it. Same user only; any connection gets full control (no extra auth).

**Protocol.** JSONL over the socket: one request per line, one reply per
request, plus unsolicited `approval_requested` / `approval_closed` notices
pushed to every client. Requests carry a client-chosen `id`; replies echo
it. (Builder: exact field names.) Commands:

| command | effect | reply |
|---------|--------|-------|
| `hello` | registers presence; returns farm id, socket protocol version, pending approvals | ok + pending list |
| `steer {part, text}` | `Handle.Steer` | ok, or `part not running` |
| `pause` / `resume` / `drop {part}` | `Handle.Pause` / `Resume` / `Drop` | ok / error text |
| `stop {hard}` | `Handle.Stop` | ok |
| `approve {approval_id, answer}` | answer a pending approval with one of the existing options (yes, no, always for this part, always for this farm), named as the chat prompt and manual already name them | ok, or `already answered (<answer>, by <source>)`, or `unknown approval` |

Every command is an `owner_decision` in `farm.jsonl` with
`source: "attach"` (and the existing fields), as the chat's are.

**Steering, no silent loss.** Steer text from attach enters through the
same path as `/farm attach`. If the part finishes before reading it, the
supervisor sends the undelivered text back to the client that sent it (a
pushed `steer_undelivered` notice); if that client has gone, the text is
printed by the farm's own host (terminal or chat) as today, and recorded as
`owner_decision{choice:"steer_undelivered"}`.

**Approvals: first answer wins.**
- Each gated call gets a stable `approval_id` (unique within the farm), and
  an `approval_requested` event `{approval_id, part, call_id, tool, summary}`
  is appended to the part's log *before* anyone is asked.
- **All pending approvals are offered (my call).** Today the TTY shows
  one prompt at a time behind a shared lock. The broker becomes a set of
  pending approvals: every pending one is pushed to attached clients at
  once and can be answered in any order; the TTY (or chat) keeps showing
  them one at a time, skipping any already answered.
- Every place that can answer is asked at once: the farm's own TTY prompt
  (when interactive), the chat's inline prompt (chat-hosted farm), and every
  attached client (pushed notice). The first answer is applied; the others
  are told `already answered (<answer>, by <source>)` and their prompt is
  withdrawn (see Withdrawing a prompt). Late or duplicate answers to a
  closed id change nothing.
- `approval_closed {approval_id, answer, by}` is pushed to clients and the
  existing `approval` event is logged with the decision and `by`.
- The "always" options keep today's meaning (never outlive the
  farm).

**Withdrawing a prompt (builder check: a landmine, designed here).** Today
`core.Approver` starts a reader goroutine per prompt that cannot be
interrupted: after a cancel it stays blocked on stdin, holding the
approver's mutex, until a line arrives (`core/approve.go:109`). That is fine
only because a cancelled run ends the process. With attach the process keeps
running, so a withdrawn prompt would leave that goroutine waiting: the next
prompt blocks on the mutex, and the owner's next line is swallowed by the
withdrawn prompt. So the farm's TTY gets **one persistent line reader** for
the process (started with the farm's approvals, reading stdin into a
channel), and prompts consume lines from it instead of reading stdin
themselves:
- A line is applied to the prompt that was on screen when the line was
  read. A line read after that prompt was withdrawn is never passed to the
  next prompt: the next prompt is printed only after the withdrawal line
  (`[backend-api] [a3] answered from attach: yes`), and a line attributed to
  a closed prompt gets `already answered` like any late answer.
- Prompts outside farms (`jas run`, chat) keep today's reader; the change
  is limited to the farm's TTY approvals.
- The chat's inline approval prompt is model state, not a reader: the chat
  host withdraws it by a message sent through `Program.Send` from the
  farm's goroutine (never from `Update`), after which a key press for it is
  ignored.

**Unattended farms (owner, 2026-11-22: wait, with a timeout).** Applies to a
farm with no interactive TTY (detached `jas farm run`, CI):
- **Default — wait only while attached.** A gated call is held only if at
  least one client is connected (has sent `hello`); with zero clients it is
  refused at once with `approval_needed`, exactly as today. CI never has
  clients, so its behavior is unchanged.
- **Hold window.** A held call waits up to the approval wait window
  (default 10m). **The wait counts against the part's clocks (owner,
  2026-11-22):** `per_worker_timeout` and `farm_timeout` stay wall-clock
  caps, as they already are for a TTY prompt. The effective hold is
  min(window, time left on the part and the farm). If a timeout fires
  first, the part ends by that timeout through its existing handling (the
  same status, reason and exit code as any timeout), not as
  `approval_needed`. When the window ends, or the last client disconnects while
  it waits, it is refused with `approval_needed` and logged
  (`approval_closed{answer:"refused", by:"timeout"|"no_client"}`). Exit
  codes unchanged.
- **No detach feature (my call).** "Detached" means the owner ran
  `jas farm run` under `nohup`, `setsid`, a service or CI; jas adds no
  `--detach`. "No interactive TTY" is the existing check. A farm in tmux
  or screen still has a TTY, so it prompts there as today, and attached
  clients can answer too.
- **Override: `--approval-wait <duration>`** on `jas farm run` (and the
  matching setting, builder: name, e.g. `farm_approval_wait`) holds gated
  calls for that window even with zero clients, so the owner can attach
  later. `0` means the default behavior above.
- **Late attach.** The request is logged before any refusal, so a terminal
  that attaches after a refusal sees what was refused; the part then follows
  the existing failure flow (retry / drop / `on_failure`). A refused request
  is never revived.
- An interactive `jas farm run` keeps prompting on its TTY as today, and
  attached clients can answer too (first answer wins).

**Attach client UI.** The attached terminal is inline: the watch stream
from phase 1, plus a prompt line accepting commands (builder: syntax;
suggested to mirror the chat's `/farm` commands, e.g. `steer <part> <text>`,
`pause <part>`, `y`/`n`/`a`/`A` at an approval prompt). A pending approval
prints as `[backend-api] approve [a3] shell: npm install zod (y/n/a/A)`.

### Supervisor death and reconnect

- Client disconnects: presence drops; held approvals follow the
  "last client" rule above. Reconnect is a new `hello`; pending approvals
  come back in its reply; the watch stream re-reads from the files.
- Supervisor dies (crash, kill): the socket stops answering; clients print
  `farm process ended` and fall back to the dead-farm view (final state from
  files). No command is accepted. Crash-resume is out of scope.
- Several clients: all watch, all may command; approvals are first-wins;
  steers queue in arrival order.

### Phasing

- **PR 1 (phase 1):** `jas attach` watch from files, discovery, dead-farm
  view, docs. No socket.
- **PR 2 (phase 2):** socket, protocol, presence, commands, approvals
  (first-wins, ids, held approvals, `--approval-wait`), attach prompt line,
  chat and `farm run` hosts serving the socket, docs and events.

### Events (new)

- Part log: `approval_requested {approval_id, call_id, tool, summary}`;
  existing `approval` gains `approval_id` and `by`. `summary` is redacted
  exactly as the logged call is (the deny floor and redaction apply;
  nothing appears in it that the `tool_started` args would not show).
- `farm.jsonl`: `owner_decision` with `source: "attach"`;
  `attach_connected` / `attach_disconnected {client}` (logged: they explain
  `no_client` refusals).

## Tests (no live model)

- Discovery: zero / one / several running farms; prefix ids; stale socket.
- Watch: dashboard from a fixture farm dir; following appended lines;
  partial line not printed; new part log picked up; farm end exits 0;
  dead farm prints final state.
- Protocol: each command against a fake Handle; unknown command; bad JSON.
- Approvals: two clients answer the same id → one applied, the other gets
  `already answered`; answer to unknown id; TTY + client race; chat host
  + client race.
- Held approvals: no client → immediate `approval_needed` (unchanged);
  client connected → held then answered; client disconnects mid-wait →
  refused `no_client`; window elapses → refused `timeout`;
  `--approval-wait` with no client → held.
- Steering: delivered at turn boundary; part finishes first → text returned
  to the sending client; client gone → host prints it.
- Supervisor death: client sees `farm process ended`, falls back to files.
- CI regression: `jas farm run --json` without TTY, no client: stdout and
  `farm.jsonl` event sequence and exit code identical to today. The part
  logs differ by one intended line per gated call (`approval_requested`,
  logged before the refusal); the test names that diff.
- Prompt withdrawal: TTY prompt open → answered from attach → the
  withdrawal line prints, and the next approval still prompts and receives
  its own answer (no swallowed line, no blocked mutex). Same for the chat
  prompt.
- Timeout wins: `per_worker_timeout` fires while a call is held → the part
  ends by that timeout (its usual status, reason and exit code), not
  `approval_needed`.
- Lock probe: attach probing while a farm starts → the farm still starts
  (retry), and attach sees it running.

## Left to the builder

- Socket protocol field names and version field; client command syntax.
- File following mechanism (poll interval) and replay line count.
- Setting name for the approval wait window.
- Exact retry counts for the lock probe.
