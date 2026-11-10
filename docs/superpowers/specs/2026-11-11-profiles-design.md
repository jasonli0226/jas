# Profiles

Status: draft, 2026-11-11. Owner decisions are marked **(owner)**; calls made
without the owner are listed at the end.

## Why

The owner uses one agent for different kinds of work: software development on
some projects, data science on others. With one global skill set, adding the
data-science skills mixes them into every software session, and the reverse.
pi has the same problem (one `~/.pi/agent`) and only solves it by swapping the
whole config dir with `PI_CODING_AGENT_DIR`, which also splits auth and
sessions and needs symlinks to share them.

A **profile** is a named, user-owned bundle of skills, instructions and
settings. jas runs with exactly one active profile. Switching profiles changes
what the agent knows how to do, without touching auth, trust or session
history.

Profiles are also the container for future per-profile **tools** and
**agents** (see Non-goals): the layout reserves room for them so they slot in
without a redesign.

## Goals

- Named profiles under the jas home, one of them active per session.
- A `default` profile that always exists **(owner)**.
- Pick a profile per run (`--profile`, `JAS_PROFILE`), change the saved
  default (`jas profile use`), and switch in chat (`/profile`) **(owner)**.
- A profile holds user skills, an `AGENTS.md` and a settings layer.
- User skills come **only** from the active profile: jas stops reading
  `~/.claude/skills`, `~/.agents/skills` and `~/.config/jas/skills` as skill
  sources **(owner)**.
- The project's `AGENTS.md` takes precedence over the profile's **(owner)**.
- Switching in chat keeps the conversation and reloads skills and
  `AGENTS.md` **(owner)**.
- The chat always shows the active profile **(owner: "there should be ui to
  display the profile")**.

## Non-goals (this cut)

- Per-profile tool allowlists. Reserved: a future `tools` setting in the
  profile's `config.json`, using the same filter farm `roles.<name>.tools`
  already uses.
- Per-profile agents (chat-usable versions of farm `roles`). Reserved: a
  future `agents/` directory or `roles` key in the profile.
- Profile-scoped auth, trust or sessions. They stay shared in the jas home.
- Importing or syncing skills from other agents' folders.
- Changing project skill discovery: `<root>/.agents/skills` and
  `<root>/.claude/skills` keep working, under project trust, as today.

## Anti-goals

- A profile must never loosen safety: the deny floor, redaction, approval
  ladder and project trust behave the same under every profile.
- No fullscreen or alt-screen UI for profiles; everything stays inline.
- `jas run`, `--json` and exit codes keep working for people who never create
  a profile (they run on `default`).
- No second copy of auth or sessions per profile (the pi pain this avoids).

## Behaviour

### Layout

```
~/.jas/                      ($JAS_HOME)
  config.json                global settings; holds `profile` (the saved default)
  auth.json  trust.json  sessions/  farm.yaml      shared, unchanged
  profiles/
    default/
      config.json            profile settings layer (optional)
      AGENTS.md              profile instructions (optional)
      skills/                profile user skills (optional)
    ds/
      ...
```

A profile is a directory under `profiles/`. Its name must match the skill
name rule (`^[a-z0-9]+(-[a-z0-9]+)*$`, at most 64 characters). Every file in
it is optional. `default/` is created (empty) on first start if missing.
Creation is best effort: on a read-only home (CI, sandbox) a missing
`default/` is treated as an empty profile in memory, with no warning and no
exit, so `jas run` and `--json` never fail because of it.

### Choosing the active profile

`profile` is a normal setting. It resolves like any other key: flag
`--profile` > `JAS_PROFILE` > local > project > global > built-in `default`.
Because project and local config can set it, a data-science repository can
commit `.jas/config.json` with `"profile": "ds"` and every session there
starts on `ds` (only once the project is trusted, like any project setting).
The profile's own `config.json` cannot set `profile` (ignored, with a
warning).

An unknown profile name (no such directory) from `--profile` or
`JAS_PROFILE` is a usage error at start (existing usage exit code), naming
the available profiles. An unknown name from project, local or global config
warns and falls back to `default`, so a repository that commits
`"profile": "ds"` still runs in CI and on machines without a `ds` profile.
Resolution therefore keeps which layer chose the profile. `default` never
fails (see Layout).

### Settings layers

The profile's `config.json` is a new layer between global and project:

flags > env > local > project > **profile** > global > defaults

The same order applies to the `models.*` and `providers.*` maps
(`Layers.Merged`, `ModelSource`): local > project > profile > global.

