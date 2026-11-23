// Package opsreport computes one agent's health numbers for a window: spend,
// tool and skill outcomes, expected against actual schedule fires, and KV
// key counts. Every source is scoped to that one agent; there is no
// unscoped path, and Build refuses an empty agent.
package opsreport

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/scheduler"
)

const (
	DefaultDays   = 7
	MaxDays       = 30
	MaxTools      = 100
	MaxNamespaces = 50
)

// Names used in Report.Unavailable.
const (
	UnavailableKV          = "kv"
	UnavailableSchedules   = "schedules"
	UnavailableActualFires = "schedules.actual_fires"
)

// ScheduleSource is the part of the scheduler the report reads.
type ScheduleSource interface {
	EntriesByAgent(agent string) []scheduler.Entry
	Location() *time.Location
}

type namespaceStatter interface {
	NamespaceStats(ctx context.Context, agent string) ([]kv.NamespaceStat, error)
}

type scheduledRunner interface {
	ScheduledRuns(ctx context.Context, agent string, from, to time.Time) ([]audit.ScheduledRunCount, error)
}

// Reporter builds reports from the stores it was given.
type Reporter struct {
	ops   agent.OpsStore
	kv    namespaceStatter // nil: no KV part
	sched ScheduleSource   // nil: no schedules part
	runs  scheduledRunner  // nil: no actual_fires
}

// New returns nil when memory cannot answer the report's queries (it does
// not implement agent.OpsStore). kvStore, sched and auditStore may be nil
// interfaces; pass a nil interface, never a nil pointer.
func New(memory agent.MemoryStore, kvStore kv.Store, sched ScheduleSource, auditStore audit.Store) *Reporter {
	ops, ok := memory.(agent.OpsStore)
	if !ok {
		return nil
	}
	r := &Reporter{ops: ops, sched: sched}
	r.kv, _ = kvStore.(namespaceStatter)
	r.runs, _ = auditStore.(scheduledRunner)
	return r
}

// Query selects the agent and window.
type Query struct {
	Agent string    // required
	Days  int       // 0 = DefaultDays; capped at MaxDays
	Now   time.Time // zero = time.Now().UTC()
}

// Report is the JSON both ops_report tools return.
type Report struct {
	Agent        string                   `json:"agent"`
	Window       Window                   `json:"window"`
	Spend        Spend                    `json:"spend"`
	Tools        []agent.ToolUsageSummary `json:"tools"`
	ToolsOmitted int                      `json:"tools_omitted,omitempty"`
	Skills       []Skill                  `json:"skills"`
	Schedules    []Schedule               `json:"schedules"`
	KV           *KV                      `json:"kv,omitempty"`
	Unavailable  []string                 `json:"unavailable,omitempty"`
}

// Window is [From, To); the previous window is [PreviousFrom, From).
type Window struct {
	Days         int       `json:"days"`
	From         time.Time `json:"from"`
	To           time.Time `json:"to"`
	PreviousFrom time.Time `json:"previous_from"`
}

// Spend is completed-turn spend in both windows.
type Spend struct {
	CostUSD         float64  `json:"cost_usd"`
	Turns           int      `json:"turns"`
	PreviousCostUSD float64  `json:"previous_cost_usd"`
	PreviousTurns   int      `json:"previous_turns"`
	ChangeUSD       float64  `json:"change_usd"`
	ChangePct       *float64 `json:"change_pct,omitempty"` // nil when the previous window had no spend
}

// Skill is one skill's runs, per-run cost and tool outcomes.
type Skill struct {
	SkillName              string  `json:"skill_name"`
	Runs                   int     `json:"runs"`
	CostUSD                float64 `json:"cost_usd"`
	CostPerRunUSD          float64 `json:"cost_per_run_usd"`
	CompletionTokens       int     `json:"completion_tokens"`
	CompletionTokensPerRun float64 `json:"completion_tokens_per_run"`
	ToolCallCount          int     `json:"tool_call_count"`
	RejectionCount         int     `json:"rejection_count"`
	FailureCount           int     `json:"failure_count"`
	DenialCount            int     `json:"denial_count"`
	ApprovalTimeoutCount   int     `json:"approval_timeout_count"`
	SupervisorErrorCount   int     `json:"supervisor_error_count"`
}

