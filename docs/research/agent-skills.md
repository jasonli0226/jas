# Research: Agent Skills (SKILL.md) — spec, pi, Claude Code, opencode → jas

Sources read on 2026-10-29. Abbreviations used in citations:

- **AS** = `agentskills/agentskills` @ `69ef37e` (spec repo; site is agentskills.io)
- **PI** = local pi install `~/.pi/agent/install/releases/1.4.0/node_modules/@earendil-works/pi-coding-agent/`
  (v1.4.0; package now lives at `github.com/earendil-works/pi`, `packages/coding-agent`, per its
  `package.json:90-94`). Citations are to the shipped `dist/*.js`; the GitHub `.ts` sources were not read.
- **CC** = https://code.claude.com/docs/en/skills.md (fetched 2026-10-29 as markdown;
  `:N` refs are line numbers in that fetched `.md` file)
- **OC** = `sst/opencode` tag `v1.19.0` @ `733b2c6` (matches `~/.opencode/bin/opencode --version` = 1.19.0)
- **jas** = this repo at `bf931e1`

Legend: ✅ verified in source/docs · ⚠️ docs and code disagree · ❓ unverified

## 1. The spec (agentskills.io)

**Layout.** A skill is a directory with at minimum `SKILL.md`; optional `scripts/`, `references/`,
`assets/`, and anything else (AS `docs/specification.mdx:8-17`). These dirs are conventions, not
requirements (`:189`). The spec does **not** say where skill dirs live — only what's inside
(AS `docs/client-implementation/adding-skills-support.mdx:54`).

**Frontmatter** (YAML between `---`, then Markdown body; AS `specification.mdx:21-32`):

| Field | Req | Constraint |
|---|---|---|
| `name` | yes | 1-64 chars; `a-z`, `0-9`, `-`; no leading/trailing `-`; no `--`; **must match parent dir name** (`:60-65`) |
| `description` | yes | 1-1024 chars, non-empty; say what + when (`:91-96`) |
| `license` | no | short name or bundled file ref (`:110-114`) |
| `compatibility` | no | 1-500 chars (`:123-128`) |
| `metadata` | no | string→string map (`:147-152`) |
| `allowed-tools` | no | space-separated, **experimental**, support varies (`:163-167`) |

Body: no format restrictions; whole file is loaded on activation; keep under 500 lines / <5000
tokens (`:176-185`, `:220-224`). File refs relative to skill root, one level deep (`:226-237`).

**Progressive disclosure** (AS `specification.mdx:216-224`; `adding-skills-support.mdx:20-28`):
1. Catalog — `name` + `description` at session start (~50-100 tokens/skill).
2. Instructions — full `SKILL.md` body when activated.
3. Resources — `scripts/`, `references/`, `assets/` only when the body references them.

**Client guide** (non-normative, AS `adding-skills-support.mdx`):
- Scan project + user scopes; per scope a client dir **and** `.agents/skills/` (`:38-54`). Some
  clients also scan `.claude/skills/`, ancestors up to git root, XDG dirs (`:56-58`).
- Look for subdirs containing exactly `SKILL.md`; skip `.git/`, `node_modules/`; bound depth 4-6 /
  2000 dirs (`:60-79`).
- Collisions: "**project-level skills override user-level skills**" is called the universal
  convention; within a scope pick first- or last-found consistently and warn (`:81-87`). (Claude Code
  does the opposite — see §3.)
- Gate project skills on a trust check (`:89-91`).
- **Lenient validation**: name≠dir → warn, load; name >64 → warn, load; missing description → skip;
  unparseable YAML → skip (`:128-141`). Suggests a fallback for unquoted colons in values (`:117-126`).
- Store `name`, `description`, `location` (absolute SKILL.md path) (`:143-157`).
- Catalog format is free (XML/JSON/list). XML example is `<available_skills><skill><name>…
  <description>…<location>…` (`:163-180`). Either in the system prompt or in a dedicated tool's
  description (`:186-194`). Suggested instruction text for file-read vs `activate_skill` (`:196-216`).
- Hide filtered skills (disabled, permission-denied, `disable-model-invocation`) entirely; omit the
  catalog when empty (`:220-232`).
- Activation: file-read (model uses its read tool on `location`) or dedicated tool; tool should
  constrain `name` to an enum, can strip frontmatter, wrap in `<skill_content name=…>`, list (not
  read) bundled files (`:238-308`).
