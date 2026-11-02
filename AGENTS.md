# Agent notes for jas

jas is a tiny, fast coding-agent harness in Go (CI-friendly, MIT, inline chat + `jas run`).

## Current focus

Farm MVP is implemented on `feat/farm-mvp` (worktree `jas-farm`), pending review
and merge: `farm plan`/`run`/`<task>`/`status`/`clean`/`vars`, `coder`/`tester`
roles, typed board, supervisor-run `check`, integrator, exit codes 6/7/8,
`--json`, `manifest.json`, budgets. Spec:
`docs/superpowers/specs/2026-11-02-farm-design.md` (MVP cut section); plan:
`docs/superpowers/plans/2026-11-02-farm-mvp.md`. README marks farm experimental
until the benchmark gate (farm vs `jas run`) lands. After merge, in spec order:
`farm resume` + take-over, `researcher`/`reviewer` roles, `--spec` planning,
`farm init`, interactive `/farm`.

## Constraints

- Stay **inline** (Bubble Tea `Println` scrollback). **No** alt-screen / fullscreen TUI, no theme arms race.
- Do **not** break `jas run`, `--json`, or exit codes; approval ladder and redaction unchanged.
- Farm (`docs/superpowers/specs/2026-11-02-farm-design.md`): MVP shipped on `feat/farm-mvp` (see Current focus); experimental until the farm-vs-`jas run` benchmark gate lands. Post-MVP order in the spec's MVP cut. Proposed, core loop (not farm): session awareness (`docs/superpowers/specs/2026-11-02-session-awareness.md`) and spec-driven flow / `jas spec` (`docs/superpowers/specs/2026-11-02-spec-driven-flow.md`). Farm interactive mode is inline only (in the farm spec); attach from another terminal, any dashboard and scenario pipelines wait for their own design.
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
