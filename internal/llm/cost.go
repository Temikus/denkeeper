package llm

import (
	"strings"
	"sync"
)

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
	mu            sync.Mutex
	sessionCosts  map[string]float64
	sessionStats  map[string]*SessionStats
	globalCost    float64
	maxPerSession float64
}

func NewCostTracker(maxPerSession float64) *CostTracker {
	return &CostTracker{
		sessionCosts:  make(map[string]float64),
		sessionStats:  make(map[string]*SessionStats),
		maxPerSession: maxPerSession,
	}
}

// Record adds cost for a session. Returns true if within budget.
func (ct *CostTracker) Record(sessionID string, cost float64) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sessionCosts[sessionID] += cost
	ct.globalCost += cost

	s := ct.getOrCreateStats(sessionID)
	s.Cost += cost
	s.Messages++

	return ct.sessionCosts[sessionID] <= ct.maxPerSession
}

// RecordWithTokens adds cost and token usage for a session. Returns true if within budget.
// The optional pricingSource parameter records which pricing method was used.
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

	return ct.sessionCosts[sessionID] <= ct.maxPerSession
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

// ExceedsBudget checks if a session has exceeded its budget.
func (ct *CostTracker) ExceedsBudget(sessionID string) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.sessionCosts[sessionID] > ct.maxPerSession
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

// AgentCosts returns per-agent aggregated stats. Agent name is extracted
// from session IDs which have the format "agentname:adapter:externalid".
func (ct *CostTracker) AgentCosts() []AgentStats {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	agents := make(map[string]*AgentStats)
	for id, s := range ct.sessionStats {
		name := agentFromSessionID(id)
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

// MaxBudgetPerSession returns the per-session cost cap.
func (ct *CostTracker) MaxBudgetPerSession() float64 {
	return ct.maxPerSession
}

// agentFromSessionID extracts the agent name from a session ID.
// Session IDs have the format "agentname:adapter:externalid".
func agentFromSessionID(id string) string {
	if i := strings.Index(id, ":"); i > 0 {
		return id[:i]
	}
	return id
}