`jas config` and `/settings` gain an `--in-profile` scope that writes the
active profile's `config.json` (named so it cannot be confused with the
run-level `--profile <name>`; write a non-active profile with
`JAS_PROFILE=ds jas config set --in-profile …`, since `config` is a
subcommand and takes no run flags). Sources print as
`profile config (ds)`. The profile layer needs no trust: it lives in the jas
home, which only the owner writes.

**Keys a profile may not set (owner):** `provider`,
`base_url` and `search_base_url` stay global-only, as today. A trusted
project can pick a profile, so a profile that could set an endpoint would let
a repository steer the API key to it. A profile setting one of these is
ignored with a warning. `profile` itself is also ignored in a profile layer.

### Skills

User skill sources become exactly one: `<home>/profiles/<active>/skills/`.
Project sources are unchanged and still listed first (a project skill wins a
name clash, as today).

The deny floor covers `~/.jas/**` (`core.DenyFloor`), and
`denyPatterns(home)` adds an absolute pattern for the jas home when
`JAS_HOME` moves it. Today both make discovery skip profile skills and the
read tool refuse their files.

**Read exception (owner):** only the **active**
profile's `<home>/profiles/<active>/skills/` is readable, and only by
skill discovery and read-only tools (`ExecPolicy.ResolveRead` and the
read-root walk). `matchDeny` has no negation, so this is a code-level
exception, not a pattern: it lets the two home patterns (the `~/.jas/**`
floor entry and the `JAS_HOME` absolute one) pass for paths inside that one
directory. Everything else keeps the full floor:

- `Resolve` (write, edit) still refuses the whole jas home, profile skills
  included.
- The shell tool's best-effort filter (`DeniedToken`) still refuses
  `~/.jas/**`, profile skills included. This read/shell difference is
  deliberate: the model reads skills with the read tool, and the shell
  filter is not widened. Do not "fix" it.
- Other profiles' skills, profile `config.json` and `AGENTS.md`,
  `auth.json` and the rest of the home stay denied.
- The floor's other patterns (`.env*`, keys, `secrets/**`) still apply
  inside a skill, via the read-root scoping in `ResolveRead`.

The exception follows the active profile, so `/profile` rebuilds it, and
farm workers get the session's. This change goes through `/security-review`.

Profile skills are not part of the project trust bundle hash, the same as
user skills today. That is correct: they are owner-written. Do not add them.

A profile skill that resolves (via symlink) outside the home keeps today's
user-skill rule: it loads, with a warning.

**Built-in manual skill.** The `jas` manual skill is no longer installed into
`~/.config/jas/skills/`. It is installed under the user cache dir
(`<UserCacheDir>/jas/manual/jas/`) and added as a built-in source in every
profile, so it is always available. A same-named profile or project skill
overrides it, as a same-named shared skill overrides it today.

### AGENTS.md

Both files load into the system prompt: the profile's `AGENTS.md` first, then
the project's `AGENTS.md` (or `CLAUDE.md`), so on any conflict the project's
later text wins **(owner: project > profile)**. The profile file needs no
trust. The project file keeps today's trust rules. A missing profile file
contributes nothing.

### Chat

- **Status line:** the profile name is the first field, before the model:
  `ds · z-ai/glm-5.3 · shell · 12% · ↑3k ↓1k · jas (main)`. `default` is shown
  too, so the field never jumps in and out.
- **Banner:** the startup banner names the active profile next to the
  context files and skills it already lists.
- **`/profile`** with no argument opens a picker (like `/model`), listing
  profiles with the active one marked.
- **`/profile <name>`** switches for this session only. It keeps the
  conversation, then reloads like `/reload` does: profile skills, the
  profile's `AGENTS.md`, the project context and read roots. The profile's
  settings layer is re-applied with today's live-settings rules: `approve`,
  `model`, `hints` and `farm_bell` change now; other keys wait for `/new`, and
  a one-line notice says which keys are waiting. A `model` on another
  provider is refused the way `/model` refuses it, with the same notice.
  Like `/reload`, it waits until the current run finishes, and it goes
  through `/reload`'s trust flow (re-asking when project trust must be
  re-confirmed).
- **The switch sticks for the rest of the chat:** the active profile is
  mutable chat state. `/new`, `/resume` and a later `/farm` stay on it. It is
  not saved as the default.
- **Ctrl+S in the picker** also saves the choice as the default (writes
  `profile` to global config), matching the `/model` picker convention.
  Today `pickSetDefault` is hard-wired to `setDefaultModel`; the picker's
  default action needs to become per-picker.
- **During a running farm:** agents already running keep the profile they
  started with. Only workers started after the switch get the new one.
- The switch result is one line: `profile ds · AGENTS.md loaded · 4 skills`.

