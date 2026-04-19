package approval

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	approvalTracer  = otel.Tracer("denkeeper.approval")
	approvalMeter   = otel.Meter("denkeeper.approval")
	approvalWaitDur metric.Float64Histogram
)

func init() {
	approvalWaitDur, _ = approvalMeter.Float64Histogram("denkeeper.approval.wait_duration",
		metric.WithDescription("Time spent waiting for approval resolution in seconds"),
		metric.WithUnit("s"))
}

// Manager coordinates the persistent Store with the in-memory action Registry.
// It is the primary API used by the Engine and REST API server.
type Manager struct {
	store     Store
	autoStore AutoApproveStore
	registry  *Registry
	logger    *slog.Logger

	waiterMu sync.Mutex
	waiters  map[string]chan Status // notified when an approval is resolved

	// sessionRules holds ephemeral auto-approve rules keyed by "agent\x00convID\x00tool".
	sessionRules sync.Map
}

// NewManager creates a Manager backed by the given store.
// The store must also implement AutoApproveStore for auto-approve support.
func NewManager(store Store, logger *slog.Logger) *Manager {
	m := &Manager{
		store:    store,
		registry: NewRegistry(),
		logger:   logger,
		waiters:  make(map[string]chan Status),
	}
	if as, ok := store.(AutoApproveStore); ok {
		m.autoStore = as
	}
	return m
}

// DefaultTTL is the time an approval request stays pending before it is
// automatically expired by the background worker.
const DefaultTTL = 24 * time.Hour

// SessionRuleTTL is the duration a session auto-approve rule stays active.
// After this period, the rule expires and the user must re-approve.
const SessionRuleTTL = 15 * time.Minute

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
	return m.submit(generateID(), ctx, agentName, kind, summary, payload, externalID, adapterName, conversationID, action)
}

// submit is the internal implementation of Submit that accepts a pre-generated
// ID. This allows SubmitAndWait to pre-register a waiter channel before the
// request is persisted, eliminating the race between Submit and waiter
// registration.
func (m *Manager) submit(
	id string,
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
				m.notifyWaiter(id, StatusApproved)
				return req, fmt.Errorf("approval action: %w", err)
			}
			m.logger.Info("approval action executed", "id", id, "resolvedBy", resolvedBy)
			m.notifyWaiter(id, StatusApproved)
			return req, nil
		}
	} else {
		// Denied — clean up any registered closure.
		m.registry.Delete(id)
		m.logger.Info("approval denied", "id", id, "resolvedBy", resolvedBy)
	}

	// Fetch the updated record BEFORE notifying the waiter so that any
	// goroutine unblocked by notifyWaiter (e.g. SubmitAndWait returning)
	// cannot race to close the underlying store before we finish the Get.
	req, err := m.store.Get(ctx, id)
	m.notifyWaiter(id, status)
	return req, err
}

// ErrStaleCallback is returned by ResolveByCallback when the callback refers to
// an approval that exists but is no longer pending (already resolved, expired,
// or approved). The caller should surface its Status to the user.
var ErrStaleCallback = fmt.Errorf("approval: callback refers to a non-pending request")

// CallbackAction identifies what a callback button requested.
type CallbackAction string

const (
	CallbackApprove        CallbackAction = "approve"
	CallbackDeny           CallbackAction = "deny"
	CallbackApproveSession CallbackAction = "approve_session"
	CallbackApproveAlways  CallbackAction = "approve_always"
)

// parseCallback splits a callback data string into the DB prefix and action.
// Formats: "appr:{id}:approve", ":deny", ":approve_session", ":approve_always".
func parseCallback(callbackData string) (prefix string, action CallbackAction, ok bool) {
	if !strings.HasPrefix(callbackData, "appr:") {
		return "", "", false
	}
	for _, suffix := range []CallbackAction{CallbackApproveSession, CallbackApproveAlways, CallbackApprove, CallbackDeny} {
		s := ":" + string(suffix)
		if strings.HasSuffix(callbackData, s) {
			return strings.TrimSuffix(callbackData, s), suffix, true
		}
	}
	return "", "", false
}

