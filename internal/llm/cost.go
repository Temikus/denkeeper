package llm

import "sync"

// CostTracker tracks token usage and estimated costs per session and globally.
type CostTracker struct {
	mu            sync.Mutex
	sessionCosts  map[string]float64
	globalCost    float64
	maxPerSession float64
}

func NewCostTracker(maxPerSession float64) *CostTracker {
	return &CostTracker{
		sessionCosts:  make(map[string]float64),
		maxPerSession: maxPerSession,
	}
}

// Record adds cost for a session. Returns true if within budget.
func (ct *CostTracker) Record(sessionID string, cost float64) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	ct.sessionCosts[sessionID] += cost
	ct.globalCost += cost

	return ct.sessionCosts[sessionID] <= ct.maxPerSession
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

// MaxBudgetPerSession returns the per-session cost cap.
func (ct *CostTracker) MaxBudgetPerSession() float64 {
	return ct.maxPerSession
}
