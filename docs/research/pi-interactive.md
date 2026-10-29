# Research: pi interactive mode, end to end → jas chat/REPL/TUI

Sources read on 2026-10-30. This note covers only the interactive side. For the broader feature
audit see [pi-mono-audit.md](pi-mono-audit.md), which already lists session tree, compaction,
steering queue, modes and TUI polish as adopt/skip items. They are not repeated here, only made
concrete. Abbreviations used in citations:

- **CA** = pi coding agent v1.4.0, shipped JS:
  `~/.pi/agent/install/releases/1.4.0/node_modules/@earendil-works/pi-coding-agent/dist/`
- **CAD** = the same package's `docs/*.md`
- **AC** = `@earendil-works/pi-agent-core` v1.4.0 `dist/` (low-level agent loop)
- **AI** = `@earendil-works/pi-ai` v1.4.0 `dist/` (provider layer)
- **TUI** = `@earendil-works/pi-tui` v1.4.0 `dist/` (all seven packages report `"version": "1.4.0"`)
- **USR** = the user's `~/.pi/agent/sessions/` (28 files). Only the **structure** was read: entry
  types and key names, aggregated. No content was read. `auth.json` was not opened.
- **XT** = `golang.org/x/term@v0.47.0/terminal.go` (module cache, the version in jas `go.mod`)
- **BT** = `github.com/charmbracelet/bubbletea@v1.3.6` (module cache; not a jas dependency)
- **jas** = this repo at `cbae13b`

The GitHub `.ts` sources were not read. Line numbers refer to the shipped `.js`, which is
compiled but readable (comments kept).

Legend: ✅ verified in source/docs · ⚠️ docs and code disagree, or a hazard found in jas · ❓ unverified

---

## 1. Session model

### Layering: who holds multi-turn state

Three layers, each wrapping the one below it:

| Layer | Class | Holds | Cite |
|---|---|---|---|
| Agent core | `Agent` (AC) | in-memory transcript `state.messages`, steering/follow-up queues, abort controller | AC `agent.js:93-147`, `:300-305` |
| Session | `AgentSession` (CA) | an `Agent` + `SessionManager` (file) + settings, compaction, model, extensions | CA `core/agent-session.js:82` |
| UI/mode | `InteractiveMode`, `runPrintMode`, `runRpcMode` | rendering + input only | CA `main.js:775-820` |

- ✅ The transcript lives in memory in `Agent._state.messages`. Every run gets a **snapshot copy**
  (`messages.slice()`) as its context (AC `agent.js:300-305`).
- ✅ Persistence is a side effect of events. On every `message_end`, `AgentSession` appends the
  message to the session file (CA `agent-session.js:754-768`). So the agent loop never touches disk.
- ✅ Going the other way (resume, tree navigation), the session file is projected back into
  `agent.state.messages` (`_refreshFinalizedContext`, CA `agent-session.js:570-577`).

### How the loop runs per user message

1. ✅ The interactive main loop is `while (true) { input = await getUserInput(); await session.prompt(input) }`
   (CA `modes/interactive/interactive-mode.js:914-923`).
2. ✅ `AgentSession.prompt` does these steps in order. First it runs an extension `/command` if one
   matches. Next come the input handlers, then `/skill:name` and prompt-template expansion. If a run is
   already streaming, the message is queued as steer or follow-up. Otherwise it checks auth and calls
   `_startRun` (CA `agent-session.js:1631-1694`).
3. ✅ `_startRun` calls `agent.prompt(messages)`, then loops `agent.continue()` while post-run work
   asks for it, for example a retry or overflow compaction (CA `agent-session.js:1423-1470`).
4. ✅ `Agent.prompt` throws if a run is already active: "Use steer() or followUp()" (AC
   `agent.js:246-252`). There is exactly **one run at a time per session**.
5. ✅ `runAgentLoop` → `runLoop`: an inner loop over tool calls and steering, inside an outer loop
   over follow-ups (AC `agent-loop.js:84-213`). See §3.

### On-disk format

- ✅ Location: `~/.pi/agent/sessions/--<cwd with / → ->--/<timestamp>_<uuid>.jsonl` (CAD
  `session-format.md:8-14`). USR matches this. The session id is a UUIDv7 (CA `core/session-manager.js:12-14`).
- ✅ The first line is a header `{"type":"session","version":3,"id","timestamp","cwd"}`, plus
  `parentSession` for forks and clones. The header is not part of the tree (CAD `session-format.md:64-76`).
- ✅ Every other line is an entry `{type, id, parentId, timestamp, ...}`. `id` is 8 hex chars,
  collision-checked against the file, with a UUID fallback (CAD `session-format.md:49-60`; CA
  `session-manager.js:21-29`).
