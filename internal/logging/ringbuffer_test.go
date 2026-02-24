package logging

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeEntry(method string) LogEntry {
	return LogEntry{
		Timestamp:  time.Now(),
		RequestID:  "req-" + method,
		Method:     method,
		Backend:    "test-backend",
		StatusCode: 200,
	}
}

func TestRingBuffer_PushAndEntries(t *testing.T) {
	rb := NewRingBuffer(5)

	rb.Push(makeEntry("a"))
	rb.Push(makeEntry("b"))
	rb.Push(makeEntry("c"))

	entries := rb.Entries(0)
	require.Len(t, entries, 3)
	assert.Equal(t, "c", entries[0].Method)
	assert.Equal(t, "b", entries[1].Method)
	assert.Equal(t, "a", entries[2].Method)
}

func TestRingBuffer_Wraps(t *testing.T) {
	rb := NewRingBuffer(3)

	rb.Push(makeEntry("a"))
	rb.Push(makeEntry("b"))
	rb.Push(makeEntry("c"))
	rb.Push(makeEntry("d")) // overwrites "a"
	rb.Push(makeEntry("e")) // overwrites "b"

	assert.Equal(t, 3, rb.Size())

	entries := rb.Entries(0)
	require.Len(t, entries, 3)
	assert.Equal(t, "e", entries[0].Method)
	assert.Equal(t, "d", entries[1].Method)
	assert.Equal(t, "c", entries[2].Method)
}

func TestRingBuffer_Limit(t *testing.T) {
	rb := NewRingBuffer(10)
	for i := 0; i < 10; i++ {
		rb.Push(makeEntry(fmt.Sprintf("m%d", i)))
	}

	entries := rb.Entries(3)
	require.Len(t, entries, 3)
	assert.Equal(t, "m9", entries[0].Method)
	assert.Equal(t, "m8", entries[1].Method)
	assert.Equal(t, "m7", entries[2].Method)
}

func TestRingBuffer_Empty(t *testing.T) {
	rb := NewRingBuffer(10)
	entries := rb.Entries(0)
	assert.Nil(t, entries)
	assert.Equal(t, 0, rb.Size())
}

func TestRingBuffer_ConcurrentAccess(t *testing.T) {
	rb := NewRingBuffer(100)
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rb.Push(makeEntry(fmt.Sprintf("method.%d", i)))
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 100, rb.Size())
	entries := rb.Entries(0)
	assert.Len(t, entries, 100)
}

func TestRingBuffer_Subscribe(t *testing.T) {
	rb := NewRingBuffer(10)

	ch, cancel := rb.Subscribe()
	defer cancel()

	rb.Push(makeEntry("test"))

	select {
	case entry := <-ch:
		assert.Equal(t, "test", entry.Method)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for subscription entry")
	}
}
