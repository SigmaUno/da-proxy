//nolint:revive // metrics is the correct domain name for this package
package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMetrics_RegistersWithoutPanic(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)
	assert.NotNil(t, m)
	assert.NotNil(t, m.RequestsTotal)
	assert.NotNil(t, m.RequestDuration)
	assert.NotNil(t, m.RequestSize)
	assert.NotNil(t, m.ResponseSize)
	assert.NotNil(t, m.ErrorsTotal)
	assert.NotNil(t, m.BackendErrorsTotal)
	assert.NotNil(t, m.BackendUp)
	assert.NotNil(t, m.BackendHealthDuration)
	assert.NotNil(t, m.RateLimitRemaining)
	assert.NotNil(t, m.RateLimitHitsTotal)
}

func TestMetrics_IncrementCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	labels := prometheus.Labels{
		"method":      "blob.Get",
		"backend":     "celestia-node-rpc",
		"token_name":  "test-service",
		"status_code": "200",
	}
	m.RequestsTotal.With(labels).Inc()

	// Gather and verify.
	families, err := reg.Gather()
	require.NoError(t, err)

	found := false
	for _, fam := range families {
		if fam.GetName() == "daproxy_requests_total" {
			found = true
			assert.Len(t, fam.GetMetric(), 1)
			assert.Equal(t, float64(1), fam.GetMetric()[0].GetCounter().GetValue())
		}
	}
	assert.True(t, found, "daproxy_requests_total not found in gathered metrics")
}

func TestMetrics_ObserveHistogram(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	labels := prometheus.Labels{
		"method":     "status",
		"backend":    "celestia-app-rpc",
		"token_name": "test",
	}
	m.RequestDuration.With(labels).Observe(0.042)

	families, err := reg.Gather()
	require.NoError(t, err)

	found := false
	for _, fam := range families {
		if fam.GetName() == "daproxy_request_duration_seconds" {
			found = true
			assert.Equal(t, uint64(1), fam.GetMetric()[0].GetHistogram().GetSampleCount())
		}
	}
	assert.True(t, found)
}

func TestMetrics_BackendUpGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.BackendUp.With(prometheus.Labels{"backend": "celestia-app-rpc"}).Set(1)
	m.BackendUp.With(prometheus.Labels{"backend": "celestia-node-rpc"}).Set(0)

	families, err := reg.Gather()
	require.NoError(t, err)

	found := false
	for _, fam := range families {
		if fam.GetName() == "daproxy_backend_up" {
			found = true
			assert.Len(t, fam.GetMetric(), 2)
		}
	}
	assert.True(t, found)
}

func TestMetrics_DoubleRegisterPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	// Second registration should panic.
	assert.Panics(t, func() {
		_ = NewMetrics(reg)
	})
}
