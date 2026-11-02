# Agent notes for jas

jas is a tiny, fast coding-agent harness in Go (CI-friendly, MIT, inline chat + `jas run`).

## Current focus

Farm MVP merged (see README's experimental farm section): `farm plan`/`run`/`<task>`/
`status`/`clean`/`vars`, `coder`/`tester` roles, typed board, supervisor-run
`check`, integrator, exit codes 6/7/8, `--json`, `manifest.json`, budgets. Spec:
`docs/superpowers/specs/2026-11-02-farm-design.md` (MVP cut section); plan:
`docs/superpowers/plans/2026-11-02-farm-mvp.md`; live acceptance results in the
PR. Next, in spec order: the farm-vs-`jas run` benchmark gate (README drops the
experimental note when it lands), then `farm resume` + take-over,
`researcher`/`reviewer` roles, `--spec` planning, `farm init`, interactive
`/farm`.

## Constraints

- Stay **inline** (Bubble Tea `Println` scrollback). **No** alt-screen / fullscreen TUI, no theme arms race.
- Do **not** break `jas run`, `--json`, or exit codes; approval ladder and redaction unchanged.
- Farm (`docs/superpowers/specs/2026-11-02-farm-design.md`): MVP shipped on `feat/farm-mvp` (see Current focus); experimental until the farm-vs-`jas run` benchmark gate lands. Post-MVP order in the spec's MVP cut. Proposed, core loop (not farm): session awareness (`docs/superpowers/specs/2026-11-02-session-awareness.md`) and spec-driven flow / `jas spec` (`docs/superpowers/specs/2026-11-02-spec-driven-flow.md`). Farm interactive mode is inline only (in the farm spec); attach from another terminal, any dashboard and scenario pipelines wait for their own design.
- Never append to the session log synchronously from `Update`: the log
  (`core.NewUILog`) forwards every event to the chat's bridge, whose
  `tea.Program.Send` blocks on an unbuffered channel the event loop — busy
  inside `Update` — never reads. Log from a `tea.Cmd` instead (see
  `tui/commands.go` `setModel`).
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
- Docs follow features: every user-visible change (CLI subcommand/flag, chat
  command/key, config key, auth, approval level, deny floor, exit code, farm
  behavior) updates `README.md` and the built-in manual skill
  `cmd/jas/manual/jas/SKILL.md` in the same commit — the manual is embedded
  (`//go:embed`) and re-installed under `~/.config/jas/skills/` at every
  start, so stale text ships until the next binary.
