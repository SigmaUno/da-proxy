package proxy

import (
	"testing"

	"github.com/SigmaUno/da-proxy/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultBackends() config.BackendsConfig {
	return config.BackendsConfig{
		CelestiaAppRPC:  config.Endpoints{"http://localhost:26657"},
		CelestiaNodeRPC: config.Endpoints{"http://localhost:26658"},
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
		body        []byte
		wantBackend Backend
		wantMethod  string
		wantErr     bool
	}{
		// DA namespace methods → celestia-node
		{
			name:        "blob.Submit",
			body:        jsonRPCBody("blob.Submit"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.Submit",
		},
		{
			name:        "blob.Get",
			body:        jsonRPCBody("blob.Get"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.Get",
		},
		{
			name:        "blob.GetAll",
			body:        jsonRPCBody("blob.GetAll"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.GetAll",
		},
		{
			name:        "blob.GetProof",
			body:        jsonRPCBody("blob.GetProof"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "blob.GetProof",
		},
		{
			name:        "header.GetByHeight",
			body:        jsonRPCBody("header.GetByHeight"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "header.GetByHeight",
		},
		{
			name:        "header.NetworkHead",
			body:        jsonRPCBody("header.NetworkHead"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "header.NetworkHead",
		},
		{
			name:        "share.GetSharesByNamespace",
			body:        jsonRPCBody("share.GetSharesByNamespace"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "share.GetSharesByNamespace",
		},
		{
			name:        "das.SamplingStats",
			body:        jsonRPCBody("das.SamplingStats"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "das.SamplingStats",
		},
		{
			name:        "state.Balance",
			body:        jsonRPCBody("state.Balance"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "state.Balance",
		},
		{
			name:        "state.SubmitPayForBlob",
			body:        jsonRPCBody("state.SubmitPayForBlob"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "state.SubmitPayForBlob",
		},
		{
			name:        "p2p.Info",
			body:        jsonRPCBody("p2p.Info"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "p2p.Info",
		},
		{
			name:        "node.Info",
			body:        jsonRPCBody("node.Info"),
			wantBackend: BackendCelestiaNodeRPC,
			wantMethod:  "node.Info",
		},

		// Consensus/Tendermint methods → celestia-app
		{
			name:        "status",
			body:        jsonRPCBody("status"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "status",
		},
		{
			name:        "health",
			body:        jsonRPCBody("health"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "health",
		},
		{
			name:        "block",
			body:        jsonRPCBody("block"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "block",
		},
		{
			name:        "block_results",
			body:        jsonRPCBody("block_results"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "block_results",
		},
		{
			name:        "tx",
			body:        jsonRPCBody("tx"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "tx",
		},
		{
			name:        "tx_search",
			body:        jsonRPCBody("tx_search"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "tx_search",
		},
		{
			name:        "broadcast_tx_sync",
			body:        jsonRPCBody("broadcast_tx_sync"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "broadcast_tx_sync",
		},
		{
			name:        "abci_query",
			body:        jsonRPCBody("abci_query"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "abci_query",
		},
		{
			name:        "consensus_state",
			body:        jsonRPCBody("consensus_state"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "consensus_state",
		},
		{
			name:        "validators",
			body:        jsonRPCBody("validators"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "validators",
		},
		{
			name:        "net_info",
			body:        jsonRPCBody("net_info"),
			wantBackend: BackendCelestiaAppRPC,
			wantMethod:  "net_info",
		},

		// Error cases
		{
			name:        "empty body defaults to tendermint",
			body:        []byte{},
			wantBackend: BackendCelestiaAppRPC,
		},
		{
			name:        "nil body defaults to tendermint",
			body:        nil,
			wantBackend: BackendCelestiaAppRPC,
		},
		{
			name:    "invalid JSON",
			body:    []byte(`{invalid`),
			wantErr: true,
		},
		{
			name:    "missing method field",
			body:    []byte(`{"jsonrpc":"2.0","id":1}`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Route(tt.body)
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
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaNodeRPC, got.Backend)
		assert.Equal(t, "blob.Get", got.Method)
	})

	t.Run("batch routed by first method", func(t *testing.T) {
		body := jsonRPCBatch("status", "block")
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppRPC, got.Backend)
		assert.Equal(t, "status", got.Method)
	})

	t.Run("empty batch", func(t *testing.T) {
		_, err := r.Route([]byte(`[]`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty JSON-RPC batch")
	})

	t.Run("invalid batch JSON", func(t *testing.T) {
		_, err := r.Route([]byte(`[{invalid}]`))
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

func TestTargetURL(t *testing.T) {
	backends := defaultBackends()
	r := NewRouter(backends)

	assert.Equal(t, "http://localhost:26657", r.TargetURL(BackendCelestiaAppRPC))
	assert.Equal(t, "http://localhost:26658", r.TargetURL(BackendCelestiaNodeRPC))
	assert.Equal(t, "", r.TargetURL(Backend("unknown")))
}

func TestRouter_HeightAwareRouting(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:          config.Endpoints{"http://localhost:26657"},
		CelestiaNodeRPC:         config.Endpoints{"http://localhost:26658"},
		CelestiaNodeArchivalRPC: config.Endpoints{"http://localhost:36658"},
		CelestiaAppArchivalRPC:  config.Endpoints{"http://localhost:36657"},
		PruningWindow:           1000,
	}

	ht := NewHeightTracker(1000)
	ht.SetHead(5000)
	r := NewRouterWithTracker(backends, ht)

	t.Run("recent height goes to pruned", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"block","params":["4500"]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppRPC, got.Backend)
		assert.Equal(t, "http://localhost:26657", got.TargetURL)
	})

	t.Run("historical height goes to archival app", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"block","params":["100"]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppArchivalRPC, got.Backend)
		assert.Equal(t, "http://localhost:36657", got.TargetURL)
	})

	t.Run("DA historical height goes to archival node", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[100,"AAAA","AA=="]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaNodeArchivalRPC, got.Backend)
		assert.Equal(t, "http://localhost:36658", got.TargetURL)
	})

	t.Run("DA recent height goes to pruned node", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"blob.Get","params":[4500,"AAAA","AA=="]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaNodeRPC, got.Backend)
		assert.Equal(t, "http://localhost:26658", got.TargetURL)
	})

	t.Run("no height param goes to pruned", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"status","params":[]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppRPC, got.Backend)
	})

	t.Run("latest height (0) goes to pruned", func(t *testing.T) {
		body := []byte(`{"jsonrpc":"2.0","id":1,"method":"block","params":["0"]}`)
		got, err := r.Route(body)
		require.NoError(t, err)
		assert.Equal(t, BackendCelestiaAppRPC, got.Backend)
	})
}

func TestTargetURL_ArchivalFallback(t *testing.T) {
	// Without archival configured, archival backends fall back to pruned.
	backends := defaultBackends()
	r := NewRouter(backends)

	assert.Equal(t, "http://localhost:26658", r.TargetURL(BackendCelestiaNodeArchivalRPC))
	assert.Equal(t, "http://localhost:26657", r.TargetURL(BackendCelestiaAppArchivalRPC))
}

func TestTargetURL_WithArchival(t *testing.T) {
	backends := config.BackendsConfig{
		CelestiaAppRPC:          config.Endpoints{"http://localhost:26657"},
		CelestiaNodeRPC:         config.Endpoints{"http://localhost:26658"},
		CelestiaNodeArchivalRPC: config.Endpoints{"http://archival:36658"},
		CelestiaAppArchivalRPC:  config.Endpoints{"http://archival:36657"},
	}
	r := NewRouter(backends)

	assert.Equal(t, "http://archival:36658", r.TargetURL(BackendCelestiaNodeArchivalRPC))
	assert.Equal(t, "http://archival:36657", r.TargetURL(BackendCelestiaAppArchivalRPC))
}
