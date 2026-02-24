//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/SigmaUno/da-proxy/internal/proxy"
)

// Backend addresses for integration tests.
const (
	prunedRPC   = "http://195.154.212.53:26657"
	archivalRPC = "http://195.154.103.60:26657"
	archivalDA  = "http://195.154.103.57:26658"
)

func newTestHandler(t *testing.T) *proxy.Handler {
	t.Helper()
	backends := config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{prunedRPC},
		CelestiaNodeRPC: config.Endpoints{archivalDA},
	}
	router := proxy.NewRouter(backends)
	logger, _ := zap.NewDevelopment()
	return proxy.NewHandler(router, 10*1024*1024, logger)
}

func jsonRPCRequest(method string, params interface{}) []byte {
	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}
	b, _ := json.Marshal(req)
	return b
}

func doProxyRequest(t *testing.T, handler *proxy.Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err := handler.HandleRequest(c)
	if err != nil {
		he, ok := err.(*echo.HTTPError)
		if ok {
			rec.Code = he.Code
			rec.Body = bytes.NewBuffer([]byte(he.Error()))
		}
	}
	return rec
}

// --- Tendermint RPC (celestia-app) ---

func TestIntegration_TendermintRPC_Status(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("status", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok, "response should have result field")

	nodeInfo, ok := result["node_info"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "mocha-4", nodeInfo["network"])
	assert.Contains(t, rec.Header().Get("X-DA-Backend"), "celestia-app-rpc")
}

func TestIntegration_TendermintRPC_Health(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("health", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotNil(t, resp["result"])
}

func TestIntegration_TendermintRPC_Block(t *testing.T) {
	h := newTestHandler(t)

	// First get the latest height.
	rec := doProxyRequest(t, h, jsonRPCRequest("status", []interface{}{}))
	require.Equal(t, http.StatusOK, rec.Code)

	var statusResp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &statusResp))
	result := statusResp["result"].(map[string]interface{})
	syncInfo := result["sync_info"].(map[string]interface{})
	latestHeight := syncInfo["latest_block_height"].(string)

	// Now fetch that block using array params (Tendermint JSON-RPC style).
	rec = doProxyRequest(t, h, jsonRPCRequest("block", []interface{}{latestHeight}))
	require.Equal(t, http.StatusOK, rec.Code)

	var blockResp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &blockResp))
	blockResult := blockResp["result"].(map[string]interface{})
	block := blockResult["block"].(map[string]interface{})
	header := block["header"].(map[string]interface{})
	assert.Equal(t, latestHeight, header["height"])
}

