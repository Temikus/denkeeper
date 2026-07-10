package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"time"

	"github.com/Temikus/denkeeper/internal/config"
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
	MCPServerEnabled         bool     `json:"mcp_server_enabled"`
	MCPServerTransport       string   `json:"mcp_server_transport"`
	MCPServerSessionTimeout  string   `json:"mcp_server_session_timeout"`
	MCPServerChatTimeout     string   `json:"mcp_server_chat_timeout"`
	MCPServerStateless       bool     `json:"mcp_server_stateless"`
	MCPServerEndpoint        string   `json:"mcp_server_endpoint"`
	WebToolsEnabled          bool     `json:"web_tools_enabled"`
	ScriptEnabled            bool     `json:"script_enabled"`
	Version                  string   `json:"version"`
	Commit                   string   `json:"commit"`
	BuildDate                string   `json:"build_date"`
	GoVersion                string   `json:"go_version"`
}

// serverConfigUpdateInput holds the mutable fields for PATCH /api/v1/server/config.
type serverConfigUpdateInput struct {
	ExternalURL             *string `json:"external_url,omitempty"`
	Timezone                *string `json:"timezone,omitempty"`
	MCPServerEnabled        *bool   `json:"mcp_server_enabled,omitempty"`
	MCPServerTransport      *string `json:"mcp_server_transport,omitempty"`
	MCPServerSessionTimeout *string `json:"mcp_server_session_timeout,omitempty"`
	MCPServerChatTimeout    *string `json:"mcp_server_chat_timeout,omitempty"`
	MCPServerStateless      *bool   `json:"mcp_server_stateless,omitempty"`
	WebToolsEnabled         *bool   `json:"web_tools_enabled,omitempty"`
	ScriptEnabled           *bool   `json:"script_enabled,omitempty"`
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
		MCPServerEnabled:         cfg.IsMCPServerEnabled(),
		MCPServerTransport:       cfg.MCPServer.Transport,
		MCPServerSessionTimeout:  cfg.MCPServer.SessionTimeout,
		MCPServerChatTimeout:     cfg.MCPServer.ChatTimeout,
		MCPServerStateless:       cfg.MCPServer.Stateless,
		MCPServerEndpoint:        s.mcpServerEndpoint(),
		WebToolsEnabled:          s.deps.Config.Web.WebEnabled(),
		ScriptEnabled:            s.deps.Config.Script.ScriptEnabled(),
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
// @Description  Partially updates server configuration (external_url, timezone, MCP server, and the in-process web_tools_enabled / script_enabled toggles). Changes are applied in-memory and persisted to the TOML config file. Toggling web_tools_enabled or script_enabled returns restart_required: true, since the in-process MCP tools are (de)registered only at process start.
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

	if errMsg := validateServerConfigInput(&input); errMsg != "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": errMsg})
		return
	}

	applyServerConfigInput(&s.deps.Config.API, &input)
	restartRequired := applyInProcessToolInput(s.deps.Config, &input)
	s.persistServerConfig(&input)

	resp := map[string]any{"status": "updated"}
	if restartRequired {
		// The [web]/[script] in-process MCP sessions are registered once at
		// agent-build time; the hot-reload path does not re-register them, so
		// the operator must restart the process for the toggle to take effect.
		resp["restart_required"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyInProcessToolInput applies the [web]/[script] enabled toggles to the
// in-memory config and reports whether either effective value changed. A change
// needs a process restart to (de)register the in-process MCP tools, since the
// hot-reload path does not re-run connectWebMCP/connectScriptMCP.
func applyInProcessToolInput(cfg *config.Config, input *serverConfigUpdateInput) bool {
	changed := false
	if input.WebToolsEnabled != nil {
		if cfg.Web.WebEnabled() != *input.WebToolsEnabled {
			changed = true
		}
		cfg.Web.Enabled = input.WebToolsEnabled
	}
	if input.ScriptEnabled != nil {
		if cfg.Script.ScriptEnabled() != *input.ScriptEnabled {
			changed = true
		}
		cfg.Script.Enabled = input.ScriptEnabled
	}
	return changed
}

func validateServerConfigInput(input *serverConfigUpdateInput) string {
	if input.ExternalURL != nil && *input.ExternalURL != "" {
		u, err := url.Parse(*input.ExternalURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return "invalid external_url: must be a valid URL with scheme and host"
		}
	}
	if input.Timezone != nil && *input.Timezone != "" {
		if _, err := time.LoadLocation(*input.Timezone); err != nil {
			return "invalid timezone: must be a valid IANA timezone name (e.g. America/New_York)"
		}
	}
	return validateMCPServerInput(input)
}

func validateMCPServerInput(input *serverConfigUpdateInput) string {
	if input.MCPServerTransport != nil {
		t := *input.MCPServerTransport
		if t != "streamable" && t != "sse" {
			return "mcp_server_transport must be 'streamable' or 'sse'"
		}
	}
	if msg := validateOptionalDuration(input.MCPServerSessionTimeout, "mcp_server_session_timeout"); msg != "" {
		return msg
	}
	return validateOptionalDuration(input.MCPServerChatTimeout, "mcp_server_chat_timeout")
}

func validateOptionalDuration(val *string, name string) string {
	if val == nil || *val == "" {
		return ""
	}
	if _, err := time.ParseDuration(*val); err != nil {
		return "invalid " + name + ": " + err.Error()
	}
	return ""
}

func applyServerConfigInput(cfg *config.APIConfig, input *serverConfigUpdateInput) {
	if input.ExternalURL != nil {
		cfg.ExternalURL = *input.ExternalURL
	}
	if input.Timezone != nil {
		cfg.Timezone = *input.Timezone
	}
	if input.MCPServerEnabled != nil {
		cfg.MCPServer.Enabled = input.MCPServerEnabled
	}
	if input.MCPServerTransport != nil {
		cfg.MCPServer.Transport = *input.MCPServerTransport
	}
	if input.MCPServerSessionTimeout != nil {
		cfg.MCPServer.SessionTimeout = *input.MCPServerSessionTimeout
	}
	if input.MCPServerChatTimeout != nil {
		cfg.MCPServer.ChatTimeout = *input.MCPServerChatTimeout
	}
	if input.MCPServerStateless != nil {
		cfg.MCPServer.Stateless = *input.MCPServerStateless
	}
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

	s.persistInProcessToolConfig(input)

	changes := make(map[string]any)
	if input.ExternalURL != nil {
		changes["external_url"] = *input.ExternalURL
	}
	if input.Timezone != nil {
		changes["timezone"] = *input.Timezone
	}

	mcpChanges := make(map[string]any)
	if input.MCPServerEnabled != nil {
		mcpChanges["enabled"] = *input.MCPServerEnabled
	}
	if input.MCPServerTransport != nil {
		mcpChanges["transport"] = *input.MCPServerTransport
	}
	if input.MCPServerSessionTimeout != nil {
		mcpChanges["session_timeout"] = *input.MCPServerSessionTimeout
	}
	if input.MCPServerChatTimeout != nil {
		mcpChanges["chat_timeout"] = *input.MCPServerChatTimeout
	}
	if input.MCPServerStateless != nil {
		mcpChanges["stateless"] = *input.MCPServerStateless
	}
	if len(mcpChanges) > 0 {
		changes["mcp_server"] = mcpChanges
	}

	if len(changes) > 0 {
		if err := config.UpdateAPIConfig(s.deps.ConfigPath, changes); err != nil {
			s.logger.Warn("failed to persist server config", "error", err)
		}
	}
}

// persistInProcessToolConfig writes the [web]/[script] enabled toggles to their
// top-level TOML sections. Persisted separately from the [api] changes because
// they live in distinct tables.
func (s *Server) persistInProcessToolConfig(input *serverConfigUpdateInput) {
	if input.WebToolsEnabled != nil {
		if err := config.UpdateWebConfig(s.deps.ConfigPath, map[string]any{"enabled": *input.WebToolsEnabled}); err != nil {
			s.logger.Warn("failed to persist web config", "error", err)
		}
	}
	if input.ScriptEnabled != nil {
		if err := config.UpdateScriptConfig(s.deps.ConfigPath, map[string]any{"enabled": *input.ScriptEnabled}); err != nil {
			s.logger.Warn("failed to persist script config", "error", err)
		}
	}
}

func (s *Server) mcpServerEndpoint() string {
	base := s.deps.Config.API.ExternalURL
	if base == "" {
		base = "http://" + s.deps.Config.API.Listen
	}
	return base + "/api/v1/mcp"
}
