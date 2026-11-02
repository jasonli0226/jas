# Starter Screen Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show a pi-style starter banner (logo, version, key hints, `[Context]`, `[Skills]`) at chat start and on `/new`.

**Architecture:** A pure renderer in `tui/banner.go` produces styled blocks for the existing `m.print` scrollback queue. Data reaches it via two small additions: `Options.Version` and `Backend.ContextFiles()`. `Init` queues the banner only when not resuming; `newSession` always queues it.

**Tech Stack:** Go, Bubble Tea v2, lipgloss v2.

**Spec:** `docs/superpowers/specs/2026-11-03-starter-screen-design.md`

## Global Constraints

- Inline only: banner goes through `m.print` (tea.Println scrollback); no alt-screen.
- Never touch the bridge synchronously from `Update`; banner prints are queued like every other block.
- Hints line content is fixed verbatim: `esc stop run · ctrl+c clear/twice-quit · / commands · /help full list`
- Resume (`--continue` / `/resume`) never shows the banner; `/new` always does.
- `go test -race ./...` green; `CGO_ENABLED=0`.
- Same commit updates `README.md` and `cmd/jas/manual/jas/SKILL.md` (manual is embedded, restated at every start).

## Review Focus

- `--continue` with a saved session must not print the banner — test in Task 3.
- Empty `ContextFiles()` must omit the `[Context]` header entirely (test double returns nil) — test in Task 2.
- Declined-trust files must still be listed, dim, marked `not loaded` — test in Task 2.
- Narrow terminals: section lines wrap at width, no overflow — test in Task 2.
- Long skill lists wrap instead of printing one huge line — test in Task 2.

---

### Task 1: Data plumbing — `Options.Version`, `Backend.ContextFiles`

**Files:**
- Modify: `tui/chat.go` (Options struct, line ~25)
- Modify: `tui/backend.go` (Backend interface)
- Modify: `tui/backend_test.go` (testBackend)
- Modify: `cmd/jas/chat.go` (chatCmd Options literal ~line 66, chatBackend methods near `SkillNames` ~line 373)

**Interfaces:**
- Produces: `type ContextFile struct{ Path string; Loaded bool }` (tui/backend.go); `ContextFiles() []ContextFile` on `Backend`; `Version string` field on `tui.Options`.

- [ ] **Step 1: Write the failing test**

In `tui/backend_test.go`, give `testBackend` a field `ctxFiles []ContextFile` (default nil) and implement:

```go
func (b *testBackend) ContextFiles() []ContextFile { return b.ctxFiles }
```

The existing `var _ Backend = (*testBackend)(nil)` at line 190 fails to compile until the interface method exists. In `tui/chat_test.go` add:

```go
func TestChat_OptionsCarryVersion(t *testing.T) {
	c, _ := newUnopenedChat(t, newTestBackend(t), Options{Version: "9.9.9"})
	if c.m.o.Version != "9.9.9" {
		t.Fatalf("version %q", c.m.o.Version)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./tui/ 2>&1 | head -20`
Expected: compile FAIL — `ContextFiles` undefined / not implemented.

- [ ] **Step 3: Implement**

- `tui/backend.go`: add `type ContextFile struct{ Path string; Loaded bool }` above the `Backend` interface; add `ContextFiles() []ContextFile` to the interface with a comment mirroring `SkillNames`'s.
- `tui/chat.go`: add `Version string` to `Options` (comment: `// shown once in the start banner`).
- `cmd/jas/chat.go`: in `chatCmd`'s `tui.Options{...}` literal add `Version: version`; implement

```go
// ContextFiles lists the context file and project skills that were offered
// to this session; Loaded is the trust decision.
func (b *chatBackend) ContextFiles() []tui.ContextFile {
	files := make([]tui.ContextFile, len(b.e.pc.files))
	for i, f := range b.e.pc.files {
		files[i] = tui.ContextFile{Path: f.Path, Loaded: f.Loaded}
	}
	return files
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./tui/ ./cmd/jas/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tui/backend.go tui/chat.go tui/backend_test.go tui/chat_test.go cmd/jas/chat.go
git commit -m "feat: plumb version and context files to the chat"
```

---

### Task 2: Banner renderer — `tui/banner.go`

**Files:**
- Create: `tui/banner.go`
- Create: `tui/banner_test.go`

**Interfaces:**
- Consumes: `ContextFile`, `Options.Version`, `Backend.SkillNames()`, `Backend.ContextFiles()`, styles from `tui/style.go` (`dimStyle`, `boldStyle` or a new `bannerStyle`).
- Produces: `func banner(version string, files []ContextFile, skills []string, width int) []string` — one entry per printed block (lines), pre-styled, wrapped to width. Model wrapper: `func (m *Model) banner() []string` = `banner(m.o.Version, m.b.ContextFiles(), m.b.SkillNames(), m.width)`.

- [ ] **Step 1: Write the failing tests**

