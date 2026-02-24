package proxy

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
)

// HeightTracker tracks the current head height and pruning window for
// height-aware routing decisions.
type HeightTracker struct {
	headHeight    atomic.Int64
	pruningWindow int64 // 0 means no archival routing
}

// NewHeightTracker creates a height tracker.
func NewHeightTracker(pruningWindow int64) *HeightTracker {
	return &HeightTracker{pruningWindow: pruningWindow}
}

// SetHead updates the current head height.
func (ht *HeightTracker) SetHead(height int64) {
	ht.headHeight.Store(height)
}

// Head returns the current head height.
func (ht *HeightTracker) Head() int64 {
	return ht.headHeight.Load()
}

// IsArchival returns true if the given height requires the archival node.
// Returns false if no pruning window is configured or height is within window.
func (ht *HeightTracker) IsArchival(height int64) bool {
	if ht.pruningWindow <= 0 {
		return false
	}
	head := ht.headHeight.Load()
	if head == 0 {
		return false // head not yet known, default to pruned
	}
	if height <= 0 {
		return false // latest/zero height, use pruned
	}
	return height < (head - ht.pruningWindow)
}

// Enabled returns true if archival routing is configured.
func (ht *HeightTracker) Enabled() bool {
	return ht.pruningWindow > 0
}

// heightBearingMethods maps JSON-RPC methods to the position of the height
// parameter in their params array. -1 means the method name itself contains
// the height or needs special handling.
var heightBearingMethods = map[string]int{
	// Tendermint RPC
	"block":          0,
	"block_results":  0,
	"validators":     0,
	"commit":         0,
	"blockchain":     0, // has min/max heights, use min
	"block_by_hash":  -1,
	"header":         0,
	"header_by_hash": -1,

	// Celestia DA node
	"header.GetByHeight":         0,
	"blob.Get":                   0,
	"blob.GetAll":                0,
	"blob.GetProof":              0,
	"share.GetSharesByNamespace": 0,
	"share.GetEDS":               0,
	"share.GetNamespaceData":     0,
}

// ExtractHeight extracts the block height from a JSON-RPC request body.
// Returns 0 if no height can be determined (meaning "latest").
func ExtractHeight(method string, body []byte) int64 {
	paramIdx, ok := heightBearingMethods[method]
	if !ok {
		return 0
	}
	if paramIdx < 0 {
		return 0 // hash-based lookup, can't determine height
	}

	// Parse the full request to get params.
	var req struct {
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return 0
	}

	if len(req.Params) == 0 {
		return 0
	}

	// Try array params first.
	trimmed := strings.TrimSpace(string(req.Params))
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var params []json.RawMessage
		if err := json.Unmarshal(req.Params, &params); err != nil || paramIdx >= len(params) {
			return 0
		}
		return parseHeight(params[paramIdx])
	}

	// Object params — try common field names.
	var objParams map[string]json.RawMessage
	if err := json.Unmarshal(req.Params, &objParams); err != nil {
		return 0
	}

	for _, key := range []string{"height", "Height"} {
		if v, exists := objParams[key]; exists {
			return parseHeight(v)
		}
	}

	return 0
}

// parseHeight parses a height value from a JSON value.
// Handles both numeric and string representations.
func parseHeight(raw json.RawMessage) int64 {
	s := strings.TrimSpace(string(raw))
	if len(s) == 0 {
		return 0
	}

	// Remove quotes if string.
	if s[0] == '"' {
		var str string
		if err := json.Unmarshal(raw, &str); err != nil {
			return 0
		}
		s = str
	}

	h, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return h
}
