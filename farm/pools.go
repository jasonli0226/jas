package farm

import "github.com/jasonli0226/jas/core"

// Token pool names; a refusal by farm or work stops the whole farm.
const (
	poolFarm       = "farm"
	poolWork       = "work"
	poolIntegrator = "integrator"
	poolPartPrefix = "part/"
)

// Pools are the farm-wide token pools.
type Pools struct{ Farm, Work *core.TokenPool }

// NewPools makes the farm pool (farm_tokens) and the workers' share of it
// (farm_tokens minus integrator_reserve), whose parent is the farm pool.
func NewPools(cfg Config) Pools {
	farm := core.NewTokenPool(poolFarm, cfg.Budget.FarmTokens, nil)
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
