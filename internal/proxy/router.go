package proxy

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// Backend represents a target backend identifier.
type Backend string

// Backend constants identify the supported proxy targets.
const (
	BackendCelestiaAppRPC          Backend = "celestia-app-rpc"
	BackendCelestiaAppGRPC         Backend = "celestia-app-grpc"
	BackendCelestiaAppREST         Backend = "celestia-app-rest"
	BackendCelestiaNodeRPC         Backend = "celestia-node-rpc"
	BackendCelestiaNodeArchivalRPC Backend = "celestia-node-archival-rpc"
	BackendCelestiaAppArchivalRPC  Backend = "celestia-app-archival-rpc"
)

// daNamespaces are JSON-RPC method prefixes routed to celestia-node.
var daNamespaces = map[string]bool{
	"blob":   true,
	"header": true,
	"share":  true,
	"das":    true,
	"state":  true,
	"p2p":    true,
	"node":   true,
}

// RouteDecision holds the result of routing logic.
type RouteDecision struct {
	Backend   Backend
	TargetURL string
	Method    string
}

// Router determines which backend to forward a request to.
type Router interface {
	Route(contentType string, path string, body []byte) (RouteDecision, error)
	TargetURL(backend Backend) string
	GetHeightTracker() *HeightTracker
}

type router struct {
	backends      config.BackendsConfig
	heightTracker *HeightTracker
}

// NewRouter creates a Router from backend configuration.
func NewRouter(backends config.BackendsConfig) Router {
	return &router{
		backends:      backends,
		heightTracker: NewHeightTracker(backends.PruningWindow),
	}
}

// NewRouterWithTracker creates a Router with an explicit HeightTracker.
func NewRouterWithTracker(backends config.BackendsConfig, ht *HeightTracker) Router {
	return &router{backends: backends, heightTracker: ht}
}

func (r *router) TargetURL(backend Backend) string {
	switch backend {
	case BackendCelestiaAppRPC:
		return r.backends.CelestiaAppRPC
	case BackendCelestiaAppGRPC:
		return r.backends.CelestiaAppGRPC
	case BackendCelestiaAppREST:
		return r.backends.CelestiaAppREST
	case BackendCelestiaNodeRPC:
		return r.backends.CelestiaNodeRPC
	case BackendCelestiaNodeArchivalRPC:
		if r.backends.CelestiaNodeArchivalRPC != "" {
			return r.backends.CelestiaNodeArchivalRPC
		}
		return r.backends.CelestiaNodeRPC // fallback to pruned
	case BackendCelestiaAppArchivalRPC:
		if r.backends.CelestiaAppArchivalRPC != "" {
			return r.backends.CelestiaAppArchivalRPC
		}
		return r.backends.CelestiaAppRPC // fallback to pruned
	default:
		return ""
	}
}

func (r *router) Route(contentType string, path string, body []byte) (RouteDecision, error) {
	// 1. gRPC detection.
	if strings.HasPrefix(contentType, "application/grpc") {
		return RouteDecision{
			Backend:   BackendCelestiaAppGRPC,
			TargetURL: r.backends.CelestiaAppGRPC,
		}, nil
	}

	// 2. REST path detection.
	if isRESTPath(path) {
		return RouteDecision{
			Backend:   BackendCelestiaAppREST,
			TargetURL: r.backends.CelestiaAppREST,
		}, nil
	}

	// 3. No body (e.g. GET /health, /status) — default to Tendermint RPC.
	if len(body) == 0 {
		return RouteDecision{
			Backend:   BackendCelestiaAppRPC,
			TargetURL: r.backends.CelestiaAppRPC,
		}, nil
	}

	// 4. JSON-RPC method routing.
	method, _, err := parseJSONRPCMethod(body)
	if err != nil {
		return RouteDecision{}, fmt.Errorf("parsing JSON-RPC request: %w", err)
	}

	// For batch requests we route based on the first method.
	// Mixed-backend batches are not supported.
	backend := r.resolveBackend(method)

	// Height-aware routing: check if this request targets a historical height
	// that requires the archival node.
	if r.heightTracker != nil && r.heightTracker.Enabled() {
		height := ExtractHeight(method, body)
		if r.heightTracker.IsArchival(height) {
			backend = r.archivalBackend(backend)
		}
	}

	return RouteDecision{
		Backend:   backend,
		TargetURL: r.TargetURL(backend),
		Method:    method,
	}, nil
}

func (r *router) resolveBackend(method string) Backend {
	ns := extractNamespace(method)
	if daNamespaces[ns] {
		return BackendCelestiaNodeRPC
	}
	return BackendCelestiaAppRPC
}

func (r *router) GetHeightTracker() *HeightTracker {
	return r.heightTracker
}

// archivalBackend maps a pruned backend to its archival equivalent.
func (r *router) archivalBackend(pruned Backend) Backend {
	switch pruned {
	case BackendCelestiaNodeRPC:
		return BackendCelestiaNodeArchivalRPC
	case BackendCelestiaAppRPC:
		return BackendCelestiaAppArchivalRPC
	default:
		return pruned
	}
}

// extractNamespace returns the namespace prefix before the first dot.
// For methods without a dot (e.g. "status"), returns the full method.
func extractNamespace(method string) string {
	idx := strings.IndexByte(method, '.')
	if idx == -1 {
		return method
	}
	return method[:idx]
}

// isRESTPath checks if a path starts with a known Cosmos REST prefix.
func isRESTPath(path string) bool {
	return strings.HasPrefix(path, "/cosmos/") ||
		strings.HasPrefix(path, "/celestia/") ||
		strings.HasPrefix(path, "/ibc/")
}

// jsonRPCRequest represents a minimal JSON-RPC 2.0 request for method extraction.
type jsonRPCRequest struct {
	Method string `json:"method"`
}

// parseJSONRPCMethod extracts the method from a JSON-RPC request body.
// Returns the method, whether it was a batch request, and any error.
func parseJSONRPCMethod(body []byte) (string, bool, error) {
	if len(body) == 0 {
		return "", false, fmt.Errorf("empty request body")
	}

	// Trim whitespace to detect array vs object.
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) == 0 {
		return "", false, fmt.Errorf("empty request body")
	}

	if trimmed[0] == '[' {
		// Batch request.
		var batch []jsonRPCRequest
		if err := json.Unmarshal(body, &batch); err != nil {
			return "", true, fmt.Errorf("invalid JSON-RPC batch: %w", err)
		}
		if len(batch) == 0 {
			return "", true, fmt.Errorf("empty JSON-RPC batch")
		}
		if batch[0].Method == "" {
			return "", true, fmt.Errorf("missing method in first batch entry")
		}
		return batch[0].Method, true, nil
	}

	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return "", false, fmt.Errorf("invalid JSON-RPC request: %w", err)
	}
	if req.Method == "" {
		return "", false, fmt.Errorf("missing method field in JSON-RPC request")
	}

	return req.Method, false, nil
}
