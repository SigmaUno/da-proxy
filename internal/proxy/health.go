package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
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
	backends      config.BackendsConfig
	interval      time.Duration
	metrics       *metrics.Metrics
	logger        *zap.Logger
	client        *http.Client
	heightTracker *HeightTracker
	mu            sync.RWMutex
	statuses      map[string]HealthStatus
}

// NewHealthChecker creates a HealthChecker for all configured backends.
// If a HeightTracker is provided (non-nil), the checker will poll head height
// on each interval for height-aware routing.
func NewHealthChecker(backends config.BackendsConfig, interval time.Duration, m *metrics.Metrics, logger *zap.Logger, ht ...*HeightTracker) HealthChecker {
	var tracker *HeightTracker
	if len(ht) > 0 {
		tracker = ht[0]
	}
	return &healthChecker{
		backends:      backends,
		interval:      interval,
		metrics:       m,
		logger:        logger,
		client:        &http.Client{Timeout: 5 * time.Second},
		statuses:      make(map[string]HealthStatus),
		heightTracker: tracker,
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

func (h *healthChecker) endpointName(base string, idx, total int) string {
	if total <= 1 {
		return base
	}
	return fmt.Sprintf("%s/%d", base, idx)
}

func (h *healthChecker) checkAll() {
	type healthCheck struct {
		name string
		fn   func() HealthStatus
	}

	var checks []healthCheck

	for i, ep := range h.backends.CelestiaAppRPC {
		endpoint := ep
		checks = append(checks, healthCheck{
			h.endpointName("celestia-app-rpc", i, len(h.backends.CelestiaAppRPC)),
			func() HealthStatus { return h.checkHTTP(endpoint, "/health") },
		})
	}
	for i, ep := range h.backends.CelestiaNodeRPC {
		endpoint := ep
		checks = append(checks, healthCheck{
			h.endpointName("celestia-node-rpc", i, len(h.backends.CelestiaNodeRPC)),
			func() HealthStatus { return h.checkJSONRPC(endpoint) },
		})
	}

	// Archival backends.
	for i, ep := range h.backends.CelestiaNodeArchivalRPC {
		endpoint := ep
		checks = append(checks, healthCheck{
			h.endpointName("celestia-node-archival-rpc", i, len(h.backends.CelestiaNodeArchivalRPC)),
			func() HealthStatus { return h.checkJSONRPC(endpoint) },
		})
	}
	for i, ep := range h.backends.CelestiaAppArchivalRPC {
		endpoint := ep
		checks = append(checks, healthCheck{
			h.endpointName("celestia-app-archival-rpc", i, len(h.backends.CelestiaAppArchivalRPC)),
			func() HealthStatus { return h.checkHTTP(endpoint, "/health") },
		})
	}

	// gRPC backends.
	for i, ep := range h.backends.CelestiaAppGRPC {
		endpoint := ep
		checks = append(checks, healthCheck{
			h.endpointName("celestia-app-grpc", i, len(h.backends.CelestiaAppGRPC)),
			func() HealthStatus { return h.checkTCPDial(endpoint) },
		})
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

	// Poll head height for height-aware routing.
	if h.heightTracker != nil && h.heightTracker.Enabled() {
		h.pollHeadHeight()
	}
}

func (h *healthChecker) pollHeadHeight() {
	body := []byte(`{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`)
	resp, err := h.client.Post(h.backends.CelestiaAppRPC.First(), "application/json", bytes.NewReader(body))
	if err != nil {
		h.logger.Debug("failed to poll head height", zap.Error(err))
		return
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Result struct {
			SyncInfo struct {
				LatestBlockHeight string `json:"latest_block_height"`
			} `json:"sync_info"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	if result.Result.SyncInfo.LatestBlockHeight != "" {
		var height int64
		if _, err := fmt.Sscanf(result.Result.SyncInfo.LatestBlockHeight, "%d", &height); err == nil && height > 0 {
			h.heightTracker.SetHead(height)
			h.logger.Debug("updated head height", zap.Int64("height", height))
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

func (h *healthChecker) checkTCPDial(addr string) HealthStatus {
	// Strip scheme if present (gRPC endpoints are typically host:port).
	addr = strings.TrimPrefix(addr, "http://")
	addr = strings.TrimPrefix(addr, "https://")

	start := time.Now()
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	latency := float64(time.Since(start).Nanoseconds()) / 1e6

	if err != nil {
		return HealthStatus{LatencyMs: latency, Error: err.Error()}
	}
	_ = conn.Close()
	return HealthStatus{Healthy: true, LatencyMs: latency}
}
