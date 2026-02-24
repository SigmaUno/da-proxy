package proxy

import (
	"sync/atomic"

	"github.com/SigmaUno/da-proxy/internal/config"
)

// Balancer selects a backend URL from a list of endpoints using round-robin.
type Balancer struct {
	endpoints config.Endpoints
	counter   atomic.Uint64
}

// NewBalancer creates a round-robin balancer for the given endpoints.
func NewBalancer(endpoints config.Endpoints) *Balancer {
	return &Balancer{endpoints: endpoints}
}

// Next returns the next endpoint URL in round-robin order.
// Returns empty string if there are no endpoints.
func (b *Balancer) Next() string {
	n := len(b.endpoints)
	if n == 0 {
		return ""
	}
	if n == 1 {
		return b.endpoints[0]
	}
	idx := b.counter.Add(1) - 1
	return b.endpoints[idx%uint64(n)]
}

// All returns all endpoints.
func (b *Balancer) All() config.Endpoints {
	return b.endpoints
}

// Len returns the number of endpoints.
func (b *Balancer) Len() int {
	return len(b.endpoints)
}
