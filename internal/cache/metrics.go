package cache

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// Metrics holds Prometheus metrics for the cache layer.
type Metrics struct {
	HitsTotal     prometheus.Counter
	MissesTotal   prometheus.Counter
	SetsTotal     prometheus.Counter
	SkippedTotal  prometheus.Counter
	HitRatio      prometheus.Gauge
	totalRequests float64
	totalHits     float64
}

// RegisterMetrics creates and registers cache metrics with the given registerer.
func RegisterMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		HitsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "daproxy_cache_hits_total",
			Help: "Total cache hits.",
		}),
		MissesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "daproxy_cache_misses_total",
			Help: "Total cache misses.",
		}),
		SetsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "daproxy_cache_sets_total",
			Help: "Total cache set operations.",
		}),
		SkippedTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "daproxy_cache_skipped_total",
			Help: "Total cache sets skipped (entry too large).",
		}),
		HitRatio: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "daproxy_cache_hit_ratio",
			Help: "Rolling cache hit ratio (0.0 to 1.0).",
		}),
	}

	reg.MustRegister(
		m.HitsTotal,
		m.MissesTotal,
		m.SetsTotal,
		m.SkippedTotal,
		m.HitRatio,
	)

	return m
}

// InstrumentedCache wraps a Cache and records Prometheus metrics.
type InstrumentedCache struct {
	inner   Cache
	metrics *Metrics
}

// NewInstrumentedCache wraps a cache with Prometheus instrumentation.
func NewInstrumentedCache(inner Cache, m *Metrics) *InstrumentedCache {
	return &InstrumentedCache{inner: inner, metrics: m}
}

// Get retrieves a cached response and records hit/miss metrics.
func (c *InstrumentedCache) Get(ctx context.Context, method string, height int64, params []byte) ([]byte, bool) {
	data, hit := c.inner.Get(ctx, method, height, params)
	if hit {
		c.metrics.HitsTotal.Inc()
		c.metrics.totalHits++
	} else {
		c.metrics.MissesTotal.Inc()
	}
	c.metrics.totalRequests++
	if c.metrics.totalRequests > 0 {
		c.metrics.HitRatio.Set(c.metrics.totalHits / c.metrics.totalRequests)
	}
	return data, hit
}

// Set stores a response and records set metrics.
func (c *InstrumentedCache) Set(ctx context.Context, method string, height int64, params []byte, response []byte) {
	c.metrics.SetsTotal.Inc()
	c.inner.Set(ctx, method, height, params, response)
}

// Close shuts down the underlying cache.
func (c *InstrumentedCache) Close() error {
	return c.inner.Close()
}
