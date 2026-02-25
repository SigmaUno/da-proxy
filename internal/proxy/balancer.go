// Package proxy implements the reverse proxy routing, load balancing, and
// request handling for Celestia backend services.
package proxy

import (
	"math"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// explorationRate is the probability (0–1) that Next() picks a random
// endpoint instead of the fastest one. This prevents starvation and
// ensures all endpoints keep being measured.
const explorationRate = 0.1

// EndpointStats holds per-endpoint latency statistics exposed via the admin API.
type EndpointStats struct {
	URL           string  `json:"url"`
	EWMALatencyMs float64 `json:"ewma_latency_ms"`
	RequestCount  uint64  `json:"request_count"`
}

// Balancer selects a backend URL from a list of endpoints.
// When latency data is available it routes to the fastest endpoint;
// otherwise it falls back to round-robin.
type Balancer struct {
	endpoints     config.Endpoints
	counter       atomic.Uint64  // fallback round-robin counter
	latencies     []*ewma        // one EWMA per endpoint (same index)
	endpointIndex map[string]int // url → index for RecordLatency
}

// NewBalancer creates a latency-aware balancer for the given endpoints.
func NewBalancer(endpoints config.Endpoints) *Balancer {
	idx := make(map[string]int, len(endpoints))
	lats := make([]*ewma, len(endpoints))
	for i, ep := range endpoints {
		idx[ep] = i
		lats[i] = &ewma{}
	}
	return &Balancer{
		endpoints:     endpoints,
		latencies:     lats,
		endpointIndex: idx,
	}
}

// Next returns the next endpoint URL.
// With latency data: picks the fastest endpoint (with exploration probability
// for random selection to avoid starvation). Without latency data: round-robin.
func (b *Balancer) Next() string {
	n := len(b.endpoints)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return b.endpoints[0]
	}

	// Check if we have any latency data at all.
	hasData := false
	for _, l := range b.latencies {
		if l.Count() > 0 {
			hasData = true
			break
		}
	}

	if !hasData {
		// No latency data yet — pure round-robin.
		idx := b.counter.Add(1) - 1
		return b.endpoints[idx%uint64(n)]
	}

	// Exploration: with small probability, pick a random endpoint.
	if rand.Float64() < explorationRate {
		return b.endpoints[rand.IntN(n)]
	}

	// Latency-based selection: pick endpoint with lowest EWMA.
	// Prioritise unmeasured endpoints so they get probed.
	bestIdx := 0
	bestVal := math.MaxFloat64
	for i, l := range b.latencies {
		if l.Count() == 0 {
			// Unmeasured — pick it immediately to probe.
			return b.endpoints[i]
		}
		if v := l.Value(); v < bestVal {
			bestVal = v
			bestIdx = i
		}
	}
	return b.endpoints[bestIdx]
}

// RecordLatency records a response duration for the given endpoint URL.
// Unknown endpoints are silently ignored.
func (b *Balancer) RecordLatency(endpoint string, d time.Duration) {
	if i, ok := b.endpointIndex[endpoint]; ok {
		b.latencies[i].Update(d)
	}
}

// Stats returns per-endpoint latency statistics.
func (b *Balancer) Stats() []EndpointStats {
	stats := make([]EndpointStats, len(b.endpoints))
	for i, ep := range b.endpoints {
		stats[i] = EndpointStats{
			URL:           ep,
			EWMALatencyMs: b.latencies[i].Value(),
			RequestCount:  b.latencies[i].Count(),
		}
	}
	return stats
}

// All returns all endpoints.
func (b *Balancer) All() config.Endpoints {
	return b.endpoints
}

// Len returns the number of endpoints.
func (b *Balancer) Len() int {
	return len(b.endpoints)
}
