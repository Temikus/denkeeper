package api

import (
	"encoding/json"
	"fmt"
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
	WebFetchMaxResponseChars int      `json:"web_fetch_max_response_chars"`
	ScriptEnabled            bool     `json:"script_enabled"`
	// TasksEnabled is the configured [tasks] enabled. The running process
	// changes only on restart; /health tasks_enabled reports what is running.
	TasksEnabled bool   `json:"tasks_enabled"`
	Version      string `json:"version"`
	Commit       string `json:"commit"`
	BuildDate    string `json:"build_date"`
	GoVersion    string `json:"go_version"`
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
	// WebFetchMaxResponseChars mirrors [web.fetch] max_response_chars; the
	// bounds match config.validateWeb so the API and TOML paths agree.
	WebFetchMaxResponseChars *int  `json:"web_fetch_max_response_chars,omitempty"`
	ScriptEnabled            *bool `json:"script_enabled,omitempty"`
	TasksEnabled             *bool `json:"tasks_enabled,omitempty"`
}

// Bounds for web_fetch_max_response_chars, kept in step with config.validateWeb.
const (
	minWebFetchMaxResponseChars = 1
	maxWebFetchMaxResponseChars = 100000
)

// handleGetServerConfig godoc
// @Summary      Get server configuration
// @Description  Returns the current server configuration including listen address, TLS, CORS, WebSocket settings, in-process tool settings (web/script), and build info.
// @Tags         server
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  serverConfigResponse
// @Failure      401  {object}  map[string]string  "Unauthorized"
// @Failure      403  {object}  map[string]string  "Forbidden — requires admin scope"
// @Router       /server/config [get]
func (s *Server) handleGetServerConfig(w http.ResponseWriter, _ *http.Request) {
	snap := s.appConfig()
	cfg := snap.API
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
		MCPServerEndpoint:        mcpServerEndpoint(snap),
		WebToolsEnabled:          snap.Web.WebEnabled(),
		WebFetchMaxResponseChars: snap.Web.Fetch.MaxResponseChars,
		ScriptEnabled:            snap.Script.ScriptEnabled(),
		TasksEnabled:             snap.Tasks.Enabled,
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
// @Description  Partially updates server configuration (external_url, timezone, MCP server, the in-process web_tools_enabled / script_enabled toggles, tasks_enabled, and web_fetch_max_response_chars). Changes are saved to the TOML config file in one write, then applied in memory; if the save fails, nothing changes and the call returns 500. Changing web_tools_enabled, script_enabled, tasks_enabled, or web_fetch_max_response_chars returns restart_required: true, since those features are wired, and their limits read, only at process start. web_fetch_max_response_chars must be between 1 and 100000.
// @Tags         server
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body  body      serverConfigUpdateInput  true  "Fields to update"
// @Success      200   {object}  map[string]string        "status: updated"
// @Failure      400   {object}  map[string]string        "Invalid JSON or validation error"
// @Failure      401   {object}  map[string]string        "Unauthorized"
// @Failure      403   {object}  map[string]string        "Forbidden — requires admin scope"
// @Failure      500   {object}  map[string]string        "Config file could not be saved; nothing was changed"
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

	s.serverConfigMu.Lock()
	defer s.serverConfigMu.Unlock()

	// Save first, so a failed write leaves the running config matching the file.
	if s.deps.ConfigPath != "" {
		if err := config.UpdateSections(s.deps.ConfigPath, serverConfigSections(&input)); err != nil {
			s.logger.Error("failed to save server config", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "config file could not be saved, nothing was changed: " + err.Error()})
			return
		}
	}

	var restartRequired bool
	s.deps.Config.Update(func(c *config.Config) {
		applyServerConfigInput(&c.API, &input)
		restartRequired = applyInProcessToolInput(c, &input)
	})

	resp := map[string]any{"status": "updated"}
	if restartRequired {
		// The [web]/[script] in-process MCP sessions are registered once at
		// agent-build time; the hot-reload path does not re-register them, so
		// the operator must restart the process for the toggle to take effect.
		resp["restart_required"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyInProcessToolInput applies the [web]/[script] settings to the in-memory
// config and reports whether any effective value changed. A change needs a
// process restart to (de)register the in-process MCP tools, since the
// hot-reload path does not re-run connectWebMCP/connectScriptMCP.
func applyInProcessToolInput(cfg *config.Config, input *serverConfigUpdateInput) bool {
	changed := false
	if input.WebToolsEnabled != nil {
		if cfg.Web.WebEnabled() != *input.WebToolsEnabled {
			changed = true
		}
		cfg.Web.Enabled = input.WebToolsEnabled
	}
	if input.WebFetchMaxResponseChars != nil {
		// The webmcp server reads this once at agent-build time, so a change
		// only reaches web_fetch after a restart — same as the toggles.
		if cfg.Web.Fetch.MaxResponseChars != *input.WebFetchMaxResponseChars {
			changed = true
		}
		cfg.Web.Fetch.MaxResponseChars = *input.WebFetchMaxResponseChars
	}
	if input.ScriptEnabled != nil {
		if cfg.Script.ScriptEnabled() != *input.ScriptEnabled {
			changed = true
		}
		cfg.Script.Enabled = input.ScriptEnabled
	}
	if input.TasksEnabled != nil {
		// main builds the task service, its tools and its routes at start.
		if cfg.Tasks.Enabled != *input.TasksEnabled {
			changed = true
		}
		cfg.Tasks.Enabled = *input.TasksEnabled
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
	if v := input.WebFetchMaxResponseChars; v != nil {
		if *v < minWebFetchMaxResponseChars || *v > maxWebFetchMaxResponseChars {
			return fmt.Sprintf("web_fetch_max_response_chars must be between %d and %d",
				minWebFetchMaxResponseChars, maxWebFetchMaxResponseChars)
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

// serverConfigSections maps a PATCH to the TOML tables it touches: [api]
// (with [api.mcp_server]), [web] (with [web.fetch]), [script] and [tasks].
func serverConfigSections(input *serverConfigUpdateInput) map[string]map[string]any {
	api := make(map[string]any)
	if input.ExternalURL != nil {
		api["external_url"] = *input.ExternalURL
	}
	if input.Timezone != nil {
		api["timezone"] = *input.Timezone
	}
	mcp := make(map[string]any)
	if input.MCPServerEnabled != nil {
		mcp["enabled"] = *input.MCPServerEnabled
	}
	if input.MCPServerTransport != nil {
		mcp["transport"] = *input.MCPServerTransport
	}
	if input.MCPServerSessionTimeout != nil {
		mcp["session_timeout"] = *input.MCPServerSessionTimeout
	}
	if input.MCPServerChatTimeout != nil {
		mcp["chat_timeout"] = *input.MCPServerChatTimeout
	}
	if input.MCPServerStateless != nil {
		mcp["stateless"] = *input.MCPServerStateless
	}
	if len(mcp) > 0 {
		api["mcp_server"] = mcp
	}

	web := make(map[string]any)
	if input.WebToolsEnabled != nil {
		web["enabled"] = *input.WebToolsEnabled
	}
	if input.WebFetchMaxResponseChars != nil {
		web["fetch"] = map[string]any{"max_response_chars": *input.WebFetchMaxResponseChars}
	}

	sections := make(map[string]map[string]any)
	for name, changes := range map[string]map[string]any{"api": api, "web": web} {
		if len(changes) > 0 {
			sections[name] = changes
		}
	}
	if input.ScriptEnabled != nil {
		sections["script"] = map[string]any{"enabled": *input.ScriptEnabled}
	}
	if input.TasksEnabled != nil {
		sections["tasks"] = map[string]any{"enabled": *input.TasksEnabled}
	}
	return sections
}

// mcpServerEndpoint takes the caller's snapshot rather than re-reading the
// holder: a response that pairs this endpoint with external_url or listen must
// derive both from one config, not from either side of a reload.
func mcpServerEndpoint(cfg *config.Config) string {
	base := cfg.API.ExternalURL
	if base == "" {
		base = "http://" + cfg.API.Listen
	}
	return base + "/api/v1/mcp"
}