- ✅ Entry types: `message` (wrapping user/assistant/toolResult/system/bashExecution/custom roles),
  `model_change`, `thinking_level_change`, `usage`, `compaction`, `context_edit`, `branch_summary`,
  `custom` (not sent to the LLM), `custom_message` (sent to the LLM), `label`, `session_info`
  (CAD `session-format.md:62-207`).
- ✅ Messages are stored **whole**: assistant `content[]` including tool calls, `usage`,
  `stopReason`, `model`, `thinkingLevel`. The system prompt and tool loadout are stored as a
  `system` message, with later patches as diffs (`sections`, `toolsAdded`/`toolsRemoved`) (CAD
  `session-format.md:78-95`). USR: a typical file is `session` → `model_change` →
  `thinking_level_change` → `message/system` → `message/user` → `message/assistant` ×N with
  `message/toolResult` between. Assistant keys: `api, content, durationMs, model, provider,
  rawStopReason, responseId, role, stopReason, thinkingLevel, timestamp, usage`.
- ✅ Writes are **lazy and unsynced**. The file is not created until the first user or assistant
  message exists. It is then written in full with `wx` (exclusive create). After that each entry is an
  `appendFileSync` with no fsync (CA `session-manager.js:794-814`; no `fsyncSync` among the `fs`
  imports at `:3`). jas is stricter: write-ahead fsync on mutating tools (design.md:200-203).

### Tree, leaf, branching

- ✅ The "leaf" is the current position. Every append becomes a child of the leaf and then becomes
  the new leaf (CA `session-manager.js:815-820`).
- ✅ On load, **the leaf is the last entry in the file** (`_buildIndex`, CA `session-manager.js:732-753`).
  `branch(id)` only moves the in-memory pointer (`:1161-1166`), so a `/tree` jump that is followed by
  no new message is not remembered after restart. This is inferred from the code and was not tested.
- ✅ Context building walks from the leaf up to the root. The latest `compaction` on that path
  replaces everything before `firstKeptEntryId`, and `context_edit`s are applied (CAD
  `session-format.md:224-248`).
- ✅ Branch commands (CAD `sessions.md:20-32`; `slash-commands.md:27-29`):
  - `/tree`: navigate inside the same file. Choosing a user message puts its text back in the
    editor so you can edit it and branch. Choosing any other entry continues from that entry. pi
    offers to write a `branch_summary` of the path you leave (CA `session-manager.js:1180-1199`).
  - `/fork`: new session file from an earlier user message.
  - `/clone`: new file holding the active branch. `createBranchedSession` writes the
    root→leaf path plus labels, with `parentSession` in the header (CA `session-manager.js:1205-1300`).
- ✅ USR: 0 of 28 files contain a branch point (no `parentId` with more than one child), and none
  contain a `compaction` entry. This user has not used `/tree` or compaction in practice. That is
  a data point for jas priorities, not a rule.

### Resume and continue

- ✅ `-c/--continue`: the most recent session for the cwd. `findMostRecentSession` sorts `.jsonl`
  files by **mtime** and checks that the header `cwd` matches (CA `session-manager.js:445-465`,
  `:1354-1362`).
- ✅ `-r/--resume` and `/resume` open a picker with search, rename (Ctrl+R) and delete (Ctrl+D, uses
  `trash` when available) (CAD `cli.md:86-101`; `sessions.md:18`; `session-format.md:20`;
  `keybindings.md:133-146`).
- ✅ Other flags: `--session <path|id>`, `--session-id`, `--fork <path|id>`, `--no-session` (in memory),
  `--session-dir`, `-n/--name` (CAD `cli.md:86-107`). `main.js` picks one `SessionManager`
  factory per flag: `open` / `forkFrom` / `inMemory` / `continueRecent` / `create` (CA `main.js:267-351`).

## 2. Input and editor

- ✅ Enter submits. Shift+Enter or Ctrl+J inserts a newline. Ctrl+G opens `$VISUAL`/`$EDITOR` (CAD
  `usage.md:13`; `keybindings.md:88-89`, `:128`). The editor is a full multi-line, Emacs-style
  component: word motion, kill ring with `ctrl+y`/`alt+y`, undo (CAD `keybindings.md:49-82`; TUI
  `components/editor.js`, 2043 lines).
- ✅ History: in memory, newest first, deduplicated against the previous entry, **capped at 100**
  (TUI `editor.js:240-243`, `:308-319`). Up/Down browse history only when the cursor is on the
  top/bottom line (CAD `keybindings.md:53-54`). On resume, history is rebuilt from the session's user
  messages (`populateHistory`, CA `interactive-mode.js:3171-3173`). ❓ There is no separate
  history file; none was found.
