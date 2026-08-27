package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/eval"
)

// Turn inspector endpoints (L1, design/eval-subsystem.md §4.2). They sit
// behind sessions:read rather than eval:read: a trace is turn content — the
// same class of data as GET /sessions/{id}/tool-calls — and the eval scopes
// exist for a judge, which must never be able to read live prompts.

// A trace's tool calls reuse dryRunToolCall: same fields, same JSON names, and
// the dashboard renders both through DryRunTranscript. One renderer, one shape.

// traceDetail is one trace with its payload unpacked.
type traceDetail struct {
	eval.TraceRow
	SystemPrompt string               `json:"system_prompt"`
	History      []agent.TraceMessage `json:"history"`
	Prompt       string               `json:"prompt"`
	Response     string               `json:"response"`
	ToolCalls    []dryRunToolCall     `json:"tool_calls"`
	SuppressedN  int                  `json:"suppressed_count"`
	// DurationMs mirrors latency_ms under the name the transcript renderer
	// reads, so the mapping lives here rather than in three UI call sites.
	DurationMs int64 `json:"duration_ms"`
	// Truncation says what the byte cap removed, so a trimmed trace is never
	// mistaken for a short turn.
	Truncation *agent.TraceTruncation `json:"truncation,omitempty"`
}

// traceListResult is the list response. It carries the capture switch so the
// page can tell "nothing recorded yet" from "recording is off", which is the
// difference between waiting and changing a config file.
type traceListResult struct {
	Traces        []eval.TraceRow `json:"traces"`
	Total         int             `json:"total"`
	Limit         int             `json:"limit"`
	Offset        int             `json:"offset"`
	Capture       bool            `json:"capture"`
	RetentionDays int             `json:"retention_days"`
	MaxTraceBytes int             `json:"max_trace_bytes"`
}

// traceSettings is the snapshot of the [eval] knobs the trace listing echoes.
// The handlers read it instead of deps.Config.Eval: hot reload overwrites the
// whole config struct in place, so a request-time read of the struct would
// race that write.
type traceSettings struct {
	capture       bool
	retentionDays int
	maxTraceBytes int
}

// refreshTraceSettings re-reads the [eval] knobs into the snapshot the trace
// handlers serve. Called at construction and after each successful config
// reload — both places where reading deps.Config cannot race the reload's
// whole-struct overwrite.
func (s *Server) refreshTraceSettings() {
	ts := &traceSettings{}
	if s.deps.Config != nil {
		ts.capture = s.deps.Config.Eval.Capture
		ts.retentionDays = s.deps.Config.Eval.RetentionDays
		ts.maxTraceBytes = s.deps.Config.Eval.TraceBytesCap()
	}
	s.traceCfg.Store(ts)
}

// traceStoreRequired writes a 503 when trace storage is not wired. The runner
// is deliberately not required: traces outlive any eval run and a live turn
// never touches it.
func (s *Server) traceStoreRequired(w http.ResponseWriter) bool {
	if s.deps.EvalStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "turn traces not configured"})
		return false
	}
	return true
}