// Schedule compares expected and actual fires for one of the agent's schedules.
type Schedule struct {
	Name          string     `json:"name"`
	Skill         string     `json:"skill"`
	Expression    string     `json:"expression"`
	Enabled       bool       `json:"enabled"`
	ExpectedFires int        `json:"expected_fires"`
	ActualFires   *int       `json:"actual_fires,omitempty"` // nil without the audit log
	FirstFire     *time.Time `json:"first_fire,omitempty"`
	LastFire      *time.Time `json:"last_fire,omitempty"`
}

// KV counts live keys, in total and per namespace.
type KV struct {
	Keys              int         `json:"keys"`
	NoExpiry          int         `json:"no_expiry"`
	Namespaces        []Namespace `json:"namespaces"`
	NamespacesOmitted int         `json:"namespaces_omitted,omitempty"`
}

// Namespace is the text before a key's first ':', or "" for none.
type Namespace struct {
	Namespace string `json:"namespace"`
	Keys      int    `json:"keys"`
	NoExpiry  int    `json:"no_expiry"`
}

// Build computes the report. A failing store query fails the call; a missing
// source leaves its part out and is named in Unavailable.
func (r *Reporter) Build(ctx context.Context, q Query) (*Report, error) {
	if q.Agent == "" {
		return nil, errors.New("opsreport: agent is required")
	}
	w, err := window(q)
	if err != nil {
		return nil, err
	}
	rep := &Report{Agent: q.Agent, Window: w}
	if rep.Spend, err = r.spend(ctx, q.Agent, w); err != nil {
		return nil, err
	}
	if rep.Tools, rep.ToolsOmitted, err = r.tools(ctx, q.Agent, w); err != nil {
		return nil, err
	}
	if rep.Skills, err = r.skills(ctx, q.Agent, w); err != nil {
		return nil, err
	}
	if rep.Schedules, err = r.schedules(ctx, q.Agent, w, rep); err != nil {
		return nil, err
	}
	if rep.KV, err = r.kvPart(ctx, q.Agent, rep); err != nil {
		return nil, err
	}
	return rep, nil
}

func window(q Query) (Window, error) {
	days := q.Days
	switch {
	case days < 0:
		return Window{}, errors.New("invalid days: must not be negative")
	case days == 0:
		days = DefaultDays
	case days > MaxDays:
		days = MaxDays
	}
	to := q.Now
	if to.IsZero() {
		to = time.Now()
	}
	to = to.UTC()
	span := time.Duration(days) * 24 * time.Hour
	return Window{Days: days, From: to.Add(-span), To: to, PreviousFrom: to.Add(-2 * span)}, nil
}

func (r *Reporter) spend(ctx context.Context, agentName string, w Window) (Spend, error) {
	cur, err := r.ops.OpsSpend(ctx, agentName, w.From, w.To)
	if err != nil {
		return Spend{}, fmt.Errorf("opsreport: %w", err)
	}
	prev, err := r.ops.OpsSpend(ctx, agentName, w.PreviousFrom, w.From)
	if err != nil {
		return Spend{}, fmt.Errorf("opsreport: %w", err)
	}
	s := Spend{
		CostUSD: usd(cur.CostUSD), Turns: cur.Turns,
		PreviousCostUSD: usd(prev.CostUSD), PreviousTurns: prev.Turns,
		ChangeUSD: usd(cur.CostUSD - prev.CostUSD),
	}
	if prev.CostUSD > 0 {
		pct := round(100*(cur.CostUSD-prev.CostUSD)/prev.CostUSD, 1)
		s.ChangePct = &pct
	}
	return s, nil
}

