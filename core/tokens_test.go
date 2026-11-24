package core_test

import (
	"context"

	"errors"
	"github.com/jasonli0226/jas/providers/fake"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
	"github.com/jasonli0226/jas/providers"
)

func TestTokenPool_ReserveSettle(t *testing.T) {
	p := core.NewTokenPool("w", 100, nil)
	r1, err := p.Reserve(60)
	assert.NoErr(t, err)
	_, err = p.Reserve(50)
	var be *core.BudgetError
	if !errors.As(err, &be) || be.Pool != "w" {
		t.Fatalf("want BudgetError on w, got %v", err)
	}
	r1.Settle(30)
	r1.Settle(10) // idempotent: first settle wins
	_, err = p.Reserve(50)
	assert.NoErr(t, err)
	assert.Eq(t, p.Used(), 80)
}

func TestTokenPool_ParentCapsChild(t *testing.T) {
	parent := core.NewTokenPool("farm", 100, nil)
	child := core.NewTokenPool("part", 1000, parent)
	_, err := child.Reserve(150)
	var be *core.BudgetError
	if !errors.As(err, &be) || be.Pool != "farm" {
		t.Fatalf("want BudgetError on farm, got %v", err)
	}
	assert.Eq(t, child.Used(), 0)
	assert.Eq(t, parent.Used(), 0)
}

func TestTokenPool_UnlimitedWhenZero(t *testing.T) {
	p := core.NewTokenPool("x", 0, nil)
	_, err := p.Reserve(1 << 30)
	assert.NoErr(t, err)
}

func TestTokenPool_ConcurrentNeverOvershoots(t *testing.T) {
	p := core.NewTokenPool("w", 100, nil)
	var ok atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Reserve(10); err == nil {
				ok.Add(1)
			}
		}()
	}
	wg.Wait()
	assert.Eq(t, int(ok.Load()), 10)
}

func TestSession_TurnResultNamesRefusingPool(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "never"}})
	farmPool := core.NewTokenPool("farm", 10, nil)
	part := core.NewTokenPool("part/a", 1_000_000, farmPool)
	s, err := core.NewSession(core.SessionConfig{
		Provider: r.prov, Model: "m", Exec: r.cfg.Exec, Log: r.cfg.Log, Tokens: part,
	})
	assert.NoErr(t, err)
	res, err := s.Send(context.Background(), "hi")
	assert.NoErr(t, err)
	assert.Eq(t, res.Code, core.ExitBudget)
	assert.Eq(t, res.Reason, "tokens")
	assert.Eq(t, res.Pool, "farm")
}

func TestChargeTokens(t *testing.T) {
	cases := []struct {
		name   string
		u      providers.Usage
		weight float64
		want   int
	}{
		{"no cache", providers.Usage{PromptTokens: 1000, CompletionTokens: 50}, 0.1, 1050},
		{"cached", providers.Usage{PromptTokens: 1000, CachedTokens: 900, CompletionTokens: 50}, 0.1, 240},
		{"weight 1", providers.Usage{PromptTokens: 1000, CachedTokens: 900, CompletionTokens: 50}, 1, 1050},
		{"cached above prompt", providers.Usage{PromptTokens: 1000, CachedTokens: 5000, CompletionTokens: 50}, 0.1, 150},
		{"negative cached", providers.Usage{PromptTokens: 1000, CachedTokens: -5, CompletionTokens: 50}, 0.1, 1050},
		{"rounds half up", providers.Usage{PromptTokens: 10, CachedTokens: 5}, 0.1, 6},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Eq(t, core.ChargeTokens(c.u, c.weight), c.want)
		})
	}
}

var cachedUsage = providers.Usage{PromptTokens: 1000, CachedTokens: 900, CompletionTokens: 50}

func TestTokenPool_SettleUsageUsesRootWeight(t *testing.T) {
	root := core.NewTokenPool("farm", 0, nil)
	root.SetCachedWeight(0.1)
	child := core.NewTokenPool("part/a", 0, root)
	r, err := child.Reserve(1050)
	assert.NoErr(t, err)
	r.SettleUsage(cachedUsage)
	assert.Eq(t, child.Used(), 240)
	assert.Eq(t, root.Used(), 240)
}

func TestTokenPool_SettleUsageUnsetWeightIsFull(t *testing.T) {
	p := core.NewTokenPool("run", 0, nil)
	r, err := p.Reserve(1050)
	assert.NoErr(t, err)
	r.SettleUsage(cachedUsage)
	assert.Eq(t, p.Used(), 1050)
}

// A weight outside (0, 1], NaN included, set by a direct caller charges in
// full rather than undercharging or overcharging.
func TestTokenPool_SettleUsageBadWeightIsFull(t *testing.T) {
	for _, w := range []float64{math.NaN(), 1.5, -0.2} {
		p := core.NewTokenPool("run", 0, nil)
		p.SetCachedWeight(w)
		r, err := p.Reserve(1050)
		assert.NoErr(t, err)
		r.SettleUsage(cachedUsage)
		assert.Eq(t, p.Used(), 1050)
	}
}

func TestSession_CachedTokensChargedAtWeight(t *testing.T) {
	r := newRig(t, []fake.Turn{{Text: "done", Usage: cachedUsage}})
	pool := core.NewTokenPool("run", 0, nil)
	pool.SetCachedWeight(0.1)
	s, err := core.NewSession(core.SessionConfig{
		Provider: r.prov, Model: "m", Exec: r.cfg.Exec, Log: r.cfg.Log, Tokens: pool,
	})
	assert.NoErr(t, err)
	_, err = s.Send(context.Background(), "hi")
	assert.NoErr(t, err)
	assert.Eq(t, pool.Used(), 240)
}
