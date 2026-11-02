# Starter screen design

Show a pi-style starter banner in the inline chat: at start and again on
`/new`. Resume (`--continue`, `/resume`) does not show it.

## Intent

First launch of a chat currently drops straight into the input. A banner
gives the user the version, the key bindings that matter in the first ten
seconds, and what the agent loaded (context files, skills) — the same
at-a-glance grounding pi's starter screen gives.

## Goals

- Banner at chat start and on `/new`, in scrollback (inline `tea.Println`,
  not alt-screen).
- Sections: logo + version, one hints line, `[Context]`, `[Skills]`.
- Hints line lists jas's actual keys only.

## Non-goals

- No `ctrl+o`/`!`-shell parity with pi; jas has neither.
- No `[Extensions]` section; jas has no extensions.
- No interactive/help-expanding behavior; `/help` already covers full help.

## Approaches

1. **Styled banner block (chosen)** — new `tui/banner.go` renders a styled
   block printed through the existing `m.print` queue in `Init` and `/new`.
   Visual match to pi's screen; ~80 lines.
2. Status-line style via existing `notice()` — cheaper, but loses the
   banner look.

## Content

```
 jas  v0.x.y
 esc stop run · ctrl+c clear/twice-quit · / commands · /help full list
 [Context]
   <file paths, one per line, wrapped>
 [Skills]
   <skill names, comma-wrapped>
```

- `[Context]` lists every context entry from `projectContext.files`:
   loaded ones plain, declined/skipped ones dim with a `not loaded` mark
   (builder's call — informative when trust was declined). The header is
   hidden when the slice is empty (same as pi). `Backend.ContextFiles()` is
   a new interface method, so `tui/backend_test.go`'s test double gets a
   stub returning nil → header omitted, which is also the empty-case
   behavior.
- `[Skills]` reuses `Backend.SkillNames()`.
- Hints reflect real bindings (`tui/keys.go`): esc stops the run,
   ctrl+c clears input / twice quits, `/` opens the command menu,
   `/help` lists everything.

## How (decided)

- `Options.Version string` — `chatCmd` passes `version` from
  `cmd/jas/main.go`.
- `Backend.ContextFiles() []ContextFile{Path string, Loaded bool}` —
  `chatBackend` wraps `e.pc.files`. Skill names already reach the TUI.
- Print site: `Init` queues the banner before the opening session —
  **only when `!o.Continue`** (`--continue` resumes, no banner); `/new`
  (`newSession`) queues it before `beginOpen`, ungated (a fresh open always
  shows, even mid-session via `/resume`'s sibling path). `/resume` never
  queues it. Both go through `m.print`, so ordering with other blocks is
  preserved.
- Styling in `tui/banner.go` using existing `style.go` styles; wrapped to
  terminal width (reuse `m.width`).

## Left to the builder

- Exact logo glyph/spacing and wrap widths for the two list sections.
- `version` is `dev` without ldflags, so dev builds read `jas vdev` —
  acceptable (pi does similar); note it if it bothers.
- Whether `/new` banner omits nothing else (e.g. session path — not shown;
  `/session` covers it).

## Docs

Update `README.md` and `cmd/jas/manual/jas/SKILL.md` in the same commit
(user-visible chat change), including the `/new` re-show behavior.
