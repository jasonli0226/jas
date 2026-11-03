# Farm dogfood — 2026-11

First real `jas farm` run on this repo, step 1 of the farm spec's
Validation section. Owner-approved plan, GLM through OpenRouter.

## Run 1 (2026-11-04, farm `20261103T160240-048ce1`)

**Task.** Two independent issues in one farm: #48 (`core/replay.go`:
`bufio.Scanner` stops replay on a line over 16 MB) and #51 (pin the skills
frontmatter parser with golden and fuzz tests).

```sh
jas farm --trust-project --model openrouter/z-ai/glm-5.3 "<both issues>"
```

**Plan.** The planner split rather than refusing (`small_change` did not
fire): two `coder` parts, `replay` (`core/**`) and `frontmatter`
(`skills/**`), with sensible contracts and interfaces. Planner cost: about
20k tokens, 6 s. The plan was approved as drafted.

**Outcome.** Exit 6, `part_dropped`. Nothing merged.

| part | result | tokens (prompt) | calls | notes |
|---|---|---|---|---|
| replay | done, but did not compile | 252k | 12 | never ran `go build` or `go test` |
| frontmatter | failed: budget | 346k (+33k out) | 23 | stuck on a missing `TMPDIR` |
| integrator | skipped | 0 | 0 | clean merge, so no session ran |

The verdict `check` on the integrated tree failed in 0.4 s:
`core/replay.go:149: declared and not used: chunk` and
`core/replay.go:438: undefined: newLogScanner`.

## Findings

1. **A part was done without compiling.** The MVP's only done-check for a
   part is the write-limit diff. The worker gave a final answer and the
   supervisor accepted it. *Fixed:* `part_check` (defaults to `check`) runs
   in the part's worktree before it counts as done, with one fix round.
2. **A clean merge skipped the integrator.** With no conflicts, nothing ran
   between the merge and the verdict, so a broken seam could not be fixed.
   *Fixed:* `check` runs after a clean merge (`integrate_check`); a failure
   starts the integrator with the output.
3. **Workers' `TMPDIR` did not exist.** The supervisor points `TMPDIR` at
   `<worktree>/.jas/tmp`, but only `RunSetup` created it, and only when
   `worktree.setup` is set (it is empty by default). Every `go` command
   failed with `go: creating work dir: stat …/.jas/tmp: no such file or
   directory`. The frontmatter worker spent most of its budget working
   around it (`env | grep tmp`, `GOTMPDIR=/tmp`, `mkdir -p .jas/tmp`).
   *Fixed:* `Worktrees.Create` makes the directory.
4. **An unknown answer at the failure prompt became `drop`.** The owner meant
   to end the farm; the answer was not `retry`/`drop`/`abort`, so the
   supervisor dropped the part and went on to integrate. *Fixed:* the prompt
   re-asks until the answer names one choice (whole word or prefix).
5. **The per-part token cap is tight.** `per_worker_tokens` (400k) counts the
   full prompt of every call, so at 15–21k context per call a part gets about
   20 calls. Not changed yet: findings 1 and 3 caused most of the waste.
   Revisit after run 2.
6. **`jas farm "<task>"` makes two farm directories**, one for the plan
   (`farm.jsonl` empty, planner log only) and one for the run. The planner's
   log and the run's log are not in one place. Not changed yet.
7. **Noise:** a user skill shadowing warning prints on every start
   (`skill "computer-use" … shadowed by …`). Harmless.

Also fixed on the way: `testutil.TempHome` did not isolate `HOME`, so on
this machine the shadowing warning broke a `cmd/jas` test and with it the
farm's `check` (#108).

**Cost.** About 650k tokens of the ~30M validation budget.

## Next

Run 2: the same task on the fixed build. Expect each part to pass
`part_check` before it is done, then a clean merge and a passing verdict.
