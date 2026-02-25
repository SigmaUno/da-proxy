package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEWMA_FirstSample(t *testing.T) {
	var e ewma
	e.Update(100 * time.Millisecond)

	assert.InDelta(t, 100.0, e.Value(), 0.01)
	assert.Equal(t, uint64(1), e.Count())
}

func TestEWMA_ConvergesToStable(t *testing.T) {
	var e ewma
	// Feed constant 50ms samples — EWMA should converge to 50.
	for i := 0; i < 50; i++ {
		e.Update(50 * time.Millisecond)
	}
	assert.InDelta(t, 50.0, e.Value(), 0.5)
}

func TestEWMA_RespondsToChange(t *testing.T) {
	var e ewma
	// Stabilize at 100ms.
	for i := 0; i < 20; i++ {
		e.Update(100 * time.Millisecond)
	}
	stable := e.Value()
	assert.InDelta(t, 100.0, stable, 1.0)

	// Shift to 10ms — EWMA should decrease.
	for i := 0; i < 10; i++ {
		e.Update(10 * time.Millisecond)
	}
	assert.Less(t, e.Value(), stable)
}

func TestEWMA_ZeroBeforeSamples(t *testing.T) {
	var e ewma
	assert.Equal(t, float64(0), e.Value())
	assert.Equal(t, uint64(0), e.Count())
}

func TestEWMA_Concurrent(t *testing.T) {
	var e ewma
	var wg sync.WaitGroup
	n := 100
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			e.Update(50 * time.Millisecond)
		}()
	}
	wg.Wait()

	assert.Equal(t, uint64(n), e.Count())
	assert.Greater(t, e.Value(), float64(0))
}
