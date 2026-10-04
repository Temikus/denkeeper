package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/llmfactory"
)

const (
	verdictApprove  = "APPROVE"
	verdictDeny     = "DENY"
	verdictEscalate = "ESCALATE"

	replayDefaultWindow = 30 * 24 * time.Hour
	replayArgsWidth     = 80
)

var (
	replayVerdicts     = []string{verdictApprove, verdictEscalate, verdictDeny}
	replayApproveSweep = []float64{0.80, 0.90, 0.95, 0.99}
	replayDenySweep    = []float64{0.01, 0.05, 0.10, 0.20}
)

type replayFlags struct {
	agent, decider, since, format string
	approveAt, denyAt             float64
	limit, show, concurrency      int
}

func newDecideCmd() *cobra.Command {
	decideCmd := &cobra.Command{
		Use:   "decide",
		Short: "Work with decision models",
		Long:  "Calibrate [[llm.deciders]] decision models against recorded supervisor reviews.",
	}
	decideCmd.PersistentFlags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	var f replayFlags
	replayCmd := &cobra.Command{
		Use:   "replay",
		Short: "Replay past supervisor reviews through a decider",
		Long: "Re-scores an agent's recorded supervisor reviews with a decision model and reports how often\n" +
			"the two agree, per threshold. Read-only: nothing is audited and no approval outcome changes.\n\n" +
			"Each replayed review sends its tool arguments and recent messages to the decider's provider.\n" +
			"Tool descriptions and skill context are not in the audit log, so treat the result as\n" +
			"indicative; shadow-mode audit events are the authoritative comparison.",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDecideReplay(os.Stdout, os.Stderr, f)
		},
	}
	replayCmd.Flags().StringVar(&f.agent, "agent", "", "agent whose supervisor reviews to replay")
	_ = replayCmd.MarkFlagRequired("agent")
	replayCmd.Flags().StringVar(&f.decider, "decider", "", "[[llm.deciders]] name (default: the agent's supervisor_decider)")
	replayCmd.Flags().StringVar(&f.since, "since", "", "replay reviews from this date (2006-01-02 or RFC3339; default: 30 days ago)")
	replayCmd.Flags().Float64Var(&f.approveAt, "approve-at", 0, "approve threshold (default: the agent's supervisor_decider_approve_at)")
	replayCmd.Flags().Float64Var(&f.denyAt, "deny-at", 0, "deny threshold (default: the agent's supervisor_decider_deny_at)")
	replayCmd.Flags().IntVar(&f.limit, "limit", 500, "maximum reviews to replay, newest first")
	replayCmd.Flags().IntVar(&f.show, "show", 20, "disagreements to list")
	replayCmd.Flags().IntVar(&f.concurrency, "concurrency", 4, "parallel decider calls")
	replayCmd.Flags().StringVarP(&f.format, "format", "f", "text", "output format: text or json")

	decideCmd.AddCommand(replayCmd)
	return decideCmd
}

func runDecideReplay(w, progress io.Writer, f replayFlags) error {
	cfg, err := config.Load(resolveConfigPath())
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	opts, dc, err := resolveReplayOpts(cfg, f, time.Now())
	if err != nil {
		return err
	}
	decider, err := replayDecider(cfg, dc)
	if err != nil {
		return err
	}

	// Stat first: NewSQLiteStore would create an empty database.
	auditPath := filepath.Join(filepath.Dir(cfg.Memory.DBPath), "audit.db")
	if _, err := os.Stat(auditPath); err != nil {
		return fmt.Errorf("no audit log at %s (is [audit] enabled?): %w", auditPath, err)
	}
	auditStore, err := audit.NewSQLiteStore(auditPath)
	if err != nil {
		return fmt.Errorf("opening audit log at %s: %w", auditPath, err)
	}
	defer func() { _ = auditStore.Close() }()
	memory, err := agent.OpenSQLiteMemoryStoreReadOnly(cfg.Memory.DBPath)
	if err != nil {
		return fmt.Errorf("opening memory store at %s: %w", cfg.Memory.DBPath, err)
	}
	defer func() { _ = memory.Close() }()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	report, err := replayDecisions(ctx, progress, auditStore, memory, decider, opts)
	if err != nil {
		return err
	}
	if opts.Format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}
	return report.writeText(w)
}

// replayOpts is a fully resolved replay request.
type replayOpts struct {
	Agent           string
	Since, Until    time.Time
	ApproveAt       float64
	DenyAt          float64
	Limit           int
	Show            int
	Concurrency     int
	ContextMessages int
	Format          string
}

