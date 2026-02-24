package proxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/middleware"
)

func TestIsWebSocketUpgrade(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected bool
	}{
		{"websocket upgrade", map[string]string{"Upgrade": "websocket"}, true},
		{"websocket uppercase", map[string]string{"Upgrade": "WebSocket"}, true},
		{"no upgrade", map[string]string{}, false},
		{"other upgrade", map[string]string{"Upgrade": "h2c"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			assert.Equal(t, tt.expected, IsWebSocketUpgrade(req))
		})
	}
}

func TestHTTPToWSURL(t *testing.T) {
	tests := []struct {
		name     string
		httpURL  string
		path     string
		expected string
	}{
		{"http to ws default", "http://localhost:26657", "/", "ws://localhost:26657/websocket"},
		{"https to wss", "https://node.example.com", "/", "wss://node.example.com/websocket"},
		{"with path", "http://localhost:26658", "/ws", "ws://localhost:26658/ws"},
		{"empty path", "http://localhost:26657", "", "ws://localhost:26657/websocket"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := httpToWSURL(tt.httpURL, tt.path)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWebSocketProxy_ResolveBackend(t *testing.T) {
	ws := &WebSocketProxy{logger: zap.NewNop()}

	tests := []struct {
		path    string
		backend Backend
	}{
		{"/", BackendCelestiaAppRPC},
		{"/websocket", BackendCelestiaAppRPC},
		{"/ws", BackendCelestiaNodeRPC},
		{"/ws/subscribe", BackendCelestiaNodeRPC},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.backend, ws.resolveWSBackend(tt.path))
		})
	}
}

func TestWebSocketProxy_BidirectionalProxy(t *testing.T) {
	// Create a WebSocket echo server as the backend.
	echoServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// Echo back with prefix.
			if err := conn.WriteMessage(mt, append([]byte("echo:"), msg...)); err != nil {
				return
			}
		}
	}))
	defer echoServer.Close()

	backends := config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{echoServer.URL},
		CelestiaNodeRPC: config.Endpoints{echoServer.URL},
		CelestiaAppGRPC: config.Endpoints{"localhost:9090"},
		CelestiaAppREST: config.Endpoints{echoServer.URL},
	}

	router := NewRouter(backends)
	wsProxy := NewWebSocketProxy(router, zap.NewNop())

	// Create Echo server with WebSocket handler.
	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(middleware.ContextKeyTokenName, "test-token")
			return next(c)
		}
	})
	e.Any("/*", func(c echo.Context) error {
		return wsProxy.Handle(c)
	})

	proxyServer := httptest.NewServer(e)
	defer proxyServer.Close()

	// Connect client WebSocket to proxy.
	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http") + "/"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer func() { _ = client.Close() }()

	// Send a message.
	err = client.WriteMessage(websocket.TextMessage, []byte("hello"))
	require.NoError(t, err)

	// Read echoed response.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, msg, err := client.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "echo:hello", string(msg))

	// Send another message.
	err = client.WriteMessage(websocket.TextMessage, []byte("world"))
	require.NoError(t, err)

	_, msg, err = client.ReadMessage()
	require.NoError(t, err)
	assert.Equal(t, "echo:world", string(msg))
}

func TestWebSocketProxy_BackendUnavailable(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://127.0.0.1:1"}, // unreachable
		CelestiaNodeRPC: config.Endpoints{"http://127.0.0.1:1"},
		CelestiaAppGRPC: config.Endpoints{"127.0.0.1:1"},
		CelestiaAppREST: config.Endpoints{"http://127.0.0.1:1"},
	}

	router := NewRouter(backends)
	wsProxy := NewWebSocketProxy(router, zap.NewNop())

	e := echo.New()
	e.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			c.Set(middleware.ContextKeyTokenName, "test-token")
			return next(c)
		}
	})
	e.Any("/*", func(c echo.Context) error {
		return wsProxy.Handle(c)
	})

	proxyServer := httptest.NewServer(e)
	defer proxyServer.Close()

	// Attempt WebSocket connection - should fail.
	wsURL := "ws" + strings.TrimPrefix(proxyServer.URL, "http") + "/"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	assert.Error(t, err)
	if resp != nil {
		assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
		_ = resp.Body.Close()
	}
}
