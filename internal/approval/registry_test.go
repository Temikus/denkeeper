package approval

import (
	"context"
	"sync"
	"testing"
)

func TestRegistry_Pop_ReturnsAndDeletes(t *testing.T) {
	r := NewRegistry()
	called := false
	r.Register("x", func(_ context.Context, _ string) error {
		called = true
		return nil
	})

	fn, ok := r.Pop("x")
	if !ok {
		t.Fatal("expected ok=true")
	}
	_ = fn(context.Background(), "")
	if !called {
		t.Error("action not called")
	}

	// Second pop should be empty.
	_, ok2 := r.Pop("x")
	if ok2 {
		t.Error("expected ok=false on second pop")
	}
}

func TestRegistry_Pop_Missing(t *testing.T) {
	r := NewRegistry()
	fn, ok := r.Pop("nonexistent")
	if ok || fn != nil {
		t.Errorf("expected nil,false, got %v,%v", fn, ok)
	}
}

func TestRegistry_Delete_RemovesEntry(t *testing.T) {
	r := NewRegistry()
	r.Register("y", func(_ context.Context, _ string) error { return nil })
	r.Delete("y")
	_, ok := r.Pop("y")
	if ok {
		t.Error("expected entry to be deleted")
	}
}

func TestRegistry_ConcurrentRegisterPop(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	const n = 100
	// Register n entries concurrently.
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := generateID()
			r.Register(id, func(_ context.Context, _ string) error { return nil })
			r.Pop(id)
		}(i)
	}
	wg.Wait()
}