- **Allowlist skill dirs in the file-permission system** so bundled resources can be read without
  prompts (`:310-312`).
- Exempt skill content from compaction; dedupe re-activations (`:318-329`).
- User activation via `/skill-name` or `$skill-name` intercepted by the harness (`:258-262`).

Note `disable-model-invocation` is mentioned only in the client guide (`:226`), not in the spec's
field table. ⚠️ pi's docs list it as a spec field (PI `docs/skills.md:69-79`); the spec doesn't.

**skills-ref** (AS `skills-ref/`, Python, "demonstration purposes only", `README.md:7`):
`skills-ref validate | read-properties | to-prompt`. The validator is **stricter than the client
guide**: rejects any frontmatter key outside the six (`validator.py:15-22,104-115`), so Claude Code
extensions like `disable-model-invocation` fail validation; requires name == dir name
(`validator.py:60-65`). ⚠️ It allows any Unicode letter via `str.isalnum()` after NFKC
(`validator.py:37,54`), looser than the spec's `a-z0-9`. Accepts `skill.md` as a fallback
(`parser.py:15,23`). `to-prompt` emits the `<available_skills>` XML, described as "what Anthropic
uses and recommends for Claude models" (`prompt.py:12-13,35-58`).

## 2. pi (v1.4.0)

**Discovery dirs** ✅ (PI `dist/core/package-manager.js:2011-2064`, `docs/skills.md:57-63`):

| Order | Dir | Gate |
|---|---|---|
| 0 | `--skill`/CLI-enabled and `settings.skills` paths | merged first (`resource-loader.js:424-426`) |
| 1 | project `.pi/skills/` | project trust (`package-manager.js:2024,2035-2039`) |
| 2 | project `.agents/skills/` in cwd **and every ancestor up to git root** | project trust (`:291-307`, `:2025-2049`) |
| 3 | `~/.pi/agent/skills/` | none (`:2057`) |
| 4 | `~/.agents/skills/` | none (`:2023,2058-2064`) |

No `.claude/skills` scanning — no `.claude` path appears in `dist/core/*.js` (grep: only a model id
in `model-resolver.js:11`). Where pi-package skills fall in this order ❓ not traced.

Scan: a dir containing `SKILL.md` is a skill root (no deeper recursion); otherwise recurse, skipping
dot-dirs and `node_modules`, honouring `.gitignore`/`.ignore`/`.fdignore`; loose `*.md` files at the
root of a pi skills dir are also accepted as skills if they have a description
(`dist/core/skills.js:113-207`, `package-manager.js:258`). Symlinks followed; same real path loaded
once (`skills.js:328-333`).

**Collisions** ✅: first discovered wins, collision diagnostic records winner/loser
(`skills.js:334-351`; `docs/skills.md:85`). With the order above, project beats user
(inferred from insertion order; not run).

**Validation** (lenient) ✅ (`skills.js:61-89,231-265`): name charset/length/hyphen rules and
description ≤1024 produce **warnings only**; skill is skipped only if description is missing/empty or
frontmatter fails to parse. Name falls back to parent dir name; **name≠dir is not checked at all**
(`skills.js:243-245`; `docs/skills.md:83`). `compatibility`, `license`, `metadata`, `allowed-tools`
are not read by the loader (only `name`, `description`, `disable-model-invocation`; `skills.js:255-263`).

**System prompt** ✅ (`skills.js:278-303`; `system-prompt.js:101-108`): skills with
`disable-model-invocation: true` are excluded. Exact text:

```
The following skills provide specialized instructions for specific tasks.
Use the read tool to load a skill's file when the task matches its description.
When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.

<available_skills>
  <skill>
    <name>…</name>
    <description>…</description>
    <location>/abs/path/SKILL.md</location>
  </skill>
</available_skills>
```

XML-escaped. The second line becomes "Use bash to load…" if only `bash` is available, or a
tool-less hint if the reader is reachable indirectly; with neither, no skills section at all
(`system-prompt.js:101-104`). The whole thing is wrapped as a `<skills>` section of the prompt
(`system-prompt.js:114-119`).

**Activation**: model uses the normal **read tool** on `<location>` — no dedicated skill tool ✅
(`docs/skills.md:43-45`). pi's read tool is not root-confined in the way jas's is (❓ not checked).

