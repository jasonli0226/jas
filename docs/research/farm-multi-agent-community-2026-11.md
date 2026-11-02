# Farm and multi-agent coding: community research (Nov 2026)

**Date:** 2026-11-02 (Asia/Tokyo)
**Written against:** farm spec draft `docs/superpowers/specs/2026-11-02-farm-design.md` at `13bef44` (branch `docs/farm-design`)
**Question:** does a split-task farm address pain points developers actually report with parallel / multi-agent coding, and which shape should jas ship?
**Used by:** the farm spec (positioning, defaults, board, budgets, CI contract) and
[`superpowers/specs/2026-11-02-session-awareness.md`](../superpowers/specs/2026-11-02-session-awareness.md).

Every link under Sources returned HTTP 200 on 2026-11-02 (Asia/Tokyo); one Reddit
thread returned 403 and was dropped. Quotes are verbatim. Numbers are from the
cited papers and vendor posts — vendor figures are self-reported, and tools in
this space change monthly, so re-check before relying on them.

---

## TL;DR

- **Yes, but only the split-task shape the draft spec picked, and only if it stays narrow.** The pattern that is getting real adoption in 2025–2026 is *decompose → parallel workers on disjoint files in isolated worktrees → deterministic verifier → one integrator/human gate*. Examples: Claude Code dynamic workflows, Cognition's "map-reduce-and-manage", OpenHands Refactor SDK, Anthropic's C-compiler run, and HN users' file-ownership hacks. The farm spec's plan → approve → code-only supervisor → worktrees → integrator → required `check` matches that pattern almost exactly.
- **Free-form agent-to-agent talk doesn't pay off.** The best controlled study, CooperBench (652 tasks), finds cooperating agents about **30% less successful** than one agent doing both halves. Success falls further as agents are added (2→4 agents: 68.6% → 46.5% → 30.0%), and communication cut merge conflicts but did **not** raise success. The board is fine as a channel for **structured commitments** (owned files, signatures, insertion points, "done/blocked"). It is not useful as chat.
- **Mode (a), "agents propose/discuss then merge", has weak evidence when it's run as debate.** Multi-agent debate usually loses to self-consistency at equal or lower compute. What *does* work and ship is the read-only version: several agents look in parallel, then a **verify step**, then rank, while writes stay single-threaded. Examples are Claude Code Review and Devin Review. So the next-version "discussion mode" should be a **clean-context review/critic pass**, not a debate.
- **The pain points developers actually report** are (1) the review bottleneck, (2) token/limit blowups they can't see or cap, (3) merge and semantic conflicts, and (4) worktree environment gaps. Farm *creates* (2)–(4) if it isn't careful. jas's distinctive answers are budgets, the event log, exit codes, plan approval and a mandatory `check`. Little else in the landscape packages these for **headless CI with an audit trail**, and that's jas's best niche.
- **v1 recommendation:** ship `farm plan` + `farm run` (CI-first), default `max_workers: 2–3` (not 4), and keep the planner refusing to split by default. Add a structured board (commitments, not prose), per-part `owns` + contract fields, a pre-call budget check, and integrator + `check` as the only path to "done". Defer discussion mode and replace it with a `reviewer` part. Prove it against solo `jas run` on the jas fixtures before promoting it.

---

## Pain points solved (by the farm design as specified)

