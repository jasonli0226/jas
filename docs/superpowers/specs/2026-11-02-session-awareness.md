# Session awareness — design

Date: 2026-11-02 (Asia/Tokyo). Status: **proposed**, owner review pending.
Scope: the core loop — chat and `jas run`. **Not a farm feature**; it is on by
default for every session, ships before farm, and farm builds on it
([`2026-11-02-farm-design.md`](2026-11-02-farm-design.md)).
Calls marked **(author's call)** may be overturned.

## Why

Developers already run several agents at once on one repo — two terminals,
a chat plus a CI `jas run`, worktrees by hand — and the common failure is
silent: two sessions edit the same file and one overwrites the other. The
community answer is hand-rolled "who is touching what" files and lock
conventions ([research](../../research/farm-multi-agent-community-2026-11.md),
"Pain points created"). jas can provide the cheap, safe part of that without
any agent-to-agent chat: **each session knows which other jas sessions are
active on this repo and what they are touching.**

## Goals

1. Every session registers a small status record for its repo.
2. A session can see its peers, read-only.
3. Before writing a file another **active** session has touched, jas warns or
   asks; headless behaviour is configurable.
4. Zero cost when you run one session: no model calls, no network, a few
   small files.

## Non-goals

- Live agent-to-agent messaging. Handoff stays explicit: `--continue`/resume,
  fork once the session tree ships (design §5), or reading the other
  session's log.
- Locks that block writes outright (records are advisory; the farm's
  `writes` limits are the enforcement layer).
- Cross-machine awareness (records are local files).

## Record

One file per session: `<repo>/.jas/sessions/<session-id>.json`
(`<repo>` = symlink-resolved project root; `.jas/sessions/` is added to
`.git/info/exclude` on first use). Written atomically (temp + rename) by the
session only.

```json
{
  "v": 1,
  "session_id": "…",
  "pid": 12345,
  "host": "laptop",
  "mode": "chat | run | farm-worker",
  "title": "first 80 chars of the first user message (redacted)",
  "branch": "feature/x",
  "worktree": "/abs/path",
  "farm": {"id": "…", "part": "core"},
  "touched": ["core/session.go", "core/trust.go"],
  "started": "2026-11-02T23:40:00+09:00",
  "heartbeat": "2026-11-02T23:52:10+09:00",
  "state": "active | idle | ended",
  "log": "~/.jas/sessions/…/events.jsonl"
}
```

- `touched` = repo-relative paths successfully written by `write`/`edit`
  (shell writes are not tracked in-process; noted in the record as
  `shell_used: true`). Capped (e.g. 200 paths, then `touched_truncated`).
- `title` passes through the same redactor as `events.jsonl`; no prompt text
  beyond that, no tool output.
- Heartbeat every 15 s while the process runs **(author's call)**; `state`
  is `idle` while waiting for user input, `ended` on clean exit (record kept
  60 s for late readers, then deleted).
- Worktrees: a session in a linked worktree registers in the **main**
  checkout's `.jas/sessions/` (via `git rev-parse --git-common-dir`), so
  sessions across worktrees of one repo see each other.

## Stale records

A record is stale if `heartbeat` is older than 60 s **and** (`host` is this
host and `pid` is not alive, or the heartbeat is older than 10 min). Any
session starting up deletes stale records. `jas sessions --prune` does it on
demand. Stale records are never shown as active peers.

## Peers (read-only)

- **Start-up note.** When a session starts and active peers exist, print one
  inline line per peer: `peer: chat "fix trust lookup" on main, touched 3
  files (core/trust.go …), 2m ago`. Also added to the system prompt as a
  short fenced, informational block (peers' titles are user-written but from
  another session, so they are labelled as context, not instructions).
- **`peers` tool** (optional, read-only, on by default in chat and `jas run`;
  disable via config): returns active peers with branch, title, touched
  paths and state. Lets the model avoid files someone else is editing.
- `jas sessions` CLI: list records for the current repo (active, idle,
  stale).

## Overlap check before writes

Before `write`/`edit` on path P, the tool checks active peers' `touched`
(cheap: read the directory, small JSON files).

| Mode | Default | Option |
|---|---|---|
| chat (TTY) | **ask**: "session X (title) also edited P 3m ago — write anyway?" | `overlap: warn` / `off` |
| `jas run` (headless) | **warn**: tool result notes the overlap; write proceeds; event logged | `--on-overlap fail` → tool call denied (model sees `denied`), session continues |
| farm worker | n/a — farm ownership is disjoint by plan; overlaps are a validation bug and logged | — |

Overlap on the same file in a **different** worktree/branch is reported as
info only (it becomes a merge concern, not a clobber). **(author's call)**

## Security

- `.jas/sessions/**` is denied to file tools (added to the policy deny list
  next to `.jas/worktrees/**`), so a model cannot forge or delete peer
  records; only jas code writes them.
- Records hold paths, titles (redacted), branch and pid — no prompts, tool
  output or secrets.
- Untrusted projects still get records (they are jas-written metadata), but
  peers' titles are shown only as text, never as instructions.

## Events

New `events.jsonl` types (versioned per design §3, additive):

- `session_registered{record_path}`
- `peers_seen{peers:[{session_id, branch, state, touched_count}]}` (start-up)
- `overlap_detected{path, peer_session_id, peer_branch, action: asked|warned|denied|allowed}`

Replay is unaffected: these are informational, and the overlap decision is
recorded, not recomputed.

## Farm

Farm workers register with `mode: farm-worker` and `farm` set, so a human
chat in the main checkout sees which parts are editing what. The farm
reuses the `.jas/` git-exclude plumbing and stale-record cleanup.

## Rollout

1. Records + stale cleanup + `jas sessions` (no behaviour change).
2. Start-up note + `peers` tool.
3. Overlap check (chat ask, headless warn, `--on-overlap fail`).

Each step is small and independent of farm's prerequisites.
