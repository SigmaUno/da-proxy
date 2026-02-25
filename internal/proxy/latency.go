package proxy

import (
	"math"
	"sync/atomic"
	"time"
)

// ewmaAlpha is the smoothing factor for the EWMA.
// Higher values make the average more responsive to recent measurements.
const ewmaAlpha = 0.3

// ewma is a lock-free exponentially weighted moving average.
// It stores the float64 value as a uint64 using math.Float64bits for
// atomic operations.
type ewma struct {
	bits  atomic.Uint64
	count atomic.Uint64
}

// Update adds a new sample to the EWMA.
func (e *ewma) Update(d time.Duration) {
	sample := float64(d.Nanoseconds()) / 1e6 // milliseconds
	e.count.Add(1)

	for {
		oldBits := e.bits.Load()
		oldVal := math.Float64frombits(oldBits)

		var newVal float64
		if oldBits == 0 && oldVal == 0 {
			// First sample — use it directly.
			newVal = sample
		} else {
			newVal = ewmaAlpha*sample + (1-ewmaAlpha)*oldVal
		}

		newBits := math.Float64bits(newVal)
		if e.bits.CompareAndSwap(oldBits, newBits) {
			return
		}
		// CAS failed — another goroutine updated; retry.
	}
}

// Value returns the current EWMA in milliseconds.
// Returns 0 if no samples have been recorded.
func (e *ewma) Value() float64 {
	return math.Float64frombits(e.bits.Load())
}

// Count returns the total number of samples recorded.
func (e *ewma) Count() uint64 {
	return e.count.Load()
}
