// Package main is the entry point for the da-proxy service.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/admin"
	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/logging"
	"github.com/SigmaUno/da-proxy/internal/metrics"
	"github.com/SigmaUno/da-proxy/internal/middleware"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

var version = "dev"

func main() {
	configPath := flag.String("config", "", "path to config file")
	flag.Parse()

	cfgPath := config.ResolveConfigPath(*configPath)

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	logger, err := logging.NewLogger(cfg.Logging.Level, cfg.Logging.Format)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to create logger: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = logger.Sync() }()

	logger.Info("starting da-proxy",
		zap.String("version", version),
		zap.String("config", cfgPath),
		zap.String("proxy_listen", cfg.Server.Listen),
		zap.String("admin_listen", cfg.Admin.Listen),
		zap.String("metrics_listen", cfg.Metrics.Listen),
	)

	// Token store (SQLite-backed with in-memory cache).
	tokenDBPath := filepath.Join(filepath.Dir(cfg.Admin.LogDBPath), "tokens.db")
	if dir := filepath.Dir(tokenDBPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	tokenStore, err := auth.NewSQLiteTokenStore(tokenDBPath, 30*time.Second)
	if err != nil {
		logger.Fatal("failed to create token store", zap.Error(err))
	}
	defer func() { _ = tokenStore.Close() }()

	// Migrate config-file tokens to database.
	if len(cfg.Tokens) > 0 {
		imported, migrateErr := tokenStore.MigrateConfigTokens(cfg.Tokens)
		if migrateErr != nil {
			logger.Error("failed to migrate config tokens", zap.Error(migrateErr))
		} else if imported > 0 {
			logger.Info("migrated config tokens to database", zap.Int("count", imported))
		}
	}

	// Rate limiter store.
	rateLimiterStore := middleware.NewRateLimiterStore()

	// Ring buffer for recent logs.
	ringBuffer := logging.NewRingBuffer(cfg.Admin.LogBufferSize)

	// SQLite persistent log store.
	if dir := filepath.Dir(cfg.Admin.LogDBPath); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	sqliteStore, err := logging.NewSQLiteStore(
		cfg.Admin.LogDBPath,
		time.Duration(cfg.Admin.LogRetentionDays)*24*time.Hour,
	)
	if err != nil {
		logger.Fatal("failed to create log store", zap.Error(err))
	}
	defer func() { _ = sqliteStore.Close() }()

	// Prometheus metrics.
	registry := prometheus.NewRegistry()
	promMetrics := metrics.NewMetrics(registry)

	// Router.
	router := proxy.NewRouter(cfg.Backends)

	// Max body size.
	maxBodySize, err := cfg.Server.MaxBodySizeBytes()
	if err != nil {
		logger.Fatal("invalid max_body_size", zap.Error(err))
	}

	// Proxy handler.
	proxyHandler := proxy.NewHandler(router, maxBodySize, logger)

	// gRPC proxy.
	grpcProxy := proxy.NewGRPCProxy(cfg.Backends.CelestiaAppGRPC, logger)

	// Health checker.
	healthChecker := proxy.NewHealthChecker(
		cfg.Backends,
		cfg.Backends.HealthCheckInterval,
		promMetrics,
		logger,
	)

	// --- Proxy server ---
	proxyServer := echo.New()
	proxyServer.HideBanner = true
	proxyServer.HidePort = true

	proxyServer.Use(
		middleware.RequestID(),
		middleware.Auth(tokenStore),
		middleware.RateLimit(rateLimiterStore),
		middleware.AccessLogger(logger, ringBuffer, sqliteStore),
		middleware.MetricsMiddleware(promMetrics),
	)

	// gRPC routing: if content-type is application/grpc, use the gRPC proxy.
	proxyServer.Any("/*", func(c echo.Context) error {
		ct := c.Request().Header.Get("Content-Type")
		if len(ct) >= 16 && ct[:16] == "application/grpc" {
			grpcProxy.Handler().ServeHTTP(c.Response(), c.Request())
			return nil
		}
		return proxyHandler.HandleRequest(c)
	})

	// --- Admin server ---
	adminServer := admin.NewServer(cfg.Admin, admin.Dependencies{
		LogBuffer:     ringBuffer,
		LogStore:      sqliteStore,
		HealthChecker: healthChecker,
		TokenStore:    tokenStore,
		Config:        cfg,
		Logger:        logger,
		StartTime:     time.Now(),
		Version:       version,
	})

	// --- Metrics server ---
	metricsServer := metrics.NewServer(cfg.Metrics.Listen, registry, logger)

	// --- Start all servers ---
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Health checker.
	go healthChecker.Start(ctx)

	// Proxy server.
	go func() {
		var srvErr error
		if cfg.Server.TLSCert != "" && cfg.Server.TLSKey != "" {
			logger.Info("proxy server starting with TLS", zap.String("addr", cfg.Server.Listen))
			srvErr = proxyServer.StartTLS(cfg.Server.Listen, cfg.Server.TLSCert, cfg.Server.TLSKey)
		} else {
			logger.Info("proxy server starting", zap.String("addr", cfg.Server.Listen))
			srvErr = proxyServer.Start(cfg.Server.Listen)
		}
		if srvErr != nil && srvErr != http.ErrServerClosed {
			logger.Fatal("proxy server failed", zap.Error(srvErr))
		}
	}()

	// Admin server.
	go func() {
		if err := adminServer.Start(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("admin server failed", zap.Error(err))
		}
	}()

	// Metrics server.
	if cfg.Metrics.IsEnabled() {
		go func() {
			if err := metricsServer.Start(); err != nil && err != http.ErrServerClosed {
				logger.Fatal("metrics server failed", zap.Error(err))
			}
		}()
	}

	// --- Graceful shutdown ---
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	cancel() // Stop health checker.

	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("proxy server shutdown error", zap.Error(err))
	}
	if err := adminServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("admin server shutdown error", zap.Error(err))
	}
	if cfg.Metrics.IsEnabled() {
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("metrics server shutdown error", zap.Error(err))
		}
	}

	logger.Info("shutdown complete")
}
