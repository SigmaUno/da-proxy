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
	BackendCelestiaAppRPC  Backend = "celestia-app-rpc"
	BackendCelestiaAppGRPC Backend = "celestia-app-grpc"
	BackendCelestiaAppREST Backend = "celestia-app-rest"
	BackendCelestiaNodeRPC Backend = "celestia-node-rpc"
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
}

type router struct {
	backends config.BackendsConfig
}

// NewRouter creates a Router from backend configuration.
func NewRouter(backends config.BackendsConfig) Router {
	return &router{backends: backends}
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

	// 3. JSON-RPC method routing.
	method, _, err := parseJSONRPCMethod(body)
	if err != nil {
		return RouteDecision{}, fmt.Errorf("parsing JSON-RPC request: %w", err)
	}

	// For batch requests we route based on the first method.
	// Mixed-backend batches are not supported.
	backend := r.resolveBackend(method)
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