func resolveReplayOpts(cfg *config.Config, f replayFlags, now time.Time) (replayOpts, config.DeciderConfig, error) {
	var none config.DeciderConfig
	if f.format != "text" && f.format != "json" {
		return replayOpts{}, none, fmt.Errorf("unknown format %q (use text or json)", f.format)
	}
	idx := slices.IndexFunc(cfg.Agents, func(a config.AgentInstanceConfig) bool { return a.Name == f.agent })
	if idx < 0 {
		names := make([]string, len(cfg.Agents))
		for i, a := range cfg.Agents {
			names[i] = a.Name
		}
		return replayOpts{}, none, fmt.Errorf("agent %q not found in config (agents: %s)", f.agent, strings.Join(names, ", "))
	}
	ac := cfg.Agents[idx]

	dc, err := resolveReplayDecider(cfg, ac, f.decider)
	if err != nil {
		return replayOpts{}, none, err
	}

	opts := replayOpts{
		Agent:           ac.Name,
		Until:           now,
		ApproveAt:       firstNonZero(f.approveAt, ac.SupervisorDeciderApproveAt, config.DefaultDeciderApproveAt),
		DenyAt:          firstNonZero(f.denyAt, ac.SupervisorDeciderDenyAt, config.DefaultDeciderDenyAt),
		Limit:           f.limit,
		Show:            f.show,
		Concurrency:     max(f.concurrency, 1),
		ContextMessages: ac.SupervisorContextMessages,
		Format:          f.format,
	}
	if opts.ContextMessages <= 0 {
		opts.ContextMessages = agent.DefaultSupervisorContextMessages
	}
	if opts.DenyAt <= 0 || opts.DenyAt >= opts.ApproveAt || opts.ApproveAt >= 1 {
		return replayOpts{}, none, fmt.Errorf("thresholds must satisfy 0 < deny-at < approve-at < 1 (got deny-at=%g approve-at=%g)",
			opts.DenyAt, opts.ApproveAt)
	}
	if opts.Limit <= 0 {
		return replayOpts{}, none, fmt.Errorf("--limit must be positive")
	}
	if opts.Since, err = parseReplaySince(f.since, now); err != nil {
		return replayOpts{}, none, err
	}
	return opts, dc, nil
}

// resolveReplayDecider picks the named decider, defaulting to the agent's own.
func resolveReplayDecider(cfg *config.Config, ac config.AgentInstanceConfig, name string) (config.DeciderConfig, error) {
	var none config.DeciderConfig
	if len(cfg.LLM.Deciders) == 0 {
		return none, fmt.Errorf("no [[llm.deciders]] configured; add one to replay reviews through it")
	}
	names := make([]string, len(cfg.LLM.Deciders))
	for i, d := range cfg.LLM.Deciders {
		names[i] = d.Name
	}
	if name == "" {
		name = ac.SupervisorDecider
	}
	if name == "" {
		return none, fmt.Errorf("agent %q has no supervisor_decider; pass --decider (configured [[llm.deciders]]: %s)",
			ac.Name, strings.Join(names, ", "))
	}
	di := slices.IndexFunc(cfg.LLM.Deciders, func(d config.DeciderConfig) bool { return d.Name == name })
	if di < 0 {
		return none, fmt.Errorf("decider %q not found (configured [[llm.deciders]]: %s)", name, strings.Join(names, ", "))
	}
	return cfg.LLM.Deciders[di], nil
}

func firstNonZero(vals ...float64) float64 {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func parseReplaySince(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return now.Add(-replayDefaultWindow), nil
	}
	if t, err := time.ParseInLocation("2006-01-02", s, time.Local); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("parsing --since %q: want 2006-01-02 or RFC3339", s)
}

// replayDecider builds only the named decider's provider. No cost tracker:
// there is no session to bill, and the report sums provider-reported cost.
func replayDecider(cfg *config.Config, dc config.DeciderConfig) (*llm.Decider, error) {
	pi := slices.IndexFunc(cfg.LLM.Providers, func(p config.ProviderInstanceConfig) bool { return p.Name == dc.Provider })
	if pi < 0 {
		return nil, fmt.Errorf("decider %q: provider %q not found", dc.Name, dc.Provider)
	}
	p, err := llmfactory.New(cfg.LLM.Providers[pi], cfg.LLM.OpenRouter, nil)
	if err != nil {
		return nil, fmt.Errorf("decider %q: %w", dc.Name, err)
	}
	dp, ok := p.(llm.DecisionProvider)
	if !ok {
		return nil, fmt.Errorf("decider %q: provider %q does not serve decisions", dc.Name, dc.Provider)
	}
	return llm.NewDecider(deciderConfig(dc), dp, nil), nil
}