- ✅ Paste: bracketed paste is enabled (`\x1b[?2004h`, TUI `terminal.js:127-128`). A paste of
  **more than 10 lines or more than 1000 chars** collapses to an atomic marker
  `[paste #N +L lines]`. The marker is expanded on submit through `getExpandedText()` (TUI `editor.js:11-14`,
  `:1086-1094`, `:912`).
- ✅ `@` triggers a fuzzy file search using `fd`, which respects `.gitignore` (TUI `autocomplete.js:120`,
  `:609`; pi auto-downloads `fd` on startup, CA `interactive-mode.js:774-778`). The completion inserts
  the **path text** (TUI `autocomplete.js:312-327`). ❓ No code was found that expands an interactive
  `@path` into file content. Only the CLI's `@file` arguments are read and inlined
  (CA `cli/file-processor.js:12`). The model presumably reads the file with its own tool. Tab
  completes paths (CAD `usage.md:18`).
- ✅ Images: Ctrl+V (Alt+V on Windows) pastes images or files from the clipboard. Images are attached
  through temp files (CAD `keybindings.md:129`; CA `interactive-mode.js:2436-2440`).
- ✅ `!cmd` runs a shell command and adds its output to the context. `!!cmd` runs it without
  sending output to the model. The editor border changes color in "bash mode" (CAD `usage.md:70-76`;
  CA `interactive-mode.js:2428-2434`, `:2648-2661`). Only one runs at a time, and Esc cancels it
  (`:2381-2382`). Output is stored as a `bashExecution` message with `excludeFromContext`
  (CA `agent-session.js:3154-3194`). It is queued and flushed after the run if the agent is busy
  (`_flushPendingBashMessages`, `:1436-1442`).
- ✅ Empty-editor Ctrl+D exits. Ctrl+Z suspends (CAD `keybindings.md:126-127`).

## 3. While the agent is running

### Interrupt semantics

| Key | Idle | Streaming | Cite |
|---|---|---|---|
| **Esc** | in bash mode: clear it; empty editor + double-Esc (<500 ms): `/tree` (setting `doubleEscapeAction`: tree/fork/none) | **abort the run**, and first move all queued messages back into the editor | CA `interactive-mode.js:2377-2407`, `:3852-3871`; CAD `settings.md:34` |
| **Ctrl+C** | clear editor; second press within 500 ms exits | same: it **never aborts the run** | CA `interactive-mode.js:3424-3433`; CAD `keybindings.md:125` |

- ✅ The terminal runs in raw mode (TUI `terminal.js:122-123`), so Ctrl+C reaches the app as a key and
  is not delivered as SIGINT.
- ✅ `AgentSession.abort()` aborts in-flight retries, compaction and branch summary, then aborts the agent and
  waits for idle (CA `agent-session.js:1971-1982`).

### Steering vs follow-up queue

- ✅ Enter while streaming = **steer**. Alt+Enter (Ctrl+Q on Windows) = **follow-up**. Alt+Up
  pops all queued messages back into the editor (CAD `usage.md:31-40`; CA `interactive-mode.js:2681-2688`,
  `:3614-3651`). While idle, Alt+Enter behaves like Enter (`:3637-3641`).
- ✅ Delivery points in the loop (AC `agent-loop.js`):
  - Steering is polled once at run start (`:90`), again after preparation such as between-turn
    compaction (`:112-117`), and after each turn's tool results are appended (`:191`). Queued
    messages are pushed into the context **before** the next LLM call (`:121-127`).
  - Follow-ups are polled only when the inner loop would stop: no tool calls and no steering
    (`:196-202`). They restart the inner loop.
- ✅ Each queue has a mode, `one-at-a-time` (the default) or `all` (AC `agent.js:60-86`, `:141-142`;
  CAD `settings.md:31-32`).
- ✅ Steering does **not** interrupt a running tool. The message waits for the current assistant turn
  *and its tool calls* (CAD `rpc-commands.md:26`; `usage.md:40`).
- ✅ Input typed during compaction is held in a separate queue and replayed afterward
  (CA `interactive-mode.js:2665-2676`).

### Partial tool calls on abort

- ✅ Tools receive the `AbortSignal`. Sequential execution stops after the current tool. In parallel
  execution, which is the **default** (AC `agent.js:147`), calls not yet started get a
  `"Operation aborted"` error result (AC `agent-loop.js:380-470`).
- ✅ An aborted assistant **stream** ends the run with `stopReason:"aborted"` and runs no tools
  (AC `agent-loop.js:148-158`). The partial message **is persisted** through `message_end`, and the UI marks
  pending tool rows "Operation aborted" (CA `interactive-mode.js:2836-2856`).
- ✅ When the next request is built, pi-ai **drops errored or aborted assistant messages entirely** and
  inserts synthetic `"No result provided"` tool results for any orphaned tool call (AI
  `api/transform-messages.js:147-180`). The repair happens at replay time, and the raw transcript keeps
  everything.
