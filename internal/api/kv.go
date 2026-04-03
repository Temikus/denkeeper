package api

import (
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

// handleListKV returns all keys for an agent, optionally filtered by prefix.
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

// handleGetKV returns the value of a single key.
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

// handleDeleteKV removes a single key.
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
