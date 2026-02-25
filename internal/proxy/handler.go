package proxy

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

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
	path := req.URL.Path
	rawQuery := req.URL.RawQuery

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
	decision, err := h.router.Route(body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Set context values for logging/metrics middleware.
	c.Set(middleware.ContextKeyBackend, string(decision.Backend))
	c.Set(middleware.ContextKeyRPCMethod, decision.Method)

	h.logger.Info("rpc_request",
		zap.String("method", decision.Method),
		zap.String("backend", string(decision.Backend)),
		zap.String("target", decision.TargetURL),
		zap.String("path", path),
		zap.String("client_ip", c.RealIP()),
	)

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

	// Determine height from body (JSON-RPC) or query string (GET).
	height := ExtractHeight(decision.Method, body)
	if height == 0 {
		height = ExtractHeightFromQuery(rawQuery)
	}

	// Cache lookup for cacheable historical queries.
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

	// If the request targets a specific height and there are multiple endpoints
	// to try, use the retry path that can fall back on pruned errors.
	endpoints := h.router.AllEndpoints(decision.Backend)
	hasArchival := h.router.HasArchivalBackend(decision.Backend)
	canRetry := height > 0 && (len(endpoints) > 1 || hasArchival)

	if canRetry {
		h.logger.Debug("using retry path",
			zap.String("backend", string(decision.Backend)),
			zap.Int64("height", height),
			zap.Int("pool_endpoints", len(endpoints)),
			zap.Bool("has_archival", hasArchival),
			zap.String("target", decision.TargetURL),
		)
		return h.proxyWithRetry(c, req, decision, targetURL, path, rawQuery, body, height, shouldCache)
	}

	return h.proxyDirect(c, req, decision, targetURL, path, rawQuery, body, height, shouldCache)
}

// proxyDirect sends the request to a single backend with no fallback.
func (h *Handler) proxyDirect(c echo.Context, req *http.Request, decision RouteDecision, targetURL *url.URL, path, rawQuery string, body []byte, height int64, shouldCache bool) error {
	proxy := h.buildProxy(c, req, decision, targetURL, path, rawQuery, body, height, shouldCache)

	start := time.Now()
	proxy.ServeHTTP(c.Response(), req)
	h.router.RecordLatency(decision.Backend, decision.TargetURL, time.Since(start))

	return nil
}

// proxyWithRetry tries the primary endpoint first. If it returns a pruned
// error, it retries against other endpoints in the same backend pool, then
// against the archival backend if configured.
func (h *Handler) proxyWithRetry(c echo.Context, req *http.Request, decision RouteDecision, targetURL *url.URL, path, rawQuery string, body []byte, height int64, shouldCache bool) error {
	// Try the initially-selected endpoint.
	buf := h.tryEndpoint(req, targetURL, path, rawQuery, body, decision.Backend, decision.TargetURL)
	if !isPrunedError(buf.statusCode, buf.body.Bytes()) {
		return h.flushBuffered(c, buf, decision.Backend, decision.Method, height, body, shouldCache)
	}

	triedURL := targetURL.String()
	h.logger.Info("endpoint returned block-not-found, trying alternatives",
		zap.String("backend", string(decision.Backend)),
		zap.String("tried", triedURL),
		zap.Int64("height", height),
	)

	// Try remaining endpoints in the same backend pool.
	for _, ep := range h.router.AllEndpoints(decision.Backend) {
		if ep == triedURL {
			continue
		}
		altTarget, err := url.Parse(ep)
		if err != nil {
			continue
		}
		if body != nil {
			req.Body = io.NopCloser(bytes.NewReader(body))
		}
		buf = h.tryEndpoint(req, altTarget, path, rawQuery, body, decision.Backend, ep)
		if !isPrunedError(buf.statusCode, buf.body.Bytes()) {
			return h.flushBuffered(c, buf, decision.Backend, decision.Method, height, body, shouldCache)
		}
		h.logger.Info("alternative endpoint also returned block-not-found",
			zap.String("endpoint", ep),
			zap.Int64("height", height),
		)
	}

	// Try archival backend if configured.
	if h.router.HasArchivalBackend(decision.Backend) {
		archivalBackend := h.router.ArchivalBackendFor(decision.Backend)
		h.logger.Info("trying archival backend",
			zap.String("archival_backend", string(archivalBackend)),
			zap.Int("archival_endpoints", len(h.router.AllEndpoints(archivalBackend))),
			zap.Int64("height", height),
		)
		for _, ep := range h.router.AllEndpoints(archivalBackend) {
			archivalTarget, err := url.Parse(ep)
			if err != nil {
				continue
			}
			if body != nil {
				req.Body = io.NopCloser(bytes.NewReader(body))
			}
			buf = h.tryEndpoint(req, archivalTarget, path, rawQuery, body, archivalBackend, ep)
			if !isPrunedError(buf.statusCode, buf.body.Bytes()) {
				c.Set(middleware.ContextKeyBackend, string(archivalBackend))
				return h.flushBuffered(c, buf, archivalBackend, decision.Method, height, body, shouldCache)
			}
		}
	}

	// All endpoints failed — return the last response.
	return h.flushBuffered(c, buf, decision.Backend, decision.Method, height, body, shouldCache)
}

