# Agent notes for jas

jas is a tiny, fast coding-agent harness in Go (CI-friendly, MIT, inline chat + `jas run`).

## Current focus

**Inline TUI UX polish.** Implement
[`docs/superpowers/plans/2026-10-31-tui-ux-polish.md`](docs/superpowers/plans/2026-10-31-tui-ux-polish.md)
**in order**, starting at **#60 (role chrome + turn gaps)**. Do not skip ahead to
slash/status polish until #60–#63 are done.

Issues: [#60](https://github.com/jasonli0226/jas/issues/60) ·
[#61](https://github.com/jasonli0226/jas/issues/61) ·
[#62](https://github.com/jasonli0226/jas/issues/62) ·
[#63](https://github.com/jasonli0226/jas/issues/63) ·
[#64](https://github.com/jasonli0226/jas/issues/64) ·
[#65](https://github.com/jasonli0226/jas/issues/65) ·
[#66](https://github.com/jasonli0226/jas/issues/66)

Spec: `docs/superpowers/specs/2026-10-30-interactive-mode-design.md`. Prior chat
plan: `docs/superpowers/plans/2026-10-31-inline-tui-chat.md`.

## Constraints

- Stay **inline** (Bubble Tea `Println` scrollback). **No** alt-screen / fullscreen TUI, no theme arms race.
- Do **not** break `jas run`, `--json`, or exit codes; approval ladder and redaction unchanged.
- This pass is **TUI only** — no farm UI, no skills-as-orchestrator, no scenario pipelines.
- Commits: `type: description`. Keep `go test -race ./...` green; `CGO_ENABLED=0`.