### CLI

- `jas --profile <name>` / `JAS_PROFILE` for chat and `jas run`.
- `jas profile list` — names, active one marked, with skill counts.
- `jas profile use <name>` — sets `profile` in global config.
- `jas profile new <name>` — creates the empty directory.
- `jas profile show [name]` — its path, settings, `AGENTS.md` presence,
  skills.

`--json` output of `jas profile list` follows the existing `--json`
conventions.

### Farm

Farm workers run on the session's active profile: same skills, same profile
`AGENTS.md`, same profile settings layer. Roles stay as configured in
`farm.yaml`.

### Events

- `session_started` gains a `profile` field.
- New `profile_changed` event (`from`, `to`, reloaded context files, skills,
  settings deferred to `/new`) for `/profile`. It is logged from a `tea.Cmd`,
  never from `Update` (AGENTS.md constraint).

### Resume

`/resume` uses the chat's active profile. `jas --resume` at start uses the
resolved profile (flags, env, config). Neither switches to the profile
recorded in the session. The recorded name is shown in the resume notice
when it differs.

### Migration

This removes `~/.claude/skills`, `~/.agents/skills` and
`~/.config/jas/skills` as user skill sources, which is a behaviour change.
On start, if the active profile has no skills and any of those folders holds
skills, jas prints a one-time notice (per profile, remembered in the jas
home) naming the folders and the profile's `skills/` path. Nothing is copied
or moved automatically. The old manual copy in `~/.config/jas/skills/jas/`
is left in place; the notice mentions it can be deleted.

## Docs

Same commit as the code, per AGENTS.md: `docs/manual.md` (new CLI command,
flag, setting, chat command, scope, event fields, Files table, Project
context and skills section), the embedded manual skill copy (`make manual`),
`README.md` (overview), `docs/design.md` where config layers are
described, and the repo's own `AGENTS.md`, which today says the manual is
re-installed under `~/.config/jas/skills/`.

## Tests (the builder's minimum)

- Profile resolution order, including project config selecting a profile and
  the profile layer ignoring its own `profile` key.
- Unknown profile → usage error; missing `default` → created.
- Skill discovery reads only the profile's `skills/` plus the built-in manual
  and project sources; home `~/.claude/skills` and `~/.agents/skills` are
  ignored.
- Deny-floor exception: read tool opens an active-profile skill file;
  refuses another profile's skill, `profiles/<name>/config.json`,
  `auth.json`, a `.env` inside a profile skill; write and edit into a
  profile skill are refused; the shell filter refuses a profile skill path;
  a path traversing out of `skills/` is refused; all of it also with
  `JAS_HOME` moved out of `~/.jas`.
- Unknown profile: usage error from flag and env, warning and `default`
  from config. Read-only home with no `default/` still runs.
- A profile setting `provider`, `base_url` or `search_base_url` is ignored
  with a warning.
- System prompt order: profile `AGENTS.md` before project `AGENTS.md`.
- `/profile <name>` in chat keeps history, swaps the skill catalog and
  `AGENTS.md`, applies live keys and reports deferred ones; status line shows
  the new name.
- `session_started.profile` and `profile_changed` events.
- `TestManual_*` stays green with the new flag, setting, command and events.

## Left to the builder

- Package boundaries (a small `profiles` package, or inside `settings`).
- Picker details and status-line styling, within the inline constraint.
- Exact wording of notices and errors.
- How the one-time migration notice is remembered.

## Calls made without the owner (check these)

1. Profiles live under `~/.jas/profiles/` and the deny floor gets a narrow
   read-only exception for `profiles/*/skills/`. The alternative is
   `~/.config/jas/profiles/`, which avoids touching the floor but splits jas
   state across two homes.
2. `profile` is a normal setting, so a project's `.jas/config.json` can pick
   a profile.
3. Settings order puts the profile layer between global and project.
4. The manual skill moves to the user cache dir and loads in every profile.
5. `/profile <name>` lasts for the rest of the chat (`/new`, `/resume`,
   `/farm` keep it) but is not saved; Ctrl+S in the picker saves it as
   default.
6. Resume uses the active profile, not the recorded one.
7. No automatic migration; a one-time notice instead.
8. Farm workers inherit the session's profile; running ones keep theirs on
   a switch.
9. Unknown profile: error from flag and env, warn and fall back from config.
10. The config scope flag is `--in-profile`.

Decided by the owner after review (2026-11-11):

- A. The read exception covers only the active profile's skills, for
  discovery and the read tool; the shell filter keeps refusing.
- B. `provider`, `base_url`, `search_base_url` stay global-only; a profile
  cannot set them.
