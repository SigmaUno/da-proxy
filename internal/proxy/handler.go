package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/auth"
	"github.com/SigmaUno/da-proxy/internal/cache"
	"github.com/SigmaUno/da-proxy/internal/middleware"
)

// HeaderXDABackend is the response header indicating which backend handled the request.
const HeaderXDABackend = "X-DA-Backend"

// HeaderXCacheStatus is the response header indicating cache hit/miss.
const HeaderXCacheStatus = "X-Cache-Status"

// Handler is the main proxy handler.
type Handler struct {
	router      Router
	maxBodySize int64
	logger      *zap.Logger
	cache       cache.Cache
}

// NewHandler creates the proxy handler.
func NewHandler(router Router, maxBodySize int64, logger *zap.Logger, opts ...HandlerOption) *Handler {
	h := &Handler{
		router:      router,
		maxBodySize: maxBodySize,
		logger:      logger,
		cache:       cache.NoopCache{},
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// HandlerOption configures the proxy handler.
type HandlerOption func(*Handler)

// WithCache sets the response cache on the handler.
func WithCache(c cache.Cache) HandlerOption {
	return func(h *Handler) {
		if c != nil {
			h.cache = c
		}
	}
}

// HandleRequest is the Echo handler for all proxied requests.
func (h *Handler) HandleRequest(c echo.Context) error {
	req := c.Request()
	contentType := req.Header.Get("Content-Type")
	path := req.URL.Path

	// Read and buffer the request body for method inspection.
	var body []byte
	if req.Body != nil {
		reader := io.LimitReader(req.Body, h.maxBodySize+1)
		var err error
		body, err = io.ReadAll(reader)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "failed to read request body")
		}
		_ = req.Body.Close()

		if int64(len(body)) > h.maxBodySize {
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "request body too large")
		}
	}

	// Route the request.
	decision, err := h.router.Route(contentType, path, body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Set context values for logging/metrics middleware.
	c.Set(middleware.ContextKeyBackend, string(decision.Backend))
	c.Set(middleware.ContextKeyRPCMethod, decision.Method)

	// Check method authorization against token scope and allowlist.
	if decision.Method != "" {
		if infoVal := c.Get(middleware.ContextKeyTokenInfo); infoVal != nil {
			if info, ok := infoVal.(auth.TokenInfo); ok {
				if !info.IsMethodAllowed(decision.Method) {
					return echo.NewHTTPError(http.StatusForbidden, "method not allowed for this token")
				}
			}
		}
	}

	// Cache lookup for cacheable historical queries.
	height := ExtractHeight(decision.Method, body)
	if cache.IsCacheable(decision.Method, height) {
		if cached, hit := h.cache.Get(req.Context(), decision.Method, height, body); hit {
			resp := c.Response()
			resp.Header().Set("Content-Type", "application/json")
			resp.Header().Set(HeaderXDABackend, string(decision.Backend))
			resp.Header().Set(HeaderXCacheStatus, "HIT")
			if reqID, _ := c.Get(middleware.ContextKeyRequestID).(string); reqID != "" {
				resp.Header().Set(middleware.HeaderXRequestID, reqID)
			}
			resp.WriteHeader(http.StatusOK)
			_, _ = resp.Write(cached)
			return nil
		}
	}

	// Build and execute the reverse proxy.
	targetURL, err := url.Parse(decision.TargetURL)
	if err != nil {
		h.logger.Error("invalid backend URL",
			zap.String("backend", string(decision.Backend)),
			zap.String("url", decision.TargetURL),
			zap.Error(err),
		)
		return echo.NewHTTPError(http.StatusBadGateway, "backend configuration error")
	}

	// Determine if we should capture the response for caching.
	shouldCache := cache.IsCacheable(decision.Method, height)

	proxy := &httputil.ReverseProxy{
		Director: func(outReq *http.Request) {
			outReq.URL.Scheme = targetURL.Scheme
			outReq.URL.Host = targetURL.Host
			outReq.Host = targetURL.Host

			// For REST requests, preserve the path.
			// For JSON-RPC, path is just "/".
			if path != "" && path != "/" {
				outReq.URL.Path = path
			} else {
				outReq.URL.Path = "/"
			}

			// Replay the buffered body.
			if body != nil {
				outReq.Body = io.NopCloser(bytes.NewReader(body))
				outReq.ContentLength = int64(len(body))
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			// Add our response headers.
			reqID, _ := c.Get(middleware.ContextKeyRequestID).(string)
			if reqID != "" {
				resp.Header.Set(middleware.HeaderXRequestID, reqID)
			}
			resp.Header.Set(HeaderXDABackend, string(decision.Backend))

			// Cache successful responses for cacheable methods.
			if shouldCache && resp.StatusCode == http.StatusOK {
				resp.Header.Set(HeaderXCacheStatus, "MISS")
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr == nil {
					h.cache.Set(req.Context(), decision.Method, height, body, respBody)
					resp.Body = io.NopCloser(bytes.NewReader(respBody))
					resp.ContentLength = int64(len(respBody))
				}
			}

			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
			h.logger.Error("backend proxy error",
				zap.String("backend", string(decision.Backend)),
				zap.Error(proxyErr),
			)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"backend unavailable"}`))
		},
	}

	proxy.ServeHTTP(c.Response(), req)
	return nil
}