- ✅ An output that stops on `"length"` with tool calls fails all those calls instead of running
  arguments that may be truncated (AC `agent-loop.js:163-170`).

## 4. Rendering

- ✅ Architecture: a retained component tree. Each component returns `string[]` lines for a width
  and caches until `invalidate()` (CAD `tui.md:22`). `requestRender()` is coalesced on `nextTick`
  (TUI `tui.js:658-667`; CAD `tui.md:28`). The file header says "Minimal TUI implementation with
  differential rendering" (TUI `tui.js:1-3`).
- ✅ Two screen modes (CAD `usage.md:86`; `settings.md:97`; CLI `--tui-mode`):
  - **fullscreen** (the default): alternate screen `?1049h`, mouse, transcript scroll/search,
    editor and footer pinned (TUI `tui-alt-screen.js:12-22`; keybindings `tui.altScreen.*`, CAD
    `keybindings.md:99-118`).
  - **regular**: normal scrollback, and the terminal owns the mouse (CAD `tui.md:62`). The diff renderer
    compares new lines with `previousLines` and repaints from the first changed line. It does a full redraw
    on the first render, on width changes, or when the change lies above the old viewport. All output is wrapped in
    synchronized-output `?2026h/l` (TUI `tui-main-screen.js:212-300`, `:395-417`).
- ✅ Keyboard: Kitty keyboard protocol with a `modifyOtherKeys` fallback, needed to tell
  Shift+Enter apart from Enter (TUI `terminal.js:79-80`, `:139`).
- ✅ Streaming: `message_start` creates an `AssistantMessageComponent`, and each `message_update`
  re-renders it as Markdown. Tool-call rows are created **as soon as the tool-call block streams in**,
  and their args update live (CA `interactive-mode.js:2796-2829`).
- ✅ Thinking blocks can be hidden (`hideThinkingBlock`) and toggled with Ctrl+T (CAD `usage.md:23`;
  CA `interactive-mode.js:2797`, `:2420`).
- ✅ Tool rows: collapsed by default to **10 preview lines**, with Ctrl+O toggling all rows (CA
  `components/tool-execution.js:6`, `:104`; CAD `keybindings.md:164`). Tools can register custom
  renderers (`tool-execution.js:243`). Separately, the **model-facing** output is truncated at
  2000 lines / 50 KB (CA `core/tools/truncate.js:10-11`).
- ✅ Footer: cwd (git branch) • session name; `↑in ↓out R<cacheRead> W<cacheWrite> CH<hit%>`;
  `$cost`; `ctx%/window (auto)`, colored yellow above 70% and red above 90%; and on the right
  `model • thinking-level` (CA `components/footer.js:135-215`). The editor border color shows the
  thinking level (CAD `usage.md:25`).

## 5. Slash commands (1.4.0)

Built-in table (CA `core/slash-commands.js:2-27`; CAD `slash-commands.md:11-52`). Dispatch is a
chain of `if (text === "/x")` checks in the submit handler (CA `interactive-mode.js:2502-2645`).

| Command | Purpose |
|---|---|
| `/settings` | settings menu |
| `/model [provider/model]` | model selector, or set directly |
| `/thinking [level]` | set the thinking level |
| `/scoped-models` | choose which models Ctrl+P cycles through |
| `/login [provider]`, `/logout` | manage provider auth |
| `/new` | start a new session (there is no `/clear`; the handler is named `handleClearCommand`, CA `interactive-mode.js:2425`) |
| `/resume` | session picker |
| `/name [name]` | set or show the session display name (`session_info` entry) |
| `/session` | file, id, message count, tokens, cost |
| `/tree` | navigate or branch inside the session |
| `/fork` | new session from an earlier user message |
| `/clone` | copy the active branch to a new session |
| `/compact [instructions]` | manual compaction with an optional focus |
| `/import <path>` | import and resume a JSONL session |
| `/copy` | copy the last assistant message (also Ctrl+X) |
| `/export [path]` | HTML (default) or `.jsonl` |
| `/share` | upload to Radius or a private gist |
| `/bug [desc]` | prepare a bug report |
| `/trust` | save a project trust decision |
| `/reload` | reload keybindings, extensions, skills, templates, themes, context files |
| `/hotkeys` | show active shortcuts |
| `/changelog` | changelog |
| `/quit` | quit |

- ✅ Not in the table: `/debug`, which dumps rendered lines and messages to `pi-debug.log`
  (CAD `usage.md:90-92`; CA `:2622`), two easter eggs (`:2627-2636`), and `/llama`, which is
  registered by the bundled llama extension (CA `extensions/llama/index.js:163`), not built in.
