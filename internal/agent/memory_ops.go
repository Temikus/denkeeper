package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// OpsStore answers the per-agent queries behind the ops report. Messages,
// tool calls and skill matches have no agent column, so every query scopes
// through conversation_stats.agent; rows of a conversation with no agent are
// never returned. Every method refuses an empty agent.
type OpsStore interface {
	OpsSpend(ctx context.Context, agent string, from, to time.Time) (OpsSpend, error)
	OpsToolUsage(ctx context.Context, agent string, from, to time.Time) ([]ToolUsageSummary, error)
	OpsSkillUsage(ctx context.Context, agent string, from, to time.Time) ([]OpsSkillUsage, error)
}

var _ OpsStore = (*SQLiteMemoryStore)(nil)

var errOpsAgentRequired = errors.New("ops query: agent is required")

// OpsSpend is completed-turn spend: assistant replies that carry a model.
type OpsSpend struct {
	CostUSD float64 `db:"cost"`
	Turns   int     `db:"turns"`
}

// OpsSkillUsage is one skill's runs and tool outcomes. Runs, CostUSD and
// CompletionTokens count only answered turns the skill owned: it was the
// scheduled skill, or the only match.
type OpsSkillUsage struct {
	SkillName            string  `db:"skill_name"`
	Runs                 int     `db:"runs"`
	CostUSD              float64 `db:"cost"`
	CompletionTokens     int     `db:"completion_tokens"`
	ToolCallCount        int     `db:"call_count"`
	RejectionCount       int     `db:"rejection_count"`
	FailureCount         int     `db:"failure_count"`
	DenialCount          int     `db:"denial_count"`
	ApprovalTimeoutCount int     `db:"approval_timeout_count"`
	SupervisorErrorCount int     `db:"supervisor_error_count"`
}

// opsBounds renders a window as SQLite DATETIME text, the layout
// CURRENT_TIMESTAMP writes, so the comparison is a plain string comparison.
// Both bounds round up to the next whole second: created_at has whole-second
// resolution, so a bound cut down would drop rows stored earlier in its own
// second. Rounding both keeps adjacent windows contiguous.
func opsBounds(from, to time.Time) (string, string) {
	return ceilSecond(from).Format(sqliteDatetimeLayout), ceilSecond(to).Format(sqliteDatetimeLayout)
}

func ceilSecond(t time.Time) time.Time {
	t = t.UTC()
	if s := t.Truncate(time.Second); s.Before(t) {
		return s.Add(time.Second)
	}
	return t
}

// OpsSpend sums the agent's completed-turn spend in [from, to).
func (s *SQLiteMemoryStore) OpsSpend(ctx context.Context, agent string, from, to time.Time) (OpsSpend, error) {
	var out OpsSpend
	if agent == "" {
		return out, errOpsAgentRequired
	}
	lo, hi := opsBounds(from, to)
	err := s.db.GetContext(ctx, &out, `
		SELECT COALESCE(SUM(m.cost), 0) AS cost, COUNT(*) AS turns
		FROM messages m
		JOIN conversation_stats cs ON cs.conversation_id = m.conversation_id
		WHERE cs.agent = ? AND m.role = 'assistant' AND m.model != ''
		  AND m.created_at >= ? AND m.created_at < ?`, agent, lo, hi)
	if err != nil {
		return out, fmt.Errorf("ops spend: %w", err)
	}
	return out, nil
}

// opsOutcomeColumns is the outcome split of GetTelemetrySummary's by_tool
// query. TestOpsToolUsage_MatchesTelemetrySummaryForOneAgent pins the two together.
const opsOutcomeColumns = `COUNT(*) AS call_count,
	SUM(CASE WHEN tc.outcome = 'rejected' THEN 1 ELSE 0 END) AS rejection_count,
	SUM(CASE WHEN tc.outcome = 'failed' THEN 1 ELSE 0 END) AS failure_count,
	SUM(CASE WHEN tc.outcome = 'denied' THEN 1 ELSE 0 END) AS denial_count,
	SUM(CASE WHEN tc.outcome = 'approval_timeout' THEN 1 ELSE 0 END) AS approval_timeout_count,
	SUM(CASE WHEN tc.outcome = 'supervisor_error' THEN 1 ELSE 0 END) AS supervisor_error_count`

