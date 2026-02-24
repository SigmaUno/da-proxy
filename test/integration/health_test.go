//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/metrics"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

func TestIntegration_HealthChecker_AllBackends(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{prunedRPC},
		CelestiaAppGRPC:     config.Endpoints{prunedGRPC},
		CelestiaAppREST:     config.Endpoints{"http://195.154.212.53:1317"},
		CelestiaNodeRPC:     config.Endpoints{archivalDA},
		HealthCheckInterval: 5 * time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	logger, _ := zap.NewDevelopment()

	checker := proxy.NewHealthChecker(backends, backends.HealthCheckInterval, m, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go checker.Start(ctx)

	// Wait for the first check cycle to complete.
	time.Sleep(2 * time.Second)
	cancel()

	statuses := checker.Status()
	require.NotEmpty(t, statuses)

	// celestia-app-rpc (pruned) should be healthy.
	appRPC, ok := statuses["celestia-app-rpc"]
	require.True(t, ok, "celestia-app-rpc should be checked")
	assert.True(t, appRPC.Healthy, "celestia-app-rpc should be healthy")
	assert.True(t, appRPC.LatencyMs > 0, "latency should be positive")
	assert.Empty(t, appRPC.Error)

	// celestia-node-rpc (DA) should be healthy.
	nodeRPC, ok := statuses["celestia-node-rpc"]
	require.True(t, ok, "celestia-node-rpc should be checked")
	assert.True(t, nodeRPC.Healthy, "celestia-node-rpc should be healthy")
	assert.True(t, nodeRPC.LatencyMs > 0, "latency should be positive")
	assert.Empty(t, nodeRPC.Error)

	// celestia-app-rest: port 1317 is closed, so this should be unhealthy.
	appREST, ok := statuses["celestia-app-rest"]
	require.True(t, ok, "celestia-app-rest should be checked")
	assert.False(t, appREST.Healthy, "celestia-app-rest should be unhealthy (port 1317 closed)")
	assert.NotEmpty(t, appREST.Error)
}

func TestIntegration_HealthChecker_MetricsUpdated(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{prunedRPC},
		CelestiaAppGRPC:     config.Endpoints{prunedGRPC},
		CelestiaAppREST:     config.Endpoints{"http://195.154.212.53:1317"},
		CelestiaNodeRPC:     config.Endpoints{archivalDA},
		HealthCheckInterval: 5 * time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	logger, _ := zap.NewDevelopment()

	checker := proxy.NewHealthChecker(backends, backends.HealthCheckInterval, m, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go checker.Start(ctx)
	time.Sleep(2 * time.Second)
	cancel()

	// Verify Prometheus metrics were set.
	families, err := reg.Gather()
	require.NoError(t, err)

	var foundBackendUp bool
	for _, fam := range families {
		if fam.GetName() == "daproxy_backend_up" {
			foundBackendUp = true
			// Should have 3 backends checked.
			assert.GreaterOrEqual(t, len(fam.GetMetric()), 3)

			for _, metric := range fam.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "backend" {
						switch label.GetValue() {
						case "celestia-app-rpc":
							assert.Equal(t, float64(1), metric.GetGauge().GetValue())
						case "celestia-node-rpc":
							assert.Equal(t, float64(1), metric.GetGauge().GetValue())
						case "celestia-app-rest":
							assert.Equal(t, float64(0), metric.GetGauge().GetValue())
						}
					}
				}
			}
		}
	}
	assert.True(t, foundBackendUp, "daproxy_backend_up metric should exist")
}

func TestIntegration_HealthChecker_ArchivalNode(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:      config.Endpoints{archivalRPC},
		CelestiaAppGRPC:     config.Endpoints{"195.154.103.60:9090"},
		CelestiaAppREST:     config.Endpoints{"http://195.154.103.60:1317"},
		CelestiaNodeRPC:     config.Endpoints{archivalDA},
		HealthCheckInterval: 5 * time.Second,
	}

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	logger, _ := zap.NewDevelopment()

	checker := proxy.NewHealthChecker(backends, backends.HealthCheckInterval, m, logger)

	ctx, cancel := context.WithCancel(context.Background())
	go checker.Start(ctx)
	time.Sleep(2 * time.Second)
	cancel()

	statuses := checker.Status()
	appRPC := statuses["celestia-app-rpc"]
	assert.True(t, appRPC.Healthy, "archival celestia-app-rpc should be healthy")

	nodeRPC := statuses["celestia-node-rpc"]
	assert.True(t, nodeRPC.Healthy, "archival DA node should be healthy")
}