// ResolveByCallback parses the full Telegram callback data string
// ("appr:{id}:approve", ":deny", ":approve_session", ":approve_always"),
// resolves the approval, and optionally creates an auto-approve rule.
// Returns ErrNotFound for unknown callbacks, ErrStaleCallback when the
// approval is no longer pending.
func (m *Manager) ResolveByCallback(ctx context.Context, callbackData string, resolvedBy string) (*Request, error) {
	prefix, action, ok := parseCallback(callbackData)
	if !ok {
		return nil, ErrNotFound
	}

	approved := action != CallbackDeny

	// Look up the pending row by prefix.
	req, err := m.store.ResolveByCallbackPrefix(ctx, prefix, statusFor(approved), resolvedBy)
	if err != nil {
		if err == ErrNotFound {
			if existing, lookupErr := m.store.GetByCallbackData(ctx, prefix); lookupErr == nil {
				return existing, ErrStaleCallback
			}
		}
		return nil, err
	}

	// If approved, invoke the action closure.
	resolvedStatus := StatusDenied
	if approved {
		resolvedStatus = StatusApproved
		fn, ok := m.registry.Pop(req.ID)
		if ok {
			if err := fn(ctx, req.Payload); err != nil {
				m.logger.Error("approval action failed", "id", req.ID, "error", err)
				m.notifyWaiter(req.ID, resolvedStatus)
				return req, fmt.Errorf("approval action: %w", err)
			}
			m.logger.Info("approval action executed via callback", "id", req.ID)
		} else {
			m.logger.Warn("approval action not found in registry (restarted?)", "id", req.ID)
		}

		// Create auto-approve rule if requested.
		m.createAutoApproveFromCallback(ctx, action, req, resolvedBy)
	} else {
		m.registry.Delete(req.ID)
		m.logger.Info("approval denied via callback", "id", req.ID)
	}

	m.notifyWaiter(req.ID, resolvedStatus)
	return req, nil
}

// createAutoApproveFromCallback creates an auto-approve rule when the callback
// action is approve_session or approve_always. Extracts the tool name from the
// approval summary. Errors are logged but do not fail the resolution.
func (m *Manager) createAutoApproveFromCallback(ctx context.Context, action CallbackAction, req *Request, resolvedBy string) {
	if req.Kind != ActionKindToolCall {
		return
	}
	toolName := ExtractToolName(req.Summary)
	if toolName == "" {
		return
	}

	switch action {
	case CallbackApproveSession:
		m.AddSessionRule(ctx, req.AgentName, toolName, req.ConversationID, resolvedBy)
	case CallbackApproveAlways:
		if _, err := m.AddPermanentRule(ctx, req.AgentName, toolName, resolvedBy); err != nil {
			m.logger.Error("failed to create permanent auto-approve rule", "error", err)
		}
	}
}

