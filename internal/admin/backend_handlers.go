package admin

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

// backendInfo is the JSON response structure for a single backend.
type backendInfo struct {
	Name            string   `json:"name"`
	Endpoints       []string `json:"endpoints"`
	Healthy         *bool    `json:"healthy,omitempty"`
	HealthLatencyMs *float64 `json:"health_latency_ms,omitempty"`
	AvgLatencyMs    float64  `json:"avg_latency_ms"`
	TotalRequests   int64    `json:"total_requests"`
	Methods         []string `json:"methods"`
}

func (h *handlers) handleBackends(c echo.Context) error {
	// 1. Parse optional window query parameter (default 24h).
	var duration time.Duration
	switch c.QueryParam("window") {
	case "1h":
		duration = time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	default:
		duration = 24 * time.Hour
	}
	since := time.Now().Add(-duration)

	// 2. Build the static backend list from config.
	type backendDef struct {
		name      string
		endpoints config.Endpoints
	}

	var defs []backendDef
	if h.deps.Config != nil {
		b := h.deps.Config.Backends
		defs = []backendDef{
			{"celestia-app-rpc", b.CelestiaAppRPC},
			{"celestia-app-grpc", b.CelestiaAppGRPC},
			{"celestia-app-rest", b.CelestiaAppREST},
			{"celestia-node-rpc", b.CelestiaNodeRPC},
			{"celestia-node-archival-rpc", b.CelestiaNodeArchivalRPC},
			{"celestia-app-archival-rpc", b.CelestiaAppArchivalRPC},
		}
	}

	// 3. Get health status.
	var healthMap map[string]proxy.HealthStatus
	if h.deps.HealthChecker != nil {
		healthMap = h.deps.HealthChecker.Status()
	}

	// 4. Get backend stats from log store.
	statsMap := make(map[string]logging.BackendStat)
	if h.deps.LogStore != nil {
		stats, err := h.deps.LogStore.BackendStats(since)
		if err != nil && h.deps.Logger != nil {
			h.deps.Logger.Error("failed to query backend stats", zap.Error(err))
		}
		for _, s := range stats {
			statsMap[s.Backend] = s
		}
	}

	// 5. Assemble response.
	backends := make([]backendInfo, 0)
	for _, def := range defs {
		if len(def.endpoints) == 0 {
			continue // Skip unconfigured backends.
		}

		info := backendInfo{
			Name:      def.name,
			Endpoints: []string(def.endpoints),
			Methods:   []string{},
		}

		// Merge health status. Health checker keys use the same base name
		// but with /0, /1 suffixes for multi-endpoint backends.
		// Aggregate: healthy = all endpoints healthy.
		if healthMap != nil {
			allHealthy := true
			var totalLatency float64
			var count int
			for key, hs := range healthMap {
				if key == def.name || strings.HasPrefix(key, def.name+"/") {
					if !hs.Healthy {
						allHealthy = false
					}
					totalLatency += hs.LatencyMs
					count++
				}
			}
			if count > 0 {
				healthy := allHealthy
				info.Healthy = &healthy
				avgHealthLatency := totalLatency / float64(count)
				info.HealthLatencyMs = &avgHealthLatency
			}
		}

		// Merge log-derived stats.
		if stat, ok := statsMap[def.name]; ok {
			info.AvgLatencyMs = stat.AvgLatencyMs
			info.TotalRequests = stat.TotalRequests
			if len(stat.Methods) > 0 {
				// Filter out empty method strings (REST/gRPC may log empty method).
				methods := make([]string, 0, len(stat.Methods))
				for _, m := range stat.Methods {
					if m != "" {
						methods = append(methods, m)
					}
				}
				if len(methods) > 0 {
					info.Methods = methods
				}
			}
		}

		backends = append(backends, info)
	}

	return c.JSON(http.StatusOK, map[string]interface{}{
		"backends": backends,
	})
}