// replayMessages is the slice of the memory store a replay reads.
type replayMessages interface {
	GetMessagesBefore(ctx context.Context, convID string, before time.Time, limit int) ([]agent.StoredMessage, error)
}

// reviewCase is one recorded supervisor verdict, with the context to re-score it.
type reviewCase struct {
	time       time.Time
	tool       string
	arguments  string
	supervisor string
	recent     []agent.StoredMessage
}

type replayOutcome struct {
	answers  map[string]llm.Answer
	cost     float64
	duration time.Duration
	cause    string // non-empty: the call failed and carries no verdict
}

func replayDecisions(ctx context.Context, progress io.Writer, store audit.Store, msgs replayMessages, d *llm.Decider, opts replayOpts) (*replayReport, error) {
	cases, unreadable, err := loadReviewCases(ctx, store, msgs, opts)
	if err != nil {
		return nil, err
	}

	outcomes := make([]replayOutcome, len(cases))
	jobs := make(chan int)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		done int
	)
	for range min(opts.Concurrency, max(len(cases), 1)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				outcomes[i] = replayCase(ctx, d, opts.Agent, cases[i])
				mu.Lock()
				done++
				_, _ = fmt.Fprintf(progress, "\rreplayed %d/%d", done, len(cases))
				mu.Unlock()
			}
		}()
	}
	for i := range cases {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if len(cases) > 0 {
		_, _ = fmt.Fprintln(progress)
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("replay interrupted: %w", err)
	}

	report := buildReplayReport(cases, outcomes, opts)
	report.Decider, report.Model = d.Name(), d.Model()
	if unreadable > 0 {
		report.Skipped["unreadable"] = unreadable
	}
	return report, nil
}

// loadReviewCases pages the agent's supervisor verdicts, newest first, and
// attaches each one's conversation context. Decider shadow events and failed
// reviews carry no supervisor verdict and are passed over.
func loadReviewCases(ctx context.Context, store audit.Store, msgs replayMessages, opts replayOpts) ([]reviewCase, int, error) {
	const page = 200 // audit.Store.List's cap
	var (
		cases      []reviewCase
		unreadable int
	)
	for offset := 0; len(cases) < opts.Limit; offset += page {
		events, _, err := store.List(ctx, audit.ListOpts{
			Categories: []string{audit.CategorySupervisor},
			Agent:      opts.Agent,
			Since:      &opts.Since,
			Until:      &opts.Until, // pins the window so offsets stay stable
			Limit:      page,
			Offset:     offset,
		})
		if err != nil {
			return nil, 0, fmt.Errorf("listing supervisor reviews: %w", err)
		}
		for _, ev := range events {
			if len(cases) == opts.Limit {
				break
			}
			if !strings.HasPrefix(ev.Source, "supervisor:") {
				continue
			}
			var detail struct {
				Tool      string `json:"tool"`
				Arguments string `json:"arguments"`
				Decision  string `json:"decision"`
			}
			if json.Unmarshal([]byte(ev.Detail), &detail) != nil || detail.Tool == "" {
				unreadable++
				continue
			}
			if !slices.Contains(replayVerdicts, detail.Decision) {
				continue
			}
			c := reviewCase{time: ev.Timestamp, tool: detail.Tool, arguments: detail.Arguments, supervisor: detail.Decision}
			if ev.ConversationID != "" {
				c.recent, err = msgs.GetMessagesBefore(ctx, ev.ConversationID, ev.Timestamp, opts.ContextMessages)
				if err != nil {
					return nil, 0, fmt.Errorf("loading context for %s: %w", ev.ConversationID, err)
				}
			}
			cases = append(cases, c)
		}
		if len(events) < page {
			break
		}
	}
	return cases, unreadable, nil
}

func replayCase(ctx context.Context, d *llm.Decider, agentName string, c reviewCase) replayOutcome {
	start := time.Now()
	resp, err := agent.ReplaySupervisorDecider(ctx, d, "decide-replay:"+agentName, agent.DeciderReplayInput{
		Agent:     agentName,
		Tool:      c.tool,
		Arguments: c.arguments,
		Recent:    c.recent,
	})
	out := replayOutcome{duration: time.Since(start)}
	if err != nil {
		out.cause = agent.SupervisorErrorCause(err)
		return out
	}
	out.answers, out.cost = resp.Answers, resp.CostUSD
	return out
}

