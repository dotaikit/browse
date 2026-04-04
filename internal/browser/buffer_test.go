package browser

import (
	"slices"
	"testing"
)

func TestBufferAddAndSnapshot(t *testing.T) {
	buf := NewRingBuffer[int](5)
	buf.Add(1)
	buf.Add(2)
	buf.Add(3)

	if got, want := buf.Len(), 3; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	if got, want := buf.TotalAdded(), 3; got != want {
		t.Fatalf("TotalAdded() = %d, want %d", got, want)
	}

	if got, want := buf.Snapshot(), []int{1, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("Snapshot() = %v, want %v", got, want)
	}
}

func TestBufferOverflowKeepsNewest(t *testing.T) {
	buf := NewRingBuffer[int](3)
	for i := 1; i <= 5; i++ {
		buf.Add(i)
	}

	if got, want := buf.Snapshot(), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("Snapshot() after overflow = %v, want %v", got, want)
	}

	if got, want := buf.Len(), 3; got != want {
		t.Fatalf("Len() = %d, want %d", got, want)
	}

	if got, want := buf.TotalAdded(), 5; got != want {
		t.Fatalf("TotalAdded() = %d, want %d", got, want)
	}
}

func TestBufferLast(t *testing.T) {
	buf := NewRingBuffer[int](5)
	for i := 1; i <= 5; i++ {
		buf.Add(i)
	}

	if got, want := buf.Last(3), []int{3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("Last(3) = %v, want %v", got, want)
	}

	if got, want := buf.Last(10), []int{1, 2, 3, 4, 5}; !slices.Equal(got, want) {
		t.Fatalf("Last(10) = %v, want %v", got, want)
	}

	if got, want := buf.Last(1), []int{5}; !slices.Equal(got, want) {
		t.Fatalf("Last(1) = %v, want %v", got, want)
	}

	if got := buf.Last(0); got != nil {
		t.Fatalf("Last(0) = %v, want nil", got)
	}

	if got := buf.Last(-2); got != nil {
		t.Fatalf("Last(-2) = %v, want nil", got)
	}
}

func TestBufferGetAndSet(t *testing.T) {
	buf := NewRingBuffer[string](3)
	buf.Add("a")
	buf.Add("b")
	buf.Add("c")
	buf.Add("d") // buffer now: b, c, d

	if got, ok := buf.Get(0); !ok || got != "b" {
		t.Fatalf("Get(0) = (%q, %v), want (%q, true)", got, ok, "b")
	}

	if got, ok := buf.Get(2); !ok || got != "d" {
		t.Fatalf("Get(2) = (%q, %v), want (%q, true)", got, ok, "d")
	}

	buf.Set(1, "C")
	if got, ok := buf.Get(1); !ok || got != "C" {
		t.Fatalf("Get(1) after Set = (%q, %v), want (%q, true)", got, ok, "C")
	}

	buf.Set(-1, "x")
	buf.Set(99, "x")
	if got, want := buf.Snapshot(), []string{"b", "C", "d"}; !slices.Equal(got, want) {
		t.Fatalf("Snapshot() after out-of-range Set = %v, want %v", got, want)
	}

	if got, ok := buf.Get(-1); ok || got != "" {
		t.Fatalf("Get(-1) = (%q, %v), want (%q, false)", got, ok, "")
	}

	if got, ok := buf.Get(10); ok || got != "" {
		t.Fatalf("Get(10) = (%q, %v), want (%q, false)", got, ok, "")
	}
}

func TestBufferClear(t *testing.T) {
	buf := NewRingBuffer[int](4)
	for i := 1; i <= 3; i++ {
		buf.Add(i)
	}

	totalBeforeClear := buf.TotalAdded()
	buf.Clear()

	if got, want := buf.Len(), 0; got != want {
		t.Fatalf("Len() after Clear = %d, want %d", got, want)
	}

	if got := buf.Snapshot(); got != nil {
		t.Fatalf("Snapshot() after Clear = %v, want nil", got)
	}

	if got, want := buf.TotalAdded(), totalBeforeClear; got != want {
		t.Fatalf("TotalAdded() after Clear = %d, want %d", got, want)
	}

	buf.Add(99)
	if got, want := buf.Snapshot(), []int{99}; !slices.Equal(got, want) {
		t.Fatalf("Snapshot() after Add post-Clear = %v, want %v", got, want)
	}
}

func TestBufferCapacityFallback(t *testing.T) {
	zeroCap := NewRingBuffer[int](0)
	zeroCap.Add(1)
	zeroCap.Add(2)
	if got, want := zeroCap.Snapshot(), []int{2}; !slices.Equal(got, want) {
		t.Fatalf("capacity=0 Snapshot() = %v, want %v", got, want)
	}

	negativeCap := NewRingBuffer[int](-3)
	negativeCap.Add(7)
	negativeCap.Add(8)
	if got, want := negativeCap.Snapshot(), []int{8}; !slices.Equal(got, want) {
		t.Fatalf("capacity=-3 Snapshot() = %v, want %v", got, want)
	}
}

func TestBufferEmptyBehavior(t *testing.T) {
	buf := NewRingBuffer[int](3)

	if got := buf.Snapshot(); got != nil {
		t.Fatalf("Snapshot() on empty buffer = %v, want nil", got)
	}
	if got := buf.Last(1); got != nil {
		t.Fatalf("Last(1) on empty buffer = %v, want nil", got)
	}
	if got, ok := buf.Get(0); ok || got != 0 {
		t.Fatalf("Get(0) on empty buffer = (%d, %v), want (0, false)", got, ok)
	}
}
