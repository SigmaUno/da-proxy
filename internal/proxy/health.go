package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/metrics"
)

// HealthStatus represents the health of a single backend.
type HealthStatus struct {
	Backend   string    `json:"backend"`
	Healthy   bool      `json:"healthy"`
	LatencyMs float64   `json:"latency_ms"`
	Error     string    `json:"error,omitempty"`
	LastCheck time.Time `json:"last_check"`
}

// HealthChecker manages periodic health checks for all backends.
type HealthChecker interface {
	Start(ctx context.Context)
	Status() map[string]HealthStatus
}

type healthChecker struct {
	backends config.BackendsConfig
	interval time.Duration
	metrics  *metrics.Metrics
	logger   *zap.Logger
	client   *http.Client
	mu       sync.RWMutex
	statuses map[string]HealthStatus
}

// NewHealthChecker creates a HealthChecker for all configured backends.
func NewHealthChecker(backends config.BackendsConfig, interval time.Duration, m *metrics.Metrics, logger *zap.Logger) HealthChecker {
	return &healthChecker{
		backends: backends,
		interval: interval,
		metrics:  m,
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Second},
		statuses: make(map[string]HealthStatus),
	}
}

func (h *healthChecker) Start(ctx context.Context) {
	// Run immediately, then on interval.
	h.checkAll()

	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			h.checkAll()
		case <-ctx.Done():
			return
		}
	}
}

func (h *healthChecker) Status() map[string]HealthStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string]HealthStatus, len(h.statuses))
	for k, v := range h.statuses {
		result[k] = v
	}
	return result
}

func (h *healthChecker) checkAll() {
	checks := []struct {
		name string
		fn   func() HealthStatus
	}{
		{"celestia-app-rpc", func() HealthStatus { return h.checkHTTP(h.backends.CelestiaAppRPC, "/health") }},
		{"celestia-app-rest", func() HealthStatus {
			return h.checkHTTP(h.backends.CelestiaAppREST, "/cosmos/base/tendermint/v1beta1/syncing")
		}},
		{"celestia-node-rpc", func() HealthStatus { return h.checkJSONRPC(h.backends.CelestiaNodeRPC) }},
	}

	for _, check := range checks {
		status := check.fn()
		status.Backend = check.name
		status.LastCheck = time.Now()

		h.mu.Lock()
		prev, existed := h.statuses[check.name]
		h.statuses[check.name] = status
		h.mu.Unlock()

		// Update metrics.
		if h.metrics != nil {
			up := float64(0)
			if status.Healthy {
				up = 1
			}
			h.metrics.BackendUp.With(prometheus.Labels{"backend": check.name}).Set(up)
			h.metrics.BackendHealthDuration.With(prometheus.Labels{"backend": check.name}).Set(status.LatencyMs / 1000)
		}

		// Log state changes.
		if existed && prev.Healthy != status.Healthy {
			if status.Healthy {
				h.logger.Info("backend recovered", zap.String("backend", check.name))
			} else {
				h.logger.Error("backend down", zap.String("backend", check.name), zap.String("error", status.Error))
			}
		} else if !existed && !status.Healthy {
			h.logger.Error("backend unreachable on first check", zap.String("backend", check.name), zap.String("error", status.Error))
		}
	}
}

func (h *healthChecker) checkHTTP(baseURL, path string) HealthStatus {
	start := time.Now()
	resp, err := h.client.Get(baseURL + path)
	latency := float64(time.Since(start).Nanoseconds()) / 1e6

	if err != nil {
		return HealthStatus{LatencyMs: latency, Error: err.Error()}
	}
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return HealthStatus{Healthy: true, LatencyMs: latency}
	}
	return HealthStatus{LatencyMs: latency, Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
}

func (h *healthChecker) checkJSONRPC(baseURL string) HealthStatus {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"node.Info","params":[]}`)
	start := time.Now()
	resp, err := h.client.Post(baseURL, "application/json", bytes.NewReader(body))
	latency := float64(time.Since(start).Nanoseconds()) / 1e6

	if err != nil {
		return HealthStatus{LatencyMs: latency, Error: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return HealthStatus{LatencyMs: latency, Error: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	var result struct {
		Error *json.RawMessage `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return HealthStatus{LatencyMs: latency, Error: "invalid JSON response"}
	}
	if result.Error != nil {
		return HealthStatus{LatencyMs: latency, Error: "JSON-RPC error"}
	}

	return HealthStatus{Healthy: true, LatencyMs: latency}
}
