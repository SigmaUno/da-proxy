package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
)

func TestGRPCProxy_ForwardsToTarget(t *testing.T) {
	var receivedContentType string
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	proxy := NewGRPCProxy(config.Endpoints{backend.Listener.Addr().String()}, zap.NewNop())
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodPost, "/cosmos.bank.v1beta1.Query/AllBalances", nil)
	req.Header.Set("Content-Type", "application/grpc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "application/grpc", receivedContentType)
	assert.Equal(t, "/cosmos.bank.v1beta1.Query/AllBalances", receivedPath)
}

func TestGRPCProxy_BackendDown(t *testing.T) {
	proxy := NewGRPCProxy(config.Endpoints{"127.0.0.1:1"}, zap.NewNop())
	handler := proxy.Handler()

	req := httptest.NewRequest(http.MethodPost, "/test.Service/Method", nil)
	req.Header.Set("Content-Type", "application/grpc")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestNewGRPCProxy(t *testing.T) {
	proxy := NewGRPCProxy(config.Endpoints{"localhost:9090"}, zap.NewNop())
	assert.NotNil(t, proxy)
	assert.NotNil(t, proxy.Handler())
}
