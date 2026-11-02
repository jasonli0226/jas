package core_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/assert"
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
