# Testing a skill

Test every new or changed skill before calling it done. Three checks, from
cheapest to most telling.

## 1. It loads

In the chat, ask the user to run `/reload` (it re-reads skills into the
running session), then check the new name is in the start banner's skill
list, or ask them to run `/skill:<name>`. jas prints a warning for a bad name,
a name that differs from its folder, a missing or over-long description, or
a project skill symlinked outside the project. Fix every warning.

A project skill also needs the project to be trusted; after the edit, jas
asks again on the next start or `/reload`.

## 2. It triggers

Write three short test prompts: two the skill should handle, phrased the way
the user would (not quoting the description), and one near miss it should
not handle. Run each in a fresh one-shot session and see whether the model
opened the skill:

```sh
jas run --json "release v1.4.0 with the usual notes" > /tmp/skill-test.jsonl
grep '"tool_started"' /tmp/skill-test.jsonl | grep '<name>/SKILL.md'
```

A match means the model read the skill. For a project skill add
`--trust-project` when the run has no terminal to answer the trust prompt.
These runs call the model and cost tokens, and a run that changes files will
really change them: ask the user before running, and use prompts that only
read or plan when the skill's job is destructive.

If a should-trigger prompt misses, rewrite the description (more of the
user's words, the file types and tools involved) and run again. If the near
miss triggers, narrow the description or add a "Not for …".

## 3. It works

Run one realistic task with the skill, or ask the user to, and compare the
result with what they expected. Read the event log or transcript, not just
the final answer: look for steps the model skipped, guessed, or did in the
wrong order, and for files it went looking for that the skill should have
named. Each of those is an instruction to add or sharpen.

Report what you tested, what failed, and what you changed. Do not claim the
skill works on the strength of a run you did not do.
