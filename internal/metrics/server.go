// Package metrics provides Prometheus metrics collection and serving for da-proxy.
package metrics //nolint:revive // metrics is the correct domain name for this package

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// Server serves the /metrics endpoint for Prometheus scraping.
type Server struct {
	httpServer *http.Server
	logger     *zap.Logger
}

// NewServer creates a Prometheus metrics server on the given address.
func NewServer(listenAddr string, registry *prometheus.Registry, logger *zap.Logger) *Server {
	// Register default Go runtime and process collectors.
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	return &Server{
		httpServer: &http.Server{
			Addr:    listenAddr,
			Handler: mux,
		},
		logger: logger,
	}
}

// Start begins serving the metrics endpoint. Blocks until the server is stopped.
func (s *Server) Start() error {
	s.logger.Info("metrics server starting", zap.String("addr", s.httpServer.Addr))
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the metrics server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}