**`/skill:name [args]`** ✅ (`agent-session.js:1723-1749,1759`): harness reads the file, strips
frontmatter, injects
`<skill name="…" location="…">\nReferences are relative to <baseDir>.\n\n<body>\n</skill>` followed
by the args; unknown name passes the text through unchanged.

**Disable**: `--no-skills` / `-ns` (`cli/args.js:188-189,323`); `enableSkillCommands: false` hides
`/skill:` commands from discovery but typed ones still work (`settings-defaults.js:33`;
`docs/skills.md:53`); `skills` setting array supports `!glob`, `+path`, `-path`
(`docs/settings.md:156-163`). Project skills are gated by trust (above).

## 3. Claude Code

**Locations** ✅ (CC `:156-165`): enterprise (managed settings dir `.claude/skills/`), personal
`~/.claude/skills/<n>/SKILL.md`, project `.claude/skills/<n>/SKILL.md` (cwd and every parent up to
repo root, `:177`), nested `<subdir>/.claude/skills/` (loaded lazily when Claude touches files there,
`:181`), `--add-dir` dirs (`:190`), plugins `<plugin>/skills/<n>/SKILL.md` namespaced
`/plugin:skill`, claude.ai-synced skills. Legacy `.claude/commands/*.md` still work (`:17,172`).
`.agents/skills/` is **not documented** as a location ❓ (not found on the page).

**Precedence** ✅ (`:209-219`): enterprise > **personal > project** (opposite of the spec guide's
"project overrides user"); skill > same-named command; plugin skills never collide (namespaced);
nested and add-dir duplicates get directory-qualified names (`/apps/web:deploy`).

**Frontmatter** ✅ (`:391-416`): all optional, `description` recommended (falls back to first body
line). Unknown fields silently ignored; malformed YAML → skill loads with no fields (`:391-393`).
Extensions beyond the spec: `when_to_use`, `argument-hint`, `arguments`, `disable-model-invocation`,
`user-invocable`, `allowed-tools` (grants for the invoking turn only, `:615-617`), `disallowed-tools`,
`model`, `effort`, `context: fork`, `agent`, `background`, `hooks`, `paths` (glob activation),
`shell`. Spec fields `license`/`compatibility`/`metadata` accepted but not acted on. claude.ai/API
uploads hard-fail on non-spec keys (`:418-432`). Body supports `$ARGUMENTS`, `$N`, `${CLAUDE_SKILL_DIR}`,
etc. (`:477-490`) and `` !`cmd` `` injection.

Name rules: no spec charset enforcement documented; it refuses only invisible/control/bidi
characters, odd whitespace and `<` `>` (`:460-475`).

