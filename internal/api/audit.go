package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
)

// handleListAudit godoc
// @Summary List audit events
// @Description Returns a paginated list of audit log events with optional filtering by category, agent, status, source, time range, and free-text search.
// @Tags audit
// @Produce json
// @Security BearerAuth
// @Param category query string false "Filter by event category (e.g. tool_call, skill, channel, approval). Comma-separated or repeated for a union of several categories."
// @Param agent query string false "Filter by agent name"
// @Param status query string false "Filter by status (ok, error, pending, denied). Comma-separated or repeated for a union of several statuses."
// @Param source query string false "Filter by event source"
// @Param exclude_source query string false "Drop events with these sources (e.g. eval,dryrun). Comma-separated or repeated."
// @Param search query string false "Free-text search across event fields"
// @Param since query string false "Start of time range (RFC3339 format)"
// @Param until query string false "End of time range (RFC3339 format)"
// @Param limit query integer false "Maximum number of events to return"
// @Param offset query integer false "Number of events to skip for pagination"
// @Success 200 {object} audit.ListResult "Paginated list of audit events"
// @Failure 400 {object} map[string]string "Invalid query parameter (since, until, limit, or offset)"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /audit [get]
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	q := r.URL.Query()
	opts := audit.ListOpts{
		Categories:     audit.ParseFilterList(q["category"]...),
		Agent:          q.Get("agent"),
		Statuses:       audit.ParseFilterList(q["status"]...),
		Source:         q.Get("source"),
		ExcludeSources: audit.ParseFilterList(q["exclude_source"]...),
		Search:         q.Get("search"),
	}

	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since: must be RFC3339"})
			return
		}
		opts.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid until: must be RFC3339"})
			return
		}
		opts.Until = &t
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid limit"})
			return
		}
		opts.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid offset"})
			return
		}
		opts.Offset = n
	}

	events, total, err := s.deps.AuditStore.List(r.Context(), opts)
	if err != nil {
		s.logger.Error("listing audit events", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	if events == nil {
		events = []audit.Event{}
	}

	writeJSON(w, http.StatusOK, audit.ListResult{
		Events: events,
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
}

// handleAuditStats godoc
// @Summary Get audit statistics
// @Description Returns aggregate counts of audit events grouped by category and status, plus events in the last hour. Optionally filtered by a start time.
// @Tags audit
// @Produce json
// @Security BearerAuth
// @Param since query string false "Only count events after this time (RFC3339 format)"
// @Param exclude_source query string false "Drop events with these sources (e.g. eval,dryrun). Comma-separated or repeated."
// @Success 200 {object} audit.Stats "Aggregate audit statistics"
// @Failure 400 {object} map[string]string "Invalid since parameter"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /audit/stats [get]
func (s *Server) handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	q := r.URL.Query()
	opts := audit.StatsOpts{ExcludeSources: audit.ParseFilterList(q["exclude_source"]...)}
	if v := q.Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since: must be RFC3339"})
			return
		}
		opts.Since = &t
	}

	stats, err := s.deps.AuditStore.Stats(r.Context(), opts)
	if err != nil {
		s.logger.Error("getting audit stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