| Developer pain | Evidence it is real | How farm addresses it |
|---|---|---|
| **One agent on a huge task drifts or runs out of context.** Large migrations "spin its wheels for several hours before delivering a woefully incomplete solution" | OpenHands refactor post; Anthropic says multi-agent wins where breadth matters (90.2% over single-agent on their research eval) | Planner splits work into parts with `owns`. Each worker gets a fresh, focused context |
| **Agents in one checkout clobber each other / "agent lag"** | HN Conductor thread: redhale says "Worktrees completely solve that problem"; Cursor 2.0 runs up to 8 agents "using git worktrees or remote machines to prevent file conflicts" | Per-part worktree branches `farm/<id>/<part>` |
| **Hand-rolled coordination hacks** (tmux panes, STOP files, lock files, "repo_2, repo_3") | HN: Syzygies' file-ownership + STOP file ("dumbest parallel task manager imaginable yet it works"); bernardom: "git checkout four copies of your repo"; C compiler used lock files in `current_tasks/` | Code-only supervisor + DAG + disjoint-`owns` validation turn the hack into a tool |
| **Orchestration that is itself an LLM, which can't be audited or reproduced** | Cognition: the manager needed heavy context engineering, and cross-agent communication "doesn't happen by default". Claude Code workflows: "A workflow moves the plan into code" | Supervisor is code, the plan is a YAML file a human approves, everything lands in JSONL |
| **"Tests pass" ≠ done; regressions between parallel commits** | C compiler: "it is easy to see tests pass and assume the job is done"; the author had to add CI because new commits broke features | Required `check` after the integrator merges in DAG order (the updated spec has the supervisor run it as the verdict); owner chooses merge/keep/discard (no auto-merge) |
| **Invisible or unbounded cost** | Claude Code costs doc: agent teams ≈ **7× tokens** in plan mode; Anthropic: multi-agent ≈ **15×** chat tokens; issue #32996: teammates silently duplicated (2× tokens) | `per_worker_tokens`, `farm_tokens`, per-part `events.jsonl`, farm-level `farm.jsonl`. jas can *show* where tokens went |
| **Agent runs in CI with no audit or exit semantics** | gh-aw built a whole staged "SafeOutputs" model plus `gh aw logs/audit`; Codex added `exec --json` JSONL events; Copilot added session logs | `jas farm run plan.yaml` without a TTY, plan approved ahead of time, exit codes, redacted JSONL, resume |

## Pain points created (or amplified) by multi-agent