func (r *Reporter) tools(ctx context.Context, agentName string, w Window) ([]agent.ToolUsageSummary, int, error) {
	rows, err := r.ops.OpsToolUsage(ctx, agentName, w.From, w.To)
	if err != nil {
		return nil, 0, fmt.Errorf("opsreport: %w", err)
	}
	if rows == nil {
		rows = []agent.ToolUsageSummary{}
	}
	if len(rows) > MaxTools {
		return rows[:MaxTools], len(rows) - MaxTools, nil
	}
	return rows, 0, nil
}

func (r *Reporter) skills(ctx context.Context, agentName string, w Window) ([]Skill, error) {
	rows, err := r.ops.OpsSkillUsage(ctx, agentName, w.From, w.To)
	if err != nil {
		return nil, fmt.Errorf("opsreport: %w", err)
	}
	out := make([]Skill, 0, len(rows))
	for _, s := range rows {
		sk := Skill{
			SkillName: s.SkillName, Runs: s.Runs, CostUSD: usd(s.CostUSD), CompletionTokens: s.CompletionTokens,
			ToolCallCount: s.ToolCallCount, RejectionCount: s.RejectionCount, FailureCount: s.FailureCount,
			DenialCount: s.DenialCount, ApprovalTimeoutCount: s.ApprovalTimeoutCount,
			SupervisorErrorCount: s.SupervisorErrorCount,
		}
		if s.Runs > 0 {
			sk.CostPerRunUSD = usd(s.CostUSD / float64(s.Runs))
			sk.CompletionTokensPerRun = round(float64(s.CompletionTokens)/float64(s.Runs), 1)
		}
		out = append(out, sk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SkillName < out[j].SkillName })
	return out, nil
}

func (r *Reporter) schedules(ctx context.Context, agentName string, w Window, rep *Report) ([]Schedule, error) {
	out := []Schedule{}
	if r.sched == nil {
		rep.Unavailable = append(rep.Unavailable, UnavailableSchedules)
		return out, nil
	}
	for _, e := range r.sched.EntriesByAgent(agentName) {
		s := Schedule{Name: e.Name, Skill: e.Skill, Expression: e.Expr, Enabled: e.Enabled}
		if e.Enabled {
			n, err := scheduler.ExpectedFires(e.Expr, w.From, w.To, r.sched.Location())
			if err != nil {
				return nil, fmt.Errorf("opsreport: schedule %q: %w", e.Name, err)
			}
			s.ExpectedFires = n
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if r.runs == nil {
		rep.Unavailable = append(rep.Unavailable, UnavailableActualFires)
		return out, nil
	}
	return out, r.addActualFires(ctx, agentName, w, out)
}

func (r *Reporter) addActualFires(ctx context.Context, agentName string, w Window, out []Schedule) error {
	runs, err := r.runs.ScheduledRuns(ctx, agentName, w.From, w.To)
	if err != nil {
		return fmt.Errorf("opsreport: %w", err)
	}
	by := make(map[string]audit.ScheduledRunCount, len(runs))
	for _, run := range runs {
		by[run.Schedule] = run
	}
	for i := range out {
		run := by[out[i].Name]
		n := run.Runs
		out[i].ActualFires = &n
		if n > 0 {
			first, last := run.First.UTC(), run.Last.UTC()
			out[i].FirstFire, out[i].LastFire = &first, &last
		}
	}
	return nil
}

func (r *Reporter) kvPart(ctx context.Context, agentName string, rep *Report) (*KV, error) {
	if r.kv == nil {
		rep.Unavailable = append(rep.Unavailable, UnavailableKV)
		return nil, nil
	}
	stats, err := r.kv.NamespaceStats(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("opsreport: %w", err)
	}
	k := &KV{Namespaces: []Namespace{}}
	for i, s := range stats {
		k.Keys += s.Keys
		k.NoExpiry += s.NoExpiry
		if i < MaxNamespaces {
			k.Namespaces = append(k.Namespaces, Namespace(s))
		}
	}
	if len(stats) > MaxNamespaces {
		k.NamespacesOmitted = len(stats) - MaxNamespaces
	}
	return k, nil
}

func usd(x float64) float64 { return round(x, 6) }

func round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}
