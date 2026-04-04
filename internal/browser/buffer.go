package browser

import "sync"

// --- Entry Types ---

// ConsoleEntry represents a captured console event.
type ConsoleEntry struct {
	Timestamp int64  `json:"timestamp"`
	Level     string `json:"level"`
	Text      string `json:"text"`
}

// NetworkEntry represents a captured network request/response.
type NetworkEntry struct {
	Timestamp int64  `json:"timestamp"`
	Method    string `json:"method"`
	URL       string `json:"url"`
	Status    int    `json:"status,omitempty"`
	Duration  int64  `json:"duration,omitempty"` // ms
	Size      int    `json:"size,omitempty"`     // bytes
}

// DialogEntry represents a captured dialog event.
type DialogEntry struct {
	Timestamp    int64  `json:"timestamp"`
	Type         string `json:"type"`                   // alert, confirm, prompt, beforeunload
	Message      string `json:"message"`
	DefaultValue string `json:"defaultValue,omitempty"`
	Action       string `json:"action"`                 // accepted, dismissed
	Response     string `json:"response,omitempty"`
}

// --- RingBuffer ---

// RingBuffer is a generic fixed-capacity circular buffer.
// It stores entries of type T in insertion order, overwriting the oldest
// when capacity is reached.
//
//	┌───┬───┬───┬───┬───┬───┐
//	│ 3 │ 4 │ 5 │   │ 1 │ 2 │  capacity=6, head=4, count=5
//	└───┴───┴───┴───┴─▲─┴───┘
//	                   │
//	                 head (oldest entry)
//
//	Add() writes at (head+count) % capacity, O(1)
//	Snapshot() returns entries in insertion order, O(n)
//	totalAdded keeps incrementing past capacity (flush cursor)
type RingBuffer[T any] struct {
	mu         sync.RWMutex
	data       []T
	head       int
	count      int
	totalAdded int
}

// NewRingBuffer creates a RingBuffer with the given capacity.
func NewRingBuffer[T any](capacity int) *RingBuffer[T] {
	if capacity <= 0 {
		capacity = 1
	}
	return &RingBuffer[T]{
		data: make([]T, capacity),
	}
}

// Add appends an entry to the buffer. When full, the oldest entry is overwritten.
func (b *RingBuffer[T]) Add(entry T) {
	b.mu.Lock()
	defer b.mu.Unlock()

	idx := (b.head + b.count) % len(b.data)
	b.data[idx] = entry
	if b.count < len(b.data) {
		b.count++
	} else {
		// Buffer full — advance head (overwrites oldest)
		b.head = (b.head + 1) % len(b.data)
	}
	b.totalAdded++
}

// Snapshot returns all entries from oldest to newest.
func (b *RingBuffer[T]) Snapshot() []T {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.count == 0 {
		return nil
	}

	items := make([]T, 0, b.count)
	for i := 0; i < b.count; i++ {
		items = append(items, b.data[(b.head+i)%len(b.data)])
	}
	return items
}

// Last returns the last n entries (most recent), in chronological order (oldest first).
func (b *RingBuffer[T]) Last(n int) []T {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n <= 0 || b.count == 0 {
		return nil
	}
	if n > b.count {
		n = b.count
	}

	items := make([]T, 0, n)
	start := (b.head + b.count - n) % len(b.data)
	for i := 0; i < n; i++ {
		items = append(items, b.data[(start+i)%len(b.data)])
	}
	return items
}

// Get returns the entry at the given index (0 = oldest currently in buffer).
// Returns the zero value and false if the index is out of range.
func (b *RingBuffer[T]) Get(index int) (T, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	var zero T
	if index < 0 || index >= b.count {
		return zero, false
	}
	return b.data[(b.head+index)%len(b.data)], true
}

// Set updates the entry at the given index (0 = oldest currently in buffer).
// No-op if the index is out of range.
func (b *RingBuffer[T]) Set(index int, entry T) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if index < 0 || index >= b.count {
		return
	}
	b.data[(b.head+index)%len(b.data)] = entry
}

// Clear resets the buffer to empty without changing capacity or totalAdded.
func (b *RingBuffer[T]) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()

	var zero T
	for i := range b.data {
		b.data[i] = zero
	}
	b.head = 0
	b.count = 0
	// Don't reset totalAdded — flush cursor depends on it
}

// Len returns the number of entries currently stored.
func (b *RingBuffer[T]) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// TotalAdded returns the total number of entries ever added (including overwritten ones).
func (b *RingBuffer[T]) TotalAdded() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalAdded
}
