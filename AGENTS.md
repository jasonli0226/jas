# Agent notes for jas

jas is a tiny, fast coding-agent harness in Go (CI-friendly, MIT, inline chat + `jas run`).

## Current focus

None set; pick from the open GitHub issues, highest label first. The inline
TUI UX polish plan
([`docs/superpowers/plans/2026-10-31-tui-ux-polish.md`](docs/superpowers/plans/2026-10-31-tui-ux-polish.md),
#60–#66) is done. Interactive mode spec:
`docs/superpowers/specs/2026-10-30-interactive-mode-design.md`.

## Constraints

- Stay **inline** (Bubble Tea `Println` scrollback). **No** alt-screen / fullscreen TUI, no theme arms race.
- Do **not** break `jas run`, `--json`, or exit codes; approval ladder and redaction unchanged.
- Farm (`docs/superpowers/specs/2026-11-02-farm-design.md`, draft) **waits for its prerequisites** (event-log spec, provider fidelity, `jas run` token/time caps, opt-in sandbox, supervisor-run success check + benches) and a farm-vs-`jas run` benchmark gate; do not start building it before those. Proposed, core loop (not farm): session awareness (`docs/superpowers/specs/2026-11-02-session-awareness.md`) and spec-driven flow / `jas spec` (`docs/superpowers/specs/2026-11-02-spec-driven-flow.md`). Farm interactive mode is inline only (in the farm spec); attach from another terminal, any dashboard and scenario pipelines wait for their own design.
- Never append to the session log synchronously from `Update`: the log
  (`core.NewUILog`) forwards every event to the chat's bridge, whose
  `tea.Program.Send` blocks on an unbuffered channel the event loop — busy
  inside `Update` — never reads. Log from a `tea.Cmd` instead (see
  `tui/commands.go` `setModel`).
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
