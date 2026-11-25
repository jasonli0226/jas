# Scenarios and `jas bootstrap` — design

Status: approved by the owner (2026-11-26). Decisions marked "owner" are the
owner's. Those marked "author's call" are proposals to check.
Parent: `docs/design.md` §8 (Scenarios) and §8.3 (`jas bootstrap`). This
document replaces both sections. Where they disagree, this document wins,
and `docs/design.md` is updated in the same change to point here.
Related: #9 (blueprints dropped), #157 (`jas spec` / project init, its own
design), `docs/design/2026-11-02-farm-design.md` (`.jas/farm.yaml`).

## Intent

A **scenario** says what a repository *is* and the rules jas follows inside
it: what kind of project (web app, server, ML…), how to check work, which
files the agent must not touch, and the project's named commands. It is
checked into the repo as `.jas/scenario.yaml` and shared by everyone who
works there (owner).

Why: today jas knows how to talk about a repo (`AGENTS.md`) but cannot
*enforce* anything repo-specific. `AGENTS.md` can only ask the model nicely
("don't edit `data/raw/`", "run the formatter"). A scenario makes those rules
harness-enforced, and makes "study a repo, draft its setup" (`jas bootstrap`)
a first-class onboarding step, which the product framing names as a core
feature (`AGENTS.md`, Current focus).

### How it fits next to what already shipped

Since §8 was written, profiles, layered settings, `AGENTS.md`, skills and
project trust shipped. Each now answers a different question, and the
scenario must not overlap any of them (owner: overlap confuses users):

| thing | answers | lives in | scope |
|---|---|---|---|
| profile | who am I / what context am I in | `~/.jas/profiles/<name>/` | personal |
| settings | how should jas behave (model, approvals) | `config.json` layers | personal or project |
| `AGENTS.md` | conventions and instructions for the model | repo root | project |
| **scenario** | what this repo is and its enforced rules | `.jas/scenario.yaml` | project |
| `farm.yaml` | how the farm runs (budgets, worktrees, review) | `~/.jas/`, `.jas/` | personal or project |

## Goals

- One project-scoped, checked-in file for repo facts and enforced rules.
- Harness enforcement: protected paths, per-edit checks and an end-of-run
  check run by jas, not requested of the model.
- `jas bootstrap` drafts that file (and, when missing, an `AGENTS.md`) for an
  existing repo, with the evidence for every line.
- One answer to "how do I check this repo": the farm reads it from the
  scenario instead of duplicating it in `farm.yaml` (owner: option A).
- A stable entry point (`scenario.Seed`) that `jas spec` will use to start a
  new project from an existing repo's scenario (`jas spec --from`).

## Non-goals