type replayReport struct {
	Agent              string                    `json:"agent"`
	Decider            string                    `json:"decider"`
	Model              string                    `json:"model"`
	Since              time.Time                 `json:"since"`
	Reviews            int                       `json:"reviews"`
	Replayed           int                       `json:"replayed"`
	Skipped            map[string]int            `json:"skipped"`
	WithoutUserRequest int                       `json:"without_user_request"`
	LatencyP50Ms       int64                     `json:"latency_p50_ms"`
	LatencyP95Ms       int64                     `json:"latency_p95_ms"`
	CostUSD            float64                   `json:"cost_usd"`
	ApproveAt          float64                   `json:"approve_at"`
	DenyAt             float64                   `json:"deny_at"`
	Matrix             map[string]map[string]int `json:"matrix"` // decider verdict → supervisor verdict → count
	ApproveSweep       []replaySweepRow          `json:"approve_sweep"`
	DenySweep          []replaySweepRow          `json:"deny_sweep"`
	DisagreementCount  int                       `json:"disagreement_count"`
	Disagreements      []replayDisagreement      `json:"disagreements"`
}

// replaySweepRow counts what one threshold would decide and how often the
// supervisor ruled otherwise: not APPROVE for an approve row, APPROVE for a
// deny row.
type replaySweepRow struct {
	At        float64 `json:"at"`
	Decided   int     `json:"decided"`
	Disagreed int     `json:"disagreed"`
}

type replayDisagreement struct {
	Time       time.Time `json:"time"`
	Tool       string    `json:"tool"`
	Supervisor string    `json:"supervisor"`
	Decider    string    `json:"decider"`
	Reason     string    `json:"reason"`
	Arguments  string    `json:"arguments"`
}

func buildReplayReport(cases []reviewCase, outcomes []replayOutcome, opts replayOpts) *replayReport {
	r := &replayReport{
		Agent:     opts.Agent,
		Since:     opts.Since,
		Reviews:   len(cases),
		Skipped:   map[string]int{},
		ApproveAt: opts.ApproveAt,
		DenyAt:    opts.DenyAt,
		Matrix:    map[string]map[string]int{},
	}
	for _, v := range replayVerdicts {
		r.Matrix[v] = map[string]int{}
	}
	approveAts, denyAts := sweepWith(replayApproveSweep, opts.ApproveAt), sweepWith(replayDenySweep, opts.DenyAt)
	r.ApproveSweep, r.DenySweep = sweepRows(approveAts), sweepRows(denyAts)

	var durations []time.Duration
	var disagreements []replayDisagreement
	for i, c := range cases {
		o := outcomes[i]
		if o.cause != "" {
			r.Skipped[o.cause]++
			continue
		}
		r.Replayed++
		r.CostUSD += o.cost
		durations = append(durations, o.duration)
		if !slices.ContainsFunc(c.recent, func(m agent.StoredMessage) bool { return m.Role == "user" }) {
			r.WithoutUserRequest++
		}

		verdict, reason := agent.SupervisorDeciderVerdict(o.answers, opts.ApproveAt, opts.DenyAt)
		r.Matrix[verdict][c.supervisor]++
		if replayDisagrees(verdict, c.supervisor) {
			disagreements = append(disagreements, replayDisagreement{
				Time: c.time, Tool: c.tool, Supervisor: c.supervisor, Decider: verdict, Reason: reason, Arguments: c.arguments,
			})
		}

		// One threshold at a time: the other is set out of reach.
		for j := range r.ApproveSweep {
			if v, _ := agent.SupervisorDeciderVerdict(o.answers, r.ApproveSweep[j].At, -1); v == verdictApprove {
				r.ApproveSweep[j].Decided++
				if c.supervisor != verdictApprove {
					r.ApproveSweep[j].Disagreed++
				}
			}
		}
		for j := range r.DenySweep {
			if v, _ := agent.SupervisorDeciderVerdict(o.answers, 2, r.DenySweep[j].At); v == verdictDeny {
				r.DenySweep[j].Decided++
				if c.supervisor == verdictApprove {
					r.DenySweep[j].Disagreed++
				}
			}
		}
	}

	r.LatencyP50Ms, r.LatencyP95Ms = percentileMs(durations, 50), percentileMs(durations, 95)
	sort.SliceStable(disagreements, func(a, b int) bool {
		return disagreementRank(disagreements[a]) < disagreementRank(disagreements[b])
	})
	r.DisagreementCount = len(disagreements)
	r.Disagreements = disagreements[:min(len(disagreements), max(opts.Show, 0))]
	return r
}

func replayDisagrees(decider, supervisor string) bool {
	switch decider {
	case verdictApprove:
		return supervisor != verdictApprove
	case verdictDeny:
		return supervisor == verdictApprove
	}
	return false
}