func TestIntegration_TendermintRPC_NetInfo(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("net_info", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result := resp["result"].(map[string]interface{})
	assert.Contains(t, result, "n_peers")
}

func TestIntegration_TendermintRPC_ABCIInfo(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("abci_info", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.NotNil(t, resp["result"])
}

// --- DA Node (celestia-node) ---

func TestIntegration_DANode_NodeInfo(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("node.Info", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, result, "type")
	assert.Contains(t, result, "api_version")
	assert.Contains(t, rec.Header().Get("X-DA-Backend"), "celestia-node-rpc")
}

func TestIntegration_DANode_HeaderNetworkHead(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("header.NetworkHead", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok)
	header, ok := result["header"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "mocha-4", header["chain_id"])
	assert.NotEmpty(t, header["height"])
}

func TestIntegration_DANode_HeaderGetByHeight(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("header.GetByHeight", []interface{}{1}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok)
	header, ok := result["header"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "1", header["height"])
}

func TestIntegration_DANode_P2PInfo(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("p2p.Info", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, result, "ID")
	assert.Contains(t, result, "Addrs")
}

func TestIntegration_DANode_StateAccountAddress(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("state.AccountAddress", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	result, ok := resp["result"].(string)
	require.True(t, ok)
	assert.Contains(t, result, "celestia1")
}

// --- Routing correctness ---

func TestIntegration_Routing_ConsensusMethod(t *testing.T) {
	h := newTestHandler(t)
	methods := []string{"status", "health", "block", "net_info", "abci_info", "validators"}
	for _, m := range methods {
		t.Run(m, func(t *testing.T) {
			rec := doProxyRequest(t, h, jsonRPCRequest(m, []interface{}{}))
			require.Equal(t, http.StatusOK, rec.Code, "method %s should succeed", m)
			assert.Contains(t, rec.Header().Get("X-DA-Backend"), "celestia-app-rpc")
		})
	}
}

func TestIntegration_Routing_DAMethod(t *testing.T) {
	h := newTestHandler(t)
	methods := []struct {
		method string
		params interface{}
	}{
		{"node.Info", []interface{}{}},
		{"header.NetworkHead", []interface{}{}},
		{"p2p.Info", []interface{}{}},
		{"state.AccountAddress", []interface{}{}},
	}
	for _, tc := range methods {
		t.Run(tc.method, func(t *testing.T) {
			rec := doProxyRequest(t, h, jsonRPCRequest(tc.method, tc.params))
			require.Equal(t, http.StatusOK, rec.Code, "method %s should succeed", tc.method)
			assert.Contains(t, rec.Header().Get("X-DA-Backend"), "celestia-node-rpc")
		})
	}
}

// --- Archival vs Pruned RPC ---

func TestIntegration_ArchivalRPC_EarlyBlock(t *testing.T) {
	// This tests the archival node for a block that the pruned node doesn't have.
	// Archival has blocks from ~286000, pruned only from ~10217301.
	// Block 500000 exists on archival but not on pruned.
	backends := config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{archivalRPC},
		CelestiaNodeRPC: config.Endpoints{archivalDA},
	}
	router := proxy.NewRouter(backends)
	logger, _ := zap.NewDevelopment()
	handler := proxy.NewHandler(router, 10*1024*1024, logger)

	rec := doProxyRequest(t, handler, jsonRPCRequest("block", []interface{}{"500000"}))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	result, ok := resp["result"].(map[string]interface{})
	require.True(t, ok, "response should have result, got: %v", resp)
	block, ok := result["block"].(map[string]interface{})
	require.True(t, ok, "result should have block, got: %v", result)
	header := block["header"].(map[string]interface{})
	assert.Equal(t, "500000", header["height"])
	assert.Equal(t, "mocha-4", header["chain_id"])
}

// --- Batch JSON-RPC ---

func TestIntegration_BatchRequest(t *testing.T) {
	h := newTestHandler(t)
	batch := []map[string]interface{}{
		{"jsonrpc": "2.0", "id": 1, "method": "status", "params": []interface{}{}},
		{"jsonrpc": "2.0", "id": 2, "method": "health", "params": []interface{}{}},
	}
	body, _ := json.Marshal(batch)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err := h.HandleRequest(c)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)

	// Tendermint batch responses are returned as an array.
	var resp []map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp, 2)
	assert.NotNil(t, resp[0]["result"])
	assert.NotNil(t, resp[1]["result"])
}

// --- Error handling ---

func TestIntegration_MalformedJSON(t *testing.T) {
	h := newTestHandler(t)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte(`not json`)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err := h.HandleRequest(c)
	require.Error(t, err)

	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, he.Code)
}

// --- Direct backend verification ---

func TestIntegration_DirectBackendReachability(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"pruned-rpc", prunedRPC + "/health"},
		{"archival-rpc", archivalRPC + "/health"},
		{"archival-da", archivalDA},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var resp *http.Response
			var err error
			if tc.name == "archival-da" {
				body := bytes.NewReader(jsonRPCRequest("node.Info", []interface{}{}))
				resp, err = http.Post(tc.url, "application/json", body)
			} else {
				resp, err = http.Get(tc.url)
			}
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()
			assert.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

// --- Response integrity ---

func TestIntegration_ResponseHeaders(t *testing.T) {
	h := newTestHandler(t)
	rec := doProxyRequest(t, h, jsonRPCRequest("status", []interface{}{}))

	require.Equal(t, http.StatusOK, rec.Code)
	assert.NotEmpty(t, rec.Header().Get("X-DA-Backend"))
	// Content-Type should be JSON from the backend.
	ct := rec.Header().Get("Content-Type")
	assert.Contains(t, ct, "application/json")
}

func TestIntegration_ResponseBody_ValidJSON(t *testing.T) {
	h := newTestHandler(t)

	methods := []struct {
		method string
		params interface{}
	}{
		{"status", []interface{}{}},
		{"health", []interface{}{}},
		{"node.Info", []interface{}{}},
		{"header.NetworkHead", []interface{}{}},
		{"p2p.Info", []interface{}{}},
	}

	for _, tc := range methods {
		t.Run(tc.method, func(t *testing.T) {
			rec := doProxyRequest(t, h, jsonRPCRequest(tc.method, tc.params))
			require.Equal(t, http.StatusOK, rec.Code)

			body, err := io.ReadAll(rec.Body)
			require.NoError(t, err)
			assert.True(t, json.Valid(body), "response body should be valid JSON for %s", tc.method)
		})
	}
}