- **Scaffolding or templates.** Nothing copies code files into a new
  project (#9). Reuse carries the scenario and conventions, never code.
- **Presets as live layers.** No `--scenario <name>` flag, no
  `~/.jas/scenarios/`, no built-in scenario that applies at runtime. A repo
  has exactly one scenario: its file. (§8.1's discovery chain is dropped; it
  would compete with the profile chain.)
- **Instructions in the scenario.** §8.2's `system:` field is dropped.
  Instructions live only in `AGENTS.md`.
- **MCP.** The MCP client is not built (v3 roadmap). No `mcp:` key in this
  version; an unknown key is an error, so adding it later is safe.
- **General hooks.** Only checks (`check`, `edit_check`). Arbitrary
  `post_run` scripts (§8.2's "summarize metrics") wait for a real need.
- **Background tools** (dev servers). v1 tools run in the foreground with a
  timeout. See Deferred.
- **`jas spec` itself.** Its interview flow is #157's design. This document
  only fixes the contract it uses.

## Anti-goals

- A second place to configure what `config.json`, `AGENTS.md` or a profile
  already configures.
- A scenario that weakens the deny floor (`core/deny.go`). `protect` only
  adds.
- Hidden runtime behavior from `types`. Changing `types` never changes what
  is enforced; only the explicit fields do.
- A bootstrap that activates anything. Its output is a draft, inert until
  the project is trusted.
- Breaking `jas run`, `--json` or exit codes for repos without a scenario.

## Approaches considered

How the scenario relates to the existing `.jas/farm.yaml`, which already
holds `check`, `vars` (`build`, `lint`, `test_globs`) and `generated`:

- **A. Scenario owns project facts, `farm.yaml` keeps farm knobs (chosen,
  owner).** The scenario holds `check`, `generated` and named tools; the farm
  reads them. `farm.yaml` keeps budgets, split, worktrees, `part_check`,
  `review`, models, roles. One answer to "how do I check this repo".
- B. Keep them independent. Least work; duplicated commands drift apart.
- C. Fold farm settings into `scenario.yaml` and retire `farm.yaml`. One
  file, but a migration, and the global `~/.jas/farm.yaml` has no home in a
  per-repo file.

## The scenario file

`.jas/scenario.yaml`, YAML, strict (unknown keys are an error). Every field
is optional; an empty file is valid and does nothing.

```yaml
# .jas/scenario.yaml
types:  [server]                 # what the repo does
stacks: [typescript, nestjs, prisma, postgres]   # how it is built

check: npx prisma validate && npm run lint && npm test

edit_check:
  - {glob: "**/*.{ts,tsx}", run: "npx prettier --check {file}"}

protect:
  - "prisma/migrations/**"       # applied migrations are history
  - "dist/**"

generated:
  - {path: package-lock.json, regenerate: "npm install --package-lock-only --no-audit --no-fund"}

tools:
  generate: {run: "npx prisma generate", description: "regenerate the Prisma client"}
  migrate:  {run: "npx prisma migrate dev", approve: always, description: "create and apply a dev migration"}
  build:    {run: "npm run build"}
  lint:     {run: "npm run lint"}
```

### Fields

**`types`** (list) — what the repo does. v1 set (owner): `web`, `server`,
`cli`, `library`, `ml`, `infra`, `other`. A list because real repos are
mixed (`[web, server]`). Unknown values are an error naming the set. Later
candidates, added on request as data: `mobile`, `data-pipeline`, `desktop`,
`embedded`.

**`stacks`** (list) — languages, runtimes and key frameworks. Free-form
lowercase names (`rust`, `react`, `tensorflow`); unknown names are allowed.
Rule for what belongs here (owner-agreed): **a stack is anything that
changes checks, protected paths or tools.** Libraries that do not
(zustand, react-query, shadcn, pandas, seaborn) go in `AGENTS.md` as
conventions.

**`check`** (string) — the command whose exit code says whether the work is
good. Run by jas at the end of a run (see Enforcement), and the farm's
default `check`. Timeout 10 minutes in `jas run` and the chat (the tools'
default; `CheckSpec.Timeout` 0 would mean none); the farm keeps its own
budgets. Replaces §8.2's `post_run` (author's call: one name shared
with the farm).

**`edit_check`** (list of `{glob, run}`) — fast per-file checks run after a
`write` or `edit` to a matching path. `{file}` is replaced by the edited
path, relative to the root and shell-quoted. Globs use the same doublestar
matching as the rest of jas. Replaces §8.2's `pre_apply` (author's call:
the name says what it does; see Enforcement for why it runs after the write).

**`protect`** (list of globs, root-relative) — paths `write` and `edit` may
not change. Adds to the deny floor; never removes from it. File tools only:
`shell` is governed by the approval levels, so `npm install` may still
rewrite a lockfile (owner).

**`generated`** (list of `{path, regenerate}`) — files a tool writes as a
side effect. Same shape and meaning as `farm.yaml`'s `generated`. Generated
paths are also protected from `write`/`edit`, and the refusal names the
`regenerate` command (author's call: hand-editing a lockfile is always
wrong).

