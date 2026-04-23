package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
)

// channelResponse is the JSON representation of a channel in API responses.
type channelResponse struct {
	Name              string   `json:"name"`
	Agent             string   `json:"agent"`
	Adapters          []string `json:"adapters"`
	Delivery          string   `json:"delivery,omitempty"`
	Implicit          bool     `json:"implicit"`
	SessionMode       string   `json:"session_mode,omitempty"`
	ConversationID    string   `json:"conversation_id"`
	ActiveAdapterKeys []string `json:"active_adapter_keys"`
}

// handleListChannels handles GET /api/v1/channels.
func (s *Server) handleListChannels(w http.ResponseWriter, r *http.Request) {
	channels := s.deps.Dispatcher.Channels()
	if channels == nil {
		writeJSON(w, http.StatusOK, []channelResponse{})
		return
	}

	result := make([]channelResponse, 0, len(channels))
	for _, ch := range channels {
		result = append(result, channelResponse{
			Name:              ch.Name,
			Agent:             ch.AgentName,
			Adapters:          ch.Adapters,
			Delivery:          ch.Delivery,
			Implicit:          ch.Implicit,
			SessionMode:       ch.SessionMode,
			ConversationID:    ch.ConversationID(),
			ActiveAdapterKeys: s.deps.Dispatcher.ActiveChannelsForChannel(ch.Name),
		})
	}

	writeJSON(w, http.StatusOK, result)
}

// handleGetChannel handles GET /api/v1/channels/{name}.
func (s *Server) handleGetChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	channels := s.deps.Dispatcher.Channels()
	if channels == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}

	ch, ok := channels[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}

	writeJSON(w, http.StatusOK, channelResponse{
		Name:              ch.Name,
		Agent:             ch.AgentName,
		Adapters:          ch.Adapters,
		Delivery:          ch.Delivery,
		Implicit:          ch.Implicit,
		SessionMode:       ch.SessionMode,
		ConversationID:    ch.ConversationID(),
		ActiveAdapterKeys: s.deps.Dispatcher.ActiveChannelsForChannel(ch.Name),
	})
}

// activateRequest is the JSON body for POST/DELETE /api/v1/channels/{name}/activate.
type activateRequest struct {
	AdapterKey string `json:"adapter_key"` // e.g. "telegram:12345"
}

// handleActivateChannel handles POST /api/v1/channels/{name}/activate.
func (s *Server) handleActivateChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	var req activateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.AdapterKey == "" || !strings.Contains(req.AdapterKey, ":") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "adapter_key must be in format 'adapter:externalID'"})
		return
	}

	if err := s.deps.Dispatcher.SetActiveChannelByKey(r.Context(), req.AdapterKey, name); err != nil {
		if errors.Is(err, agent.ErrChannelNotFound) || errors.Is(err, agent.ErrChannelsNotConfigured) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		s.logger.Error("activating channel", "error", err, "channel", name, "adapter_key", req.AdapterKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to activate channel"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":      "activated",
		"channel":     name,
		"adapter_key": req.AdapterKey,
	})
}

// handleDeactivateChannel handles DELETE /api/v1/channels/{name}/activate.
func (s *Server) handleDeactivateChannel(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")

	// Verify channel exists.
	channels := s.deps.Dispatcher.Channels()
	if channels == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}
	if _, ok := channels[name]; !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return
	}

	var req activateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if req.AdapterKey == "" || !strings.Contains(req.AdapterKey, ":") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "adapter_key must be in format 'adapter:externalID'"})
		return
	}

	if err := s.deps.Dispatcher.ClearActiveChannelByKey(r.Context(), req.AdapterKey, name); err != nil {
		if errors.Is(err, agent.ErrAdapterKeyNotActive) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "adapter key is not active on this channel"})
			return
		}
		s.logger.Error("deactivating channel", "error", err, "channel", name, "adapter_key", req.AdapterKey)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to deactivate channel"})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deactivated"})
}