// OpsToolUsage is the agent's per-tool outcome counts in [from, to), most
// called first.
func (s *SQLiteMemoryStore) OpsToolUsage(ctx context.Context, agent string, from, to time.Time) ([]ToolUsageSummary, error) {
	if agent == "" {
		return nil, errOpsAgentRequired
	}
	lo, hi := opsBounds(from, to)
	out := []ToolUsageSummary{}
	err := s.db.SelectContext(ctx, &out, `
		SELECT tc.tool_name, tc.server_name, `+opsOutcomeColumns+`,
		       SUM(CASE WHEN tc.outcome = 'cached' THEN 1 ELSE 0 END) AS cached_count,
		       COALESCE(AVG(CASE WHEN tc.outcome != 'cached' THEN tc.duration_ms END), 0) AS avg_duration
		FROM tool_calls tc
		JOIN conversation_stats cs ON cs.conversation_id = tc.conversation_id
		WHERE cs.agent = ? AND tc.created_at >= ? AND tc.created_at < ?
		GROUP BY tc.tool_name, tc.server_name
		ORDER BY call_count DESC, tc.tool_name, tc.server_name`, agent, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("ops tool usage: %w", err)
	}
	return out, nil
}

// OpsSkillUsage merges the agent's owned skill runs with its skill-attributed
// tool outcomes (all versions summed), sorted by skill name.
func (s *SQLiteMemoryStore) OpsSkillUsage(ctx context.Context, agent string, from, to time.Time) ([]OpsSkillUsage, error) {
	if agent == "" {
		return nil, errOpsAgentRequired
	}
	lo, hi := opsBounds(from, to)
	var runs []OpsSkillUsage
	// A match owns its turn when it is the scheduled skill or the only match.
	// The reply is the next assistant row before the next user row: a turn that
	// failed before storing its reply has none, so it is not a run and does not
	// take a later turn's cost.
	err := s.db.SelectContext(ctx, &runs, `
		SELECT ms.skill_name, COUNT(*) AS runs,
		       COALESCE(SUM(r.cost), 0) AS cost, COALESCE(SUM(r.tokens_completion), 0) AS completion_tokens
		FROM message_skills ms
		JOIN conversation_stats cs ON cs.conversation_id = ms.conversation_id
		JOIN messages r ON r.id = (
			SELECT a.id FROM messages a
			WHERE a.conversation_id = ms.conversation_id AND a.role = 'assistant' AND a.id > ms.message_id
			  AND NOT EXISTS (
				SELECT 1 FROM messages u
				WHERE u.conversation_id = ms.conversation_id AND u.role = 'user'
				  AND u.id > ms.message_id AND u.id < a.id)
			ORDER BY a.id LIMIT 1)
		WHERE cs.agent = ? AND ms.created_at >= ? AND ms.created_at < ?
		  AND (ms.match_type = 'schedule'
		       OR (SELECT COUNT(*) FROM message_skills o WHERE o.message_id = ms.message_id) = 1)
		GROUP BY ms.skill_name`, agent, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("ops skill runs: %w", err)
	}
	var calls []OpsSkillUsage
	err = s.db.SelectContext(ctx, &calls, `
		SELECT tc.skill_name, `+opsOutcomeColumns+`
		FROM tool_calls tc
		JOIN conversation_stats cs ON cs.conversation_id = tc.conversation_id
		WHERE cs.agent = ? AND tc.skill_name != '' AND tc.created_at >= ? AND tc.created_at < ?
		GROUP BY tc.skill_name`, agent, lo, hi)
	if err != nil {
		return nil, fmt.Errorf("ops skill tool usage: %w", err)
	}
	return mergeSkillUsage(runs, calls), nil
}

func mergeSkillUsage(runs, calls []OpsSkillUsage) []OpsSkillUsage {
	by := map[string]*OpsSkillUsage{}
	for i := range runs {
		by[runs[i].SkillName] = &runs[i]
	}
	for _, c := range calls {
		r, ok := by[c.SkillName]
		if !ok {
			r = &OpsSkillUsage{SkillName: c.SkillName}
			by[c.SkillName] = r
		}
		r.ToolCallCount, r.RejectionCount, r.FailureCount = c.ToolCallCount, c.RejectionCount, c.FailureCount
		r.DenialCount, r.ApprovalTimeoutCount, r.SupervisorErrorCount = c.DenialCount, c.ApprovalTimeoutCount, c.SupervisorErrorCount
	}
	out := make([]OpsSkillUsage, 0, len(by))
	for _, r := range by {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillName < out[j].SkillName })
	return out
}
