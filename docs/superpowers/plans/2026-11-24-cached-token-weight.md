# Cached Token Weight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Token budgets charge cached prompt tokens at `cached_token_weight` (default 0.1) instead of in full.

**Architecture:** The weight is a property of a token pool's root, read by every pool in its chain at settle time. A pure `chargeTokens` computes the weighted charge from `providers.Usage`; `Reservation.SettleUsage` applies it. Reservations still hold the full estimate, so no cap is crossed. `cmd/jas` sets the weight from settings on each root pool it makes (`run`, `dream`, the farm planner, the farm pools).

**Tech Stack:** Go, `CGO_ENABLED=0`, `go test -race ./...`.

**Spec:** `docs/superpowers/specs/2026-11-24-cached-token-weight-design.md`

## Global Constraints

- Charge = `prompt - cached + math.Round(cached × weight) + completion`, with `cached` clamped to `[0, prompt]`.
- No usage report: settle at the estimate, as today. `cached_tokens` 0: the full charge.
- Setting `cached_token_weight`, default `0.1`, validated by `fractionOpen` (`0 < x <= 1`); env `JAS_CACHED_TOKEN_WEIGHT`; no CLI flag.
- Reservation estimates are unchanged (full prompt).
- `budget_stop.used` is the weighted figure; `usage` events keep raw counts (already true).
- Do not break `jas run`, `--json` or exit codes. Commits: `type: description`.
- Docs follow features (AGENTS.md): `docs/manual.md` plus `make manual` in the same commit.

**Deviation from the spec (recorded here and in the spec):** the weight lives on the root token pool rather than on each session config. A farm uses the host's setting for all its pools, so there are no mixed rates within a farm, and the manual note about mixed rates is dropped.

## Review Focus

1. A pool with no weight set (zero value): it must charge in full, so the existing pool tests and any caller that never sets a weight keep today's behavior.
2. A child pool (farm part, reviewer, integrator) settling: it must use the root's weight, not its own unset zero.
3. A provider reporting `cached_tokens > prompt_tokens`: the charge never goes below `completion + round(prompt × weight)`.
4. A compaction summary (`charge()`), which settles outside the request path: it must be weighted too.
5. A request with no usage event: it settles at the estimate, unweighted.

---

### Task 1: Weighted settle in core

**Files:**
- Modify: `core/tokens.go` (TokenPool, Reservation, `charge`)
- Modify: `core/session.go:574-578` (request settle)
- Test: `core/tokens_test.go`

**Interfaces:**
- Produces: `func chargeTokens(u providers.Usage, weight float64) int` (unexported; `core/tokens_test.go` is in package `core_test`, so test it through `export_test.go` as `core.ChargeTokens = chargeTokens`, or put that table test in an internal test file, whichever the package already does).
- Produces: `func (p *TokenPool) SetCachedWeight(w float64)` sets the weight on p. Only a root's weight is read.
- Produces: `func (r *Reservation) SettleUsage(u providers.Usage)` settles at `chargeTokens(u, root weight)`; a nil r is a no-op, like `Settle`. An unset weight (0) means 1.

- [ ] **Step 1: Write the failing tests**

`TestChargeTokens` (table, weight → want):
- `{Prompt 1000, Cached 0, Completion 50}`, 0.1 → 1050
- `{1000, 900, 50}`, 0.1 → 100 + 90 + 50 = 240
- `{1000, 900, 50}`, 1 → 1050
- `{1000, 5000, 50}`, 0.1 → 0 + 100 + 50 = 150 (clamped to prompt)
- `{1000, -5, 50}`, 0.1 → 1050 (clamped to 0)
- `{10, 5, 0}`, 0.1 → 5 + round(0.5)=1 → 6 (math.Round rounds half away from zero)

`TestTokenPool_SettleUsageUsesRootWeight`: root `NewTokenPool("farm", 0, nil)` with `SetCachedWeight(0.1)`, child `NewTokenPool("part/a", 0, root)`; `child.Reserve(1050)` then `SettleUsage({1000, 900, 50})`; assert `child.Used() == 240` and `root.Used() == 240`.

`TestTokenPool_SettleUsageUnsetWeightIsFull`: pool with no weight set, same usage → `Used() == 1050`.

