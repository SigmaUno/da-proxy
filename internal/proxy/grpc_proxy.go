// Package proxy provides HTTP and gRPC reverse proxy routing for Celestia backends.
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// GRPCProxy handles transparent gRPC reverse proxying.
// In Phase 1 this uses HTTP/1.1 transport for simplicity.
// Full HTTP/2 gRPC passthrough is planned for Phase 2.
type GRPCProxy struct {
	balancer *Balancer
	logger   *zap.Logger
	proxy    *httputil.ReverseProxy
}

// NewGRPCProxy creates a new gRPC reverse proxy targeting the given addresses.
func NewGRPCProxy(targets config.Endpoints, logger *zap.Logger) *GRPCProxy {
	bal := NewBalancer(targets)

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			addr := bal.Next()
			target := &url.URL{
				Scheme: "http",
				Host:   addr,
			}
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Error("gRPC proxy error", zap.Error(err))
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	return &GRPCProxy{
		balancer: bal,
		logger:   logger,
		proxy:    proxy,
	}
}

// Handler returns an http.Handler that proxies gRPC traffic.
func (g *GRPCProxy) Handler() http.Handler {
	return g.proxy
}
