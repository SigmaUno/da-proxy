package proxy

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/middleware"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

// WebSocketProxy handles WebSocket upgrade requests and proxies them to backends.
type WebSocketProxy struct {
	router Router
	logger *zap.Logger
}

// NewWebSocketProxy creates a WebSocket proxy.
func NewWebSocketProxy(router Router, logger *zap.Logger) *WebSocketProxy {
	return &WebSocketProxy{
		router: router,
		logger: logger,
	}
}

// IsWebSocketUpgrade checks if the request is a WebSocket upgrade.
func IsWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

// Handle proxies a WebSocket connection to the appropriate backend.
func (ws *WebSocketProxy) Handle(c echo.Context) error {
	req := c.Request()
	path := req.URL.Path

	// Determine backend based on path.
	backend := ws.resolveWSBackend(path)
	targetURL := ws.router.TargetURL(backend)
	if targetURL == "" {
		return echo.NewHTTPError(http.StatusBadGateway, "no backend configured for WebSocket")
	}

	// Convert HTTP URL to WebSocket URL.
	wsURL, err := httpToWSURL(targetURL, path)
	if err != nil {
		ws.logger.Error("invalid WebSocket backend URL", zap.Error(err))
		return echo.NewHTTPError(http.StatusBadGateway, "backend configuration error")
	}

	c.Set(middleware.ContextKeyBackend, string(backend))

	// Connect to backend.
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	backendHeaders := http.Header{}
	if auth := req.Header.Get("Authorization"); auth != "" {
		backendHeaders.Set("Authorization", auth)
	}

	backendConn, resp, err := dialer.Dial(wsURL, backendHeaders)
	if err != nil {
		ws.logger.Error("failed to connect to backend WebSocket",
			zap.String("backend", string(backend)),
			zap.String("url", wsURL),
			zap.Error(err),
		)
		if resp != nil {
			_ = resp.Body.Close()
		}
		return echo.NewHTTPError(http.StatusBadGateway, "backend WebSocket unavailable")
	}
	defer func() { _ = backendConn.Close() }()

	// Upgrade client connection.
	clientConn, err := upgrader.Upgrade(c.Response(), req, nil)
	if err != nil {
		ws.logger.Error("failed to upgrade WebSocket", zap.Error(err))
		return nil // Upgrade already wrote the response.
	}
	defer func() { _ = clientConn.Close() }()

	ws.logger.Info("WebSocket connection established",
		zap.String("backend", string(backend)),
		zap.String("token", c.Get(middleware.ContextKeyTokenName).(string)),
	)

	// Bidirectional proxy.
	var wg sync.WaitGroup
	wg.Add(2)

	// Client → Backend
	go func() {
		defer wg.Done()
		ws.proxyFrames(clientConn, backendConn, "client→backend")
	}()

	// Backend → Client
	go func() {
		defer wg.Done()
		ws.proxyFrames(backendConn, clientConn, "backend→client")
	}()

	wg.Wait()

	ws.logger.Info("WebSocket connection closed",
		zap.String("backend", string(backend)),
	)

	return nil
}

func (ws *WebSocketProxy) proxyFrames(src, dst *websocket.Conn, direction string) {
	for {
		msgType, msg, err := src.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				ws.logger.Debug("WebSocket read error",
					zap.String("direction", direction),
					zap.Error(err),
				)
			}
			// Send close to the other side.
			_ = dst.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			return
		}

		if err := dst.WriteMessage(msgType, msg); err != nil {
			ws.logger.Debug("WebSocket write error",
				zap.String("direction", direction),
				zap.Error(err),
			)
			return
		}
	}
}

// resolveWSBackend determines which backend to use for WebSocket.
// Tendermint RPC WebSocket is at /websocket, DA node WebSocket is at /ws or /.
func (ws *WebSocketProxy) resolveWSBackend(path string) Backend {
	// If path explicitly targets DA node endpoints, route there.
	if path == "/ws" || strings.HasPrefix(path, "/ws/") {
		return BackendCelestiaNodeRPC
	}
	// Default WebSocket connections go to Tendermint RPC.
	return BackendCelestiaAppRPC
}

func httpToWSURL(httpURL, path string) (string, error) {
	u, err := url.Parse(httpURL)
	if err != nil {
		return "", err
	}

	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}

	// For Tendermint RPC, WebSocket endpoint is /websocket.
	if path == "" || path == "/" {
		u.Path = "/websocket"
	} else {
		u.Path = path
	}

	return u.String(), nil
}
