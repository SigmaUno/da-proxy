// Package proxy provides HTTP and gRPC reverse proxy routing for Celestia backends.
package proxy

import (
	"net/http"
	"net/http/httputil"
	"net/url"

	"go.uber.org/zap"
)

// GRPCProxy handles transparent gRPC reverse proxying.
// In Phase 1 this uses HTTP/1.1 transport for simplicity.
// Full HTTP/2 gRPC passthrough is planned for Phase 2.
type GRPCProxy struct {
	targetAddr string
	logger     *zap.Logger
	proxy      *httputil.ReverseProxy
}

// NewGRPCProxy creates a new gRPC reverse proxy targeting the given address.
func NewGRPCProxy(targetAddr string, logger *zap.Logger) *GRPCProxy {
	target := &url.URL{
		Scheme: "http",
		Host:   targetAddr,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			logger.Error("gRPC proxy error",
				zap.String("target", targetAddr),
				zap.Error(err),
			)
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	return &GRPCProxy{
		targetAddr: targetAddr,
		logger:     logger,
		proxy:      proxy,
	}
}

// Handler returns an http.Handler that proxies gRPC traffic.
func (g *GRPCProxy) Handler() http.Handler {
	return g.proxy
}
