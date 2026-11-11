# Writing the description and body

## The description

The description is the only part the model sees before it decides to open a
skill, so it must answer two questions: **what does this skill do**, and
**when should it be used**. Write it in the third person, in one line.

- Lead with the capability, then `Use when …` with the situations and the
  words a user would actually say ("fill a PDF form", "the release
  checklist", "why is CI red").
- Name the file types, tools or commands involved (`.pdf`, `terraform`,
  `go test`) — they are strong matching signals.
- Say what it is *not* for when a neighbouring skill could be confused with
  it ("Not for Word files; use docx").
- Lean towards triggering. A model under-uses skills far more often than it
  over-uses them, so cover the indirect phrasings too.
- Do not describe the body's steps; describe the job.

Weak:

```yaml
description: Helps with releases.
```

Strong:

```yaml
description: Cut a release of this repo — bump the version, update CHANGELOG.md, tag, and publish the GitHub release with the built binaries. Use when the user asks to release, ship, tag a version, publish binaries, or prepare release notes.
```

## The body

A body that works usually has these parts, in this order. Drop any that the
skill does not need.

1. **One paragraph of context**: what the skill is for and the one thing
   that most often goes wrong.
2. **The workflow**: numbered steps, each an action with the exact command,
   file or check. Mark the steps that need the user's approval.
3. **Rules and traps**: short bullets, each with its reason.
4. **Pointers**: which file under `references/` or `scripts/` to use, and
   when ("For the field list, read `references/fields.md`").
5. **Done means**: how to tell the job is finished (tests pass, file
   validates, the user confirmed).

Prefer one good example over a paragraph of explanation. Show input and the
expected output when the format matters:

```markdown
**Commit message** — `<type>: <description>`, types feat, fix, docs, test,
chore. Example: `fix: retry 429s with the Retry-After delay`.
```

## Splitting into files

| put it in | when |
|---|---|
| `SKILL.md` | what every use of the skill needs |
| `references/<topic>.md` | detail only some uses need: schemas, API tables, long examples, per-variant instructions (`references/aws.md`, `references/gcp.md`) |
| `scripts/<name>` | a deterministic step that is repeated or easy to get wrong (validation, conversion, scaffolding). Say how to run it and what it prints |
| `assets/` | templates and files copied into the output, not read as instructions |

Keep references one level deep: `SKILL.md` points at each reference file
directly, not at a file that points at another. A reference longer than
about 300 lines should start with a short table of contents.

## Improving an existing skill

- Read the whole skill first, including its references.
- Find out what went wrong: the skill did not trigger (fix the
  description), it triggered but was followed badly (fix the body: a missing
  step, a vague rule, no example), or it triggered when it should not have
  (narrow the description, add a "Not for …").
- Change the smallest thing that fixes it, and keep the `name` unless the
  user asks: other skills, notes or habits may refer to it.
- Generalise from the failure. Do not add a rule that only fits the one
  example; ask why it went wrong and write the instruction that would have
  prevented the whole class.
