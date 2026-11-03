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

## Run 2 (2026-11-04, farm `20261103T163357-b4b2f8`)

Same task on the build with the run 1 fixes (#109) and in-repo worktrees
(#110). The planner split it the same way (`replay-fix`, `frontmatter-tests`).
Stopped by the owner with Ctrl-C; nothing merged.

| part | result | tokens (prompt / cached) | calls |
|---|---|---|---|
| replay-fix | failed: budget, after 70 s | 369k / 320k | 18 |
| frontmatter-tests | `part_check` failed, in its fix round when stopped | — | — |

What worked: `TMPDIR` existed, so workers built and tested normally;
`part_check` caught a failing tree and handed the output back to the worker.

## Findings (run 2)

8. **The budget counts cached tokens in full.** `replay-fix` was making
   steady progress (edit, test, fix) when it hit 400k; 87% of that was
   cached prompt prefix, billed at a fraction. *Fixed:* defaults raised to
   `per_worker_tokens` 1.5M and `farm_tokens` 5M (owner's choice over
   discounting cached tokens). Tune with the benchmark.
9. **`TMPDIR` inside the worktree breaks the project's own tests.** Every
   `t.TempDir()` landed inside a git checkout, so jas tests that expect a
   plain temp dir failed (`TestFindRoot_GitAndPlain` and 12 more) under
   `part_check`, whatever the worker wrote. Not caused by the in-repo move:
   the cache-dir layout had the same `TMPDIR`. *Fixed:* `TMPDIR` is now
   `<system temp>/jas-farm/<farm-id>/<part>`.
10. **No live output.** `jas farm run` printed nothing between the plan
    prompt and the final line, so a farm in a fix round looked hung.
    *Fixed:* one line per farm event and per worker tool call.
11. **`check` ran twice after a clean merge** (`integrate_check`, then the
    verdict, on the same tree). *Fixed:* the verdict reuses a passing
    merge check.
12. **Plain `grep -r` sees `.jas/worktrees`** (as expected from the in-repo
    choice); jas's own file tools in the main checkout are denied there.

## Run 3 (2026-11-04, farm `20261103T165153-e414f5`)

Same task on the build with #109–#112. **Exit 0** in 3 min 54 s, the first
passing farm. Owner kept the branches; reviewed and landed as #113, closing
#48 and #51.

| part | result | time | tokens (prompt / cached / out) | calls |
|---|---|---|---|---|
| frontmatter-tests | done; `part_check` passed first time | 2m 0s | 170k / 163k / 6k | 13 |
| replay-fix | done; `part_check` passed first time | 2m 26s | 309k / 288k / 6k | 16 |
| integrate | clean merge; `check` passed, reused as the verdict | 46s | 0 | 0 |

Total 491k tokens, about 93% of them cached prompt prefix.

## Findings (run 3)

13. **A green `check` still let a regression through.** The frontmatter part
    "fixed" nested-map folding by skipping any bare `key:` followed by
    indented lines, which also dropped valid multi-line plain values (an
    indented `description:`), and wrote goldens that pinned the new
    behaviour. The task said to fix only clear bugs. Human review caught it;
    the change was reverted in #113. Supports the spec's post-integrate
    `reviewer` step and "nothing merges without the owner".
14. **The replay part was sound but missed one edge**: on a persistent read
    error its reader looped instead of stopping. Fixed in review (#113).
15. **`check` does not run gofmt**, and neither does CI (`go vet` only); two
    worker test files were not gofmt-clean. Worth adding `gofmt -l` to the
    project's `check`.
16. **Progress lines were overwritten in the owner's terminal.** jas writes
    plain text and `\n` only (no `\r`, escapes or raw mode); the overwritten
    lines were the ones longer than a row, consistent with the terminal
    reflowing wrapped lines. *Fixed:* progress lines are clipped to the
    terminal width (the spec's "truncate, never wrap").

## Where this leaves validation

Three runs, about 1.6M tokens. The farm can now run a real two-part task end
to end. One task is not evidence about farm vs solo: next is the benchmark
harness (spec, Validation step 2).