- ✅ Resource commands: `/skill:name [args]` inlines a `<skill name location>` block followed by the
  args (CA `agent-session.js:1727-1750`; setting `enableSkillCommands`, CAD `settings.md:159`).
  Prompt templates appear under their own names. Extensions call `pi.registerCommand(name,
  {description, handler})`; their handlers run even while the agent is streaming (CAD
  `extensions.md:23-29`; CA `agent-session.js:1638-1647`).
- ✅ An unknown `/foo` is **sent to the model as plain text**. An unknown skill passes through
  unchanged (CA `agent-session.js:1735`).
- ✅ Typing `/` opens a fuzzy menu built from built-ins, templates, extension commands and skills
  (CA `interactive-mode.js:543`).

## 6. Compaction

- ✅ Trigger: `contextTokens > contextWindow − reserveTokens`, with `reserveTokens = 16384` and
  `keepRecentTokens = 20000` by default (CA `core/compaction/compaction.js:53-56`, `:173-176`; CAD
  `compaction.md:29-35`, `:417-437`). Per-model overrides go under `compaction.modelOverrides`
  (`compaction.md:439-461`).
- ✅ When the check runs: between turns, after tool results and before the next LLM call
  (`prepareNextTurn`); before a new user prompt; and as a single compact-and-retry on a provider
  overflow error or an early `stopReason:"length"` (CAD `compaction.md:37-39`).
- ✅ Token count: uses the last provider-reported usage plus a chars/4 estimate for messages after it
  (CA `compaction.js:108-127`, `:198`). This is the same idea as jas `Budget.Count` (core/budget.go:55-66).
- ✅ The cut walks backward until `keepRecentTokens` is reached. It never cuts at a tool result. If one
  user-message span alone exceeds the budget, its prefix is summarized separately and the two
  summaries are merged (CAD `compaction.md:43-138`).
- ✅ Summary prompt:
  - System prompt: "You are a context summarization assistant… Do NOT continue the
    conversation… ONLY output the structured summary" (CA `compaction/utils.js:145-147`).
  - User message: `<conversation>…</conversation>` plus `<previous-summary>` when one exists,
    then a fixed-format instruction (CA `compaction.js:379`, `:447`, `:515-545`). Sections: Goal /
    Constraints & Preferences / Progress (Done, In Progress, Blocked) / Key Decisions / Next Steps /
    Critical Context, plus `<read-files>` and `<modified-files>` lists (CAD `compaction.md:234-274`).
  - The conversation is serialized as text (`[User]: …`, `[Assistant tool calls]: read(path=…)`,
    `[Tool result]: …`) so the model does not continue it. Thinking is dropped. Tool results are
    cut to 2000 chars (CAD `compaction.md:276-291`).
  - A summary that tries to call a tool is rejected (CA `compaction.js:537-538`).
  - Summarization uses the **current session model** (`this.model`, CA `agent-session.js:2237`).
    Manual `/compact` first **aborts** any running turn (`:2231`).
- ✅ Storage: an appended `compaction` entry `{summary, firstKeptEntryId, tokensBefore, usage?,
  details{readFiles, modifiedFiles}, systemMessage?}`. The originals stay in the file. The LLM sees
  system + summary + messages from `firstKeptEntryId` onward (CAD `session-format.md:123-137`;
  `compaction.md:51-81`). Repeated compactions summarize again from the previous kept boundary
  (`compaction.md:83`).
- ✅ Extensions can cancel compaction or supply their own summary through `session_before_compact`
  (CAD `compaction.md:297-332`).

## 7. Models, thinking, keybindings

- ✅ Mid-session model switch: `/model` or Ctrl+L opens the selector. Ctrl+P / Shift+Ctrl+P cycles the
  scoped models. Each switch appends a `model_change` entry (CA `agent-session.js:2008-2015`,
  `:2046-2067`; CAD `session-format.md:97-103`; `keybindings.md:152-154`).
- ✅ The loop can swap model and thinking level **between turns** of one run (`prepareNextTurn` /
  `prepareRequest` return `model`/`thinkingLevel`; AC `agent-loop.js:98-111`, `:128-141`).
  Every assistant message records the model that produced it (CAD `session-format.md:95`).
- ✅ Thinking: `/thinking` or Shift+Tab cycles off/minimal/low/medium/high/xhigh/max, clamped to
  what the model supports. A change appends `thinking_level_change` (CAD `cli.md:70`;
  `keybindings.md:156`; CA `agent-session.js:2114-2125`). Ctrl+S in the selectors saves the
  default to settings (`keybindings.md:155-157`).
- ✅ Keybindings: about 80 named actions (`tui.editor.*`, `tui.input.*`, `tui.altScreen.*`,
  `app.*`). Override them in `~/.pi/agent/keybindings.json` (one key or a list per action; `[]`
  disables it), apply with `/reload`, and inspect with `/hotkeys` (CAD `keybindings.md:1-195`).

