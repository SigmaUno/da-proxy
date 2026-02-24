package proxy

import (
	"testing"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultBackends() config.BackendsConfig {
	return config.BackendsConfig{
		CelestiaAppRPC:  "http://localhost:26657",
		CelestiaAppGRPC: "localhost:9090",
		CelestiaAppREST: "http://localhost:1317",
		CelestiaNodeRPC: "http://localhost:26658",
	}
}

func jsonRPCBody(method string) []byte {
	return []byte(`{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":[]}`)
}

func jsonRPCBatch(methods ...string) []byte {
	batch := "["
	for i, m := range methods {
		if i > 0 {
			batch += ","
		}
		batch += `{"jsonrpc":"2.0","id":` + string(rune('1'+i)) + `,"method":"` + m + `"}`
	}
	batch += "]"
	return []byte(batch)
}

func TestRouter_Route(t *testing.T) {
	r := NewRouter(defaultBackends())

	tests := []struct {
		name        string
		contentType string
		path        string
		body        []byte
		wantBackend Backend
		wantMethod  string
		wantErr     bool
	}{
		// gRPC routing
		{
			name:        "gRPC request",
			contentType: "application/grpc",
			path:        "/",
			body:        nil,
			wantBackend: BackendCelestiaAppGRPC,
		},
		{
			name:        "gRPC with proto",
			contentType: "application/grpc+proto",
			path:        "/",
			body:        nil,
			wantBackend: BackendCelestiaAppGRPC,
		},

		// REST path routing
		{
			name:        "cosmos REST path",
			contentType: "application/json",
			path:        "/cosmos/bank/v1beta1/balances/celestia1abc",
			wantBackend: BackendCelestiaAppREST,
		},
		{
			name:        "celestia REST path",
			contentType: "application/json",
			path:        "/celestia/blob/v1/params",
			wantBackend: BackendCelestiaAppREST,
		},
		{
			name:        "ibc REST path",
			contentType: "application/json",
			path:        "/ibc/core/client/v1/client_states",
			wantBackend: BackendCelestiaAppREST,
		},

		// DA namespace methods → celestia-node
		{
			name:        "blob.Submit",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("blob.Submit"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.Submit",
		},
		{
			name:        "blob.Get",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("blob.Get"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.Get",
		},
		{
			name:        "blob.GetAll",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("blob.GetAll"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.GetAll",
		},
		{
			name:        "blob.GetProof",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("blob.GetProof"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.GetProof",
		},
		{
			name:        "header.GetByHeight",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("header.GetByHeight"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "header.GetByHeight",
		},
		{
			name:        "header.NetworkHead",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("header.NetworkHead"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "header.NetworkHead",
		},
		{
			name:        "share.GetSharesByNamespace",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("share.GetSharesByNamespace"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "share.GetSharesByNamespace",
		},
		{
			name:        "das.SamplingStats",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("das.SamplingStats"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "das.SamplingStats",
		},
		{
			name:        "state.Balance",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("state.Balance"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "state.Balance",
		},
		{
			name:        "state.SubmitPayForBlob",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("state.SubmitPayForBlob"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "state.SubmitPayForBlob",
		},
		{
			name:        "p2p.Info",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("p2p.Info"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "p2p.Info",
		},
		{
			name:        "node.Info",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("node.Info"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "node.Info",
		},

		// Consensus/Tendermint methods → celestia-app
		{
			name:        "status",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("status"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "status",
		},
		{
			name:        "health",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("health"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "health",
		},
		{
			name:        "block",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("block"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "block",
		},
		{
			name:        "block_results",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("block_results"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "block_results",
		},
		{
			name:        "tx",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("tx"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "tx",
		},
		{
			name:        "tx_search",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("tx_search"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "tx_search",
		},
		{
			name:        "broadcast_tx_sync",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("broadcast_tx_sync"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "broadcast_tx_sync",
		},
		{
			name:        "abci_query",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("abci_query"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "abci_query",
		},
		{
			name:        "consensus_state",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("consensus_state"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "consensus_state",
		},
		{
			name:        "validators",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("validators"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "validators",
		},
		{
			name:        "net_info",
			contentType: "application/json",
			path:        "/",
			body:        jsonRPCBody("net_info"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "net_info",
		},

		// Error cases
		{
			name:        "empty body",
			contentType: "application/json",
			path:        "/",
			body:        []byte{},
			wantErr:     true,
		},
		{
			name:        "nil body",
			contentType: "application/json",
			path:        "/",
			body:        nil,
			wantErr:     true,
		},
		{
			name:        "invalid JSON",
			contentType: "application/json",
			path:        "/",
			body:        []byte(`{invalid`),
			wantErr:     true,
		},
		{
			name:        "missing method field",
			contentType: "application/json",
			path:        "/",
			body:        []byte(`{"jsonrpc":"2.0","id":1}`),
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Route(tt.contentType, tt.path, tt.body)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBackend, got.Backend)
			if tt.wantMethod != "" {
				assert.Equal(t, tt.wantMethod, got.Method)
			}
			assert.NotEmpty(t, got.TargetURL)
		})
	}
}

func TestRouter_BatchRequest(t *testing.T) {
	r := NewRouter(defaultBackends())

	t.Run("batch with same namespace", func(t *testing.T) {
		body := jsonRPCBatch("blob.Get", "blob.Submit")
		got, err := r.Route("application/json", "/", body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaNodeRPC, got.Backend)
		assert.Equal(t, "blob.Get", got.Method)
	})

	t.Run("batch routed by first method", func(t *testing.T) {
		body := jsonRPCBatch("status", "block")
		got, err := r.Route("application/json", "/", body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppRPC, got.Backend)
		assert.Equal(t, "status", got.Method)
	})

	t.Run("empty batch", func(t *testing.T) {
		_, err := r.Route("application/json", "/", []byte(`[]`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty JSON-RPC batch")
	})

	t.Run("invalid batch JSON", func(t *testing.T) {
		_, err := r.Route("application/json", "/", []byte(`[{invalid}]`))
		require.Error(t, err)
	})
}

func TestExtractNamespace(t *testing.T) {
	tests := []struct {
		method string
		want   string
	}{
		{"blob.Get", "blob"},
		{"header.GetByHeight", "header"},
		{"status", "status"},
		{"broadcast_tx_sync", "broadcast_tx_sync"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			assert.Equal(t, tt.want, extractNamespace(tt.method))
		})
	}
}

func TestIsRESTPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/cosmos/bank/v1beta1/balances", true},
		{"/celestia/blob/v1/params", true},
		{"/ibc/core/client/v1/client_states", true},
		{"/", false},
		{"/other/path", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			assert.Equal(t, tt.want, isRESTPath(tt.path))
		})
	}
}

func TestTargetURL(t *testing.T) {
	backends := defaultBackends()
	r := NewRouter(backends)

	assert.Equal(t, "http://localhost:26657", r.TargetURL(BackendCelestiaAppRPC))
	assert.Equal(t, "localhost:9090", r.TargetURL(BackendCelestiaAppGRPC))
	assert.Equal(t, "http://localhost:1317", r.TargetURL(BackendCelestiaAppREST))
	assert.Equal(t, "http://localhost:26658", r.TargetURL(BackendCelestiaNodeRPC))
	assert.Equal(t, "", r.TargetURL(Backend("unknown")))
}
