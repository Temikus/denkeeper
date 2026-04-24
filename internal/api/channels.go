package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/tool"
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

// channelCreateInput is the JSON body for POST /api/v1/channels and
// PATCH /api/v1/channels/{name}.
type channelCreateInput struct {
	Name        string   `json:"name"`
	Agent       string   `json:"agent"`
	Adapters    []string `json:"adapters"`
	Delivery    string   `json:"delivery"`
	SessionMode string   `json:"session_mode"`
}

// validateChannelDelivery returns an error message if delivery is invalid.
func validateChannelDelivery(delivery string) string {
	if delivery != "" && delivery != "single" && delivery != "broadcast" {
		return "delivery must be 'single' or 'broadcast'"
	}
	return ""
}

// validateChannelSessionMode returns an error message if session_mode is invalid.
func validateChannelSessionMode(mode string) string {
	if mode != "" && mode != "persistent" && mode != "ephemeral" {
		return "session_mode must be 'persistent' or 'ephemeral'"
	}
	return ""
}

// channelToResponse converts a Channel to its API response representation.
func (s *Server) channelToResponse(ch *agent.Channel) channelResponse {
	return channelResponse{
		Name:              ch.Name,
		Agent:             ch.AgentName,
		Adapters:          ch.Adapters,
		Delivery:          ch.Delivery,
		SessionMode:       ch.SessionMode,
		ConversationID:    ch.ConversationID(),
		ActiveAdapterKeys: s.deps.Dispatcher.ActiveChannelsForChannel(ch.Name),
	}
}

// handleCreateChannel handles POST /api/v1/channels.
func (s *Server) handleCreateChannel(w http.ResponseWriter, r *http.Request) {
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return
	}

	var input channelCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	input.Name = strings.TrimSpace(input.Name)

	if msg := s.validateChannelCreate(input); msg != "" {
		var code int
		switch msg {
		case "agent not found":
			code = http.StatusNotFound
		case "channel already exists":
			code = http.StatusConflict
		default:
			code = http.StatusBadRequest
		}
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	ch := &agent.Channel{
		Name:        input.Name,
		AgentName:   input.Agent,
		Adapters:    input.Adapters,
		Delivery:    input.Delivery,
		SessionMode: input.SessionMode,
	}

	if err := s.deps.Dispatcher.AddChannel(ch); err != nil {
		if errors.Is(err, agent.ErrChannelsNotConfigured) {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "channels not configured; add at least one channel first"})
			return
		}
		s.logger.Error("adding channel to dispatcher", "error", err, "channel", input.Name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to add channel"})
		return
	}

	if err := tool.AddChannelToConfig(s.deps.ConfigPath, input.Name, input.Agent, input.Delivery, input.SessionMode, input.Adapters); err != nil {
		s.logger.Error("persisting channel to config", "error", err, "channel", input.Name)
	}

	writeJSON(w, http.StatusCreated, s.channelToResponse(ch))
}

// validateChannelCreate validates create-specific constraints and returns a
// non-empty error message if validation fails.
func (s *Server) validateChannelCreate(input channelCreateInput) string {
	if input.Name == "" {
		return "name is required"
	}
	if input.Agent == "" {
		return "agent is required"
	}
	if s.deps.Dispatcher.Agent(input.Agent) == nil {
		return "agent not found"
	}
	if msg := validateChannelDelivery(input.Delivery); msg != "" {
		return msg
	}
	if msg := validateChannelSessionMode(input.SessionMode); msg != "" {
		return msg
	}
	if channels := s.deps.Dispatcher.Channels(); channels != nil {
		if _, exists := channels[input.Name]; exists {
			return "channel already exists"
		}
	}
	return ""
}

// handleUpdateChannel handles PATCH /api/v1/channels/{name}.
func (s *Server) handleUpdateChannel(w http.ResponseWriter, r *http.Request) {
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return
	}

	name := r.PathValue("name")
	existing, ok := s.requireExplicitChannel(w, name)
	if !ok {
		return
	}

	var input channelCreateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}

	merged := s.mergeChannelUpdate(name, existing, input)

	if msg, code := s.validateChannelMerged(merged); msg != "" {
		writeJSON(w, code, map[string]string{"error": msg})
		return
	}

	if err := s.deps.Dispatcher.UpdateChannel(name, merged); err != nil {
		s.logger.Error("updating channel in dispatcher", "error", err, "channel", name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to update channel"})
		return
	}

	if err := tool.UpdateChannelInConfig(s.deps.ConfigPath, name, merged.AgentName, merged.Delivery, merged.SessionMode, merged.Adapters); err != nil {
		s.logger.Error("persisting channel update to config", "error", err, "channel", name)
	}

	writeJSON(w, http.StatusOK, s.channelToResponse(merged))
}

// requireExplicitChannel looks up a channel by name and returns it if it
// exists and is not implicit. Writes the appropriate error response and
// returns false on failure.
func (s *Server) requireExplicitChannel(w http.ResponseWriter, name string) (*agent.Channel, bool) {
	channels := s.deps.Dispatcher.Channels()
	if channels == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return nil, false
	}
	ch, ok := channels[name]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "channel not found"})
		return nil, false
	}
	if ch.Implicit {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cannot modify implicit channel"})
		return nil, false
	}
	return ch, true
}

// mergeChannelUpdate applies non-zero fields from input onto existing.
func (s *Server) mergeChannelUpdate(name string, existing *agent.Channel, input channelCreateInput) *agent.Channel {
	merged := &agent.Channel{
		Name:        name,
		AgentName:   existing.AgentName,
		Adapters:    existing.Adapters,
		Delivery:    existing.Delivery,
		SessionMode: existing.SessionMode,
	}
	if input.Agent != "" {
		merged.AgentName = input.Agent
	}
	if input.Adapters != nil {
		merged.Adapters = input.Adapters
	}
	if input.Delivery != "" {
		merged.Delivery = input.Delivery
	}
	if input.SessionMode != "" {
		merged.SessionMode = input.SessionMode
	}
	return merged
}

// validateChannelMerged validates a merged channel and returns an error
// message and HTTP status code if invalid.
func (s *Server) validateChannelMerged(ch *agent.Channel) (string, int) {
	if s.deps.Dispatcher.Agent(ch.AgentName) == nil {
		return "agent not found", http.StatusNotFound
	}
	if msg := validateChannelDelivery(ch.Delivery); msg != "" {
		return msg, http.StatusBadRequest
	}
	if msg := validateChannelSessionMode(ch.SessionMode); msg != "" {
		return msg, http.StatusBadRequest
	}
	return "", 0
}

// handleDeleteChannel handles DELETE /api/v1/channels/{name}.
func (s *Server) handleDeleteChannel(w http.ResponseWriter, r *http.Request) {
	if s.deps.ConfigPath == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config persistence not available"})
		return
	}

	name := r.PathValue("name")
	if _, ok := s.requireExplicitChannel(w, name); !ok {
		return
	}

	if err := s.deps.Dispatcher.RemoveChannel(r.Context(), name); err != nil {
		s.logger.Error("removing channel from dispatcher", "error", err, "channel", name)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete channel"})
		return
	}

	if err := tool.RemoveChannelFromConfig(s.deps.ConfigPath, name); err != nil {
		s.logger.Error("persisting channel removal to config", "error", err, "channel", name)
	}

	w.WriteHeader(http.StatusNoContent)
}