**`tools`** (map name → `{run, description?, approve?, timeout?}`) — named
project commands the model can run. Names are lowercase `[a-z0-9_-]`.
`approve: always` prompts at every approval level and is never covered by an
always-grant (for commands that touch a database or start long jobs).
`timeout` defaults to 10 minutes. The tools named `build` and `lint` also
feed the farm's `build`/`lint` vars.

### Validation

Loading fails with a clear error naming the file and key for: unknown keys,
unknown `types`, an empty `run`, a bad glob, a tool name outside the
pattern, a `protect` glob or `generated` path that escapes the root (`../`
or absolute). In the chat a bad file is a notice and the session runs
without the scenario; in `jas run` and `jas farm` it is exit 2 (author's
call: a headless run must not silently drop enforcement the repo asked for;
an invalid file is closer to "invalid setting" than to "unreadable file").
The Exit codes table's row for 2 gains "an invalid `.jas/scenario.yaml`" in
the same change.

## Trust

The scenario names commands jas runs, so it joins the existing project trust
prompt with `AGENTS.md`, project skills, `.jas/farm.yaml` and settings
files (`core/trust.go`, `DecideContextFile`). The trust prompt lists the
scenario's commands (`check`, each `edit_check.run`, each tool's `run`) so
the owner sees what will execute. A content change asks again. Untrusted:
the file is ignored with a notice (`skipping untrusted .jas/scenario.yaml`),
like `farm.yaml`. `--trust-project` covers it.

## Enforcement

### Protect

`protect` globs and `generated` paths are added to `ExecPolicy` as a write
deny list (`Protect`). `WriteAllowed` checks it **before and independently
of** the allow-list: today it returns "allowed" as soon as `Writes == nil`,
so a deny check placed after that short-circuit would silently lose
protection in every session without write limits. A refused call returns a tool error naming the rule:
`prisma/migrations/2026_init/migration.sql is protected by .jas/scenario.yaml (prisma/migrations/**)`,
or for a generated file `package-lock.json is generated; run: npm install --package-lock-only …`.
Farm workers get the same deny list.

### `edit_check`

After a `write` or `edit` lands on disk, each matching `edit_check` runs (in
file order, through `core.RunCheck` with the shell's allowlisted env and a
30 s default timeout). If one fails, jas **restores the file's previous
content** (or removes a newly created file) and returns the check's output
as the tool error, so the model fixes its edit and tries again (owner:
block, feed the output back).

Author's call — run after writing, then roll back, rather than on a temp
copy: formatters and linters resolve config from the file's real location
(`.prettierrc`, `pyproject.toml`), which a temp path breaks. The rollback
uses the pre-image the diff code already reads.

`edit_check` commands do not prompt: the trusted scenario authorized them.
They are logged as events.

### `check`