| New pain | Evidence | Mitigation in spec / still missing |
|---|---|---|
| **Coordination curse: more agents, lower success** | CooperBench: coop ≈ 30% lower success; 2→4 agents 68.6%→46.5%→30.0%; failures from expectation (42%), commitment (32%), communication (26%) | Spec has disjoint `owns` and a planner that can refuse. **Missing:** a lower default `max_workers`; explicit interface/contract fields per part |
| **Semantic conflicts that git won't flag** | CooperBench's "spatial vs semantic" gap (Jinja `case_sensitive` example); CodeCRDT: 5–10% of runs had semantic conflicts, and git-style branching "defers 15–30% of conflicts to merge time" | Integrator + `check`. **Missing:** contract/commitment posts that the integrator verifies, plus a `check` strong enough to catch behaviour |
| **Parallel isn't faster or better by default** | CodeCRDT (600 trials): 13.1% slower overall, 82–189% more code on complex tasks, quality −7.7%. MAST: 14 failure modes; ChatDev 33.33% correct on ProgramDev | Planner refusal + `min_turns_per_part`. Make "don't split" the default outcome |
| **Review bottleneck gets worse** | Simon Willison: "the natural bottleneck on all of this is how fast I can review the results". HN oc1: "git worktrees doesn't help solve any of these problems". CuriouslyC: "Fancy orchestration is mostly a waste, validation is the bottleneck" | Integrator produces **one** branch to review. Add a per-part summary + diffstat in the final report |
| **Token / plan-limit burn** | HN ecliptik: parallel agents "hit Max plan limits in under an hour"; findjashua: reviewer-per-step "eat up tokens _very_ quickly"; CooperBench: up to 20% of steps spent communicating | Budgets exist. **Missing:** budget checked *before* each call (see claude-code#100111: "People budget fleets by adding up caps (N workers × $X), and that sum is wrong by up to one call per worker") |
| **Worktree environment gaps** | HN Conductor: untracked `.env` not copied (pjm331), submodules (clbrmbr), dependency installs/ports (_1tem). Claude Code added `.worktreeinclude` for gitignored files | **Missing in spec:** worktree bootstrap (copy-list / setup command), port/env isolation guidance |
| **Duplicate effort / thrash on a shared bug** | C compiler: "Every agent would hit the same bug, fix that bug, and then overwrite each other's changes. Having 16 agents running didn't help" | DAG + disjoint `owns` + a "blocked on X" board post. Fail-pause dependents (already in spec) |

---

## Landscape table

| Tool / pattern | Shape | Coordination | Verification gate | Headless / CI | Notes |
|---|---|---|---|---|---|
| **Claude Code dynamic workflows** | Plan in code (`agent()`, `parallel()`, `pipeline()`), `isolation: 'worktree'` | Script, no LLM orchestrator | User-defined | Yes (JS script) | Closest analogue to the farm supervisor; aimed at 500-file migrations and audits |
| **Claude Code agent teams** | Lead + teammates, shared task list, messaging | LLM lead + chat | None built in | Interactive, experimental, off by default | Docs: "use significantly more tokens"; ~7× in plan mode; no worktree isolation, "partition the work so each teammate owns a different set of files"; start with 3–5 |
| **Claude Code subagents / `--worktree`** | Single writer + read-only helpers; manual parallel sessions | Human | Human | `claude -p`, `--max-turns`, `--max-budget-usd` | Budget cap is checked after each call (claude-code#100111) |
| **Claude Code Review** | Several agents find issues in parallel → verify → rank → post | Pipeline | Verification step drops false positives | Runs on PRs | The working version of "many agents propose, one answer" |
| **Cognition Devin (manager + Devin Review)** | Map-reduce-and-manage; clean-context reviewer | Manager agent | Review agent (~2 bugs/PR, ~58% severe) | Cloud | "writes stay single-threaded and the additional agents contribute intelligence rather than actions" |
| **Cursor 2.x multi-agent** | Up to 8 agents on one prompt in worktrees/remote; best-of-N | None (independent) | Human picks | Background agents | Best-of-N oriented, the mode jas dropped |
| **OpenHands Refactor SDK** | Dependency-analysis decomposition → parallel batches → PRs into a rolling branch | Code + human | Fixers + verifiers (compile, tests, linters) | SDK, private beta | Author "max[es] out at around 5 agents"; tasks "only 80-90% automatable" |
| **GitHub Agent HQ / Copilot coding agent** | Assign many agents/tasks; mission control | Human | PR review | Runs in Actions; session logs | Parallel *tasks*, not cooperating agents |
| **GitHub Agentic Workflows (gh-aw)** | Markdown → Actions; Copilot/Claude/Codex/Gemini engines | Staged plan compiled by trusted compiler | SafeOutputs + threat detection | Native CI | Agent read-only; writes buffered and filtered; `gh aw logs/audit` for cost and forensics |
| **Codex CLI `exec`** | Single agent, headless | n/a | n/a | `--json` JSONL events, `--output-schema` | Shows CI users want machine-readable event streams |
| **Amp (Task + Oracle)** | Parallel Task subagents on disjoint write targets; read-only Oracle | Main agent | Oracle review | CLI | Prompt guidance: serialize tasks touching same files/shared contracts |
| **Vibe Kanban, claude-squad, Conductor, Sculptor, Crystal, etc.** | UI/TUI for many sessions in worktrees/containers | Human | Human | Mostly no | Vibe Kanban's company (Bloop) shut down Apr 2026, though the OSS keeps working locally. Many entrants, low lock-in |
| **Anthropic C compiler run** | 16 agents, lock files in `current_tasks/`, git | None (no orchestrator) | "nearly perfect" test verifier + CI + GCC oracle | Scripted loop | ~2,000 sessions, ~$20k; "Merge conflicts are frequent" |

**Pattern summary:** what gets adopted is (1) parallel *independent* tasks managed by a human, (2) a single writer with read-only helpers or reviewers, and (3) code-driven fan-out on decomposable work with a hard verifier. True peer-to-peer cooperating writers are mostly experimental (agent teams, swarms). Cognition: "most of the sexy ideas in that space still don't see meaningful adoption".

---

## Evidence (numbers)

- **Cost multipliers:** agents ≈ 4× chat tokens; multi-agent ≈ 15× (Anthropic research system). Agent teams ≈ 7× standard sessions in plan mode (Claude Code costs doc). C compiler: 2B input + 140M output tokens, just under $20k, 16 agents.
- **Where multi-agent wins:** breadth-first research: +90.2% vs single-agent Opus 4 on Anthropic's internal eval. Token usage alone explains 80% of BrowseComp variance, so much of the win is "spend more tokens in parallel". Anthropic adds that "most coding tasks involve fewer truly parallelizable tasks than research".
- **Where it loses (coding, cooperation):** CooperBench: GPT-5 / Sonnet 4.5 ≈ 25% in Coop, ~50% lower than Solo. Pooled retention 0.59. First-turn planning roughly halves conflicts (29.4% vs 51.5%). Symptoms: work overlap 33.2%, divergent architecture 29.7%.
- **Blackboard/observation coordination (closest to the farm board):** CodeCRDT: speed ranges from 21.1% faster to 39.4% slower, 13.1% slower overall. 82–189% more code on complex tasks, quality −7.7%. Best at 3–5 agents.
- **Failure taxonomy:** MAST: 14 failure modes, 1,600+ annotated traces across 7 frameworks (κ = 0.88). Top modes: step repetition 15.7%, reasoning-action mismatch ~13%, not recognizing completion 12.4%, disobeying task spec 11.8%.
- **Debate:** "MAD often fail to outperform simple single-agent baselines such as Chain-of-Thought and Self-Consistency, even when consuming significantly more inference-time computation" (5 methods, 9 benchmarks, 4 models). Model heterogeneity was the consistent helper. Smit et al. (ICML 2024) reach a similar conclusion.
- **Review-style multi-agent that ships:** Devin Review averages ~2 bugs/PR, ~58% severe. Claude Code Review = parallel finders + verification + ranking.
- **Adoption signal:** Devin enterprise usage grew ~8× in 6 months (Cognition, Apr 2026). Cursor 2.0 shipped 8-way parallel agents (Oct 2025). Many HN launches of parallel-agent UIs (Conductor, Sculptor, FleetCode, Emdash, Superset).
- **Context caveat:** METR's 2025 RCT found experienced OSS devs 19% slower with AI. Its Feb 2026 update changed the study design and says the earlier data is unreliable. Don't lean on it either way.

---

## CI angle

- **The demand is real and concentrated in governance.** gh-aw's design is basically a list of what CI users need from agents: read-only agent, buffered and filtered writes ("SafeOutputs"), secret redaction with `if: always()`, artifacts (prompt, patch, engine logs with token usage), `gh aw logs`/`gh aw audit` for cost and forensics. Codex added `exec --json` (thread/turn/item events with usage) and `--output-schema`. Copilot coding agent added session-log visibility. Claude added `--max-turns` / `--max-budget-usd` for headless runs.
- **No major tool offers *multi-agent decomposition* as a first-class headless CI primitive with an approved plan file, per-part logs, budgets and exit codes.** gh-aw does multi-*stage* CI but not parallel cooperating coders. Claude workflows are close but tied to Claude. Agent teams are interactive only. This is the gap `jas farm plan` → human approval → `jas farm run plan.yaml` fills. The approved `plan.yaml` is the CI analogue of gh-aw's compiled lock file: a reviewed artifact that limits what the run may do.
- **The best CI fit is the work the community already parallelises:** mechanical migrations, codemods, test backfill per package, dependency bumps across modules, audits. These have disjoint file sets and a cheap, strong verifier (the C compiler, OpenHands verifiers, Claude workflows' "500-file migrations and audits").
- **Budgets need to be hard in CI.** claude-code#100111 shows a "max" that's checked after the call can overshoot by one call per worker. Fleets multiply this. jas should check before sending (estimate input + `max_tokens`) and emit a distinct exit code for budget stops.
- **Exit codes matter for CI.** The spec leaves refusal/failure codes to the builder. Pin them in the spec: e.g. 0 ok, distinct codes for planner-refused, part failed, `check` failed, budget exhausted, policy violation. Then pipelines can branch on them without parsing logs.

---

## Fit for jas + recommendation

**Which mode matches demand**

- **Mode (b), one big task in parts:** matches demand, as long as it's code-orchestrated, file-disjoint and verifier-gated. Keep it as the v1 headline. The community evidence says the value is (i) context isolation per part, (ii) wall-clock parallelism on decomposable work, (iii) one reviewable result. It does **not** say "agents collaborating like a team".
- **Mode (a), propose/discuss → merge:** demand is real, but the winning implementation is **parallel read-only critics + a verify step + a single writer**, not debate. Reframe "discussion mode" as `reviewer` parts (clean context, optionally a *different* model) whose findings the integrator or owner must address. This also fits jas's "roles are data, write limits in code".

**Where jas has an edge**

1. Code-only supervisor + human-approved `plan.yaml`: auditable and reproducible, unlike LLM-lead orchestration (agent teams, swarms).
2. Mandatory `check` + integrator + no auto-merge: matches the one thing every successful run had, a strong verifier.
3. Versioned JSONL per part + farm log + redaction + replay: answers the "where did my tokens go" problem (claude-code#32996, 7×/15×) and the CI audit need (gh-aw).
4. Model-agnostic, MIT, tiny: lets you use heterogeneous models for reviewers (the MAD paper's "antidote") and run in any CI.

**Risks / anti-patterns to avoid**

- Defaulting to 4 workers. Evidence (CooperBench scaling, CodeCRDT's 3–5 sweet spot, Claude's "start with 3–5", OpenHands author's ~5) suggests **2–3 default, 4 max-by-config**.
- Letting the board become chat. Up to 20% of steps go to communication with no success gain. Restrict posts to typed messages: `claim(files)`, `contract(name, signature/insertion point)`, `done(summary, diffstat)`, `blocked(on)`, `question(to)`. The supervisor validates them. Keep the ~500-char cap.
- Over-promising token efficiency. Market farm as "faster and more reviewable on decomposable work, at higher token cost". Never say "near one agent's tokens".
- Weak `check`. Auto-detected `go test ./...` may be too thin for semantic conflicts. Warn when `check` is trivial or has no tests touching the changed packages.
- Worktree setup gaps (.env, deps, ports, submodules). Add `worktree.copy: [...]` and `worktree.setup: "<cmd>"` to `farm.yaml`.

**Recommended v1 scope**

1. `jas farm plan -o plan.yaml` and `jas farm run plan.yaml` first (CI path). Interactive `jas farm "<task>"` is a thin wrapper.
2. Planner refuses unless parts have disjoint `owns` **and** each part has a stated contract (inputs/outputs/touch points). Refusal is a normal, cheap outcome with its own exit code.
3. `max_workers` default 2–3. `min_turns_per_part` kept, so tiny tasks don't get split.
4. Typed board as above. Inject only commitments relevant to the part's dependencies.
5. Pre-call budget enforcement per worker and per farm. Budget-stop exit code. Final report with tokens per part, per role.
6. Integrator merges in DAG order → `check` → writes one branch + a summary (per-part diffstat, contracts honoured/violated, check output). No auto-merge.
7. `reviewer` role as an optional final read-only part (clean context, configurable model). This is the v1 stand-in for discussion mode.
8. Defer: free-form discussion/debate, best-of-N, attach/TUI, containers.
9. **Prove it:** add a farm-vs-solo harness on the jas fixtures (CooperBench-style), reporting success, wall-clock, tokens and conflict rate. Ship "farm" defaults only where it beats `jas run`.

---

## Sources

All checked HTTP 200 on 2026-11-02 (Asia/Tokyo).

Research / benchmarks
- Anthropic, How we built our multi-agent research system: https://www.anthropic.com/research/multi-agent-research-system
- CooperBench (Stanford/SAP), arXiv 2601.13295: https://arxiv.org/abs/2601.13295 (site: https://cooperbench.com)
- CodeCRDT, arXiv 2510.18893: https://arxiv.org/abs/2510.18893
- MAST, Why Do Multi-Agent LLM Systems Fail?, arXiv 2503.13657: https://arxiv.org/abs/2503.13657
- Stop Overvaluing Multi-Agent Debate, arXiv 2502.08788: https://arxiv.org/abs/2502.08788
- Smit et al., Should we be going MAD? (ICML 2024): https://proceedings.mlr.press/v235/smit24a.html
- METR 2025 study: https://metr.org/blog/2025-07-10-early-2025-ai-experienced-os-dev-study/ and 2026 update: https://metr.org/blog/2026-02-24-uplift-update/

Practitioner / vendor
- Cognition, Don't Build Multi-Agents (2025-06): https://cognition.com/blog/dont-build-multi-agents
- Cognition, Multi-Agents: What's Actually Working (2026-04): https://cognition.com/blog/multi-agents-working
- Anthropic, Building a C compiler with agent teams (2026-02): https://www.anthropic.com/engineering/building-c-compiler
- OpenHands, Automating Massive Refactors with Parallel Agents: https://www.openhands.dev/blog/automating-massive-refactors-with-parallel-agents
- Simon Willison, Embracing the parallel coding agent lifestyle (2025-10): https://simonwillison.net/2025/Oct/5/parallel-coding-agents/

Tool docs
- Claude Code agent teams: https://code.claude.com/docs/en/agent-teams
- Claude Code costs: https://code.claude.com/docs/en/costs
- Claude Code run agents in parallel: https://code.claude.com/docs/en/agents
- Claude Code dynamic workflows: https://code.claude.com/docs/en/workflows
- Claude Code worktrees: https://code.claude.com/docs/en/worktrees
- Claude Code Review: https://code.claude.com/docs/en/code-review
- Cursor 2.0 changelog: https://cursor.com/changelog/2-0 ; worktrees doc: https://cursor.com/docs/configuration/worktrees
- GitHub Agentic Workflows: https://github.github.com/gh-aw/ ; security architecture: https://github.github.com/gh-aw/introduction/architecture/
- GitHub Agent HQ: https://github.blog/news-insights/company-news/welcome-home-agents/
- Copilot coding agent session visibility: https://github.blog/changelog/2026-03-19-more-visibility-into-copilot-coding-agent-sessions/
- Codex non-interactive mode: https://developers.openai.com/codex/noninteractive ; codex-action: https://github.com/openai/codex-action
- Amp modes & subagents: https://ampcode.com/docs/models-and-subagents
- Vibe Kanban: https://github.com/bloopai/vibe-kanban ; shutdown note: https://vibekanban.com/blog/shutdown
- Imbue Sculptor: https://imbue.com/blog/sculptor-announce
- claude-squad: https://github.com/smtg-ai/claude-squad

Community threads / issues
- anthropics/claude-code#32996 (agent teams duplicated teammates, ~2× tokens): https://github.com/anthropics/claude-code/issues/32996
- anthropics/claude-code#100111 (`--max-budget-usd` checked after the call; fleet caps undercount): https://github.com/anthropics/claude-code/issues/100111
- HN, Show HN: Conductor: https://news.ycombinator.com/item?id=44594584
- HN, Orchestrate teams of Claude Code sessions: https://news.ycombinator.com/item?id=46902368
- HN, Parallel coding agents with tmux and Markdown specs: https://news.ycombinator.com/item?id=47218318
- HN, Embracing the parallel coding agent lifestyle: https://news.ycombinator.com/item?id=45489884
- HN, C compiler with agent teams: https://news.ycombinator.com/item?id=46903616

---

## How this maps to the jas spec (2026-11-02)

| Research recommendation | Where it landed |
|---|---|
| Position as "too big/slow for one agent, safely and auditably", not "better than one agent" | farm spec, Positioning |
| Planner refuses by default; code-enforced refuse rules | farm spec, The plan → Refusal |
| `max_workers` default 2–3 | farm spec, Configuration |
| Typed board (commitments, not chat), capped | farm spec, Board |
| Worktree copy list + setup command | farm spec, Worktrees |
| Pre-call budget check, budget-stop exit code | farm spec, Budgets; CI contract |
| Pinned exit codes, `--json`, manifest | farm spec, CI contract |
| Discussion mode → clean-context `reviewer` part | farm spec, Next version |
| Farm-vs-solo benchmark before promoting | farm spec, Prerequisites and benchmark gate |
| Hand-rolled "who is touching what" hacks for parallel sessions | session-awareness spec |
