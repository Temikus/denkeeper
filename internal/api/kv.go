package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// kvRequired writes 503 when the KV store is not configured.
func (s *Server) kvRequired(w http.ResponseWriter) bool {
	if s.deps.KVStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "KV store not configured"})
		return false
	}
	return true
}

// handleListKV godoc
// @Summary List KV entries for an agent
// @Description Returns all key-value entries for the specified agent, optionally filtered by a key prefix.
// @Tags kv
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param prefix query string false "Filter keys by prefix"
// @Success 200 {object} object "Object with entries array containing key, value, created_at, updated_at, and optional expires_at"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Failure 503 {object} map[string]string "KV store not configured"
// @Router /kv/{agent} [get]
func (s *Server) handleListKV(w http.ResponseWriter, r *http.Request) {
	if !s.kvRequired(w) {
		return
	}

	agentName := r.PathValue("agent")
	if s.deps.Dispatcher.Agent(agentName) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	prefix := r.URL.Query().Get("prefix")
	entries, err := s.deps.KVStore.List(r.Context(), agentName, prefix)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("listing keys: %v", err)})
		return
	}

	type entryJSON struct {
		Key       string     `json:"key"`
		Value     string     `json:"value"`
		CreatedAt time.Time  `json:"created_at"`
		UpdatedAt time.Time  `json:"updated_at"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
	}

	result := make([]entryJSON, len(entries))
	for i, e := range entries {
		result[i] = entryJSON{
			Key:       e.Key,
			Value:     e.Value,
			CreatedAt: e.CreatedAt,
			UpdatedAt: e.UpdatedAt,
			ExpiresAt: e.ExpiresAt,
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{"entries": result})
}

// handleGetKV godoc
// @Summary Get a KV entry
// @Description Returns the value of a single key for the specified agent.
// @Tags kv
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param key path string true "Key name"
// @Success 200 {object} object "Object with key and value fields"
// @Failure 404 {object} map[string]string "Agent or key not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Failure 503 {object} map[string]string "KV store not configured"
// @Router /kv/{agent}/{key} [get]
func (s *Server) handleGetKV(w http.ResponseWriter, r *http.Request) {
	if !s.kvRequired(w) {
		return
	}

	agentName := r.PathValue("agent")
	key := r.PathValue("key")

	if s.deps.Dispatcher.Agent(agentName) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	value, ok, err := s.deps.KVStore.Get(r.Context(), agentName, key)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("getting key: %v", err)})
		return
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("key %q not found", key)})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"key":   key,
		"value": value,
	})
}

// handleSetKV godoc
// @Summary Set a KV entry
// @Description Creates or updates a key-value pair for the specified agent. Optionally accepts a TTL duration string (e.g. "1h", "30m").
// @Tags kv
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param key path string true "Key name"
// @Param body body object true "Value and optional TTL" Example({"value": "hello", "ttl": "1h"})
// @Success 200 {object} object "Object with key and value fields"
// @Failure 400 {object} map[string]string "Invalid JSON body or TTL"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Failure 503 {object} map[string]string "KV store not configured"
// @Router /kv/{agent}/{key} [put]
func (s *Server) handleSetKV(w http.ResponseWriter, r *http.Request) {
	if !s.kvRequired(w) {
		return
	}

	agentName := r.PathValue("agent")
	key := r.PathValue("key")

	if s.deps.Dispatcher.Agent(agentName) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	var body struct {
		Value string `json:"value"`
		TTL   string `json:"ttl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return
	}

	var ttl time.Duration
	if body.TTL != "" {
		var err error
		ttl, err = time.ParseDuration(body.TTL)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid ttl %q: %v", body.TTL, err)})
			return
		}
		if ttl < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "ttl must be non-negative"})
			return
		}
	}

	if err := s.deps.KVStore.Set(r.Context(), agentName, key, body.Value, ttl); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("setting key: %v", err)})
		return
	}

	s.logger.Info("kv key set via API", "agent", agentName, "key", key)
	writeJSON(w, http.StatusOK, map[string]any{
		"key":   key,
		"value": body.Value,
	})
}

// handleDeleteKV godoc
// @Summary Delete a KV entry
// @Description Removes a single key-value pair for the specified agent.
// @Tags kv
// @Security BearerAuth
// @Param agent path string true "Agent name"
// @Param key path string true "Key name"
// @Success 204 "Key deleted"
// @Failure 404 {object} map[string]string "Agent not found"
// @Failure 500 {object} map[string]string "Internal error"
// @Failure 503 {object} map[string]string "KV store not configured"
// @Router /kv/{agent}/{key} [delete]
func (s *Server) handleDeleteKV(w http.ResponseWriter, r *http.Request) {
	if !s.kvRequired(w) {
		return
	}

	agentName := r.PathValue("agent")
	key := r.PathValue("key")

	if s.deps.Dispatcher.Agent(agentName) == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("agent %q not found", agentName)})
		return
	}

	if err := s.deps.KVStore.Delete(r.Context(), agentName, key); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("deleting key: %v", err)})
		return
	}

	s.logger.Info("kv key deleted via API", "agent", agentName, "key", key)
	w.WriteHeader(http.StatusNoContent)
}
