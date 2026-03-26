package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Manager coordinates the persistent Store with the in-memory action Registry.
// It is the primary API used by the Engine and REST API server.
type Manager struct {
	store    Store
	registry *Registry
	logger   *slog.Logger
}

// NewManager creates a Manager backed by the given store.
func NewManager(store Store, logger *slog.Logger) *Manager {
	return &Manager{
		store:    store,
		registry: NewRegistry(),
		logger:   logger,
	}
}

// DefaultTTL is the time an approval request stays pending before it is
// automatically expired by the background worker.
const DefaultTTL = 24 * time.Hour

// Submit creates a new pending approval, registers the action closure, and
// returns the persisted Request with its ID populated. The request expires
// after DefaultTTL if not resolved.
func (m *Manager) Submit(
	ctx context.Context,
	agentName string,
	kind ActionKind,
	summary string,
	payload string,
	externalID string,
	adapterName string,
	conversationID string,
	action ActionFunc,
) (*Request, error) {
	id := generateID()
	expiresAt := time.Now().UTC().Add(DefaultTTL)
	req := Request{
		ID:             id,
		AgentName:      agentName,
		Kind:           kind,
		Status:         StatusPending,
		Summary:        summary,
		Payload:        payload,
		CallbackData:   "appr:" + id,
		ExternalID:     externalID,
		AdapterName:    adapterName,
		ConversationID: conversationID,
		ExpiresAt:      &expiresAt,
	}

	if _, err := m.store.Create(ctx, req); err != nil {
		return nil, fmt.Errorf("submitting approval: %w", err)
	}

	m.registry.Register(id, action)
	m.logger.Info("approval submitted", "id", id, "kind", kind, "agent", agentName)
	return &req, nil
}

// Resolve marks an approval as approved or denied and, if approved, invokes
// the registered action closure. Returns the updated Request.
func (m *Manager) Resolve(ctx context.Context, id string, approved bool, resolvedBy string) (*Request, error) {
	status := StatusDenied
	if approved {
		status = StatusApproved
	}

	if err := m.store.Resolve(ctx, id, status, resolvedBy); err != nil {
		return nil, err
	}

	if approved {
		fn, ok := m.registry.Pop(id)
		if !ok {
			// Registry is empty after a restart — the DB row was already expired
			// at startup, so this path should not be reached in practice.
			m.logger.Warn("approval action not found in registry (restarted?)", "id", id)
		} else {
			req, err := m.store.Get(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("fetching approved request: %w", err)
			}
			if err := fn(ctx, req.Payload); err != nil {
				m.logger.Error("approval action failed", "id", id, "error", err)
				// The status is already set to approved in the DB; we log the error
				// but still return the resolved record so the caller can notify.
				return req, fmt.Errorf("approval action: %w", err)
			}
			m.logger.Info("approval action executed", "id", id, "resolvedBy", resolvedBy)
			return req, nil
		}
	} else {
		// Denied — clean up any registered closure.
		m.registry.Delete(id)
		m.logger.Info("approval denied", "id", id, "resolvedBy", resolvedBy)
	}

	return m.store.Get(ctx, id)
}

// ErrStaleCallback is returned by ResolveByCallback when the callback refers to
// an approval that exists but is no longer pending (already resolved, expired,
// or approved). The caller should surface its Status to the user.
var ErrStaleCallback = fmt.Errorf("approval: callback refers to a non-pending request")

// ResolveByCallback parses the full Telegram callback data string
// ("appr:{id}:approve" or "appr:{id}:deny"), resolves the approval, and
// returns the updated Request. Returns ErrNotFound for unknown callbacks,
// ErrStaleCallback when the approval is no longer pending.
func (m *Manager) ResolveByCallback(ctx context.Context, callbackData string, resolvedBy string) (*Request, error) {
	if !strings.HasPrefix(callbackData, "appr:") {
		return nil, ErrNotFound
	}
	// Strip the trailing :approve or :deny to get the prefix stored in DB.
	approved := strings.HasSuffix(callbackData, ":approve")
	prefix := strings.TrimSuffix(strings.TrimSuffix(callbackData, ":approve"), ":deny")

	// Look up the pending row by prefix.
	req, err := m.store.ResolveByCallbackPrefix(ctx, prefix, statusFor(approved), resolvedBy)
	if err != nil {
		if err == ErrNotFound {
			// No pending row. Check whether the row exists in any other status so
			// we can give the user an informative message instead of silence.
			if existing, lookupErr := m.store.GetByCallbackData(ctx, prefix); lookupErr == nil {
				_ = existing // Status is available to the caller via ErrStaleCallback
				return existing, ErrStaleCallback
			}
		}
		return nil, err
	}

	// If approved, invoke the action closure.
	if approved {
		fn, ok := m.registry.Pop(req.ID)
		if ok {
			if err := fn(ctx, req.Payload); err != nil {
				m.logger.Error("approval action failed", "id", req.ID, "error", err)
				return req, fmt.Errorf("approval action: %w", err)
			}
			m.logger.Info("approval action executed via callback", "id", req.ID)
		} else {
			m.logger.Warn("approval action not found in registry (restarted?)", "id", req.ID)
		}
	} else {
		m.registry.Delete(req.ID)
		m.logger.Info("approval denied via callback", "id", req.ID)
	}

	return req, nil
}

// Get returns a single approval by ID.
func (m *Manager) Get(ctx context.Context, id string) (*Request, error) {
	return m.store.Get(ctx, id)
}

// List returns approvals filtered by status ("" = all).
func (m *Manager) List(ctx context.Context, status Status) ([]Request, error) {
	return m.store.List(ctx, status)
}

// ExpirePending expires all pending approvals. Call at startup.
func (m *Manager) ExpirePending(ctx context.Context) (int, error) {
	return m.store.ExpirePending(ctx)
}

// StartExpiryWorker starts a background goroutine that expires pending
// approvals whose TTL has elapsed. It ticks every interval until ctx is
// cancelled. Expired closures are removed from the in-memory registry.
// Safe to call once per process lifetime.
func (m *Manager) StartExpiryWorker(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				n, err := m.store.ExpireBefore(ctx, time.Now().UTC())
				if err != nil {
					m.logger.Warn("expiry worker failed", "error", err)
					continue
				}
				if n > 0 {
					m.logger.Info("expired pending approvals by TTL", "count", n)
				}
			}
		}
	}()
}

// GetByCallbackData fetches an approval by its callback_data prefix regardless
// of status. Used to provide informative feedback when a user clicks an
// already-resolved or expired Telegram button.
func (m *Manager) GetByCallbackData(ctx context.Context, callbackData string) (*Request, error) {
	return m.store.GetByCallbackData(ctx, callbackData)
}

func statusFor(approved bool) Status {
	if approved {
		return StatusApproved
	}
	return StatusDenied
}

func generateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
