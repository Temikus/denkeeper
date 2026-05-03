package llm

import (
	"errors"
	"strings"
	"sync"
)

// SessionLimits holds cost limit thresholds for a session. A zero value means
// the corresponding limit is disabled.
type SessionLimits struct {
	Soft float64 `json:"soft"`
	Hard float64 `json:"hard"`
}

// ErrSoftLimitExceeded is returned when the session's soft cost limit is reached.
var ErrSoftLimitExceeded = errors.New("session soft cost limit exceeded")

// ErrHardLimitExceeded is returned when the session's hard cost limit is reached.
var ErrHardLimitExceeded = errors.New("session hard cost limit exceeded")

// SessionStats holds per-session cost and token tracking.
type SessionStats struct {
	Cost           float64        `json:"cost"`
	InputTokens    int            `json:"input_tokens"`
	OutputTokens   int            `json:"output_tokens"`
	Messages       int            `json:"messages"`
	PricingSources map[string]int `json:"pricing_sources,omitempty"`
}

// AgentStats holds aggregated per-agent cost and token data.
type AgentStats struct {
	Agent        string  `json:"agent"`
	Cost         float64 `json:"cost"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	Messages     int     `json:"messages"`
	Sessions     int     `json:"sessions"`
}

// CostTracker tracks token usage and estimated costs per session and globally.
type CostTracker struct {
	mu               sync.Mutex
	sessionCosts     map[string]float64
	sessionStats     map[string]*SessionStats
	sessionAgents    map[string]string // session ID → agent name
	sessionProviders map[string]string // session ID → last active provider
	globalCost       float64
	defaultLimits    SessionLimits
	agentOverrides   map[string]SessionLimits
	providerLimits   map[string]SessionLimits
}

// NewCostTracker creates a CostTracker with default limits and optional per-agent overrides.
func NewCostTracker(defaults SessionLimits, agentOverrides map[string]SessionLimits) *CostTracker {
	if agentOverrides == nil {
		agentOverrides = make(map[string]SessionLimits)
	}
	return &CostTracker{
		sessionCosts:     make(map[string]float64),
		sessionStats:     make(map[string]*SessionStats),
		sessionAgents:    make(map[string]string),
		sessionProviders: make(map[string]string),
		defaultLimits:    defaults,
		agentOverrides:   agentOverrides,
		providerLimits:   make(map[string]SessionLimits),
	}
}

// RegisterSessionAgent associates a session ID with an agent name for accurate
// per-agent cost attribution and limit enforcement. This is needed for
// channel-based session IDs (e.g. "chan:channelname") where the agent name
// cannot be parsed from the ID format.
func (ct *CostTracker) RegisterSessionAgent(sessionID, agent string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.sessionAgents[sessionID] = agent
}

// Record adds cost for a session. Returns true if within hard budget.
func (ct *CostTracker) Record(sessionID string, cost float64) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sessionCosts[sessionID] += cost
	ct.globalCost += cost

	s := ct.getOrCreateStats(sessionID)
	s.Cost += cost
	s.Messages++

	hard := ct.limitsForSession(sessionID).Hard
	return hard == 0 || ct.sessionCosts[sessionID] <= hard
}

// RecordWithTokens adds cost and token usage for a session. Returns true if within budget.
// The optional pricingSource parameter records which pricing method was used.
// Deprecated: use RecordWithProvider for correct per-provider limit enforcement.
func (ct *CostTracker) RecordWithTokens(sessionID string, cost float64, inputTokens, outputTokens int, pricingSource ...string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sessionCosts[sessionID] += cost
	ct.globalCost += cost

	s := ct.getOrCreateStats(sessionID)
	s.Cost += cost
	s.InputTokens += inputTokens
	s.OutputTokens += outputTokens
	s.Messages++

	if len(pricingSource) > 0 && pricingSource[0] != "" {
		if s.PricingSources == nil {
			s.PricingSources = make(map[string]int)
		}
		s.PricingSources[pricingSource[0]]++
	}

	hard := ct.limitsForSession(sessionID).Hard
	return hard == 0 || ct.sessionCosts[sessionID] <= hard
}

// limitsForSession resolves the effective limits for a session.
// Priority: per-agent override → per-provider limit → default.
// Must be called with ct.mu held.
func (ct *CostTracker) limitsForSession(sessionID string) SessionLimits {
	agent := ct.agentForSession(sessionID)
	if override, ok := ct.agentOverrides[agent]; ok {
		return override
	}
	if provider, ok := ct.sessionProviders[sessionID]; ok {
		if l, ok := ct.providerLimits[provider]; ok {
			return l
		}
	}
	return ct.defaultLimits
}

func (ct *CostTracker) getOrCreateStats(sessionID string) *SessionStats {
	s, ok := ct.sessionStats[sessionID]
	if !ok {
		s = &SessionStats{}
		ct.sessionStats[sessionID] = s
	}
	return s
}

// SessionCost returns the total cost for a session.
func (ct *CostTracker) SessionCost(sessionID string) float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.sessionCosts[sessionID]
}

// GlobalCost returns the total cost across all sessions.
func (ct *CostTracker) GlobalCost() float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.globalCost
}

// ExceedsSoftLimit checks if a session has exceeded its soft cost limit.
// Returns false if the soft limit is disabled (zero).
func (ct *CostTracker) ExceedsSoftLimit(sessionID string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	soft := ct.limitsForSession(sessionID).Soft
	return soft > 0 && ct.sessionCosts[sessionID] > soft
}

// ExceedsHardLimit checks if a session has exceeded its hard cost limit.
// Returns false if the hard limit is disabled (zero).
func (ct *CostTracker) ExceedsHardLimit(sessionID string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	hard := ct.limitsForSession(sessionID).Hard
	return hard > 0 && ct.sessionCosts[sessionID] > hard
}

// ExceedsBudget checks if a session has exceeded its hard budget.
// Deprecated: use ExceedsHardLimit.
func (ct *CostTracker) ExceedsBudget(sessionID string) bool {
	return ct.ExceedsHardLimit(sessionID)
}

// AllSessionCosts returns a copy of all session costs.
func (ct *CostTracker) AllSessionCosts() map[string]float64 {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make(map[string]float64, len(ct.sessionCosts))
	for k, v := range ct.sessionCosts {
		out[k] = v
	}
	return out
}

// AllSessionStats returns a copy of all session stats.
func (ct *CostTracker) AllSessionStats() map[string]SessionStats {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	out := make(map[string]SessionStats, len(ct.sessionStats))
	for k, v := range ct.sessionStats {
		out[k] = *v
	}
	return out
}

// AgentCosts returns per-agent aggregated stats from the in-memory tracker.
func (ct *CostTracker) AgentCosts() []AgentStats {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	agents := make(map[string]*AgentStats)
	for id, s := range ct.sessionStats {
		name := ct.agentForSession(id)
		a, ok := agents[name]
		if !ok {
			a = &AgentStats{Agent: name}
			agents[name] = a
		}
		a.Cost += s.Cost
		a.InputTokens += s.InputTokens
		a.OutputTokens += s.OutputTokens
		a.Messages += s.Messages
		a.Sessions++
	}

	result := make([]AgentStats, 0, len(agents))
	for _, a := range agents {
		result = append(result, *a)
	}
	return result
}

// DefaultLimits returns the default cost limits.
func (ct *CostTracker) DefaultLimits() SessionLimits {
	return ct.defaultLimits
}

// SetAgentLimits sets per-agent cost limit overrides.
func (ct *CostTracker) SetAgentLimits(agent string, limits SessionLimits) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.agentOverrides[agent] = limits
}

// SetProviderLimits sets per-provider cost limit overrides.
func (ct *CostTracker) SetProviderLimits(provider string, limits SessionLimits) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.providerLimits[provider] = limits
}

// RecordWithProvider adds cost, token usage, and provider attribution for a session.
// Returns true if within hard budget.
func (ct *CostTracker) RecordWithProvider(sessionID, provider string, cost float64, inputTokens, outputTokens int, pricingSource string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if provider != "" {
		ct.sessionProviders[sessionID] = provider
	}

	ct.sessionCosts[sessionID] += cost
	ct.globalCost += cost

	s := ct.getOrCreateStats(sessionID)
	s.Cost += cost
	s.InputTokens += inputTokens
	s.OutputTokens += outputTokens
	s.Messages++

	if pricingSource != "" {
		if s.PricingSources == nil {
			s.PricingSources = make(map[string]int)
		}
		s.PricingSources[pricingSource]++
	}

	hard := ct.limitsForSession(sessionID).Hard
	return hard == 0 || ct.sessionCosts[sessionID] <= hard
}

// SetDefaultLimits updates the default cost limits applied to sessions
// without agent-specific overrides.
func (ct *CostTracker) SetDefaultLimits(limits SessionLimits) {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	ct.defaultLimits = limits
}

// MaxBudgetPerSession returns the default hard cost cap.
// Deprecated: use DefaultLimits().Hard.
func (ct *CostTracker) MaxBudgetPerSession() float64 {
	return ct.defaultLimits.Hard
}

// agentForSession returns the agent name for a session ID, preferring the
// explicitly registered mapping (needed for channel-based IDs like "chan:name")
// and falling back to parsing the session ID prefix.
// Must be called with ct.mu held.
func (ct *CostTracker) agentForSession(sessionID string) string {
	if name, ok := ct.sessionAgents[sessionID]; ok {
		return name
	}
	return agentFromSessionID(sessionID)
}

// agentFromSessionID extracts the agent name from a session ID.
// Session IDs have the format "agentname:adapter:externalid".
func agentFromSessionID(id string) string {
	if i := strings.Index(id, ":"); i > 0 {
		return id[:i]
	}
	return id
}