// tryEndpoint proxies to a single endpoint and returns the buffered response.
// endpointURL is the raw config string used to record latency (avoids URL normalization mismatches).
func (h *Handler) tryEndpoint(req *http.Request, targetURL *url.URL, path, rawQuery string, body []byte, backend Backend, endpointURL string) *bufferedResponseWriter {
	buf := newBufferedResponseWriter()
	proxy := h.buildRawProxy(targetURL, path, rawQuery, body, backend)

	start := time.Now()
	proxy.ServeHTTP(buf, req)
	h.router.RecordLatency(backend, endpointURL, time.Since(start))

	return buf
}

// flushBuffered writes a buffered proxy response to the real Echo response,
// adding our custom headers and optionally caching.
func (h *Handler) flushBuffered(c echo.Context, buf *bufferedResponseWriter, backend Backend, method string, height int64, reqBody []byte, shouldCache bool) error {
	resp := c.Response()
	reqID, _ := c.Get(middleware.ContextKeyRequestID).(string)

	// Copy backend response headers.
	for k, vals := range buf.header {
		for _, v := range vals {
			resp.Header().Set(k, v)
		}
	}

	// Add our custom headers.
	if reqID != "" {
		resp.Header().Set(middleware.HeaderXRequestID, reqID)
	}
	resp.Header().Set(HeaderXDABackend, string(backend))

	respBytes := buf.body.Bytes()

	// Cache successful, non-pruning-error responses.
	if shouldCache && buf.statusCode == http.StatusOK && !isPrunedError(buf.statusCode, respBytes) {
		resp.Header().Set(HeaderXCacheStatus, "MISS")
		h.cache.Set(c.Request().Context(), method, height, reqBody, respBytes)
	}

	resp.WriteHeader(buf.statusCode)
	_, _ = resp.Write(respBytes)
	return nil
}

// buildProxy creates a reverse proxy with full header decoration, caching,
// and error handling for the direct (non-fallback) path.
func (h *Handler) buildProxy(c echo.Context, req *http.Request, decision RouteDecision, targetURL *url.URL, path, rawQuery string, body []byte, height int64, shouldCache bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: h.director(targetURL, path, rawQuery, body),
		ModifyResponse: func(resp *http.Response) error {
			reqID, _ := c.Get(middleware.ContextKeyRequestID).(string)
			if reqID != "" {
				resp.Header.Set(middleware.HeaderXRequestID, reqID)
			}
			resp.Header.Set(HeaderXDABackend, string(decision.Backend))

			if shouldCache && resp.StatusCode == http.StatusOK {
				respBody, readErr := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				if readErr == nil {
					if !isPrunedError(resp.StatusCode, respBody) {
						resp.Header.Set(HeaderXCacheStatus, "MISS")
						h.cache.Set(req.Context(), decision.Method, height, body, respBody)
					}
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
}

// buildRawProxy creates a reverse proxy without custom header decoration,
// used for the fallback path where we buffer and inspect the response.
func (h *Handler) buildRawProxy(targetURL *url.URL, path, rawQuery string, body []byte, backend Backend) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: h.director(targetURL, path, rawQuery, body),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, proxyErr error) {
			h.logger.Error("backend proxy error",
				zap.String("backend", string(backend)),
				zap.Error(proxyErr),
			)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(`{"error":"backend unavailable"}`))
		},
	}
}

// director returns the Director function for httputil.ReverseProxy.
func (h *Handler) director(targetURL *url.URL, path, rawQuery string, body []byte) func(*http.Request) {
	return func(outReq *http.Request) {
		outReq.URL.Scheme = targetURL.Scheme
		outReq.URL.Host = targetURL.Host
		outReq.Host = targetURL.Host

		if path != "" && path != "/" {
			outReq.URL.Path = path
		} else {
			outReq.URL.Path = "/"
		}

		outReq.URL.RawQuery = rawQuery

		if body != nil {
			outReq.Body = io.NopCloser(bytes.NewReader(body))
			outReq.ContentLength = int64(len(body))
		}
	}
}

// bufferedResponseWriter captures an HTTP response in memory.
type bufferedResponseWriter struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
}

func newBufferedResponseWriter() *bufferedResponseWriter {
	return &bufferedResponseWriter{
		header:     make(http.Header),
		statusCode: http.StatusOK,
	}
}

func (w *bufferedResponseWriter) Header() http.Header {
	return w.header
}

func (w *bufferedResponseWriter) Write(b []byte) (int, error) {
	return w.body.Write(b)
}

func (w *bufferedResponseWriter) WriteHeader(code int) {
	w.statusCode = code
}

// isPrunedError returns true when the response indicates the block was pruned
// or not available on the node. Tendermint may return HTTP 200 or HTTP 500
// with a JSON-RPC error body for these cases.
func isPrunedError(statusCode int, body []byte) bool {
	if statusCode != http.StatusOK && statusCode != http.StatusInternalServerError {
		return false
	}
	if len(body) == 0 {
		return false
	}

	var resp struct {
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return false
	}
	if len(resp.Error) == 0 || string(resp.Error) == "null" {
		return false
	}

	errStr := strings.ToLower(string(resp.Error))
	return strings.Contains(errStr, "is not available") ||
		strings.Contains(errStr, "block not found") ||
		strings.Contains(errStr, "header not found") ||
		strings.Contains(errStr, "lowest height is") ||
		strings.Contains(errStr, "height must be less than or equal") ||
		strings.Contains(errStr, "could not find results")
}
