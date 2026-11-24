# Cached prompt tokens at a discount (#225)

Short design for a small change. Approved approach: A (default weight 0.1).

## Intent

`--max-tokens`, farm pools and `jas dream --max-tokens` charge every prompt
token in full on every turn. Providers bill cached prompt tokens at a
fraction, so the cap mostly counts the same prefix sent again. In the jas
review of #223, 91% of 1.36M prompt tokens were cached; the 800k cap would
have stopped a review whose real cost was far lower. The cap should track
cost, so a long run is not stopped early by re-sent prefixes.

## Goals

- When a provider reports `cached_tokens`, settle a request at
  `prompt - cached + math.Round(cached × weight) + completion`, with
  `cached` clamped to `[0, prompt]` first, so odd provider data (cached
  above prompt, or negative) keeps the charge between "all prompt tokens
  cached" and the full charge.
- Without a report (`cached_tokens` 0 or no usage), charge as today.
- Same rule for every pool: `jas run`, chat, farm workers and farm totals,
  `jas dream`, compaction summaries.

## Non-goals

- Per-provider or per-model rates.
- Changing the reservation estimate: it stays the full prompt, so the cap
  is never overshot (a run may stop up to one prompt early near the cap).
- Raw-token caps alongside weighted ones.

## Anti-goal

The cap must never be crossed: weighting applies only at settle time.

## Approaches

- **A. Default 0.1 for everyone (chosen by the owner).** Every cap loosens
  at once for cache-heavy runs; matches what providers bill.
- B. Default 1 (opt in), reviewer profile sets 0.1. No change for anyone
  else; most users would never benefit.

## Decisions

- Owner: approach A, default `0.1`.
- My call: setting `cached_token_weight`, a float in `(0, 1]` (no provider
  bills cached tokens at 0), validated with the existing `fractionOpen`
  (`settings/config.go`), at the usual levels: global, profile, trusted
  project/local, and env `JAS_CACHED_TOKEN_WEIGHT` (`KeySpec.EnvName` maps
  every key). An out-of-range value is a config error. No CLI flag.
- My call: a pure helper `chargeTokens(u providers.Usage, weight float64) int`
  in `core/tokens.go`, next to `charge()`, table-tested; both `Settle` call
  sites (`core/session.go` request, `core/tokens.go` charge) use it.
- My call (changed while planning): the weight is set on a root token pool
  (`TokenPool.SetCachedWeight`) and every pool in its chain uses it, rather
  than being threaded through each session config. A farm uses the host's
  setting for all its pools, so there are no mixed rates within a farm.
- My call: `--max-tokens` and farm budgets are documented as "tokens, with
  cached prompt tokens counted at `cached_token_weight`". `budget_stop.used`
  is the weighted figure; raw counts stay on `usage` events. The
  `--wrap-up` token reserve works from the pool's room, so it is weighted
  with no change.
- My call: the review script's 1.5M stopgap cap (#221) is left as is; it
  can be lowered once review logs show weighted totals.

## Left to the builder

Where the setting is parsed and plumbed into `core.Config`; test names.
Docs: `docs/manual.md`: `--max-tokens` (`jas run` and `jas dream`), the
settings table, the farm budget section, exit code 7, and the `budget_stop`
event row; the skill copy via `make manual`. The `--wrap-up` row's "room
left" wording stays correct as is.
