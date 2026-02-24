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
	Route(body []byte) (RouteDecision, error)
	TargetURL(backend Backend) string
	GetHeightTracker() *HeightTracker
	ArchivalBackendFor(backend Backend) Backend
	HasArchivalBackend(backend Backend) bool
}

type router struct {
	backends      config.BackendsConfig
	balancers     map[Backend]*Balancer
	heightTracker *HeightTracker
}

// NewRouter creates a Router from backend configuration.
func NewRouter(backends config.BackendsConfig) Router {
	return &router{
		backends:      backends,
		balancers:     buildBalancers(backends),
		heightTracker: NewHeightTracker(backends.PruningWindow),
	}
}

// NewRouterWithTracker creates a Router with an explicit HeightTracker.
func NewRouterWithTracker(backends config.BackendsConfig, ht *HeightTracker) Router {
	return &router{
		backends:      backends,
		balancers:     buildBalancers(backends),
		heightTracker: ht,
	}
}

func buildBalancers(b config.BackendsConfig) map[Backend]*Balancer {
	m := make(map[Backend]*Balancer, 4)
	m[BackendCelestiaAppRPC] = NewBalancer(b.CelestiaAppRPC)
	m[BackendCelestiaNodeRPC] = NewBalancer(b.CelestiaNodeRPC)
	m[BackendCelestiaNodeArchivalRPC] = NewBalancer(b.CelestiaNodeArchivalRPC)
	m[BackendCelestiaAppArchivalRPC] = NewBalancer(b.CelestiaAppArchivalRPC)
	return m
}

// Balancers returns the backend balancers map for health checking.
func (r *router) Balancers() map[Backend]*Balancer {
	return r.balancers
}

func (r *router) TargetURL(backend Backend) string {
	if bal, ok := r.balancers[backend]; ok && bal.Len() > 0 {
		return bal.Next()
	}
	// Archival backends fall back to pruned when not configured.
	switch backend {
	case BackendCelestiaNodeArchivalRPC:
		return r.TargetURL(BackendCelestiaNodeRPC)
	case BackendCelestiaAppArchivalRPC:
		return r.TargetURL(BackendCelestiaAppRPC)
	default:
		return ""
	}
}

func (r *router) Route(body []byte) (RouteDecision, error) {
	// 1. No body (e.g. GET /health, /status) — default to Tendermint RPC.
	if len(body) == 0 {
		return RouteDecision{
			Backend:   BackendCelestiaAppRPC,
			TargetURL: r.TargetURL(BackendCelestiaAppRPC),
		}, nil
	}

	// 2. JSON-RPC method routing.
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

// ArchivalBackendFor maps a pruned backend to its archival equivalent.
func (r *router) ArchivalBackendFor(pruned Backend) Backend {
	return r.archivalBackend(pruned)
}

// HasArchivalBackend returns true if the archival variant of the given backend
// has endpoints configured (i.e. it won't just fall back to the pruned node).
func (r *router) HasArchivalBackend(backend Backend) bool {
	archival := r.archivalBackend(backend)
	if archival == backend {
		return false // no archival mapping exists
	}
	if bal, ok := r.balancers[archival]; ok && bal.Len() > 0 {
		return true
	}
	return false
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
