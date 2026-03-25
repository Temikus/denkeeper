package approval

import (
	"context"
	"sync"
)

// ActionFunc is the callback invoked when an approval is resolved.
// It receives the stored payload and performs the approved action.
type ActionFunc func(ctx context.Context, payload string) error

// Registry holds in-memory action closures keyed by approval ID.
// These are ephemeral: on restart the registry is empty, and ExpirePending
// ensures no stale DB entries are left in "pending" state.
type Registry struct {
	mu      sync.Mutex
	actions map[string]ActionFunc
}

// NewRegistry creates an empty Registry.
func NewRegistry() *Registry {
	return &Registry{actions: make(map[string]ActionFunc)}
}

// Register stores an action closure under the given ID.
func (r *Registry) Register(id string, fn ActionFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.actions[id] = fn
}

// Pop retrieves and removes the action for the given ID atomically.
// Returns (nil, false) if no action is registered for that ID.
func (r *Registry) Pop(id string) (ActionFunc, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn, ok := r.actions[id]
	if ok {
		delete(r.actions, id)
	}
	return fn, ok
}

// Delete removes the action for the given ID without invoking it.
func (r *Registry) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.actions, id)
}
