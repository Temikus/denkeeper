package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
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
	Version                  string   `json:"version"`
	Commit                   string   `json:"commit"`
	BuildDate                string   `json:"build_date"`
	GoVersion                string   `json:"go_version"`
}

// serverConfigUpdateInput holds the mutable fields for PATCH /api/v1/server/config.
type serverConfigUpdateInput struct {
	ExternalURL *string `json:"external_url,omitempty"`
	Timezone    *string `json:"timezone,omitempty"`
}

// handleGetServerConfig godoc
// @Summary      Get server configuration
// @Description  Returns the current server configuration including listen address, TLS, CORS, WebSocket settings, and build info.
// @Tags         server
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  serverConfigResponse
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Router       /server/config [get]
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
		Version:                  s.deps.Version,
		Commit:                   s.deps.Commit,
		BuildDate:                s.deps.BuildDate,
		GoVersion:                runtime.Version(),
	}
	if resp.CORSOrigins == nil {
		resp.CORSOrigins = []string{}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handlePatchServerConfig godoc
// @Summary      Update server configuration
// @Description  Partially updates server configuration (external_url, timezone). Changes are applied in-memory and persisted to the TOML config file.
// @Tags         server
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      serverConfigUpdateInput  true  "Fields to update"
// @Success      200   {object}  map[string]string        "status: updated"
// @Failure      400   {object}  map[string]string        "Invalid JSON or validation error"
// @Failure      401   {object}  map[string]string        "Unauthorized"
// @Failure      403   {object}  map[string]string        "Forbidden — requires admin scope"
// @Router       /server/config [patch]
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

// handleReloadConfig godoc
// @Summary      Reload configuration from disk
// @Description  Re-reads the TOML configuration file and applies changes. Returns an error if no reload function is configured or the reload fails.
// @Tags         server
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]string  "status: reloaded"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Failure      500  {object}  map[string]string  "Reload failed"
// @Failure      503  {object}  map[string]string  "Config reload not available"
// @Router       /server/reload [post]
func (s *Server) handleReloadConfig(w http.ResponseWriter, _ *http.Request) {
	if s.deps.ReloadFunc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "config reload not available"})
		return
	}
	s.logger.Info("config reload requested via API")
	if err := s.deps.ReloadFunc(); err != nil {
		s.logger.Error("config reload failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reload failed: " + err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "reloaded"})
}

// handleRestartProcess godoc
// @Summary      Restart the server process
// @Description  Triggers a graceful server process restart. The response is sent before the restart occurs. Returns an error if no restart function is configured.
// @Tags         server
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  map[string]string  "status: restarting"
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Failure      503  {object}  map[string]string  "Process restart not available"
// @Router       /server/restart [post]
func (s *Server) handleRestartProcess(w http.ResponseWriter, _ *http.Request) {
	if s.deps.RestartFunc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "process restart not available"})
		return
	}
	s.logger.Info("process restart requested via API")
	writeJSON(w, http.StatusOK, map[string]string{"status": "restarting"})

	// Send the signal after writing the response so the client gets the 200.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := s.deps.RestartFunc(); err != nil {
			s.logger.Error("process restart failed", "error", err)
		}
	}()
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