## 8. Permissions and approval

- ✅ **pi does not ask before tool calls.** "Pi does not ask before every tool call… use a
  sandbox" (CAD `usage.md:27`; `security.md:3-7`). The safety story is OS or container isolation
  (`security.md:9-23`).
- ✅ The only built-in interactive prompt is **project trust**, a startup gate for `.pi/*`
  resources. pi's `--approve/--no-approve` flags refer to *project trust*, not to tool calls (CAD
  `security.md:27-82`).
- ✅ Tool approval exists only as an **extension**. A `tool_call` handler can mutate the call or return
  `{block, reason}`, and the docs show a Codex-style `ctx.ui.confirm` gate keyed on MCP annotations
  (CAD `extensions.md:278-292`; `examples/extensions/confirm-destructive.ts`, `permission-gate.ts`).
  A failing handler blocks the call (fail-closed) (`extensions.md:375`). The core hook is
  `agent.beforeToolCall → _beforeToolCall` (CA `agent-session.js:322-333`; AC `agent-loop.js:499-520`).
- Difference from jas: jas has the gate **in core**, as the `Gate` interface plus the `Approver` ladder
  never/shell/mutating, with a no-TTY result of `unavailable` → exit 5 (jas `core/tools.go:90-92`;
  `core/approve.go:52-129`; design.md §3.1). A blocked call becomes an error tool result in pi. In jas
  it gets `status: denied`. These are equivalent from the model's point of view.

## 9. Other modes and how they share the core

- ✅ "All interfaces use the same agent and session mechanisms" (CAD `how-pi-works.md:39`).
  `main.js` builds one `AgentSessionRuntime` and passes it to `runRpcMode`, `InteractiveMode` or
  `runPrintMode` (CA `main.js:701`, `:775-820`).
- ✅ Print mode (`-p`) runs the prompts and writes the final text. `--mode json` uses the same path but
  `session.subscribe` writes each event as JSONL (CA `modes/print-mode.js:84-86`). If stdin or stdout is not
  a TTY, the default is print (CAD `cli.md:30-48`).
- ✅ RPC (`--mode rpc`): long-lived, JSONL commands on stdin, responses plus events on stdout, with an optional
  `id` for correlation. Commands include `prompt` (with `streamingBehavior: steer|followUp`),
  `steer`, `follow_up`, `abort`, `clear_queue`, `new_session`, `get_state`, `set_model`,
  `cycle_model`, `set_thinking_level`, and `bash` (CAD `rpc.md:1-60`; `rpc-commands.md:7-331`).
  The docs' recipe for Esc is "send `clear_queue` before `abort`, restore text in the client
  editor" (`rpc-commands.md:128`). That is exactly what the TUI does internally.
- ✅ Extension UI (`ctx.ui.confirm/select/input`) is forwarded over RPC as `extension_ui_request` /
  `extension_ui_response` (CAD `rpc.md:31`, `:48`). An approval prompt therefore works in RPC clients too.

## 10. Implications for jas

### What jas already has that maps directly

| pi piece | jas today | Gap |
|---|---|---|
| `Agent` + `runLoop` | `core.Run` → `runState.loop/turn` (jas `core/loop.go:69-158`) | takes one `Prompt` and builds `messages` from scratch (`:76-78`); returns `Summary.Messages` (`:48`, `:252`), so history already comes out but cannot go back in |
| `message_end` → `SessionManager.append` | `EventLog` single writer, fsync discipline (jas `core/events.go:103-113`) | the log is **not replayable into messages** (see ⚠️ below) |
| `parentId` tree | `parent_id` field reserved, always null (jas `core/events.go:43`; design.md:192-193) | needs per-entry parent = previous entry on the active branch |
| replay-time orphan repair (AI `transform-messages.js`) | write-time synthesis of `cancelled` results (jas `core/loop.go:176-200`; design.md:223-225) | fine as is; keep it |
| `shouldCompact` + chars/4 | `Budget` with `Observe`/`Count`/`WouldExceed`/`MaybeElide` (jas `core/budget.go:34-80`) | add `reserve`-based trigger + summarize; reset observation after compaction |
| `beforeToolCall` gate | `Gate`/`Approver` in core | the Approver must stop reading stdin itself (see ⚠️) |
| `session.subscribe` → modes | `UI{Text, Event}` + `NewUILog` tee (jas `core/ui.go:15-18`; `cmd/jas/run.go:111`) | good seam; a REPL UI is another `UI` implementation |
| print / json modes | `jas run` / `jas run --json` | done |

### ⚠️ Hazards found in jas that block a REPL