// disagreementRank lists the unsafe direction first: an approval the
// supervisor denied, then one it escalated, then denials it approved.
func disagreementRank(d replayDisagreement) int {
	switch {
	case d.Decider == verdictApprove && d.Supervisor == verdictDeny:
		return 0
	case d.Decider == verdictApprove:
		return 1
	}
	return 2
}

func sweepWith(grid []float64, chosen float64) []float64 {
	out := slices.Clone(grid)
	if !slices.Contains(out, chosen) {
		out = append(out, chosen)
	}
	slices.Sort(out)
	return out
}

func sweepRows(ats []float64) []replaySweepRow {
	rows := make([]replaySweepRow, len(ats))
	for i, at := range ats {
		rows[i].At = at
	}
	return rows
}

// percentileMs is the nearest-rank percentile, 0 for no samples.
func percentileMs(ds []time.Duration, p int) int64 {
	if len(ds) == 0 {
		return 0
	}
	sorted := slices.Clone(ds)
	slices.Sort(sorted)
	rank := (len(sorted)*p + 99) / 100
	return sorted[max(rank, 1)-1].Milliseconds()
}

func (r *replayReport) writeText(w io.Writer) error {
	if r.Reviews == 0 {
		_, err := fmt.Fprintf(w, "No supervisor reviews found for agent %q since %s.\n", r.Agent, r.Since.Format("2006-01-02"))
		return err
	}
	_, _ = fmt.Fprintf(w, "Replayed %d of %d supervisor reviews for agent %q through decider %q (%s), since %s\n",
		r.Replayed, r.Reviews, r.Agent, r.Decider, r.Model, r.Since.Format("2006-01-02"))
	if len(r.Skipped) > 0 {
		causes := make([]string, 0, len(r.Skipped))
		for cause, n := range r.Skipped {
			causes = append(causes, fmt.Sprintf("%s %d", cause, n))
		}
		sort.Strings(causes)
		_, _ = fmt.Fprintf(w, "Skipped: %s\n", strings.Join(causes, ", "))
	}
	_, _ = fmt.Fprintf(w, "Latency p50 %dms, p95 %dms. Cost $%.4f.\n", r.LatencyP50Ms, r.LatencyP95Ms, r.CostUSD)
	_, _ = fmt.Fprintf(w, "Replayed without tool descriptions or skill context; %d had no user request in history. Indicative only.\n", r.WithoutUserRequest)

	_, _ = fmt.Fprintf(w, "\nAgreement at approve_at=%g deny_at=%g\n", r.ApproveAt, r.DenyAt)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DECIDER\tSUP APPROVE\tSUP ESCALATE\tSUP DENY")
	for _, v := range replayVerdicts {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\n", strings.ToLower(v),
			r.Matrix[v][verdictApprove], r.Matrix[v][verdictEscalate], r.Matrix[v][verdictDeny])
	}
	_ = tw.Flush()

	_, _ = fmt.Fprintln(w, "\nThreshold sweep")
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "APPROVE_AT\tWOULD APPROVE\tSUPERVISOR DID NOT")
	for _, row := range r.ApproveSweep {
		_, _ = fmt.Fprintf(tw, "%.2f\t%d\t%d\n", row.At, row.Decided, row.Disagreed)
	}
	_ = tw.Flush()
	_, _ = fmt.Fprintln(w)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DENY_AT\tWOULD DENY\tSUPERVISOR APPROVED")
	for _, row := range r.DenySweep {
		_, _ = fmt.Fprintf(tw, "%.2f\t%d\t%d\n", row.At, row.Decided, row.Disagreed)
	}
	_ = tw.Flush()

	if r.DisagreementCount == 0 {
		_, err := fmt.Fprintln(w, "\nNo disagreements at these thresholds.")
		return err
	}
	_, _ = fmt.Fprintf(w, "\nDisagreements (%d of %d shown)\n", len(r.Disagreements), r.DisagreementCount)
	tw = tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME\tTOOL\tSUPERVISOR\tDECIDER\tREASON\tARGUMENTS")
	for _, d := range r.Disagreements {
		args := strings.Join(strings.Fields(d.Arguments), " ")
		if runes := []rune(args); len(runes) > replayArgsWidth {
			args = string(runes[:replayArgsWidth]) + "..."
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			d.Time.Local().Format("2006-01-02 15:04"), d.Tool, d.Supervisor, d.Decider,
			strings.TrimPrefix(d.Reason, "decider: "), args)
	}
	return tw.Flush()
}
