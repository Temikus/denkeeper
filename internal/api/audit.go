package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Temikus/denkeeper/internal/audit"
)

// handleListAudit handles GET /api/v1/audit.
func (s *Server) handleListAudit(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	q := r.URL.Query()
	opts := audit.ListOpts{
		Category: q.Get("category"),
		Agent:    q.Get("agent"),
		Status:   q.Get("status"),
		Source:   q.Get("source"),
		Search:   q.Get("search"),
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

// handleAuditStats handles GET /api/v1/audit/stats.
func (s *Server) handleAuditStats(w http.ResponseWriter, r *http.Request) {
	if s.deps.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "audit not configured"})
		return
	}

	var since *time.Time
	if v := r.URL.Query().Get("since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid since: must be RFC3339"})
			return
		}
		since = &t
	}

	stats, err := s.deps.AuditStore.Stats(r.Context(), since)
	if err != nil {
		s.logger.Error("getting audit stats", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
		return
	}

	writeJSON(w, http.StatusOK, stats)
}
