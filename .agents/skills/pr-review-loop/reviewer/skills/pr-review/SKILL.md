---
name: pr-review
description: Use when asked to review a pull request, a branch, or a diff before it merges.
---

# PR review

## Gather

1. The prompt's "Already run for you" section has the `make test` result,
   `git log`, the diff stat, the importers of each changed Go package and
   the full diff (or a note that it was too big; then read it with
   `git diff <base>...HEAD`, a file at a time). Do not rerun any of them.
2. Read the linked issue or spec if the task names one, plus the repo's
   `AGENTS.md`: the review does not trust the project, so neither it nor
   the project's skills are in your prompt. Read surrounding code for every
   changed function, not just the hunk.
3. Do not rerun the suite: `make test` already passed. Run only targeted
   checks the change calls for: one test you want to watch (`go test -run TestX ./pkg`),
   `GOOS=windows go vet ./...` when paths or OS code change. If the prompt
   says `make test` did not run, run the repo's checks yourself.

## Budget

The run has 40 model turns. At the last one, or when the token budget runs
low, jas asks you for the review with tools off: anything you have not
checked by then goes unchecked. Spend at most 25 turns gathering, then write
the review:

- Batch independent reads and commands into one turn (several tool calls
  at once).
- Short review, about 10 turns, only when the diff is docs only, or under
  50 changed lines and touching none of the security boundaries in check 5.
  Check each changed claim against the code it names; do not audit the rest
  of the docs. Any other diff gets the full review.
- Logic changes: for each changed function whose behavior changes (not a
  pure refactor), read its callers, including those in the importing
  packages the prompt lists, and its tests, once. A caller that relies on
  the old behavior is a finding; one that no test covers is at least
  medium.
- Never re-read a file you already read; use `offset` to see another part.

## Check, in this order

1. **Correctness:** bugs, edge cases, error handling, concurrency, behavior
   on every OS the repo supports.
2. **Intent:** the change does what the issue, spec and docs promise; nothing
   the issue asked for is missing; no unrelated changes.
3. **Tests:** new behavior has a test that would fail without the change.
4. **Repo rules:** everything `AGENTS.md` requires for this kind of change
   (docs updated, generated copies refreshed, naming, commit format).
5. **Security:** secrets, input validation, path and permission checks.
   When the diff touches a boundary below, also read its tests and say in
   Checked which test shows the boundary still holds, or that none does:
   - path policy: `core/policy.go` (`Resolve`, `ResolveRead`, read roots),
     the deny floor, symlink and `Confine` checks in `skills/discover.go`;
   - project trust: `decideProjectTrust`, `trustBundle`, the trust store,
     farm workers' `workerContext`;
   - what runs or writes: the approval ladder, `shell`, `write`, `edit`,
     the profile skills write exception;
   - secrets: redaction, `auth.json`, keys in config, env or session logs;
   - this review itself: `review-pr.sh`'s sandbox, the merge hook, and the
     reviewer profile.
6. **User-facing behavior** (only when the diff touches a CLI, TUI, prompts,
   messages or docs): flag and command naming matches existing ones, error
   messages say what happened and how to fix it, exit codes and output match
   the docs, README and manual agree.

## Severity

- **high:** wrong behavior, data loss, security hole, broken build or test.
  A change that lets the model read, write or run more than before without
  the owner's approval is never low; a test that should prove a boundary
  holds and is missing is at least medium.
- **medium:** a repo rule broken, a missing test for new behavior, the issue
  only partly addressed, a confusing user-facing message.
- **low:** style, naming, wording, optional improvements.

## Output

Your final message is exactly this shape, and nothing after it:

```
## Findings

1. [high|medium|low] path/to/file.go:123 — what is wrong.
   Why it matters, and the fix in one or two sentences.

## Checked

One line per check above: what you ran or read, and the result.

VERDICT: PASS
```

- No findings: write `None.` under Findings.
- The last line is `VERDICT: CHANGES` when any finding is high or medium,
  otherwise `VERDICT: PASS`. This holds when jas asks for your final answer
  early (tools off, at the turn or token cap): still end with that exact
  line. A review without it does not count, whatever else it says.
- Every finding cites a file and line in the diff or in code the diff breaks.