// handleListTraces godoc
// @Summary List captured turn traces
// @Description Returns turn trace headers, newest first, without their payloads. A trace records what a turn actually saw — the built system prompt, the history window as sent, the per-round tool calls with arguments and results, and the final response. Live turns are captured only when [eval] capture is on (off by default, since a trace holds everything the model saw); eval samples are always captured. The response repeats the capture switch and the retention window so a caller can tell "nothing recorded yet" from "recording is off".
// @Tags traces
// @Produce json
// @Security BearerAuth
// @Param agent query string false "Filter by agent name"
// @Param conversation_id query string false "Filter by conversation id"
// @Param source query string false "Filter by source (live, eval)"
// @Param since query string false "Start of time range (RFC3339 format)"
// @Param until query string false "End of time range (RFC3339 format)"
// @Param limit query integer false "Maximum number of traces to return (default 50, max 200)"
// @Param offset query integer false "Number of traces to skip for pagination"
// @Success 200 {object} traceListResult "Trace headers plus the capture settings"
// @Failure 400 {object} map[string]string "Invalid query parameter (since, until, limit, or offset)"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Turn traces not configured"
// @Router /traces [get]
func (s *Server) handleListTraces(w http.ResponseWriter, r *http.Request) {
	if !s.traceStoreRequired(w) {
		return
	}
	filter, ok := traceFilterFrom(w, r)
	if !ok {
		return
	}

	// Echo what the store will actually use, not what the caller asked for: a
	// pager that trusts a clamped limit skips rows.
	filter.Limit = eval.BoundTraceLimit(filter.Limit)

	rows, err := s.deps.EvalStore.ListTraces(r.Context(), filter)
	if err != nil {
		s.logger.Error("listing turn traces", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	// Counted through the same filter, so "load more" stops when the filtered
	// set runs out rather than chasing rows another filter would have shown.
	total, err := s.deps.EvalStore.CountTraces(r.Context(), filter)
	if err != nil {
		s.logger.Error("counting turn traces", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	out := traceListResult{
		Traces: rows,
		Total:  total,
		Limit:  filter.Limit,
		Offset: filter.Offset,
	}
	if ts := s.traceCfg.Load(); ts != nil {
		out.Capture = ts.capture
		out.RetentionDays = ts.retentionDays
		out.MaxTraceBytes = ts.maxTraceBytes
	}
	writeJSON(w, http.StatusOK, out)
}

// traceFilterFrom parses the listing query, writing a 400 and reporting false
// on a malformed one.
func traceFilterFrom(w http.ResponseWriter, r *http.Request) (eval.TraceFilter, bool) {
	q := r.URL.Query()
	f := eval.TraceFilter{
		Agent:          q.Get("agent"),
		ConversationID: q.Get("conversation_id"),
		Source:         q.Get("source"),
	}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since: must be RFC3339"})
			return f, false
		}
		f.Since = t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid until: must be RFC3339"})
			return f, false
		}
		f.Until = t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return f, false
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid offset"})
			return f, false
		}
		f.Offset = n
	}
	return f, true
}

// handleGetTrace godoc
// @Summary Get one turn trace with its payload
// @Description Returns the full trace: the built system prompt as it was assembled post-skill-injection, the history window as it went on the wire, every tool call with its arguments and the result the model read, and the final response. When the trace exceeded [eval] max_trace_bytes it also reports what was dropped — oldest rounds first — so a trimmed trace is not mistaken for a short turn.
// @Tags traces
// @Produce json
// @Security BearerAuth
// @Param id path int true "Trace id"
// @Success 200 {object} traceDetail "Turn trace"
// @Failure 400 {object} map[string]string "Bad trace id"
// @Failure 404 {object} map[string]string "Trace not found"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Turn traces not configured"
// @Router /traces/{id} [get]
func (s *Server) handleGetTrace(w http.ResponseWriter, r *http.Request) {
	if !s.traceStoreRequired(w) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid trace id"})
		return
	}

	row, payload, err := s.deps.EvalStore.GetTrace(r.Context(), id)
	if err != nil {
		if errors.Is(err, eval.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		s.logger.Error("getting turn trace", "id", id, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}
	writeJSON(w, http.StatusOK, buildTraceDetail(row, payload))
}

// buildTraceDetail unpacks a stored trace into the response shape.
func buildTraceDetail(row *eval.TraceRow, payload agent.TracePayload) traceDetail {
	d := traceDetail{
		TraceRow:     *row,
		SystemPrompt: payload.SystemPrompt,
		History:      payload.History,
		Prompt:       payload.Prompt,
		Response:     payload.Response,
		ToolCalls:    make([]dryRunToolCall, 0, 4),
		DurationMs:   row.LatencyMs,
		Truncation:   payload.Truncation,
	}
	if d.History == nil {
		d.History = []agent.TraceMessage{}
	}
	for _, round := range payload.Rounds {
		for _, call := range round.ToolCalls {
			tc := dryRunToolCall{
				Tool:       call.Tool,
				Server:     call.Server,
				Round:      round.Round,
				Outcome:    call.Outcome,
				Suppressed: call.Outcome == "suppressed",
				DurationMs: call.DurationMs,
				Arguments:  call.Arguments,
				Result:     call.Result,
				Error:      call.Error,
			}
			if tc.Suppressed {
				d.SuppressedN++
			}
			d.ToolCalls = append(d.ToolCalls, tc)
		}
	}
	return d
}
