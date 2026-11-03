# Farm in the chat — acceptance run

How to run the acceptance check for the interactive farm
([design](superpowers/specs/2026-11-04-farm-interactive-design.md), section
Acceptance). It is a live run with a real model, by the owner at a terminal;
it uses every piece of the feature once. Budget: roughly 0.5–1M tokens.

## Before you start

- Build the binary from an up-to-date `main`:

  ```sh
  cd ~/projects/jas
  git switch main && git pull
  make build                           # → bin/jas
  ```

- Run it in a **clean, trusted checkout** with a farm `check` (this repo
  detects `go build ./... && go test ./...` from `go.mod`):

  ```sh
  cd ~/projects/jas
  git status --short          # must print nothing
  ./bin/jas farm vars --trust-project   # check resolves
  ```

- A terminal that sends Ctrl+digit (kitty, Ghostty, WezTerm, recent
  iTerm2) is best; otherwise use Alt+digit or `/farm attach <N>`.
- Use a model that works through OpenRouter for this account (`z-ai/glm-*`
  or `deepseek/*`).

## Task

Two small, independent issues in different files, so the planner splits:

- **#32** — approval-granted events are noisy under `--approve=never` (`core`)
- **#33** — README's "~7 MB binary" claim is misleading (`README.md`)

## Steps and what you should see

Start the chat: `./bin/jas` (pass
`--model openrouter/z-ai/glm-5.3` or similar if your default differs).

| # | Do | Expect |
|---|---|---|
| 1 | `/approve shell` | status line shows `shell`; the farm's workers inherit it, so their shell calls ask. |
| 2 | Ask: *"Fix issues #32 and #33 in parallel — they're independent."* | Ideally the agent calls `suggest_farm`: a notice says it suggests a farm and the input holds `/farm "…"`. If not, type `/farm "fix issues #32 and #33"`. |
| 3 | Send the `/farm` line | `farm: planning …`, then the plan summary and a picker: approve · edit · replan · quit. |
| 4 | Choose **approve** | `farm <id> started · ctrl+1..9 flip · ctrl+0 back`; one row per agent under the input (`1 <part> ● running …`) and a totals line `farm … tok / 5.0M · … · check pending`. |
| 5 | **Ctrl+1** | Screen clears; header `── <part> · coder · .jas/worktrees/<farm>/<part> ──`; that agent's transcript streams live; the input prompt reads `<part> ›`; a strip at the bottom lists agents. |
| 6 | Type a short steer, e.g. `keep the change minimal`, Enter | Shown as `↳ queued for <part>: …` until the agent reads it, then as a user block in its transcript. |
| 7 | **Ctrl+2**, then **Ctrl+0** | Flips to the other agent, then back to the main chat; anything the main chat printed meanwhile appears (the strip counted it as `main chat: N new`). |
| 8 | `/farm` | Dashboard: `#  part  role  status  last activity  tokens  check`. |
| 9 | When `[<part>] approve shell: …? [y/N/always/farm]` appears, answer it — once while viewing the **other** agent | The prompt opens in whatever view you are in; the bell rings; that agent's row shows `⚠ needs approval` until answered; your half-typed text comes back after. |
| 10 | Wait for the end | `integrate: …`, `check: passed …`, then the final prompt `[m/k/d]` in the main chat. Answer **k** (keep). |
| 11 | Ask the main agent: *"what did the farm do?"* | The farm summary (outcome, branch, parts) prints when the farm ends and is attached to this message (marked `(farm summary attached)`); the agent answers from it. |
| 12 | `/quit` | Exits normally (no farm running). |

Optional extras if you have time: `/farm pause 1` then `/farm resume 1`;
`/farm stop` on a second farm (twice cancels now); `/quit` while a farm runs
(asks `a farm is running — stop it and quit? y/n`); a farm question while the
main agent is replying (waits, strip shows `farm needs you · ctrl+0`).

## Record the result

- Farm id and logs: `~/.jas/farms/<farm-id>/` (`farm.jsonl`, `manifest.json`,
  `parts/*/events.jsonl`). `jas farm status <farm-id>` prints the summary.
- Note anything that looked wrong, with the step number, and what you saw
  vs the Expect column.
- Add it as **Run 4** in [`research/farm-dogfood-2026-11.md`](research/farm-dogfood-2026-11.md)
  (same table shape as runs 1–3: parts, result, time, tokens; then findings).
- Review the kept branch `jas/farm/<id>/integrate` before anything merges;
  delete it with `jas farm clean <farm-id> --branches` when done.
