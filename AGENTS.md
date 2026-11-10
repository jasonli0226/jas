# Agent notes for jas

jas is a tiny, fast coding-agent harness in Go (CI-friendly, MIT, inline chat + `jas run`).

## Current focus

**Product framing (owner, 2026-11-03):** interactive **chat is a core feature**
(pair programming), alongside **init from instructions / future scenarios or
`jas spec`**, and **study→draft scenario** (`jas bootstrap`, design §8.3).
CI/`--json` and farm remain differentiators vs pi — a receipt and a narrow
split — not the only lead story. Docs: README pitch, `docs/use-cases.md`.

Farm MVP is merged (README experimental section): `farm plan`/`run`/`<task>`/
`status`/`clean`/`vars`, `coder`/`tester` roles, typed board, supervisor-run
`check`, integrator, exit codes 6/7/8, `--json`, `manifest.json`, budgets.
Spec: `docs/superpowers/specs/2026-11-02-farm-design.md`; plan:
`docs/superpowers/plans/2026-11-02-farm-mvp.md`.
Farm in the chat (`/farm`, Ctrl/Alt+1–9 flips to an agent, `/farm` dashboard,
in-chat approvals and prompts, `suggest_farm`): spec
`docs/superpowers/specs/2026-11-04-farm-interactive-design.md`, plan
`docs/superpowers/plans/2026-11-04-farm-interactive.md`. Every farm call from
the chat runs in a `tea.Cmd` or goroutine, never in `Update` (farm hooks call
`Program.Send`).
Status (2026-11-04): merged to main after build and review; next is the
owner's acceptance run
(`docs/farm-acceptance-run.md`), then a PR. Rulings, deferred minors and one
open question are in the plan's Build record.

**Next feature (owner, 2026-11-07):** `jas attach` — reach a running farm's
agents from another terminal over a Unix socket (`docs/design.md` §5). Phase
1 read-only (stream dashboard and agent output), phase 2 control (steering,
pause/resume/drop, approval inbox). Needs its own design (brainstorming) first.

**Other work (first-class together):** chat polish that stays inline; template /
scenario / `jas bootstrap` / `jas spec` quality (design §8 +
`docs/superpowers/specs/2026-11-02-spec-driven-flow.md`); farm post-MVP in
spec order — `farm resume` + take-over,
`researcher`/`reviewer`/`fuzz` roles, blast-radius merge policy, `--spec`
planning, `farm init`, interactive `/farm`.

## Constraints

- Stay **inline** (Bubble Tea `Println` scrollback). **No** alt-screen / fullscreen TUI, no theme arms race.
- Do **not** break `jas run`, `--json`, or exit codes; approval ladder and redaction unchanged.
- Farm (`docs/superpowers/specs/2026-11-02-farm-design.md`): MVP shipped on `feat/farm-mvp` (see Current focus); experimental (the farm-vs-`jas run` benchmark gate was cancelled by the owner, 2026-11-07). Post-MVP order in the spec's MVP cut. Proposed, core loop (not farm): session awareness (`docs/superpowers/specs/2026-11-02-session-awareness.md`) and spec-driven flow / `jas spec` (`docs/superpowers/specs/2026-11-02-spec-driven-flow.md`). Farm interactive mode is inline only (in the farm spec); attach from another terminal, any dashboard and scenario pipelines wait for their own design.
- Never append to the session log synchronously from `Update`: the log
  (`core.NewUILog`) forwards every event to the chat's bridge, whose
  `tea.Program.Send` blocks on an unbuffered channel the event loop — busy
  inside `Update` — never reads. Log from a `tea.Cmd` instead (see
  `tui/commands.go` `setModel`).
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
- Docs follow features: every user-visible change (CLI subcommand/flag, chat
  command/key, config key, auth, approval level, deny floor, exit code, farm
  behavior) updates `docs/manual.md` (the full reference; `make manual`
  copies it into the skill, and `TestManual_*` fails on a missing flag,
  setting, chat command, exit code or event, or a stale copy), `README.md`
  when the overview changes, and the built-in manual skill
  `cmd/jas/manual/jas/SKILL.md` in the same commit — the manual is embedded
  (`//go:embed`) and re-installed under `<UserCacheDir>/jas/manual/` at every
  start, so stale text ships until the next binary.
