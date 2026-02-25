package proxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/SigmaUno/da-proxy/internal/config"
)

func TestBalancer_RoundRobin_NoLatencyData(t *testing.T) {
	b := NewBalancer(config.Endpoints{"http://a:1", "http://b:2", "http://c:3"})

	seen := make(map[string]int)
	for i := 0; i < 30; i++ {
		seen[b.Next()]++
	}
	assert.Equal(t, 10, seen["http://a:1"])
	assert.Equal(t, 10, seen["http://b:2"])
	assert.Equal(t, 10, seen["http://c:3"])
}

func TestBalancer_PrefersFastest(t *testing.T) {
	b := NewBalancer(config.Endpoints{"http://slow:1", "http://fast:2"})

	// Seed enough samples so both endpoints have latency data.
	for i := 0; i < 20; i++ {
		b.RecordLatency("http://slow:1", 200*time.Millisecond)
		b.RecordLatency("http://fast:2", 20*time.Millisecond)
	}

	counts := make(map[string]int)
	for i := 0; i < 1000; i++ {
		counts[b.Next()]++
	}
	assert.Greater(t, counts["http://fast:2"], counts["http://slow:1"],
		"fast endpoint should be selected more often")
	// Slow endpoint should still get some exploration traffic.
	assert.Greater(t, counts["http://slow:1"], 0,
		"slow endpoint should still get exploration traffic")
}

func TestBalancer_SingleEndpoint(t *testing.T) {
	b := NewBalancer(config.Endpoints{"http://only:1"})
	for i := 0; i < 10; i++ {
		assert.Equal(t, "http://only:1", b.Next())
	}
}

func TestBalancer_EmptyEndpoints(t *testing.T) {
	b := NewBalancer(config.Endpoints{})
	assert.Equal(t, "", b.Next())
}

func TestBalancer_RecordLatency_Unknown(_ *testing.T) {
	b := NewBalancer(config.Endpoints{"http://a:1"})
	// Should not panic on unknown endpoint.
	b.RecordLatency("http://unknown:999", 100*time.Millisecond)
}

func TestBalancer_Stats(t *testing.T) {
	b := NewBalancer(config.Endpoints{"http://a:1", "http://b:2"})
	b.RecordLatency("http://a:1", 50*time.Millisecond)
	b.RecordLatency("http://a:1", 60*time.Millisecond)
	b.RecordLatency("http://b:2", 100*time.Millisecond)

	stats := b.Stats()
	assert.Len(t, stats, 2)

	assert.Equal(t, "http://a:1", stats[0].URL)
	assert.Equal(t, uint64(2), stats[0].RequestCount)
	assert.Greater(t, stats[0].EWMALatencyMs, float64(0))

	assert.Equal(t, "http://b:2", stats[1].URL)
	assert.Equal(t, uint64(1), stats[1].RequestCount)
	assert.Greater(t, stats[1].EWMALatencyMs, float64(0))
}

func TestBalancer_UnmeasuredEndpointPrioritized(t *testing.T) {
	b := NewBalancer(config.Endpoints{"http://measured:1", "http://unmeasured:2"})
	b.RecordLatency("http://measured:1", 50*time.Millisecond)

	// With latency data on one endpoint, the unmeasured one should be
	// prioritised for probing on most non-exploration calls.
	unmeasuredCount := 0
	for i := 0; i < 100; i++ {
		if b.Next() == "http://unmeasured:2" {
			unmeasuredCount++
		}
	}
	// The unmeasured endpoint should be picked deterministically by the
	// latency path (90%) and sometimes by exploration (5% of 10%).
	assert.Greater(t, unmeasuredCount, 80,
		"unmeasured endpoint should be prioritised")
}