`banner_test.go`, using the existing `plain()` ANSI-stripping helper from `chat_test.go`:

- `TestBanner` — version `1.2.3`, one loaded file `/p/AGENTS.md`, skills `a,b,c`, width 80: assert first non-empty line contains `jas` and `v1.2.3`; one line equals the hints string verbatim; one line contains `[Context]` and one contains `/p/AGENTS.md` (no `not loaded`); one contains `[Skills]` and one lists `a, b, c`.
- `TestBanner_NotLoadedDimmed` — file with `Loaded: false`: its line still appears with `not loaded` after the path.
- `TestBanner_EmptySections` — nil files, nil skills: no line contains `[Context]` or `[Skills]`.
- `TestBanner_Wraps` — width 30, 5 long skill names and one long path: no output line exceeds 30 cells (use `lipgloss.Width`); continuation lines are indented under the section.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./tui/ -run TestBanner`
Expected: FAIL — `banner` undefined.

- [ ] **Step 3: Implement `banner` in `tui/banner.go`**

Small ASCII `jas` wordmark (two or three lines of block characters, or a single bold `jas` if the glyph art reads badly at 80 cols) + version on one line; hints line; `[Context]` section with one entry per file, `not loaded` dim suffix when `!Loaded`; `[Skills]` section as comma-joined names wrapped at `width` with a two-space continuation indent. Reuse `mutedFg`/`dimStyle` for headers and marks; wrap with `lipgloss.Width` checks or `ansi.WrapString` (already an import in `tui/markdown.go`, use the same helper if it exports one). Section headers omitted when the slice is empty.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./tui/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tui/banner.go tui/banner_test.go
git commit -m "feat: render start banner with version, keys, context and skills"
```

---

### Task 3: Wire prints — `Init` gate and `/new`

**Files:**
- Modify: `tui/chat.go:136` (`Init`)
- Modify: `tui/sessions.go` (`newSession`)
- Modify: `tui/chat_test.go` or `tui/sessions_test.go` (wherever `/new` and `Continue` tests live; both exist already)

**Interfaces:**
- Consumes: `m.banner()` from Task 2; `m.print` queue; `Options.Continue`.

- [ ] **Step 1: Write the failing tests**

- `TestChat_BannerOnStart` — `newUnopenedChat` with `Options{Version: "1.0.0"}`, run Init, pump: first `Printed()` blocks include the banner's version line, before any session content.
- `TestChat_NoBannerOnContinue` — same but `Options{Continue: true}` and a backend with a saved session: no printed line contains `v1.0.0` or `[Skills]`.
- `TestChat_BannerOnNew` — open a chat, send `/new`, pump: banner appears again after the new session opens.
- `TestChat_NoBannerOnResume` — open a chat, send `/resume` picking a saved session: banner does not appear again (only the one banner from start remains).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./tui/ -run 'Banner'`
Expected: FAIL — no banner blocks printed.

- [ ] **Step 3: Implement**

```go
// Init opens the first session, after the start banner (a resumed session
// skips it).
func (m *Model) Init() tea.Cmd {
	if m.o.Continue {
		return m.openFirst()
	}
	return tea.Batch(m.openFirst(), m.print(m.banner()...))
}
```

In `tui/sessions.go` `newSession`, batch the banner before the open:

```go
func (m *Model) newSession() tea.Cmd {
	b, gate, model := m.b, m.gate, m.model
	return tea.Batch(m.print(m.banner()...), m.beginOpen(func(ui core.UI) sessionMsg {
		live, err := b.NewSession(model, ui, gate)
		return sessionMsg{live: live, err: err}
	}))
}
```

(`print` queues into the outbox, so the banner precedes the new session's blocks even though the open runs concurrently; `resumeSession` is left untouched.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -race ./tui/ ./cmd/jas/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tui/chat.go tui/sessions.go tui/chat_test.go tui/sessions_test.go
git commit -m "feat: show start banner at start and on /new"
```

---

### Task 4: Documentation

**Files:**
- Modify: `README.md`
- Modify: `cmd/jas/manual/jas/SKILL.md`

**Interfaces:**
- Consumes: shipped behavior from Tasks 1–3.

- [ ] **Step 1: Update both docs**

`README.md`: in the chat/keys section, one short paragraph: the chat shows a start banner (version, key hints, loaded context files and skills) when it starts fresh and on `/new`; resuming (`--continue`, `/resume`) does not show it. `cmd/jas/manual/jas/SKILL.md`: same fact next to the existing keys paragraph (~line 31) and the `/new` row (~line 38), so the embedded manual matches the binary.

- [ ] **Step 2: Verify**

Run: `go vet ./... && go test -race ./...`
Expected: PASS. Rebuild `go build ./cmd/jas` to confirm the manual still embeds.

- [ ] **Step 3: Commit**

```bash
git add README.md cmd/jas/manual/jas/SKILL.md
git commit -m "docs: start banner at start and on /new"
```