**Exposure** ✅: a single `Skill` tool "executes a skill within the main conversation"
(https://code.claude.com/docs/en/tools-reference.md, tools table line 52). The listing of names +
descriptions is in context; each entry's description+`when_to_use` capped at 1,536 chars, total
listing budget 1% of context window, least-used descriptions dropped first (CC `:1222-1230`).
Exact listing format ❓ undocumented; observed in this session as a system-reminder bullet list
`- <name>: <description>` under "The following skills are available for use with the Skill tool".
Skill content enters as one message, persists, deduped if identical; on compaction the latest
invocation of each is re-attached (first 5k tokens each, 25k total) (`:605-611`).

**Invocation** ✅ (`:555-603`): `/name args` at message start runs it; `disable-model-invocation`
hides it from the listing and blocks the Skill tool; `user-invocable: false` hides it from `/`.
Permissions: deny `Skill` to disable all, `Skill(name)` / `Skill(name *)` rules (`:859-893`);
`skillOverrides` setting (`on`/`off`/`name-only`/`user-invocable-only`) (`:909-941`).
`allowed-tools` is **not gated by workspace trust** (`:619`).

## 4. opencode (v1.19.0)

**Discovery** ✅ (OC `packages/opencode/src/skill/index.ts:21-25,173-233`):

1. Unless `OPENCODE_DISABLE_EXTERNAL_SKILLS`: `~/.claude/skills/**/SKILL.md` (skipped with
   `OPENCODE_DISABLE_CLAUDE_CODE_SKILLS` or `OPENCODE_DISABLE_CLAUDE_CODE`) and `~/.agents/skills/**/SKILL.md`;
   then `.claude/` and `.agents/` found walking up from cwd to the worktree root, nearest first
   (`:185-203`; flags `src/effect/runtime-flags.ts:21,27-30`).
2. Config dirs — `~/.config/opencode` (XDG), `.opencode/` up to worktree, `OPENCODE_CONFIG_DIR`
   (`src/config/paths.ts:23-41`) — pattern `{skill,skills}/**/SKILL.md` (singular accepted).
3. `skills.paths` from config (`**/SKILL.md`), and `skills.urls` (pulled remote indexes) (`:210-227`).
4. A built-in `customize-opencode` skill registered first so disk skills can override it (`:276-283`).

**Collisions** ⚠️: map assignment, so the later write wins with a `duplicate skill name` warning
(`:125-139`); but files are parsed with `concurrency: "unbounded"` (`:240-243`), so which write is
"later" depends on async parse completion — effectively nondeterministic (my reading of the code;
not tested). The docs list locations but state no precedence (opencode.ai/docs/skills).

**Validation** ⚠️: the docs require `^[a-z0-9]+(-[a-z0-9]+)*$`, 1-64 chars, name == dir,
description 1-1024 (OC `packages/web/src/content/docs/skills.mdx:62`; opencode.ai/docs/skills). The
v1.19.0 loader only checks `name` is a string and `description` is a string or absent
(`skill/index.ts:53-59,123`); `NameMismatchError` is declared (`:67-71`) but unused in `src/`
(grep). Skills without a description load but are omitted from the listing (`:321-323`).

**Listing** ✅: system prompt gets (`src/session/system.ts:107-119`, format `skill/index.ts:321-338`):

```
Skills provide specialized instructions and workflows for specific tasks.
Use the skill tool to load a skill when a task matches its description.
<available_skills>
  <skill>
    <name>…</name>
    <description>…</description>
    <location>…</location>
  </skill>
</available_skills>
```

sorted by name; only `location` is HTML-escaped. A terser `## Available Skills` markdown variant
exists (`:340-345`).

**`skill` tool** ✅ (`src/tool/skill.ts:8-67`, `skill.txt`): param `name` (plain string, not an
enum); unknown name dies with the available list; asks permission `skill` with pattern = name;
returns `<skill_content name=…>` + `# Skill: name` + body (frontmatter stripped) + "Base directory
for this skill: …" + up to 10 sampled sibling files in `<skill_files>`.

**Permissions** ✅: `permission.skill` patterns allow/ask/deny; `deny` hides the skill from the
listing (`skill/index.ts:310-315`); skill section omitted when the tool is disabled
(`system.ts:108`). Every discovered skill dir is added to the `external_directory` **allow** list so
reads of bundled files outside the project don't prompt (`src/agent/agent.ts:101-123`) — the spec
guide's "allowlist skill directories" advice, implemented.

❓ `packages/core/src/skill*.ts` (a `SkillV2`) also exists and is used by the HTTP API
(`packages/server/src/handlers/skill.ts:1`); whether it drives the CLI session loop in 1.19 was not
traced. The above is the `packages/opencode` path the session prompt uses.

## 5. Comparison

| | Spec (AS) | pi 1.4.0 | Claude Code | opencode 1.19.0 |
|---|---|---|---|---|
| Native dirs | not specified; guide: `.<client>/skills` | `.pi/skills`, `~/.pi/agent/skills` | `.claude/skills`, `~/.claude/skills`, managed, plugins | `.opencode/skill(s)`, `~/.config/opencode/skill(s)` |
| `.agents/skills` | recommended (guide) | project (ancestors→git root) + user | ❓ not documented | project (ancestors→worktree) + user |
| `.claude/skills` | "some implementations" | no | native | project + user (env opt-out) |
| Ancestor walk | optional | `.agents` only | yes, to repo root | yes, to worktree |
| Precedence | project > user; warn | first wins: project > user; warn | enterprise > **personal > project** | last write wins, warn; order racy |
| Trust gate | recommended for project | yes (project dirs) | workspace trust for some features; `allowed-tools` not gated | ❓ none seen |
| Catalog | XML `<available_skills>` suggested | XML in system prompt, `<location>` | Skill-tool listing, budgeted | XML in system prompt |
| Activation | read tool or dedicated tool | **read tool** | **`Skill` tool** | **`skill` tool** |
| User invoke | `/name` or `$name` suggested | `/skill:name args` | `/name args` | ❓ not checked (TUI `dialog-skill.tsx` exists) |
| name rules | strict, name==dir | warn only; no dir check | only bad-char refusal | docs strict, code: string only |
| Missing description | skip | skip | fallback to first line | load, hide from listing |
| Unknown fields | skills-ref rejects | ignored | ignored | ignored |
| `disable-model-invocation` | guide mentions | honoured | honoured | ❓ not seen in loader |

## 6. Implications for jas

**Minimal interoperable subset.**
- Scan `<root>/.agents/skills/*/SKILL.md` and `~/.agents/skills/*/SKILL.md` — the only location pi
  and opencode both read and the spec guide recommends. Add `.claude/skills` (project + user) for
  the large existing corpus; opencode does this by default. A jas-native dir is optional — see the
  deny-floor problem below.
- Honour `name`, `description`, `disable-model-invocation`. Ignore everything else (all three
  clients do). Lenient: warn on name rules / name≠dir, skip on missing description or bad YAML
  (spec guide + pi). Add the unquoted-colon fallback; Claude-authored skills hit it.
- Precedence: project > user, first-found within a scope, warn on collision (spec guide, pi).
  Sort deterministically — don't copy opencode's race.
- Catalog: pi's XML block verbatim is the safest default (spec example, skills-ref `to-prompt`, pi,
  opencode all converge on it). It goes in `BuildSystem` — skill set must be computed once at
  startup and rendered in a stable order to keep the cached prefix byte-identical (`core/prompt.go:3-19`).