1. ⚠️ **events.jsonl cannot rebuild the conversation.** The user prompt is never logged: `Run` puts it
   straight into `messages` (`core/loop.go:76-78`), and `session_started` carries only
   metadata (`:79-82`). `assistant_message` logs only `text` + `final`, without tool calls
   (`:213-215`). Tool calls appear only as `tool_started` (`core/exec.go:231-244`), and denied or
   settled calls may skip that path. The system prompt is not logged either. Resume needs, at minimum,
   a `user_message` event and the full assistant content (tool_calls). Adding types or fields is
   backward compatible per design.md:195-199. pi stores whole messages (§1).
2. ⚠️ **Event IDs restart on reopen.** `fileLog.seq` starts at 0 and IDs are `e%06d`
   (`core/events.go:148`, `:166-167`), while the file opens `O_APPEND`. Appending to an existing session
   would produce duplicate IDs. This breaks any `parent_id` tree. Fix: seed `seq` from the
   last line on open, or switch to random short IDs as pi does (`session-manager.js:21-29`).
3. ⚠️ **`session_started`/`session_ended` are per `Run`** (`core/loop.go:82`, `:251`). Several Runs
   on one log would interleave many start/end pairs. Decide between per-turn `run_started/run_ended`
   events with session events written once, and one Run per session.
4. ⚠️ **The Approver owns stdin and leaks a reader on cancel.** It wraps `In` in its own
   `bufio.Reader` (`core/approve.go:99-104`). The comment admits that after a cancel the goroutine "stays
   blocked on In… acceptable because a cancelled run ends the process" (`:90-93`). In a REPL
   the process continues, so that stale goroutine would **swallow the user's next line**. In a
   REPL, approval has to go through whatever owns the terminal input, the line editor or the TUI. The
   `Gate` interface can stay, but the implementation must change.
5. ⚠️ **Cancellation is process-wide.** `signal.NotifyContext(…, os.Interrupt…)` in `main`
   (`cmd/jas/main.go:27`) means Ctrl-C cancels the one root ctx. A REPL needs a per-turn ctx, with
   SIGINT (cooked mode) or Esc/Ctrl-C keys (raw mode) cancelling only the current turn.
6. ⚠️ **Exit codes are per run.** Turn cap, context full and approval-needed come back as an `*ExitError` from
   `Run` (`core/loop.go:228-265`). A REPL should print these as per-turn statuses and keep going.
7. ❓ After a stream cancel with **no** tool calls, jas drops the partial assistant text
   (`core/loop.go:178-185` appends only when `len(uses) > 0`). The next user message would then
   follow the previous user message directly. OpenAI-compatible servers usually accept
   consecutive user turns, but this was not verified against the GLM endpoints. pi drops aborted
   assistant messages the same way (AI `transform-messages.js:172-180`), so this matches pi.

### Recommended slice order (recommendations)

Each slice ships on its own and is useful without the next.

1. **core `Session` API** (no UI change). `core.Session{messages, log, budget, cfg}` with
   `Send(ctx, prompt) (TurnSummary, error)`. It runs the existing `loop/turn` over the held
   `messages`. Refactor `Run` into `NewSession` + one `Send`, so `jas run` stays a one-shot.
   Fix hazards 1-3 here: log `user_message` and the full assistant content, seed `seq`, and either fill
   `parent_id` with the previous event id on the trunk (a linear chain is a valid tree) or leave it
   null until slice 4. Add `jas run --continue` / `--session <id>` replay *only if* the replay is
   cheap after hazard 1. **Copy from pi:** state in memory, persistence as a side effect of
   events, one run at a time per session (AC `agent.js:246-252`). **Skip:** the pi
   `system`-message diffs and `context_edit`.
2. **Line REPL `jas chat`** on `golang.org/x/term`, which is already a dependency. Prompt → `Send` →
   stream through the existing `Printer`. Per-turn ctx; SIGINT cancels the turn; Ctrl-D exits.
   The approval prompt goes through the same `term.Terminal` (fixes hazard 4). A minimal set of
   commands: `/new`, `/quit`, `/model <id>`, `/compact` (stub until slice 3). **Copy:** "unknown
   `/foo` is sent as text" and `!cmd`. **Skip:** queueing (no input while a turn streams),
   `@`-autocomplete, image paste.
3. **Compaction**: pi's design as is. Trigger `window − reserve` (16384), keep about 20k
   recent tokens, cut only at user/assistant boundaries, use the structured summary prompt with
   `<previous-summary>` iteration and the 2000-char tool-result clip. Store it as a `compaction` event
   `{summary, first_kept_id, tokens_before, usage}` and keep the originals. Elision stays as the cheap
   first stage. **Decision needed:** whether the summary runs on the session model (pi, CA
   `agent-session.js:2237`) or on a cheap model (jas design.md:388-390).
