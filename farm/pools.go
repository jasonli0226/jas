package farm

import "github.com/jasonli0226/jas/core"

// Token pool names; a refusal by farm or work stops the whole farm.
const (
	poolFarm       = "farm"
	poolWork       = "work"
	poolIntegrator = "integrator"
	poolReview     = "review"
	poolPartPrefix = "part/"
)

// Pools are the farm-wide token pools.
type Pools struct{ Farm, Work *core.TokenPool }

// NewPools makes the farm pool (farm_tokens) and the workers' share of it
// (farm_tokens minus integrator_reserve), whose parent is the farm pool.
// Every farm pool charges cached prompt tokens at cfg.CachedTokenWeight.
func NewPools(cfg Config) Pools {
	farm := core.NewTokenPool(poolFarm, cfg.Budget.FarmTokens, nil)
	farm.SetCachedWeight(cfg.CachedTokenWeight)
	work := int(float64(cfg.Budget.FarmTokens) * (1 - cfg.Budget.IntegratorReserve))
	return Pools{Farm: farm, Work: core.NewTokenPool(poolWork, work, farm)}
}

// Part is a worker's pool: limit tokens (role budget or per_worker_tokens),
// also held in the work pool.
func (p Pools) Part(id string, limit int) *core.TokenPool {
	return core.NewTokenPool(poolPartPrefix+id, limit, p.Work)
}

// Integrator is the integrator's pool, drawing on the whole farm pool
// including the reserve.
func (p Pools) Integrator(limit int) *core.TokenPool {
	return core.NewTokenPool(poolIntegrator, limit, p.Farm)
}

// Reviewer is a reviewer round's pool. Round 1 draws on the work pool, so
// it never touches integrator_reserve, which the fix round needs; round 2
// has no fix round after it and draws on the whole farm pool.
func (p Pools) Reviewer(round, limit int) *core.TokenPool {
	parent := p.Work
	if round > 1 {
		parent = p.Farm
	}
	return core.NewTokenPool(poolReview, limit, parent)
}
