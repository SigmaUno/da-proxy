package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/middleware"
)

// HeaderXDABackend is the response header indicating which backend handled the request.
const HeaderXDABackend = "X-DA-Backend"

// Handler is the main proxy handler.
type Handler struct {
	router      Router
	maxBodySize int64
	logger      *zap.Logger
}

// NewHandler creates the proxy handler.
func NewHandler(router Router, maxBodySize int64, logger *zap.Logger) *Handler {
	return &Handler{
		router:      router,
		maxBodySize: maxBodySize,
		logger:      logger,
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
