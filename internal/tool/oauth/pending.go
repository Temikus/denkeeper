package oauth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"time"
)

const pendingTimeout = 5 * time.Minute

// AuthResult is the result sent back from the OAuth callback.
type AuthResult struct {
	Code  string
	State string
	Err   error
}

// PendingAuth represents an in-progress OAuth authorization flow.
type PendingAuth struct {
	ID        string    `json:"id"`
	ToolName  string    `json:"tool_name"`
	AuthURL   string    `json:"auth_url,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	done      chan AuthResult
	closeOnce sync.Once
}

// closeDone safely closes the done channel exactly once,
// preventing double-close panics from concurrent cleanup paths.
func (pa *PendingAuth) closeDone() {
	pa.closeOnce.Do(func() { close(pa.done) })
}

// PendingManager tracks active OAuth authorization requests.
// The lifecycle:
//  1. The AuthorizationCodeFetcher callback creates a pending auth via Create().
//  2. It sets the auth URL via SetAuthURL() after the SDK generates it.
//  3. It blocks on WaitForCompletion() until the callback resolves.
//  4. The API callback handler calls Complete() with the code+state.
//  5. WaitForCompletion() returns the result to the fetcher.
type PendingManager struct {
	mu      sync.Mutex
	pending map[string]*PendingAuth // keyed by ID
	byState map[string]string       // state param → pending ID
	logger  *slog.Logger
}

// NewPendingManager creates a PendingManager.
func NewPendingManager(logger *slog.Logger) *PendingManager {
	return &PendingManager{
		pending: make(map[string]*PendingAuth),
		byState: make(map[string]string),
		logger:  logger,
	}
}

// Create registers a new pending authorization for the given tool.
// If there's already a pending auth for this tool, it is cancelled first.
func (pm *PendingManager) Create(toolName string) *PendingAuth {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Cancel any existing pending auth for this tool.
	for id, p := range pm.pending {
		if p.ToolName == toolName {
			p.closeDone()
			delete(pm.pending, id)
			pm.logger.Info("oauth: cancelled stale pending auth",
				slog.String("tool", toolName),
				slog.String("pending_id", id))
		}
	}

	id := generateID()
	pa := &PendingAuth{
		ID:        id,
		ToolName:  toolName,
		CreatedAt: time.Now(),
		done:      make(chan AuthResult, 1),
	}
	pm.pending[id] = pa

	pm.logger.Info("oauth: created pending auth",
		slog.String("tool", toolName),
		slog.String("pending_id", id))

	return pa
}

// SetAuthURL sets the authorization URL and registers the state→ID mapping.
// The state parameter is extracted from the auth URL's query string.
func (pm *PendingManager) SetAuthURL(id, authURL string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pa, ok := pm.pending[id]
	if !ok {
		return fmt.Errorf("oauth: pending auth %q not found", id)
	}
	pa.AuthURL = authURL

	// Extract state from the auth URL for callback matching.
	u, err := url.Parse(authURL)
	if err == nil {
		if state := u.Query().Get("state"); state != "" {
			pm.byState[state] = id
		}
	}

	return nil
}

// WaitForCompletion blocks until the pending auth is resolved or the context
// is cancelled. Returns the authorization code and state on success.
func (pm *PendingManager) WaitForCompletion(ctx context.Context, id string) (code, state string, err error) {
	pm.mu.Lock()
	pa, ok := pm.pending[id]
	pm.mu.Unlock()

	if !ok {
		return "", "", fmt.Errorf("oauth: pending auth %q not found", id)
	}

	// Create a timeout context for the pending auth.
	ctx, cancel := context.WithTimeout(ctx, pendingTimeout)
	defer cancel()

	select {
	case result, ok := <-pa.done:
		if !ok {
			return "", "", fmt.Errorf("oauth: pending auth %q was cancelled", id)
		}
		if result.Err != nil {
			return "", "", result.Err
		}
		return result.Code, result.State, nil
	case <-ctx.Done():
		pm.cleanup(id)
		return "", "", fmt.Errorf("oauth: authorization timed out for %q", pa.ToolName)
	}
}

// CompleteByState resolves a pending auth by the OAuth state parameter.
// This is called by the callback endpoint which receives state from the provider.
func (pm *PendingManager) CompleteByState(state, code string) error {
	pm.mu.Lock()
	id, ok := pm.byState[state]
	if !ok {
		pm.mu.Unlock()
		return fmt.Errorf("oauth: no pending auth for state")
	}
	pa, exists := pm.pending[id]
	if !exists {
		delete(pm.byState, state)
		pm.mu.Unlock()
		return fmt.Errorf("oauth: pending auth %q expired", id)
	}
	// Clean up state mapping (one-time use).
	delete(pm.byState, state)
	pm.mu.Unlock()

	pa.done <- AuthResult{Code: code, State: state}

	pm.logger.Info("oauth: completed pending auth",
		slog.String("tool", pa.ToolName),
		slog.String("pending_id", id))

	return nil
}

// Cancel cancels a pending auth with an error.
func (pm *PendingManager) Cancel(id string) {
	pm.mu.Lock()
	pa, ok := pm.pending[id]
	if ok {
		pa.done <- AuthResult{Err: fmt.Errorf("oauth: authorization cancelled")}
		delete(pm.pending, id)
		// Clean state mapping.
		for state, pid := range pm.byState {
			if pid == id {
				delete(pm.byState, state)
			}
		}
		pm.logger.Info("oauth: cancelled pending auth",
			slog.String("tool", pa.ToolName),
			slog.String("pending_id", id))
	}
	pm.mu.Unlock()
}

// List returns all active pending authorizations. Safe for JSON serialization.
func (pm *PendingManager) List() []*PendingAuth {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	result := make([]*PendingAuth, 0, len(pm.pending))
	for _, pa := range pm.pending {
		// Return a copy without the channel.
		result = append(result, &PendingAuth{
			ID:        pa.ID,
			ToolName:  pa.ToolName,
			AuthURL:   pa.AuthURL,
			CreatedAt: pa.CreatedAt,
		})
	}
	return result
}

// Get returns a pending auth by ID, or nil if not found.
func (pm *PendingManager) Get(id string) *PendingAuth {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pa, ok := pm.pending[id]
	if !ok {
		return nil
	}
	return &PendingAuth{
		ID:        pa.ID,
		ToolName:  pa.ToolName,
		AuthURL:   pa.AuthURL,
		CreatedAt: pa.CreatedAt,
	}
}

// GetByToolName returns the pending auth for a tool, or nil if none exists.
func (pm *PendingManager) GetByToolName(toolName string) *PendingAuth {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for _, pa := range pm.pending {
		if pa.ToolName == toolName {
			return &PendingAuth{
				ID:        pa.ID,
				ToolName:  pa.ToolName,
				AuthURL:   pa.AuthURL,
				CreatedAt: pa.CreatedAt,
			}
		}
	}
	return nil
}

// Cleanup removes expired pending auths. Call periodically or on demand.
func (pm *PendingManager) Cleanup() {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	now := time.Now()
	for id, pa := range pm.pending {
		if now.Sub(pa.CreatedAt) > pendingTimeout {
			pa.closeDone()
			delete(pm.pending, id)
			pm.logger.Debug("oauth: cleaned up expired pending auth",
				slog.String("tool", pa.ToolName),
				slog.String("pending_id", id))
		}
	}
	// Clean orphaned state mappings.
	for state, id := range pm.byState {
		if _, ok := pm.pending[id]; !ok {
			delete(pm.byState, state)
		}
	}
}

func (pm *PendingManager) cleanup(id string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pa, ok := pm.pending[id]; ok {
		pa.closeDone()
		delete(pm.pending, id)
		// Clean state mapping.
		for state, pid := range pm.byState {
			if pid == id {
				delete(pm.byState, state)
			}
		}
		pm.logger.Debug("oauth: cleaned up pending auth",
			slog.String("tool", pa.ToolName),
			slog.String("pending_id", id))
	}
}

// StartCleanup runs periodic cleanup of expired pending auths.
// It blocks until the context is cancelled; call from a goroutine.
func (pm *PendingManager) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pm.Cleanup()
		}
	}
}

func generateID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("oauth: generating ID: %v", err))
	}
	return hex.EncodeToString(b)
}
