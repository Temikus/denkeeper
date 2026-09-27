package api

import (
	"errors"
	"net/http"
	"net/url"
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
// @Param detail_max_chars query integer false "Cut each event's detail to this many characters; cut details end with a marker carrying the original length. Omit for full detail."
// @Success 200 {object} audit.ListResult "Paginated list of audit events"
// @Failure 400 {object} map[string]string "Invalid query parameter (since, until, limit, offset, or detail_max_chars)"
// @Failure 500 {object} map[string]string "Internal server error"
// @Failure 503 {object} map[string]string "Audit not configured"
// @Router /audit [get]
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	opts, err := parseAuditListOpts(r.URL.Query())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
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

// parseAuditListOpts maps GET /audit query params to ListOpts. Errors are
// safe to return to the client verbatim.
func parseAuditListOpts(q url.Values) (audit.ListOpts, error) {
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
			return opts, errors.New("invalid since: must be RFC3339")
		}
		opts.Since = &t
	}
	if v := q.Get("until"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return opts, errors.New("invalid until: must be RFC3339")
		}
		opts.Until = &t
	}

	var err error
	if opts.Limit, err = queryIntMin(q, "limit", 1); err != nil {
		return opts, err
	}
	if opts.Offset, err = queryIntMin(q, "offset", 0); err != nil {
		return opts, err
	}
	if opts.DetailMaxChars, err = queryIntMin(q, "detail_max_chars", 1); err != nil {
		return opts, err
	}
	return opts, nil
}

// queryIntMin parses an optional integer param; absent yields 0.
func queryIntMin(q url.Values, key string, minVal int) (int, error) {
	v := q.Get(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minVal {
		return 0, errors.New("invalid " + key)
	}
	return n, nil
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