`TestSession_CachedTokensChargedAtWeight`: rig (as in `TestSession_TurnResultNamesRefusingPool`) with a pool of limit 0 and weight 0.1, fake turn `Usage{PromptTokens: 1000, CachedTokens: 900, CompletionTokens: 50}`, one `Send`; assert `pool.Used() == 240`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./core/ -run 'TestChargeTokens|TestTokenPool_SettleUsage|TestSession_CachedTokensChargedAtWeight'`
Expected: build failure (undefined `SetCachedWeight` / `SettleUsage`).

- [ ] **Step 3: Implement**

Add `weight float64` to `TokenPool`, guarded by `mu` (or set before use and read with the lock). The root is `chain()`'s last element. Add `chargeTokens`, `SetCachedWeight` and `SettleUsage`. In `core/session.go` replace `res.Settle(usage.PromptTokens + usage.CompletionTokens)` with `res.SettleUsage(*usage)`. Keep `res.Settle(est)` for the no-usage path. In `charge()` replace the `Settle` line with `r.SettleUsage(u)`.

- [ ] **Step 4: Run the core tests**

Run: `go test -race ./core/`
Expected: PASS (existing pool tests unchanged by Review Focus 1).

- [ ] **Step 5: Commit**

`git commit -m "feat: settle token reservations with cached prompt tokens at a weight"`

### Task 2: Setting, wiring and docs

**Files:**
- Modify: `settings/config.go` (Keys, default constant), `settings/run.go` (`Run.CachedTokenWeight float64`)
- Modify: `cmd/jas/run.go:129`, `cmd/jas/dream_cmd.go:328`, `cmd/jas/farm_cmd.go:333` (call `SetCachedWeight(e.cfg.CachedTokenWeight)` on each new root pool)
- Modify: `farm/config.go` (`Config.CachedTokenWeight float64`, not a `farm.yaml` key), `farm/pools.go` `NewPools` (sets it on the farm root)
- Modify: `cmd/jas/chat_farm.go:55,250`, `cmd/jas/farm_run.go:129` (after `farm.LoadConfig`, set `cfg.CachedTokenWeight = e.cfg.CachedTokenWeight`)
- Modify: `docs/manual.md`; run `make manual`
- Modify: spec (record the deviation)
- Test: `settings/config_test.go`, `farm/review_test.go` (next to `TestPools_ReviewerRoundParents`)

**Interfaces:**
- Consumes: `(*core.TokenPool).SetCachedWeight(float64)` from Task 1.
- Produces: `settings.Run.CachedTokenWeight float64`, `farm.Config.CachedTokenWeight float64`.

- [ ] **Step 1: Write the failing tests**

In `TestConfig_ValidatesAllKeys`, add cases: `{"cached_token_weight", "0", false}`, `{"cached_token_weight", "1.5", false}`, `{"cached_token_weight", "0.25", true}`, `{"cached_token_weight", "1", true}`.

`TestKeys_CachedTokenWeightDefault`: the resolved default for `cached_token_weight` is `"0.1"`. Follow `TestKeys_CompactDefaults`.

`TestPools_CachedWeightFromConfig`: `NewPools(Config{Budget: Budget{FarmTokens: 0}, CachedTokenWeight: 0.1})`; `p.Part("a", 0)` reserve 1050 and `SettleUsage({1000, 900, 50})` → `p.Farm.Used() == 240`.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./settings/ ./farm/ -run 'TestConfig_ValidatesAllKeys|TestKeys_CachedTokenWeightDefault|TestPools_CachedWeightFromConfig'`
Expected: FAIL (unknown key; undefined field).

- [ ] **Step 3: Implement**

Add the key `{Name: "cached_token_weight", Default: DefaultCachedTokenWeight, Validate: fractionOpen}` with `DefaultCachedTokenWeight = "0.1"`. Parse it in `settings/run.go` as `elide_at` is parsed. Then the pool and farm wiring listed under Files.

- [ ] **Step 4: Docs**

`docs/manual.md`:
- `--max-tokens` rows (`jas run`, `jas dream`): add "cached prompt tokens count at `cached_token_weight`".
- Settings table: a `cached_token_weight` row, default `0.1`: "share of a cached prompt token charged against `--max-tokens` and farm budgets (`0 < x <= 1`); 1 charges in full".
- Farm budget rows (`budget.*`): one line that farm pools use the host's `cached_token_weight`.
- Exit code 7 row and the `budget_stop` event row: `used` is weighted.

Then run `make manual`. In the spec, replace the mixed-rate decision with the pool-root deviation.

- [ ] **Step 5: Full verification**

Run: `go vet ./... && go test -race ./...`
Expected: all `ok`. `TestManual_*` catches a missing setting row or a stale copy.

- [ ] **Step 6: Commit**

`git commit -m "feat: cached_token_weight setting for token budgets"` with body `Fixes #225`.
