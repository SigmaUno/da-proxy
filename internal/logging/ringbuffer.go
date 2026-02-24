package logging

import "sync"

// RingBuffer is a thread-safe fixed-size circular buffer of LogEntries.
type RingBuffer struct {
	mu          sync.RWMutex
	entries     []LogEntry
	head        int
	size        int
	cap         int
	subscribers map[chan LogEntry]struct{}
}

// NewRingBuffer creates a ring buffer with the given capacity.
func NewRingBuffer(capacity int) *RingBuffer {
	return &RingBuffer{
		entries: make([]LogEntry, capacity),
		cap:     capacity,
	}
}

// Push adds an entry, overwriting the oldest entry if the buffer is full.
// It also notifies any active subscribers.
func (rb *RingBuffer) Push(entry LogEntry) {
	rb.mu.Lock()
	defer rb.mu.Unlock()

	rb.entries[rb.head] = entry
	rb.head = (rb.head + 1) % rb.cap
	if rb.size < rb.cap {
		rb.size++
	}

	// Notify subscribers (non-blocking).
	for ch := range rb.subscribers {
		select {
		case ch <- entry:
		default:
			// Drop if subscriber is slow.
		}
	}
}

// Entries returns all entries in the buffer, newest first.
func (rb *RingBuffer) Entries(limit int) []LogEntry {
	rb.mu.RLock()
	defer rb.mu.RUnlock()

	if rb.size == 0 {
		return nil
	}

	if limit <= 0 || limit > rb.size {
		limit = rb.size
	}

	result := make([]LogEntry, 0, limit)
	// Walk backwards from head.
	idx := (rb.head - 1 + rb.cap) % rb.cap
	for i := 0; i < limit; i++ {
		result = append(result, rb.entries[idx])
		idx = (idx - 1 + rb.cap) % rb.cap
	}
	return result
}

// Size returns the current number of entries in the buffer.
func (rb *RingBuffer) Size() int {
	rb.mu.RLock()
	defer rb.mu.RUnlock()
	return rb.size
}

// Subscribe returns a channel that receives new log entries as they are pushed.
// The returned cancel function should be called to stop the subscription.
func (rb *RingBuffer) Subscribe() (<-chan LogEntry, func()) {
	ch := make(chan LogEntry, 100)
	rb.mu.Lock()
	if rb.subscribers == nil {
		rb.subscribers = make(map[chan LogEntry]struct{})
	}
	rb.subscribers[ch] = struct{}{}
	rb.mu.Unlock()

	cancel := func() {
		rb.mu.Lock()
		delete(rb.subscribers, ch)
		rb.mu.Unlock()
		close(ch)
	}
	return ch, cancel
}
