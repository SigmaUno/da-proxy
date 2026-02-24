package cache

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dto "github.com/prometheus/client_model/go"
)

// memCache is a simple in-memory cache for testing.
type memCache struct {
	store map[string][]byte
}

func newMemCache() *memCache {
	return &memCache{store: make(map[string][]byte)}
}

func (m *memCache) Get(_ context.Context, method string, height int64, params []byte) ([]byte, bool) {
	key := Key(method, height, params)
	data, ok := m.store[key]
	return data, ok
}

func (m *memCache) Set(_ context.Context, method string, height int64, params []byte, response []byte) {
	key := Key(method, height, params)
	m.store[key] = append([]byte(nil), response...)
}

func (m *memCache) Close() error { return nil }

func counterValue(c prometheus.Counter) float64 {
	var m dto.Metric
	_ = c.(prometheus.Metric).Write(&m)
	return m.GetCounter().GetValue()
}

func gaugeValue(g prometheus.Gauge) float64 {
	var m dto.Metric
	_ = g.(prometheus.Metric).Write(&m)
	return m.GetGauge().GetValue()
}

func TestInstrumentedCache_HitMiss(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := RegisterMetrics(reg)
	inner := newMemCache()
	ic := NewInstrumentedCache(inner, metrics)

	ctx := context.Background()
	params := []byte(`{"jsonrpc":"2.0","id":1,"method":"block","params":["100"]}`)
	resp := []byte(`{"result":{}}`)

	// Miss.
	_, hit := ic.Get(ctx, "block", 100, params)
	assert.False(t, hit)
	assert.Equal(t, float64(1), counterValue(metrics.MissesTotal))
	assert.Equal(t, float64(0), counterValue(metrics.HitsTotal))

	// Set.
	ic.Set(ctx, "block", 100, params, resp)
	assert.Equal(t, float64(1), counterValue(metrics.SetsTotal))

	// Hit.
	data, hit := ic.Get(ctx, "block", 100, params)
	assert.True(t, hit)
	assert.Equal(t, resp, data)
	assert.Equal(t, float64(1), counterValue(metrics.HitsTotal))
	assert.Equal(t, float64(1), counterValue(metrics.MissesTotal))

	// Hit ratio: 1 hit / 2 total = 0.5
	assert.Equal(t, 0.5, gaugeValue(metrics.HitRatio))
}

func TestInstrumentedCache_Close(t *testing.T) {
	reg := prometheus.NewRegistry()
	metrics := RegisterMetrics(reg)
	inner := newMemCache()
	ic := NewInstrumentedCache(inner, metrics)

	require.NoError(t, ic.Close())
}

func TestRegisterMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := RegisterMetrics(reg)

	// Verify all metrics are registered.
	families, err := reg.Gather()
	require.NoError(t, err)

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}

	assert.True(t, names["daproxy_cache_hits_total"])
	assert.True(t, names["daproxy_cache_misses_total"])
	assert.True(t, names["daproxy_cache_sets_total"])
	assert.True(t, names["daproxy_cache_skipped_total"])
	assert.True(t, names["daproxy_cache_hit_ratio"])
	assert.NotNil(t, m)
}