- Gate project skills behind the existing project trust (`core/trust.go`, content-hash keyed); the
  hash should cover every project `SKILL.md`, since a skill is injected instructions just like
  `AGENTS.md`.

**Read tool vs dedicated tool, given `core/policy.go` Resolve.**
`ExecPolicy.Resolve` joins relative paths to Root, resolves symlinks, and refuses anything not
`within(root, …)` with "path outside project root" (`core/policy.go:41-59,94-100`); then the deny
floor applies (`core/deny.go:20-23`). Consequences:
- Project skills under `<root>/.agents/skills` work with the plain `read` tool today — but a skill
  dir that is a **symlink to outside the root** (common: `npx skills` / pi packages symlink installs)
  resolves outside and is refused.
- **User skills (`~/.agents/skills`, `~/.claude/skills`) are unreadable** by `read`, `grep`, and so
  are their `references/`. The model's only fallback is `shell` (`cat ~/…`), which is mutating, so
  under `--approve=mutating` every skill load prompts — and it teaches the model to route around
  confinement.
- **`~/.jas/skills` (design.md §12.2) is doubly blocked**: outside root, and `~/.jas/**` is in
  `DenyFloor` (`core/deny.go:22`) plus `homeDenyPattern` (`cmd/jas/run.go:82,130-132`). The shell
  token filter also denies `~/.jas/...` words (`core/deny.go:57-70`, `tools/shell.go:107`), so even
  `sh ~/.jas/skills/x/scripts/run.sh` is blocked. Either move user skills to `~/.agents/skills`
  (recommended — interop for free) or carve out `~/.jas/skills/**` while keeping `.pending/` and
  auth/config denied.

Options:
1. **Read allowlist**: extend `ExecPolicy` with read-only extra roots = the discovered skill dirs
   (opencode's `external_directory` allow, spec guide `:310-312`). Keeps pi's zero-tool design and
   lets the model read `references/` on demand. Must apply only to ReadOnly tools, re-check the deny
   floor, and resolve symlinked skill dirs once at discovery (allow the resolved target).
2. **Dedicated `skill` tool** (opencode/Claude Code): `name` as an enum, returns body + base dir +
   file list. Clean audit event per activation, frontmatter stripped, easy to protect from
   compaction elision (#2). But it does **not** solve reading `references/`/`scripts` outside root —
   you still need option 1 for tier 3.
3. Both: tool for tier 2, allowlist for tier 3. Most work, best UX.

Recommendation: option 1 first (smallest change, matches pi which jas mirrors, and tier 3 needs it
regardless); add the dedicated tool later only if activation telemetry or compaction protection
demands it. Skill scripts still run through `shell` with normal approval — don't implement
`allowed-tools` (experimental, and Claude Code shows it bypasses trust).

**Unverified / not done:** pi package-skill ordering; Claude Code `.agents/skills` support and exact
listing text; opencode `/skill` slash invocation and `SkillV2` usage; nothing was executed end to end.
