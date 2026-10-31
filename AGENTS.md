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
- Farm UI, skills-as-orchestrator and scenario pipelines wait for their own design.
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
