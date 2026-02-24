package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/metrics"
)

func TestHealthChecker_HealthyBackends(t *testing.T) {
	// Mock healthy backends.
	appRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer appRPC.Close()

	nodeRPC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{"type":"full"},"id":1}`))
	}))
	defer nodeRPC.Close()

	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{appRPC.URL},
		CelestiaNodeRPC:     config.Endpoints{nodeRPC.URL},
		HealthCheckInterval: time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	hc := NewHealthChecker(backends, time.Second, m, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go hc.Start(ctx)
	defer cancel()

	// Wait for first check.
	time.Sleep(500 * time.Millisecond)

	statuses := hc.Status()
	require.Len(t, statuses, 2)

	assert.True(t, statuses["celestia-app-rpc"].Healthy)
	assert.True(t, statuses["celestia-node-rpc"].Healthy)

	for _, s := range statuses {
		assert.Greater(t, s.LatencyMs, float64(0))
		assert.False(t, s.LastCheck.IsZero())
	}
}

func TestHealthChecker_UnhealthyBackend(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{"http://127.0.0.1:1"},
		CelestiaNodeRPC:     config.Endpoints{"http://127.0.0.1:1"},
		HealthCheckInterval: time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	core, _ := observer.New(zap.ErrorLevel)
	logger := zap.New(core)

	hc := NewHealthChecker(backends, time.Second, m, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go hc.Start(ctx)
	defer cancel()

	time.Sleep(500 * time.Millisecond)

	statuses := hc.Status()
	for _, s := range statuses {
		assert.False(t, s.Healthy)
		assert.NotEmpty(t, s.Error)
	}
}

func TestHealthChecker_MetricsUpdated(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
	}))
	defer healthy.Close()

	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{healthy.URL},
		CelestiaNodeRPC:     config.Endpoints{healthy.URL},
		HealthCheckInterval: time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	hc := NewHealthChecker(backends, time.Second, m, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go hc.Start(ctx)
	defer cancel()

	time.Sleep(500 * time.Millisecond)

	// Verify Prometheus gauges were updated.
	families, err := reg.Gather()
	require.NoError(t, err)

	found := false
	for _, fam := range families {
		if fam.GetName() == "daproxy_backend_up" {
			found = true
			for _, metric := range fam.GetMetric() {
				assert.Equal(t, float64(1), metric.GetGauge().GetValue())
			}
		}
	}
	assert.True(t, found, "daproxy_backend_up metric not found")
}

func TestHealthChecker_StateChangeLogged(t *testing.T) {
	// Start with healthy backend, then make it unhealthy.
	var healthy atomic.Bool
	healthy.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if healthy.Load() {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","result":{},"id":1}`))
		} else {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{srv.URL},
		CelestiaNodeRPC:     config.Endpoints{srv.URL},
		HealthCheckInterval: 200 * time.Millisecond,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	core, logs := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	hc := NewHealthChecker(backends, 200*time.Millisecond, m, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go hc.Start(ctx)
	defer cancel()

	// Wait for healthy check.
	time.Sleep(300 * time.Millisecond)
	statuses := hc.Status()
	for _, s := range statuses {
		assert.True(t, s.Healthy)
	}

	// Make unhealthy.
	healthy.Store(false)
	time.Sleep(400 * time.Millisecond)

	// Should have "backend down" log messages.
	hasDownLog := false
	for _, entry := range logs.All() {
		if entry.Message == "backend down" {
			hasDownLog = true
			break
		}
	}
	assert.True(t, hasDownLog, "expected 'backend down' log message")
}