4. **Resume and tree**: `--continue`, which picks the newest session by mtime whose `session_started.root`
   matches the cwd, the way pi matches `cwd` (CA `session-manager.js:445-465`). Add `--resume` as a plain numbered
   list in REPL mode, then `/fork`, which copies the path to a new file with `parent_session`. **Copy:**
   the leaf = last-entry rule, so no separate pointer state. **Defer:** `/tree` with branch summaries.
   USR shows no branch use, so the value is unproven for this user.
5. **Full TUI** (Bubble Tea): multi-line editor, a streaming region, steer (Enter) and follow-up
   (Alt+Enter) queues with "Esc = clear queue to editor + abort" (copy CAD
   `rpc-commands.md:128` exactly), collapsible tool rows (10-line preview), and a footer with
   `↑in ↓out ctx%/window model`. The queue lives in `core.Session`, not in the UI, so the RPC and
   attach transports (design.md:380-385, `:395-398`) get it free. The loop polls steering after the tool
   results are appended and follow-ups when it would stop (AC `agent-loop.js:191`, `:196-202`).

### Input/TUI library options (Go)

| Option | Status | Fits | Limits |
|---|---|---|---|
| `golang.org/x/term` `Terminal` | ✅ already a dep (v0.47.0) | slice 2 | ✅ single-line only; Enter submits even inside a bracketed paste, so **a multi-line paste becomes several submissions** (XT `terminal.go:512-515`, `:582`); Ctrl-C → `io.EOF` like Ctrl-D (`:827-834`); has `History`, `AutoCompleteCallback`, `SetBracketedPasteMode`/`ErrPasteIndicator` (`:41-120`, `:986-999`); no Esc key event. Workaround: treat `ErrPasteIndicator` lines as "accumulate", or use a `"""` block / trailing `\` for multi-line |
| Bubble Tea v1 + bubbles `textarea` | ✅ v1.3.6 in the module cache (BT), not a jas dep | slice 5 | inline by default; `WithAltScreen()` is optional (BT `options.go:109`); `tea.Println` prints above the live view into the real scrollback (BT `standard_renderer.go:768`), which is pi's **regular** mode for free; `Program.Send` injects agent events from the loop goroutine (`tea.go:737`); `ReleaseTerminal`/`RestoreTerminal` handles `$EDITOR`/`!cmd` (`tea.go:827-848`). Cost: about 10+ transitive deps (lipgloss, x/ansi, …) ❓ count not measured |
| Bubble Tea v2 (`charm.land/…`) | ❓ only `charm.land/lipgloss/v2` is in the cache; v2 API and stability not checked | slice 5 alt | v2 reportedly adds better key disambiguation (Kitty protocol), which pi needs for Shift+Enter (TUI `terminal.js:139`) ❓ |
| readline forks (chzyer/ergochat/reeflective) | ❓ not checked | slice 2 alt | more Emacs keys than x/term; another dependency for something slice 5 replaces anyway |
| hand-rolled pi-tui port | — | — | pi-tui is about 16k lines of JS (TUI `dist/*.js`); not worth it for jas |

Recommendation: use x/term for slice 2 (no new dependency, proves the Session API), then Bubble Tea v1 in
**inline** mode for slice 5 (already named in design.md:90, `:387`). Choose alt-screen
later only if a transcript search is wanted. Bubble Tea v1 and v2 should be compared at slice 5,
not now.

### Deliberately not copying (recommendations)

- Extensions, custom renderers, themes, `/share`, `/bug`, `/import`, cache warming, virtual models
  in the REPL. These are covered or rejected in pi-mono-audit.md.
- The pi default of **no tool approval**. jas keeps its ladder. Only the plumbing changes (hazard 4).
- Unsynced session writes. jas's fsync discipline stays.
- Raw-mode Esc/Ctrl-C semantics before slice 5. In cooked mode, Ctrl-C = cancel the turn is the honest
  equivalent.

### Decisions the user needs to make

1. **One file or two?** Extend `events.jsonl` until it is replayable (one audit plus session file;
   recommended, and matches design.md:184-186), or add a separate pi-style message log next to it.
2. **Session lifecycle events:** per-turn `run_*` events inside one `session_started/ended`, or one
   `Run` per process.
3. **Interrupt keys:** stay with "Ctrl-C cancels the turn, Ctrl-D exits" (cooked mode, slice 2), or
   adopt pi's "Esc aborts, Ctrl-C clears, double Ctrl-C exits" once raw mode arrives in slice 5.
4. **Compaction model:** the session model (pi) or a cheap model (jas design).
5. **TUI style:** inline/scrollback (recommended) or fullscreen like pi's default.
6. **Enter while streaming:** steer (pi) or follow-up. pi's default is steer.
