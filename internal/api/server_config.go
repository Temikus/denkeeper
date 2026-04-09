package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/Temikus/denkeeper/internal/tool"
)

// serverConfigResponse is the safe-to-expose subset of APIConfig.
type serverConfigResponse struct {
	ExternalURL              string   `json:"external_url"`
	Timezone                 string   `json:"timezone"`
	Listen                   string   `json:"listen"`
	TLS                      bool     `json:"tls"`
	CORSOrigins              []string `json:"cors_origins"`
	RateLimit                float64  `json:"rate_limit"`
	WebSocketEnabled         bool     `json:"websocket_enabled"`
	WebSocketMaxConnections  int      `json:"websocket_max_connections"`
	WebSocketReplayBufferTTL string   `json:"websocket_replay_buffer_ttl"`
}

// serverConfigUpdateInput holds the mutable fields for PATCH /api/v1/server/config.
type serverConfigUpdateInput struct {
	ExternalURL *string `json:"external_url,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

func (s *Server) handleGetServerConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := s.deps.Config.API
	resp := serverConfigResponse{
		ExternalURL:              cfg.ExternalURL,
		Timezone:                 cfg.Timezone,
		Listen:                   cfg.Listen,
		TLS:                      cfg.TLS,
		CORSOrigins:              cfg.CORSOrigins,
		RateLimit:                cfg.RateLimit,
		WebSocketEnabled:         cfg.IsWebSocketEnabled(),
		WebSocketMaxConnections:  cfg.WebSocketMaxConnections,
		WebSocketReplayBufferTTL: cfg.WebSocketReplayBufferTTL,
	}
	if resp.CORSOrigins == nil {
		resp.CORSOrigins = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePatchServerConfig(w http.ResponseWriter, r *http.Request) {
	var input serverConfigUpdateInput
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}

	if input.ExternalURL != nil && *input.ExternalURL != "" {
		u, err := url.Parse(*input.ExternalURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid external_url: must be a valid URL with scheme and host",
			})
			return
		}
	}

	if input.Timezone != nil && *input.Timezone != "" {
		if _, err := time.LoadLocation(*input.Timezone); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid timezone: must be a valid IANA timezone name (e.g. America/New_York)",
			})
			return
		}
	}

	// Apply to in-memory config.
	if input.ExternalURL != nil {
		s.deps.Config.API.ExternalURL = *input.ExternalURL
	}
	if input.Timezone != nil {
		s.deps.Config.API.Timezone = *input.Timezone
	}

	// Persist to TOML.
	s.persistServerConfig(&input)

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

func (s *Server) persistServerConfig(input *serverConfigUpdateInput) {
	if s.deps.ConfigPath == "" {
		return
	}

	changes := make(map[string]any)
	if input.ExternalURL != nil {
		changes["external_url"] = *input.ExternalURL
	}
	if input.Timezone != nil {
		changes["timezone"] = *input.Timezone
	}
	if len(changes) > 0 {
		if err := tool.UpdateAPIConfig(s.deps.ConfigPath, changes); err != nil {
			s.logger.Warn("failed to persist server config", "error", err)
		}
	}
}