- **`jas run`:** after the model's final answer, if the run changed
  anything (below), jas runs `check`. Pass: exit 0 as today. Fail: the
  output goes back to the model for **one** fix round (like the farm
  integrator's); then `check` runs again, and a second failure exits **6**
  (the existing "a check run by jas failed" code). `--no-check` skips it. A
  repo with no scenario, or a scenario without `check`, behaves exactly as
  today.
  Author's calls: the check and fix round live in `cmd/jas`'s `jas run`
  path, not in the core loop, so farm workers never get them (the farm
  runs its own `part_check` and `check`). The fix round is the same
  session: it counts toward `max_turns`, `--max-tokens` and `--timeout`,
  and a cap hit during it exits with that cap's code (4, 7 or 9), not 6.
  With `--json` the `check_ran` events show each round; the printed
  final answer is the fix round's when there was one.
- **"Changed anything"** (author's call): a `write` or `edit` succeeded
  (the executor's existing touched tracking), or, in a git repo, `git
  status --porcelain` differs from its snapshot at the start of the run
  (catches changes made through `shell` or `project`). Outside git, only
  `write`/`edit` count. The git snapshot is `jas run` only: in the chat,
  "a turn that changed files" means the executor's touched tracking alone,
  since the owner edits files while the session runs and a git diff would
  trigger checks on their changes.
- **Chat:** `/check` runs `check` on demand and prints a one-line
  verdict; on failure it offers to send the output tail to the model. The
  setting `scenario_check` (`off` default, `turn`) opts in to running it
  automatically after a turn that changed files; a failure is then sent to
  the model as the next user message, once per turn (no loop). Off by
  default (owner): `jas run` and the farm check either way, so the chat
  setting only decides interactive noise, and a full suite after every turn
  makes pair programming slow. The check runs in a `tea.Cmd`, never in
  `Update`, and its events are logged from a command too (the
  `core.NewUILog` → bridge deadlock rule in `AGENTS.md`).
- **Farm:** `check` is the farm's verdict, as `farm.yaml`'s `check` is
  today. See Farm.

### Tools

Scenario tools are exposed to the model as **one** tool, `project`, with a
`name` argument and the scenario's tools listed in its description
(author's call: one tool keeps the tool list, and the cached prompt prefix,
stable when tools change). With no scenario tools, `project` is not
registered. A `project` call counts as a `shell` call for the approval
levels and grants (grant key: `project <name>`), except `approve: always`
(above). Output and redaction follow `shell`.

Grants wiring is new code, not reuse: `core/grants.go` `Covers` and `Add`
switch on `shell`/`edit`/`write` and return false otherwise, so `project`
needs its own case (key `project <name>`). An `approve: always` tool must be
excluded from both `Covers` and `Add` (the risky-command checks in `Add`
will not catch it), and `core/approve.go` must prompt for it at every level.

An `approve: always` tool that cannot be asked (a headless `jas run`, or a
farm planner) is refused before any prompt as a tool error the model sees,
not exit 5 (author's call, following the memory-write precedent), so adding
such a tool never kills a CI run. Farm workers send it to the farm's
approval gate as they do any gated call.

### Prompt orientation

When a scenario is trusted, one line goes in the system prompt after the
project's `AGENTS.md`:
`Repo: server (typescript, nestjs, prisma, postgres). Protected: prisma/migrations/**, dist/**. Check: npx prisma validate && npm run lint && npm test.`
It is built only from the file, so the cached prefix changes only when the
file does.

## Types: what they do

`types` drive three things in v1, ranked (owner-agreed). None of them
changes enforcement. (Type skill packs are deferred; see Deferred.)

1. **`jas spec` interview shape** (#157): a `web` project is asked about
   pages, a `cli` about commands, a `library` about its public API. This is
   how #157 avoids forcing Data model / API / Pages on every project.
2. **Bootstrap defaults**: which `protect`, `edit_check`, `check` and tools
   the draft proposes.
3. **Prompt orientation**: the one line above.

## Farm (approach A)

`farm.LoadConfig` reads the trusted scenario as a layer between the global
and project `farm.yaml`:

```
project .jas/farm.yaml   check / generated / vars.build / vars.lint   (wins; kept for compatibility)
.jas/scenario.yaml       check / generated / tools.build / tools.lint
~/.jas/farm.yaml         (global defaults)
farm.Detect              (marker-file fallback)
```

When both the project `farm.yaml` and the scenario set the same key, the
farm uses `farm.yaml` and prints a notice once
(`.jas/farm.yaml check overrides .jas/scenario.yaml check; move it to the scenario`).
`part_check` still defaults to `check`. `protect` and `edit_check` apply
inside workers' worktrees too. `jas farm vars` shows which file each value
came from.

`generated` follows the same layering as a whole list (author's call): a
project `farm.yaml` `generated` (including `[]`) wins over the scenario's; a
scenario `generated` (including `[]`, which turns detection off for this
repo) wins over `~/.jas/farm.yaml`'s, so a global `[]` no longer suppresses
a scenario's list; with neither, detection runs as today.

Write protection of generated files (author's call): only paths the
**scenario** lists are denied to `write`/`edit`, so a repo without a
scenario keeps today's behavior. In a farm this applies to every worker,
including one whose `owns` covers the file: hand-editing a lockfile is
never right, and the regenerate step still runs after the merge. The farm
section of `docs/manual.md` says so. Paths listed only in `farm.yaml`'s
`generated` (or detected) are deliberately **not** edit-protected; that is
the compatibility trade for repos without a scenario.

## `jas bootstrap`

`jas bootstrap [--no-llm] [--json] [--trust-project]` in an existing repo.
`--trust-project` only trusts the repo's existing project context
(`AGENTS.md`, settings) for the study session, as on other commands. It
never trusts the drafts bootstrap writes and never applies `AGENTS.md`
additions without a `yes` (author's call).

### Output

1. **`.jas/scenario.yaml`** — a draft, with a header comment
   (`# drafted by jas bootstrap <date> — review, then trust`) and one
   evidence comment per line (`# go.mod + cmd/ + spf13/cobra → cli`). If the
   file already exists, the draft goes to `.jas/scenario.draft.yaml` and
   bootstrap prints a diff; it never overwrites.
2. **`AGENTS.md`** (owner):
   - missing → bootstrap drafts one with the repo's conventions (the
     libraries that are not stacks, layout, commands) and writes it;
   - present → bootstrap shows the proposed additions as a diff and applies
     them only on a `yes`; no terminal → the diff is printed, nothing
     changes. Never a silent rewrite: other tools read the same file.
3. **A report** on stdout: the detected types and stacks with evidence, what
   was put in each field and why, and the next step (`review the files, then
   start jas to trust them`). `--json` prints the report and both drafts as
   one JSON object for CI.

Nothing it writes is active until the project is trusted; writing the file
changes its hash, so the trust prompt asks on the next start.

### How it works (LLM-assisted, owner)

1. **Detect (deterministic, no model).** Collect facts: marker files
   (`go.mod`, `package.json`, `pyproject.toml`, `Cargo.toml`, `*.tf`,
   `Chart.yaml`, …), dependencies from manifests, top-level layout
   (`cmd/`, `app/`, `pages/`, `migrations/`, `data/`, `experiments/`,
   `*.ipynb`), script entries (`package.json` scripts, `Makefile` targets),
   lockfiles (reuse `farm.DetectGenerated`), the existing `check`
   (reuse `farm.Detect`), and `.mcp.json` presence (reported, not
   converted). Each fact keeps its source path. A signal table maps facts
   to candidate types:

   | type | strong signals |
   |---|---|
   | web | `next`/`vite`/`react`/`vue`/`svelte` deps, `app/` or `pages/`, `index.html` |
   | server | `express`/`nestjs`/`fastapi`/`django`/`gin`/`axum`/`actix`, `ListenAndServe`, `openapi.yaml`, Dockerfile `EXPOSE` |
   | cli | Go `cmd/` + `cobra`/`flag`, `package.json` `bin`, `[project.scripts]`, Rust `clap` |
   | library | no entry point, publish config (`exports`, PyPI metadata, crate `[lib]`) |
   | ml | `torch`/`tensorflow`/`keras`/`sklearn`, `*.ipynb`, `data/`, `experiments/`, MLflow/W&B config |
   | infra | `*.tf`, `Chart.yaml`, Kubernetes manifests, `Pulumi.yaml`, Ansible playbooks |

2. **Study (LLM).** A session on the configured model with **read-only
   tools** (`read`, `grep`, `ls`; no `shell`, `write`, `edit`), given the
   facts, the signal table's candidates and the per-type/stack defaults. It
   reads the README and key files to settle ambiguity (is `app/` a Next.js
   app or a Python package?), picks types and stacks, chooses commands that
   actually exist (`npm run lint` only if the script exists), and returns
   the draft as a structured final answer: scenario fields, an evidence
   string per field, and the `AGENTS.md` draft or additions. Limits: its
   own `max_turns` and token budget (defaults in the plan); a budget stop
   falls back to step 1's draft.
3. **Validate and write.** jas validates the returned scenario with the
   same loader as runtime (unknown keys, globs, commands non-empty) and
   checks that every `tools`/`check` command's first word names a script,
   target or binary detection found or that is on `PATH`; an invalid answer
   gets one retry with the errors, then falls back to step 1's draft.

`--no-llm`, or no model/key configured, stops after step 1 and writes the
deterministic draft with a notice. The deterministic draft must be useful
alone (author's call: CI, offline and first-run without auth).

### Per-type and per-stack defaults

Data shipped with jas (embedded YAML, one entry per type and per stack),
not code. Examples, not exhaustive:

| key | contributes |
|---|---|
| stack `go` | `edit_check` `gofmt -l {file}` on `**/*.go`; `check` from `farm.Detect` |
| stack `rust` | `edit_check` `rustfmt --check {file}`; `check` `cargo clippy -- -D warnings && cargo test`; protect `target/**` |
| stack `typescript` | `edit_check` prettier if configured; `check` adds `npx tsc --noEmit` |
| stack `python` | `edit_check` `ruff format --check {file}` if ruff is configured; `check` `pytest -q` |
| stack `prisma` | protect `prisma/migrations/**`; tools `generate`, `migrate` (`approve: always`) |
| type `web` | protect `dist/**`, `build/**`; tool `build` |
| type `server` | protect `migrations/**` when present |
| type `ml` | protect `data/raw/**`, model weight files (`*.keras`, `*.pt`, `*.ckpt`) |
| type `infra` | protect `*.tfstate*`, `.terraform/**`; tool `plan` (`terraform plan`), never `apply` |

A default is proposed only when its evidence exists (a `.prettierrc`, a
`migrations/` folder). Adding a type or stack is a data change.

## Reuse in a new project (`jas spec --from`)

Owner flow 2: study an existing repo, then start a new one with the same
setup. The scenario package exposes:

```go
// Seed is what a new project inherits from an existing repo's setup.
type Seed struct {
    Scenario    Scenario // types, stacks, check, edit_check, protect, tools
    Conventions string   // the AGENTS.md text to carry over, if any
    Source      string   // path of the repo it came from
}
func LoadSeed(repoRoot string) (Seed, error)
```

`jas spec --from <repo>` (#157) prefills its interview from a `Seed`, asks
only what is new, and writes the new project's `.jas/scenario.yaml`,
`AGENTS.md` and spec. No code file is copied (#9). Paths in `protect`,
`generated` and `edit_check` that do not exist in the new project are kept,
since the project starts empty (author's call). The interview design is
#157's.

## Event log

New events (names for the plan to confirm; `TestManual_*` requires each in
`docs/manual.md`; each is also documented in `docs/event-log.md`):

- `scenario_loaded` — path, hash, types, stacks.
- `protect_denied` — path, rule.
- `edit_check_ran` — path, command, exit code, duration, rolled back (bool).
- The end-of-run check reuses the existing **`check_ran`**
  (`core.EvCheckRan`, already read by `dream/evidence.go`) with a new
  `round` field (1, or 2 for the check after the fix round). The farm's own
  `check_ran` events omit `round` (the field is additive and optional), so
  `dream/evidence.go` and existing readers are unaffected. No separate
  event: one concept, one event, and no clash with the `scenario_check`
  setting.
- `bootstrap_result` — types, stacks, fields written, llm used (bool),
  fallback reason.

Command output is redacted like `shell` output.

## Docs

Per `AGENTS.md`: `docs/manual.md` gains a Scenarios section, `jas bootstrap`
under Commands, `--no-check`, the `scenario_check` setting, the `/check`
chat command, the `project` tool, the events, the exit-code row for 2, the trust prompt change and the farm layer; `README.md`
mentions scenarios and bootstrap in the overview;
`cmd/jas/manual/jas/SKILL.md` gets the summary, including `/check` and
`jas bootstrap` (`TestManualSkill_*` fails otherwise); `docs/event-log.md`
documents the new events and `check_ran`'s `round`; `docs/design.md` §8 and
§8.3 shrink to a pointer to this document.

## Testing (for the plan)

- Loader: table tests for every validation error and an empty file.
- Grants: `project <name>` grant covers only that tool; an `approve: always`
  tool is never covered or added, prompts at `never`, and is a tool error
  headless.
- Protect: refused write/edit for protect and generated paths, including
  with `Writes == nil` (no write limit), the deny floor still wins, farm workers inherit the rules.
- `edit_check`: failing check restores the pre-image and deletes a new
  file; config resolution from the real path works (fixture with a
  `.prettierrc`-style config file read by a fake checker).
- `check`: `jas run` exits 0 / fix round / 6; a cap hit in the fix round
  exits 4/7/9; `--no-check`; `/check` in the chat; `scenario_check: turn`; no scenario →
  unchanged behavior and exit codes (regression test against today's
  fixtures).
- Trust: untrusted scenario ignored with notice; hash change re-asks.
- Farm: precedence table above, the override notice, `jas farm vars`
  provenance.
- Bootstrap: deterministic detection on fixture repos for each example
  below (golden drafts); the LLM step against a scripted fake provider
  (valid answer, invalid then valid, budget stop → fallback); never
  overwrites; `AGENTS.md` missing vs present; `--json` shape.
- `go test -race ./...` green, `CGO_ENABLED=0`.

## Worked examples

**React app** (react, tailwind, zustand, react-query, shadcn):

```yaml
types:  [web]
stacks: [typescript, react, vite, tailwind]
check: npm run lint && npx tsc --noEmit && npm test
edit_check: [{glob: "src/**/*.{ts,tsx}", run: "npx prettier --check {file}"}]
protect: ["dist/**"]
generated: [{path: package-lock.json, regenerate: "npm install --package-lock-only --no-audit --no-fund"}]
tools: {build: {run: npm run build}}
```
`AGENTS.md` gets: zustand for client state, react-query for server state,
shadcn components in `src/components/ui/` (extend, don't fork).

**NestJS API** (nestjs, prisma, postgres): the example under The scenario
file.

**TensorFlow prediction model** (python, pandas, seaborn, tensorflow):

```yaml
types:  [ml]
stacks: [python, tensorflow]
check: pytest -q
edit_check: [{glob: "**/*.py", run: "ruff format --check {file}"}]
protect: ["data/raw/**", "models/**/*.keras"]
generated: [{path: uv.lock, regenerate: "uv lock"}]
tools: {train: {run: "uv run python train.py", approve: always, timeout: 2h}}
```
`AGENTS.md` gets: analysis in pandas, plots with seaborn saved to
`reports/figures/`.

**New Rust server** (no repo yet): `jas spec` asks what and in which
language, and writes `types: [server]`, `stacks: [rust]` with the rust and
server defaults.

## Deferred

- **Type skill packs** (owner: later). Built-in skills, one per type
  (`jas-server`, `jas-ml`, …), installed under `<UserCacheDir>/jas/` like
  the manual skill and listed in the catalog only when the scenario names
  that type; a same-named profile or project skill overrides one. Examples:
  server → migration safety, API compatibility; ml → fix seeds, never tune
  on the test split, log every run; infra → plan before apply, never touch
  state files. Ships as its own small change: the catalog wiring plus the
  first pack (the owner's most-used type), then one pack at a time, each
  pack's text approved by the owner.

- `mcp:` block (needs the MCP client).
- Background tools (`dev` servers) and screenshot tools for `web`.
- General hooks beyond checks.
- `protect` for reads (keeping huge folders out of context).
- More types: `mobile`, `data-pipeline`, `desktop`, `embedded`.
- Monorepo per-folder scenarios.

## Left to the builder

- Package layout (`scenario/` at the repo root, matching `farm/`,
  `memory/`), YAML library (the one `farm` uses), and how the defaults data
  is embedded.
- Exact event field names, notice wording, the bootstrap session's
  structured-answer schema and its budget defaults.
- Shell quoting of `{file}` on each platform.