// ExtractToolName parses the tool name from an approval summary.
// Expected format: `Execute tool "toolname" with args: ...`
func ExtractToolName(summary string) string {
	const prefix = `Execute tool "`
	idx := strings.Index(summary, prefix)
	if idx < 0 {
		return ""
	}
	rest := summary[idx+len(prefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// notifyWaiter signals any goroutine blocked in SubmitAndWait for the given ID.
func (m *Manager) notifyWaiter(id string, status Status) {
	m.waiterMu.Lock()
	defer m.waiterMu.Unlock()
	if ch, ok := m.waiters[id]; ok {
		select {
		case ch <- status:
		default:
		}
	}
}

// SubmitAndWait creates an approval, blocks until it is resolved or the
// context expires, and returns the outcome. The action func is registered
// with the approval and fired automatically by Resolve on approval. Pass nil
// for action if the caller handles the approved action itself.
//
// The waiter channel is pre-registered before the request is persisted,
// eliminating the race between Submit and waiter registration that exists
// when calling Submit + WaitForResolution separately.
func (m *Manager) SubmitAndWait(
	ctx context.Context,
	agentName string,
	kind ActionKind,
	summary string,
	payload string,
	externalID string,
	adapterName string,
	conversationID string,
	action ActionFunc,
) (Status, *Request, error) {
	ctx, span := approvalTracer.Start(ctx, "approval.wait", trace.WithAttributes(
		attribute.String("approval.agent", agentName),
		attribute.String("approval.kind", string(kind)),
	))
	start := time.Now()
	defer func() {
		elapsed := time.Since(start).Seconds()
		approvalWaitDur.Record(ctx, elapsed,
			metric.WithAttributes(
				attribute.String("approval.agent", agentName),
				attribute.String("approval.kind", string(kind)),
			))
		span.End()
	}()

	if action == nil {
		action = func(_ context.Context, _ string) error { return nil }
	}

	// Pre-register the waiter channel BEFORE submitting, so that a
	// near-instant Resolve never drops the notification.
	id := generateID()
	ch := make(chan Status, 1)
	m.waiterMu.Lock()
	m.waiters[id] = ch
	m.waiterMu.Unlock()

	defer func() {
		m.waiterMu.Lock()
		delete(m.waiters, id)
		m.waiterMu.Unlock()
	}()

	req, err := m.submit(id, ctx, agentName, kind, summary, payload, externalID, adapterName, conversationID, action)
	if err != nil {
		span.SetAttributes(attribute.String("approval.resolution", "error"))
		return StatusDenied, nil, err
	}

	span.SetAttributes(attribute.String("approval.id", req.ID))

	select {
	case status := <-ch:
		span.SetAttributes(attribute.String("approval.resolution", string(status)))
		return status, req, nil
	case <-ctx.Done():
		span.SetAttributes(attribute.String("approval.resolution", "expired"))
		return StatusExpired, req, ctx.Err()
	}
}

// WaitForResolution blocks until the approval with the given ID is resolved.
// Returns StatusApproved, StatusDenied, or StatusExpired (if the context is cancelled).
func (m *Manager) WaitForResolution(ctx context.Context, id string) Status {
	ch := make(chan Status, 1)
	m.waiterMu.Lock()
	m.waiters[id] = ch
	m.waiterMu.Unlock()

	defer func() {
		m.waiterMu.Lock()
		delete(m.waiters, id)
		m.waiterMu.Unlock()
	}()

	select {
	case status := <-ch:
		return status
	case <-ctx.Done():
		return StatusExpired
	}
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

// --- Auto-approve rule management ---

// sessionRuleKey builds the lookup key for session-scoped auto-approve rules.
func sessionRuleKey(agentName, conversationID, toolName string) string {
	return agentName + "\x00" + conversationID + "\x00" + toolName
}

// ShouldAutoApprove checks whether the given tool call should be auto-approved.
// It checks session rules (in-memory) first, then permanent rules (DB).
// Returns (true, scope) on match, (false, "") on no match.
func (m *Manager) ShouldAutoApprove(ctx context.Context, agentName, toolName, conversationID string) (bool, AutoApproveScope) {
	// 1. Session rules (in-memory, conversation-scoped, time-limited).
	key := sessionRuleKey(agentName, conversationID, toolName)
	if v, ok := m.sessionRules.Load(key); ok {
		if expiresAt, isTime := v.(time.Time); isTime && time.Now().Before(expiresAt) {
			return true, ScopeSession
		}
		// Expired — clean up lazily.
		m.sessionRules.Delete(key)
	}

	// 2. Permanent rules (DB, agent-scoped).
	if m.autoStore != nil {
		if _, err := m.autoStore.MatchAutoApproveRule(ctx, agentName, toolName); err == nil {
			return true, ScopePermanent
		}
	}

	// 3. Future: config-based rules would be checked here.

	return false, ""
}

// AddSessionRule creates a time-limited auto-approve rule for the current conversation.
// The rule expires after SessionRuleTTL (15 minutes).
// After storing the rule it auto-resolves any pending approvals for the same agent+tool.
func (m *Manager) AddSessionRule(ctx context.Context, agentName, toolName, conversationID, createdBy string) {
	key := sessionRuleKey(agentName, conversationID, toolName)
	expiresAt := time.Now().Add(SessionRuleTTL)
	m.sessionRules.Store(key, expiresAt)
	m.logger.Info("session auto-approve rule added",
		"agent", agentName, "tool", toolName, "conversation", conversationID,
		"by", createdBy, "expires_at", expiresAt.Format(time.RFC3339))
	m.autoResolvePending(ctx, agentName, toolName)
}

// AddPermanentRule creates a persistent auto-approve rule for the given agent+tool.
func (m *Manager) AddPermanentRule(ctx context.Context, agentName, toolName, createdBy string) (*AutoApproveRule, error) {
	if m.autoStore == nil {
		return nil, fmt.Errorf("adding permanent auto-approve rule: store does not support auto-approve")
	}
	rule := AutoApproveRule{
		ID:        generateID(),
		AgentName: agentName,
		ToolName:  toolName,
		Scope:     ScopePermanent,
		CreatedBy: createdBy,
	}
	if _, err := m.autoStore.CreateAutoApproveRule(ctx, rule); err != nil {
		return nil, fmt.Errorf("adding permanent auto-approve rule: %w", err)
	}
	m.logger.Info("permanent auto-approve rule added",
		"id", rule.ID, "agent", agentName, "tool", toolName, "by", createdBy)
	m.autoResolvePending(ctx, agentName, toolName)
	return &rule, nil
}

// autoResolvePending approves all pending approvals for the given agent+tool.
// Called after an auto-approve rule is created so that already-queued approvals
// for the same tool are resolved instead of going stale.
func (m *Manager) autoResolvePending(ctx context.Context, agentName, toolName string) {
	pending, err := m.store.List(ctx, StatusPending)
	if err != nil {
		m.logger.Warn("auto-resolve: failed to list pending approvals", "error", err)
		return
	}
	for _, req := range pending {
		if req.AgentName != agentName {
			continue
		}
		if req.Kind != ActionKindToolCall {
			continue
		}
		if ExtractToolName(req.Summary) != toolName {
			continue
		}
		if _, resolveErr := m.Resolve(ctx, req.ID, true, "auto_approve"); resolveErr != nil {
			m.logger.Warn("auto-resolve: failed to resolve pending approval",
				"id", req.ID, "error", resolveErr)
		} else {
			m.logger.Info("auto-resolved pending approval", "id", req.ID, "tool", toolName)
		}
	}
}

// RemoveAutoApproveRule deletes a permanent auto-approve rule by ID.
func (m *Manager) RemoveAutoApproveRule(ctx context.Context, id string) error {
	if m.autoStore == nil {
		return fmt.Errorf("removing auto-approve rule: store does not support auto-approve")
	}
	if err := m.autoStore.DeleteAutoApproveRule(ctx, id); err != nil {
		return fmt.Errorf("removing auto-approve rule: %w", err)
	}
	m.logger.Info("auto-approve rule removed", "id", id)
	return nil
}

// ListAutoApproveRules returns all auto-approve rules for the given agent.
// Pass "" for all agents. Combines permanent (DB) and session (in-memory) rules.
func (m *Manager) ListAutoApproveRules(ctx context.Context, agentName string) ([]AutoApproveRule, error) {
	var rules []AutoApproveRule

	// Permanent rules from DB.
	if m.autoStore != nil {
		dbRules, err := m.autoStore.ListAutoApproveRules(ctx, agentName)
		if err != nil {
			return nil, fmt.Errorf("listing auto-approve rules: %w", err)
		}
		rules = append(rules, dbRules...)
	}

	// Session rules from memory (skip expired).
	now := time.Now()
	m.sessionRules.Range(func(key, val any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		expiresAt, isTime := val.(time.Time)
		if !isTime || !now.Before(expiresAt) {
			m.sessionRules.Delete(key) // clean up expired
			return true
		}
		parts := strings.SplitN(k, "\x00", 3)
		if len(parts) != 3 {
			return true
		}
		agent, convID, tool := parts[0], parts[1], parts[2]
		if agentName != "" && agent != agentName {
			return true
		}
		rules = append(rules, AutoApproveRule{
			AgentName:      agent,
			ToolName:       tool,
			Scope:          ScopeSession,
			ConversationID: convID,
			ExpiresAt:      &expiresAt,
		})
		return true
	})

	return rules, nil
}

// ClearSessionRules removes all session-scoped auto-approve rules for a conversation.
func (m *Manager) ClearSessionRules(conversationID string) {
	m.sessionRules.Range(func(key, _ any) bool {
		k, ok := key.(string)
		if !ok {
			return true
		}
		parts := strings.SplitN(k, "\x00", 3)
		if len(parts) == 3 && parts[1] == conversationID {
			m.sessionRules.Delete(key)
		}
		return true
	})
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
