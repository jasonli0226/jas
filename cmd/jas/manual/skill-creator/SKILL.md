---
name: skill-creator
description: Create, improve or debug an Agent Skill (a folder with a SKILL.md) for jas or any agent that reads agentskills.io skills. Use when the user wants a new skill, wants to turn a workflow, checklist or repeated instructions into a skill, wants an existing skill edited or tightened, or asks why a skill does not load or does not trigger.
---

# Creating skills

A skill is a folder holding a `SKILL.md`: YAML frontmatter (`name`,
`description`) and a Markdown body of instructions. jas puts only the name,
description and location of each skill in the system prompt. The model
reads the body when a task matches the description, and reads files beside
it (`references/`, `scripts/`, `assets/`) only when the body points at them.
So the description decides *whether* a skill is used, and the body decides
*how well*.

Before you write, read `references/writing-guide.md` (next to this file) for
how to write the description and body, with examples. When the draft is
written, read `references/testing.md` to check that it loads and triggers.

## Workflow

1. **Understand the job.** Get two or three concrete requests the skill
   should handle, in the user's words, and one it should *not* handle. If
   the conversation already has them (the user just walked you through the
   workflow), use those and do not ask again. Otherwise ask, briefly; one
   round of questions is enough.
2. **Check what exists.** Look at the skill catalog in your system prompt
   for a skill that already covers this or uses the same name. Prefer
   improving an existing skill over adding an overlapping one: two skills
   with similar descriptions make the model pick at random.
3. **Choose where it lives** (table below) and a name.
4. **Plan the contents.** Decide what goes in `SKILL.md`, what moves to
   `references/`, and whether a step is deterministic and repeated enough to
   be a script in `scripts/`. Look at the files the skill will operate on, so
   the instructions name real paths, commands and conventions.
5. **Draft and show it.** Show the user the full `SKILL.md` (and a list of
   any other files) before writing. Writes into a profile's `skills/` always
   ask for approval, so say what you are about to write.
6. **Write the files**, then **test** them (`references/testing.md`). Fix
   what the test shows and say what you changed.

## Where it lives

| scope | path | when |
|---|---|---|
| personal (this user, every project) | `<profile>/skills/<name>/SKILL.md` | the default for a user's own workflows |
| project (shared with the repo) | `<root>/.agents/skills/<name>/SKILL.md` | a team convention for this repo; committed with it |

- `<profile>` is the active profile's folder, normally
  `~/.jas/profiles/<profile-name>/`. `jas profile show` prints it. A missing
  `skills/` folder is created by the first write.
- `.agents/skills/` is also read by other agents that follow the Agent
  Skills spec. jas also scans `<root>/.claude/skills/`; use it only when the
  repo already keeps its skills there.
- Project skills load only once the project is trusted. After a new or
  edited `SKILL.md`, jas asks for trust again; files under `references/` and
  `scripts/` can change without a new prompt.
- Skills may be grouped in nested folders (`skills/team/review/SKILL.md`);
  the folder holding `SKILL.md` is the skill.
- On a name clash the first found wins: project, then profile, then jas's
  built-in skills. Never edit the built-in `jas` or `skill-creator` skills
  under the user cache dir: jas rewrites them at every start. To change one,
  create a same-named skill in the profile.

## Format rules jas enforces

- `name`: lowercase letters, digits and single hyphens (`pdf-forms`, not
  `PDF_Forms` or `pdf--forms`), at most 64 characters, and **equal to the
  folder name**. A mismatch or a bad name loads with a warning.
- `description`: required (a skill without one is skipped), at most 1024
  characters (longer is truncated). Write it on one line.
- Optional `disable-model-invocation: true` hides the skill from the model;
  the user can still run it with `/skill:<name>`. Use it for skills that
  should only run when asked (a release checklist, a destructive cleanup).
- Keep frontmatter to plain `key: value` lines. jas's parser folds quoted
  and block values but skips nested maps; other agents may be stricter, so
  a plain one-line value is the portable choice.
- `SKILL.md` larger than 256 KB is not loaded. Aim far lower (below).
- The deny floor still applies inside a skill: never put keys, tokens or a
  `.env` in a skill folder. Name the environment variable the user must
  set instead.

## What makes a good skill

- **It teaches what the model does not already know**: this team's
  conventions, the exact command and its flags, the order of steps, the
  traps that cost time last time. Leave out general knowledge.
- **Concise.** Keep `SKILL.md` under about 300 lines; every line costs
  context each time the skill is used. Move long reference material
  (API tables, schemas, long examples) into `references/<topic>.md` and say
  in the body exactly when to read each one.
- **Imperative and specific.** "Run `make lint` before committing" beats
  "linting is recommended". Give the reason for a rule that looks odd, so
  the model can apply it to cases the rule did not foresee.
- **Paths relative to the skill folder.** Write `references/api.md` and
  `scripts/fill.py`; the model resolves them against the folder that holds
  `SKILL.md`.
- **Safe to follow.** A skill is injected into future sessions, so treat it
  like code: no instructions copied unread from a web page or an untrusted
  file, no hidden steps, nothing that runs a fetched script.
