package config

import (
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/scope"
)

type Config struct {
	DataDir   string                  `toml:"data_dir"` // base directory for all data; defaults to ~/.denkeeper
	Telegram  TelegramConfig          `toml:"telegram"`
	Discord   DiscordConfig           `toml:"discord"`
	LLM       LLMConfig               `toml:"llm"`
	Memory    MemoryConfig            `toml:"memory"`
	Log       LogConfig               `toml:"log"`
	Agent     AgentConfig             `toml:"agent"`
	Session   SessionConfig           `toml:"session"`
	Agents    []AgentInstanceConfig   `toml:"agents"`
	Channels  []ChannelConfig         `toml:"channels"`
	Schedules []ScheduleConfig        `toml:"schedules"`
	Tools     map[string]ToolConfig   `toml:"tools"`
	MaxTools  int                     `toml:"max_tools"` // combined limit for tools + plugins; 0 = default (50)
	Plugins   map[string]PluginConfig `toml:"plugins"`
	Voice     VoiceConfig             `toml:"voice"`
	API       APIConfig               `toml:"api"`
	Security  SecurityConfig          `toml:"security"`
	KV        KVConfig                `toml:"kv"`
	Sandbox   SandboxConfig           `toml:"sandbox"`
	Web       WebConfig               `toml:"web"`
	Browser   BrowserConfig           `toml:"browser"`
	OTel      OTelConfig              `toml:"otel"`
	MCP       MCPConfig               `toml:"mcp"`
	Costs     CostsConfig             `toml:"costs"`
	Audit     AuditConfig             `toml:"audit"`

	// ToolWarnings holds per-tool validation errors that were demoted to
	// warnings (the tool is auto-disabled instead of blocking startup).
	ToolWarnings map[string]string `toml:"-"`
}

// CostsConfig controls the pricing registry and cost calculation.
type CostsConfig struct {
	// DefaultRatePerKTokens is the fallback rate (USD per 1K tokens) used when
	// the model is not found in the bundled registry or operator overrides.
	// Set to 0 to record $0.00 and emit a warning. Default: 0.
	DefaultRatePerKTokens float64 `toml:"default_rate_per_1k_tokens"`
	// ModelPrices allows operators to override or add model pricing.
	// Keys are model name prefixes; values are [input, output, cached_input]
	// rates per million tokens.
	ModelPrices map[string]ModelPriceConfig `toml:"model_prices"`
}

// ModelPriceConfig holds per-million-token pricing for a model override.
type ModelPriceConfig struct {
	InputPerMTok       float64 `toml:"input"        json:"input"`
	OutputPerMTok      float64 `toml:"output"       json:"output"`
	CachedInputPerMTok float64 `toml:"cached_input" json:"cached_input"`
}

// OTelConfig controls OpenTelemetry observability instrumentation.
type OTelConfig struct {
	// Enabled activates OTel metric collection and Prometheus /metrics endpoint. Default: false.
	Enabled bool `toml:"enabled"`
	// TracesEndpoint is the OTLP HTTP endpoint for trace export (e.g. "localhost:4318").
	// When empty, tracing is disabled even if Enabled is true.
	TracesEndpoint string `toml:"traces_endpoint"`
	// ServiceName is the OTel service name. Default: "denkeeper".
	ServiceName string `toml:"service_name"`
}

// AuditConfig controls the audit log subsystem.
type AuditConfig struct {
	// Enabled controls whether audit logging is active. Default: true.
	Enabled *bool `toml:"enabled"`
	// RetentionDays is how long audit events are kept. 0 = unlimited. Default: 30.
	RetentionDays int `toml:"retention_days"`
	// CleanupInterval is how often retention is enforced (e.g. "1h"). Default: "1h".
	CleanupInterval string `toml:"cleanup_interval"`
	// BufferSize is the capacity of the in-memory event buffer. Default: 1000.
	BufferSize int `toml:"buffer_size"`
}

// AuditEnabled returns whether audit logging is enabled (default: true).
func (c *AuditConfig) AuditEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// WebConfig controls built-in web search and URL fetching tools.
type WebConfig struct {
	// Enabled controls whether web tools are available to agents. Default: true.
	// Use a pointer so that an omitted field can be distinguished from an
	// explicit false, allowing applyDefaults to set the value to true when
	// unspecified.
	Enabled *bool           `toml:"enabled"`
	Search  WebSearchConfig `toml:"search"`
	Fetch   WebFetchConfig  `toml:"fetch"`
}

// WebSearchConfig configures the web search provider.
type WebSearchConfig struct {
	// Provider selects the search backend: "duckduckgo" (default) or "tavily".
	Provider string `toml:"provider"`
	// APIKey is required for providers that need authentication (e.g. Tavily).
	APIKey string `toml:"api_key"`
	// MaxResults is the default number of search results to return. Default: 5.
	MaxResults int `toml:"max_results"`
}

// WebFetchConfig configures URL fetching and content extraction.
type WebFetchConfig struct {
	// Timeout is the HTTP request timeout as a Go duration string. Default: "30s".
	Timeout string `toml:"timeout"`
	// MaxSizeBytes limits the response body size. Default: 5242880 (5MB).
	MaxSizeBytes int64 `toml:"max_size_bytes"`
	// UserAgent is the HTTP User-Agent header. Default: "Denkeeper/1.0 (+https://denkeeper.io)".
	UserAgent string `toml:"user_agent"`
	// RespectRobotsTxt checks robots.txt before fetching. Default: false.
	RespectRobotsTxt bool `toml:"respect_robots_txt"`
	// RespectAgentsTxt checks agents.txt before fetching. Default: false.
	RespectAgentsTxt bool `toml:"respect_agents_txt"`
	// Jina configures optional Jina Reader integration for JS-heavy pages.
	Jina JinaFetchConfig `toml:"jina"`
}

// JinaFetchConfig configures the optional Jina Reader enhanced fetcher.
type JinaFetchConfig struct {
	// Enabled activates Jina Reader as a fallback for JS-heavy pages. Default: false.
	Enabled bool `toml:"enabled"`
}

// BrowserConfig controls the Playwright-based browser automation Docker plugin.
type BrowserConfig struct {
	// Enabled controls whether browser automation is available. Default: false.
	Enabled bool `toml:"enabled"`
	// Image is the Docker/OCI image for the browser plugin. Default: "ghcr.io/temikus/denkeeper-browser:latest".
	Image string `toml:"image"`
	// MemoryLimit is the container memory limit. Default: "512m".
	MemoryLimit string `toml:"memory_limit"`
	// CPULimit is the container CPU limit. Default: "1".
	CPULimit string `toml:"cpu_limit"`
	// ProfileDir is the base directory for per-agent browser profiles, relative to the data directory.
	// Default: "data/browser-profiles".
	ProfileDir string `toml:"profile_dir"`
	// SessionTTL is the duration after which an idle browser session is closed. Default: "10m".
	SessionTTL string `toml:"session_ttl"`
	// MaxPages is the maximum number of concurrent pages per agent. Default: 5.
	MaxPages int `toml:"max_pages"`
	// URLAllowlist restricts which domains the browser can navigate to.
	// Empty list means unrestricted. Supports wildcards: "*.example.com".
	URLAllowlist BrowserURLAllowlist `toml:"url_allowlist"`
}

// BrowserURLAllowlist defines domain restrictions for browser navigation.
type BrowserURLAllowlist struct {
	// Domains is the list of allowed domains. Empty = unrestricted.
	// Supports wildcards: "*.example.com" matches all subdomains.
	Domains []string `toml:"domains"`
}

// SandboxConfig selects the runtime backend for sandboxed (Docker-type) plugins.
type SandboxConfig struct {
	// Runtime selects the sandbox backend: "docker" (default) or "kubernetes".
	Runtime string `toml:"runtime"`
	// Kubernetes holds Kubernetes-specific sandbox settings.
	Kubernetes KubernetesSandboxConfig `toml:"kubernetes"`
}

// KubernetesSandboxConfig configures the Kubernetes sandbox runtime backend.
type KubernetesSandboxConfig struct {
	// Namespace is the Kubernetes namespace for sandbox Pods. Default: "denkeeper-sandboxes".
	Namespace string `toml:"namespace"`
	// Kubeconfig is the path to a kubeconfig file. Empty uses in-cluster config.
	Kubeconfig string `toml:"kubeconfig"`
	// RuntimeClass is the Kubernetes RuntimeClassName for sandbox Pods (e.g. "gvisor", "kata").
	RuntimeClass string `toml:"runtime_class"`
}

// SecurityConfig controls plugin signature verification.
type SecurityConfig struct {
	// TrustedKeys is a list of file paths to PEM-encoded Ed25519 public keys.
	// Plugins signed by any of these keys are trusted.
	TrustedKeys []string `toml:"trusted_keys"`
	// AllowUnsigned controls whether unsigned plugins are allowed.
	// Defaults to true. Set to false to require all plugins to be signed.
	AllowUnsigned *bool `toml:"allow_unsigned"`
}

// DiscordConfig configures the Discord bot adapter.
type DiscordConfig struct {
	// Token is the Discord bot token. Required to enable the Discord adapter.
	Token string `toml:"token"`
	// AllowedUsers is a list of Discord user snowflake IDs (as strings) that
	// may interact with the bot. Required when token is set.
	AllowedUsers []string `toml:"allowed_users"`
}

// APIConfig controls the external REST API server.
type APIConfig struct {
	// Enabled controls whether the API server starts. Default: true.
	// Use a pointer so that an omitted field can be distinguished from an
	// explicit false, allowing applyDefaults to set the value to true when
	// unspecified.
	Enabled *bool `toml:"enabled"`

	// Listen is the address to listen on (e.g. "0.0.0.0:8443", ":8080").
	Listen string `toml:"listen"`

	// TLS enables HTTPS. When true, CertFile and KeyFile are required.
	TLS      bool   `toml:"tls"`
	CertFile string `toml:"cert_file"`
	KeyFile  string `toml:"key_file"`

	// CORS configures allowed origins for cross-origin requests.
	// Empty means no CORS headers are sent.
	CORSOrigins []string `toml:"cors_origins"`

	// RateLimit is the maximum requests per second per API key. 0 = unlimited.
	RateLimit float64 `toml:"rate_limit"`

	// Keys defines API keys with scoped permissions.
	Keys []APIKeyConfig `toml:"keys"`

	// Auth configures optional password and OIDC authentication for the dashboard.
	Auth APIAuthConfig `toml:"auth"`

	// LoginRateLimit is the maximum number of password login attempts per IP
	// within LoginRateWindow before requests are rejected with 429.
	// Default: 5. Set to 0 to disable login rate limiting.
	LoginRateLimit *int `toml:"login_rate_limit"`

	// LoginRateWindow is the duration (Go duration string) of the login rate
	// limit window. Default: "15m".
	LoginRateWindow string `toml:"login_rate_window"`

	// WebSocketEnabled controls whether the WebSocket endpoint (GET /api/v1/ws)
	// is available. Default: true. Use a pointer so omitted = true.
	WebSocketEnabled *bool `toml:"websocket_enabled"`

	// WebSocketMaxConnections is the maximum number of concurrent WebSocket
	// connections. 0 = unlimited.
	WebSocketMaxConnections int `toml:"websocket_max_connections"`

	// WebSocketReplayBufferTTL is how long to buffer events for replay after
	// a client disconnects. Parsed as time.Duration. Default: "5m".
	WebSocketReplayBufferTTL string `toml:"websocket_replay_buffer_ttl"`

	// ExternalURL is the publicly-reachable base URL for this instance.
	// Used for constructing OAuth callback URLs for remote MCP tool authorization.
	// If empty, defaults to http(s)://<listen>.
	ExternalURL string `toml:"external_url"`

	// Timezone is the IANA timezone name used for evaluating cron schedule
	// expressions (e.g. "America/New_York", "Europe/London"). Default: "UTC".
	// Changes take effect after restart.
	Timezone string `toml:"timezone"`

	// OnboardingDismissed hides the onboarding checklist on the Overview page.
	// Set automatically via POST /api/v1/onboarding/dismiss.
	OnboardingDismissed bool `toml:"onboarding_dismissed"`

	// WizardCompleted indicates the post-auth setup wizard has been completed
	// (or skipped). Set via POST /api/v1/onboarding/wizard-complete.
	WizardCompleted bool `toml:"wizard_completed"`

	// MCPServer configures the MCP server endpoint that allows external MCP
	// clients (Claude Code, other AI tools) to interact with Denkeeper agents.
	MCPServer APIMCPServerConfig `toml:"mcp_server"`
}

// APIMCPServerConfig controls the MCP server endpoint exposed at /api/v1/mcp.
type APIMCPServerConfig struct {
	// Enabled controls whether the MCP server endpoint is active. Default: false (opt-in).
	Enabled *bool `toml:"enabled"`

	// SessionTimeout is the idle session cleanup duration (Go duration string). Default: "30m".
	SessionTimeout string `toml:"session_timeout"`

	// Transport selects the MCP transport: "streamable" (default) or "sse" (legacy).
	Transport string `toml:"transport"`

	// ChatTimeout is the maximum time for a single chat tool call (Go duration string). Default: "2m".
	ChatTimeout string `toml:"chat_timeout"`

	// Stateless disables session tracking when true. Default: false.
	Stateless bool `toml:"stateless"`
}

// IsEnabled returns whether the API server should start. After applyDefaults
// the pointer is always non-nil, but this method is safe to call at any stage.
func (a *APIConfig) IsEnabled() bool {
	return a.Enabled == nil || *a.Enabled
}

// IsWebSocketEnabled returns whether the WebSocket endpoint should be
// registered. After applyDefaults the pointer is always non-nil.
func (a *APIConfig) IsWebSocketEnabled() bool {
	return a.WebSocketEnabled == nil || *a.WebSocketEnabled
}

// IsMCPServerEnabled returns whether the MCP server endpoint should be active.
// Defaults to false (opt-in) when the pointer is nil.
func (a *APIConfig) IsMCPServerEnabled() bool {
	return a.MCPServer.Enabled != nil && *a.MCPServer.Enabled
}

// MCPServerSessionTimeout parses and returns the MCP session timeout duration.
// Returns 30m if the value is empty or unparseable.
func (a *APIConfig) MCPServerSessionTimeout() time.Duration {
	if a.MCPServer.SessionTimeout == "" {
		return 30 * time.Minute
	}
	d, err := time.ParseDuration(a.MCPServer.SessionTimeout)
	if err != nil {
		return 30 * time.Minute
	}
	return d
}

// MCPServerChatTimeout parses and returns the MCP chat tool timeout duration.
// Returns 2m if the value is empty or unparseable.
func (a *APIConfig) MCPServerChatTimeout() time.Duration {
	if a.MCPServer.ChatTimeout == "" {
		return 2 * time.Minute
	}
	d, err := time.ParseDuration(a.MCPServer.ChatTimeout)
	if err != nil {
		return 2 * time.Minute
	}
	return d
}

// GetLoginRateLimit returns the configured login rate limit, defaulting to 5.
// A value of 0 means unlimited (rate limiting disabled).
func (a *APIConfig) GetLoginRateLimit() int {
	if a.LoginRateLimit == nil {
		return 5
	}
	return *a.LoginRateLimit
}

// GetLoginRateWindow parses and returns the login rate limit window duration.
// Returns 15m if the value is empty or unparseable.
func (a *APIConfig) GetLoginRateWindow() time.Duration {
	if a.LoginRateWindow == "" {
		return 15 * time.Minute
	}
	d, err := time.ParseDuration(a.LoginRateWindow)
	if err != nil {
		return 15 * time.Minute
	}
	return d
}

// WebSocketReplayTTL parses and returns the replay buffer TTL duration.
// Returns 5m if the value is empty or unparseable.
func (a *APIConfig) WebSocketReplayTTL() time.Duration {
	if a.WebSocketReplayBufferTTL == "" {
		return 5 * time.Minute
	}
	d, err := time.ParseDuration(a.WebSocketReplayBufferTTL)
	if err != nil {
		return 5 * time.Minute
	}
	return d
}

// APIAuthConfig configures password and OIDC authentication.
type APIAuthConfig struct {
	// PasswordHash is a bcrypt hash of the dashboard password. Generated via `denkeeper passwd`.
	PasswordHash string `toml:"password_hash"`
	// SessionSecret is a hex-encoded AES key (≥32 bytes) for encrypting session cookies.
	// Required when either password or OIDC auth is configured.
	SessionSecret string `toml:"session_secret"`
	// SessionMaxAge is the session cookie lifetime as a Go duration string. Default: "24h".
	SessionMaxAge string `toml:"session_max_age"`
	// PreferredLoginMethod controls which login method is shown first on the login page.
	// Values: "auto" (default), "password", "apikey".
	PreferredLoginMethod string `toml:"preferred_login_method"`
	// SessionRecordRetention is how long to keep session records after expiry. Default: "720h" (30 days).
	SessionRecordRetention string `toml:"session_record_retention"`
	// OIDC configures optional OpenID Connect SSO.
	OIDC OIDCConfig `toml:"oidc"`
}

// OIDCConfig configures the OpenID Connect SSO provider.
type OIDCConfig struct {
	// Enabled activates OIDC login.
	Enabled bool `toml:"enabled"`
	// Issuer is the OIDC discovery URL (e.g. "https://accounts.google.com").
	Issuer string `toml:"issuer"`
	// ClientID is the OAuth2 client ID.
	ClientID string `toml:"client_id"`
	// ClientSecret is the OAuth2 client secret.
	ClientSecret string `toml:"client_secret"`
	// RedirectURL is the callback URL (e.g. "https://myserver/auth/callback").
	RedirectURL string `toml:"redirect_url"`
	// Scopes requested from the OIDC provider. Default: ["openid", "email", "profile"].
	Scopes []string `toml:"scopes"`
	// AllowedEmails restricts login to these email addresses (case-insensitive).
	// Required when OIDC is enabled.
	AllowedEmails []string `toml:"allowed_emails"`
}

// APIKeyConfig defines a single API key with named scopes.
type APIKeyConfig struct {
	// Name is a human-readable label for this key.
	Name string `toml:"name"`

	// Key is the secret API key value. Loaded from config or env.
	Key string `toml:"key"`

	// Scopes controls what this key can access.
	// Valid scopes: "chat", "sessions:read", "sessions:write", "costs:read", "skills:read",
	// "skills:write", "schedules:read", "schedules:write", "approvals:read",
	// "approvals:write", "tools:read", "tools:write", "browser:read",
	// "browser:write", "health", "admin".
	Scopes []string `toml:"scopes"`
}

// AgentInstanceConfig defines a named agent with its own persona, skills,
// LLM model, permission tier, and adapter bindings. Multiple agents can
// run within a single denkeeper instance.
type AgentInstanceConfig struct {
	// Name is a unique identifier for this agent. One agent must be named "default".
	Name string `toml:"name"`

	// Description is a human-readable summary of the agent's purpose.
	Description string `toml:"description"`

	// PersonaDir is the path to the agent's persona directory (SOUL.md, USER.md, MEMORY.md).
	PersonaDir string `toml:"persona_dir"`

	// SkillsDir overrides the global skills directory for this agent. If empty,
	// the global skills directory is used. Agent-specific skills in
	// <persona_dir>/skills/ are always loaded and override global skills by name.
	SkillsDir string `toml:"skills_dir"`

	// Adapters lists the adapter bindings for this agent.
	// "telegram" — wildcard: all messages on that adapter go to this agent.
	// "telegram:12345" — specific: only messages from that chat ID.
	Adapters []string `toml:"adapters"`

	// LLMProvider overrides the global default_provider for this agent.
	// Must match a registered provider name (e.g. "anthropic", "openrouter", "openai", "ollama").
	LLMProvider string `toml:"llm_provider"`

	// LLMModel overrides the global default_model for this agent.
	LLMModel string `toml:"llm_model"`

	// SessionTier overrides the global session.tier for this agent.
	SessionTier string `toml:"session_tier"`

	// BrowserURLAllowlist overrides the global browser URL allowlist for this agent.
	// If set, only these domains are reachable. Supports wildcards: "*.example.com".
	BrowserURLAllowlist []string `toml:"browser_url_allowlist"`

	// CostLimitSoft overrides the global cost_limit_soft for this agent.
	// Nil means inherit global. 0 means disabled.
	CostLimitSoft *float64 `toml:"cost_limit_soft"`

	// CostLimitHard overrides the global cost_limit_hard for this agent.
	// Nil means inherit global. 0 means disabled.
	CostLimitHard *float64 `toml:"cost_limit_hard"`

	// MaxContextMessages limits the number of conversation messages included in
	// the LLM context window. When a conversation exceeds this limit, only the
	// most recent N messages are sent. 0 means use the default (50).
	MaxContextMessages int `toml:"max_context_messages"`

	// MaxToolRounds limits the number of tool-call rounds per message.
	// 0 means use the default (50). The REST API requires >= 1; the zero
	// default sentinel is only valid in TOML config (at startup).
	MaxToolRounds int `toml:"max_tool_rounds"`

	// Fallbacks overrides the global [[llm.fallback]] rules for this agent.
	// When non-empty, these rules replace (not merge with) the global fallbacks.
	Fallbacks []FallbackConfig `toml:"fallback"`

	// Supervisor names another agent that reviews tool calls before execution.
	// Only meaningful when session_tier = "supervised". The supervisor agent
	// must exist, must not itself require supervision (no cycles), and should
	// use session_tier = "autonomous".
	Supervisor string `toml:"supervisor"`

	// SupervisorTimeout overrides the default timeout (30s) for the
	// supervisor's LLM review call. On timeout, escalates to human.
	SupervisorTimeout string `toml:"supervisor_timeout"`

	// SupervisorContextMessages overrides the default number (5) of recent
	// conversation messages included in the supervisor's review prompt.
	SupervisorContextMessages int `toml:"supervisor_context_messages"`

	// SupervisorBodyExcerptLen overrides the max characters of skill body
	// included in the supervisor review prompt (default 500; 0 = use default).
	SupervisorBodyExcerptLen int `toml:"supervisor_body_excerpt_len"`

	// SupervisorToolDescLen overrides the max characters of the MCP tool
	// description included in the supervisor review prompt (default 200;
	// 0 = use default).
	SupervisorToolDescLen int `toml:"supervisor_tool_desc_len"`

	// ReviewerModel is the LLM model used for post-turn reviews. If empty,
	// post-turn review is disabled for this agent.
	ReviewerModel string `toml:"reviewer_model"`

	// ReviewerProvider is the LLM provider for the reviewer. If empty,
	// inherits the agent's own llm_provider.
	ReviewerProvider string `toml:"reviewer_provider"`

	// ReviewMaxIterations limits the reviewer's tool-call rounds. 0 means
	// use the default (6).
	ReviewMaxIterations int `toml:"review_max_iterations"`

	// ReviewTimeout is the maximum duration for a review pass (e.g. "2m").
	// 0 or empty means use the default (2m).
	ReviewTimeout string `toml:"review_timeout"`

	// NudgeMemoryInterval is the number of user turns between automatic
	// memory review nudges. 0 means disabled.
	NudgeMemoryInterval int `toml:"nudge_memory_interval"`

	// NudgeSkillInterval is the number of tool-call rounds between automatic
	// skill review nudges. 0 means disabled.
	NudgeSkillInterval int `toml:"nudge_skill_interval"`
}

// ChannelConfig defines a named routing endpoint that binds adapter chats to an
// agent with explicit session identity. Channels decouple conversations from the
// rigid 1:1 agent-adapter binding, enabling session switching and cross-adapter
// session sharing.
type ChannelConfig struct {
	// Name is a unique identifier for this channel.
	Name string `toml:"name"`

	// Agent is the name of the agent that handles messages on this channel.
	Agent string `toml:"agent"`

	// Adapters lists the adapter bindings for this channel, using the same
	// format as AgentInstanceConfig.Adapters: "telegram" (wildcard) or
	// "telegram:12345" (specific). Multiple bindings enable cross-adapter
	// session sharing. An empty list means the channel is reachable only via
	// /session command or the API.
	Adapters []string `toml:"adapters"`

	// Delivery controls how scheduled messages are delivered through this
	// channel's adapter bindings. "single" (default) picks the first specific
	// binding; "broadcast" delivers through all specific bindings.
	Delivery string `toml:"delivery"`

	// SessionMode controls conversation persistence: "persistent" (default)
	// maintains a single conversation per channel; "ephemeral" creates a
	// fresh conversation for each interaction.
	SessionMode string `toml:"session_mode"`

	// Implicit is true when the channel was auto-synthesized from an agent's
	// adapter bindings (backward compatibility). Not set via TOML — only
	// populated by synthesizeChannels(). Implicit channels are hidden from
	// /session listings.
	Implicit bool `toml:"-"`
}

// VoiceConfig controls speech-to-text and text-to-speech.
type VoiceConfig struct {
	STTProvider    string            `toml:"stt_provider"`     // "openai" or "" (disabled)
	TTSProvider    string            `toml:"tts_provider"`     // "openai" or "" (disabled)
	TTSVoice       string            `toml:"tts_voice"`        // e.g. "alloy"
	AutoVoiceReply bool              `toml:"auto_voice_reply"` // reply with voice when user sends voice
	OpenAI         VoiceOpenAIConfig `toml:"openai"`
}

type VoiceOpenAIConfig struct {
	APIKey string `toml:"api_key"`
}

// MCPConfig holds global MCP settings that apply to all tool servers.
type MCPConfig struct {
	// RequestTimeoutSecs is the default per-request timeout for individual MCP calls
	// (applied via context deadline, not http.Client.Timeout). Default: 30.
	RequestTimeoutSecs int `toml:"request_timeout_secs"`
	// SSEKeepAliveSecs is the TCP keepalive interval for SSE connections. Default: 15.
	SSEKeepAliveSecs int `toml:"sse_keep_alive_secs"`
	// AutoRestart enables automatic restart of crashed MCP servers. Default: true.
	AutoRestart *bool `toml:"auto_restart"`
	// MaxRestartAttempts is the maximum number of consecutive restart attempts before
	// a server is disabled. Default: 3.
	MaxRestartAttempts int `toml:"max_restart_attempts"`
	// RestartCooldown is the duration a server must stay connected before its
	// consecutive failure counter resets (e.g. "5m"). Default: "5m".
	RestartCooldown string `toml:"restart_cooldown"`
	// InitRetryAttempts is the number of times to retry the initial connection
	// to a remote MCP server at startup before giving up. Applies only to
	// sse/http transports. Default: 5. Set to 1 to disable retries.
	InitRetryAttempts int `toml:"init_retry_attempts"`
	// InitRetryBackoff is the base backoff between initial-connection retries
	// (e.g. "2s"). Each attempt doubles the delay up to 30s. Default: "2s".
	InitRetryBackoff string `toml:"init_retry_backoff"`
	// URLAllowlist restricts which hosts SSE tool servers may connect to.
	// Supports wildcards (e.g. "*.internal.corp"). Empty = all non-blocked hosts allowed.
	URLAllowlist []string `toml:"url_allowlist"`
}

// ToolConfig defines an MCP tool server to spawn.
type ToolConfig struct {
	Enabled            *bool             `toml:"enabled"` // nil = true (default); explicit false = disabled
	Command            string            `toml:"command"`
	Args               []string          `toml:"args"`
	Env                map[string]string `toml:"env"`
	Transport          string            `toml:"transport"`            // "stdio" (default) or "sse"
	URL                string            `toml:"url"`                  // required for sse transport
	Headers            map[string]string `toml:"headers"`              // optional HTTP headers for sse
	RequestTimeoutSecs int               `toml:"request_timeout_secs"` // per-server override (0 = use global)
	SSEKeepAliveSecs   int               `toml:"sse_keep_alive_secs"`  // per-server override (0 = use global)

	// OAuth fields — only valid when Transport is "sse".
	Auth         string   `toml:"auth"`          // "" (none) or "oauth"
	ClientID     string   `toml:"client_id"`     // pre-registered OAuth client ID (optional)
	ClientSecret string   `toml:"client_secret"` // pre-registered OAuth client secret (optional)
	Scopes       []string `toml:"scopes"`        // OAuth scopes to request (optional)

	// Tool filtering — MCP tool names to exclude from the LLM tool payload.
	DisabledTools []string `toml:"disabled_tools"`

	// Unsafe options.
	AllowLoopback bool `toml:"allow_loopback"` // bypass SSRF loopback block (localhost/127.x/::1)
}

// IsEnabled returns whether the tool server is enabled.
func (tc ToolConfig) IsEnabled() bool {
	return tc.Enabled == nil || *tc.Enabled
}

// PluginConfig defines a denkeeper plugin with explicit capability declarations.
// Unlike [tools.*] entries (raw MCP servers), plugins participate in permission
// checks and lifecycle management.
type PluginConfig struct {
	// Type is the execution strategy: "subprocess" or "docker".
	Type    string            `toml:"type"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	// Capabilities declares contracts this plugin satisfies.
	// Currently only "tools" is meaningful — registers the plugin as an MCP server.
	Capabilities []string `toml:"capabilities"`

	// Docker-specific fields (only used when type = "docker").

	// Image is the Docker/OCI image to run (e.g. "myregistry/mcp-plugin:v1").
	// Required for docker plugins.
	Image string `toml:"image"`
	// MemoryLimit is the container memory limit (e.g. "256m", "1g").
	// Passed directly to --memory. Optional; no limit if empty.
	MemoryLimit string `toml:"memory_limit"`
	// CPULimit is the container CPU limit (e.g. "0.5", "2").
	// Passed directly to --cpus. Optional; no limit if empty.
	CPULimit string `toml:"cpu_limit"`
	// Network is the Docker network mode. Defaults to "none" (fully isolated).
	// Valid values: "none", "host", "bridge", or a named network.
	Network string `toml:"network"`
	// Volumes is a list of bind mounts in Docker format ("host:container[:ro]").
	Volumes []string `toml:"volumes"`
}

// SessionConfig controls the default permission tier for agent sessions.
type SessionConfig struct {
	Tier string `toml:"tier"` // "supervised" (default), "autonomous", "restricted"

	// ApprovalTimeout is how long to wait for operator approval before timing
	// out. Accepts Go duration strings (e.g. "5m", "30s"). Default: "5m".
	ApprovalTimeout string `toml:"approval_timeout"`

	// ApprovalRetries is the number of times to re-submit a timed-out approval
	// before reporting failure to the LLM. Default: 0 (no retries).
	ApprovalRetries int `toml:"approval_retries"`
}

type AgentConfig struct {
	PersonaDir string `toml:"persona_dir"`
	SkillsDir  string `toml:"skills_dir"` // defaults to ~/.denkeeper/skills
}

type TelegramConfig struct {
	Token        string  `toml:"token"`
	AllowedUsers []int64 `toml:"allowed_users"`
}

type LLMConfig struct {
	DefaultProvider       string                   `toml:"default_provider"`
	DefaultModel          string                   `toml:"default_model"`
	Providers             []ProviderInstanceConfig `toml:"providers"`
	OpenRouter            OpenRouterConfig         `toml:"openrouter"`
	Ollama                OllamaConfig             `toml:"ollama"`
	Anthropic             AnthropicConfig          `toml:"anthropic"`
	OpenAI                OpenAIConfig             `toml:"openai"`
	MaxCostPerSession     float64                  `toml:"max_cost_per_session"` // Deprecated: use CostLimitHard.
	CostLimitSoft         float64                  `toml:"cost_limit_soft"`
	CostLimitHard         float64                  `toml:"cost_limit_hard"`
	StreamIdleTimeoutSecs int                      `toml:"stream_idle_timeout_secs"`
	Fallbacks             []FallbackConfig         `toml:"fallback"`
}

// ProviderInstanceConfig defines a named LLM provider instance.
// Multiple instances of the same type are allowed (e.g. two OpenAI-compatible endpoints).
type ProviderInstanceConfig struct {
	Name         string `toml:"name"         json:"name"`
	Type         string `toml:"type"         json:"type"` // "anthropic", "openai", "openrouter", "ollama"
	APIKey       string `toml:"api_key"      json:"-"`
	BaseURL      string `toml:"base_url"     json:"base_url,omitempty"`
	Organization string `toml:"organization" json:"organization,omitempty"` // openai-specific

	// Auth selects the authentication scheme. "" / "api_key" use the X-Api-Key
	// header (the default). "oauth" authenticates with a Claude subscription
	// token (anthropic only) via the Authorization: Bearer header. See AuthMode*.
	Auth string `toml:"auth" json:"auth,omitempty"`
	// OAuthToken is a Claude subscription OAuth token (sk-ant-oat...) minted by
	// `claude setup-token`. Only used when Auth == "oauth". Never serialized to
	// JSON — it is a subscription credential. May also be supplied via the
	// CLAUDE_CODE_OAUTH_TOKEN environment variable.
	OAuthToken string `toml:"oauth_token" json:"-"`

	CostLimitSoft         *float64                    `toml:"cost_limit_soft"            json:"cost_limit_soft,omitempty"`
	CostLimitHard         *float64                    `toml:"cost_limit_hard"            json:"cost_limit_hard,omitempty"`
	DefaultRatePerKTokens *float64                    `toml:"default_rate_per_1k_tokens" json:"default_rate_per_1k_tokens,omitempty"`
	ModelPrices           map[string]ModelPriceConfig `toml:"model_prices"               json:"model_prices,omitempty"`
}

// IsOAuth reports whether this provider authenticates with a Claude
// subscription OAuth token rather than a static API key.
func (p ProviderInstanceConfig) IsOAuth() bool {
	return p.Auth == AuthModeOAuth
}

// ProviderNames returns the names of all configured provider instances.
func (l *LLMConfig) ProviderNames() []string {
	names := make([]string, len(l.Providers))
	for i, p := range l.Providers {
		names[i] = p.Name
	}
	return names
}

// HasProvider returns true if a provider with the given name is configured.
func (l *LLMConfig) HasProvider(name string) bool {
	for _, p := range l.Providers {
		if p.Name == name {
			return true
		}
	}
	return false
}

// AnthropicConfig configures the Anthropic direct LLM provider.
type AnthropicConfig struct {
	// APIKey is the Anthropic API key (sk-ant-...). Required to enable the provider.
	APIKey string `toml:"api_key"`
	// BaseURL overrides the default API endpoint. Useful for Bedrock/Vertex proxies.
	BaseURL string `toml:"base_url"`
	// Auth selects the authentication scheme: "" / "api_key" (default) or "oauth".
	Auth string `toml:"auth"`
	// OAuthToken is a Claude subscription token (sk-ant-oat...) used when Auth == "oauth".
	OAuthToken string `toml:"oauth_token"`
}

// OpenAIConfig configures the OpenAI-compatible LLM provider.
// Works with OpenAI, Azure OpenAI, vLLM, LiteLLM, and any OpenAI-format endpoint.
type OpenAIConfig struct {
	// APIKey is the OpenAI API key. Required to enable the provider.
	APIKey string `toml:"api_key"`
	// BaseURL overrides the default API endpoint. Useful for Azure, vLLM, etc.
	BaseURL string `toml:"base_url"`
	// Organization is the optional OpenAI organization ID.
	Organization string `toml:"organization"`
}

// FallbackConfig defines a single fallback rule for the LLM router.
// Rules are evaluated in declaration order; first match wins per trigger type.
type FallbackConfig struct {
	Trigger    string  `toml:"trigger"     json:"trigger"`               // "error" | "rate_limit" | "cost_limit"
	Action     string  `toml:"action"      json:"action"`                // "switch_provider" | "switch_model" | "wait_and_retry"
	Provider   string  `toml:"provider"    json:"provider,omitempty"`    // required for switch_provider
	Model      string  `toml:"model"       json:"model,omitempty"`       // required for switch_model; optional for switch_provider
	Scope      string  `toml:"scope"       json:"scope,omitempty"`       // "soft" | "hard" — required for cost_limit
	Threshold  float64 `toml:"threshold"   json:"threshold,omitempty"`   // Deprecated: legacy low_funds field, auto-migrated on load.
	MaxRetries int     `toml:"max_retries" json:"max_retries,omitempty"` // required for wait_and_retry
	Backoff    string  `toml:"backoff"     json:"backoff,omitempty"`     // "exponential" (default) | "constant"
}

type OpenRouterConfig struct {
	APIKey    string                 `toml:"api_key"`
	Reasoning OpenRouterReasoningCfg `toml:"reasoning"`
}

// OpenRouterReasoningCfg controls the reasoning parameter sent to OpenRouter.
// See https://openrouter.ai/docs/guides/best-practices/reasoning-tokens
type OpenRouterReasoningCfg struct {
	// Enabled activates reasoning with model defaults. Inferred true when
	// Effort or MaxTokens is set.
	Enabled *bool `toml:"enabled" json:"enabled,omitempty"`
	// Effort sets the reasoning effort level: "xhigh", "high", "medium",
	// "low", "minimal", "none". Mutually exclusive with MaxTokens.
	Effort string `toml:"effort" json:"effort,omitempty"`
	// MaxTokens sets the reasoning token budget. Mutually exclusive with Effort.
	MaxTokens int `toml:"max_tokens" json:"max_tokens,omitempty"`
	// Exclude omits reasoning from the response (tokens are still billed).
	Exclude *bool `toml:"exclude" json:"exclude,omitempty"`
}

// OllamaConfig configures the local Ollama LLM provider.
type OllamaConfig struct {
	// BaseURL is the Ollama server address. Defaults to http://localhost:11434.
	BaseURL string `toml:"base_url"`
}

// MemoryConfig configures conversation persistence and retention.
type MemoryConfig struct {
	DBPath string `toml:"db_path"`
	// RetentionDays is how long conversations are kept. 0 = unlimited. Default 90.
	RetentionDays int `toml:"retention_days"`
	// MaxConversations limits the total number of stored conversations. 0 = unlimited. Default 10000.
	MaxConversations int `toml:"max_conversations"`
	// CleanupInterval is how often retention policies are enforced (Go duration string). Default "1h".
	CleanupInterval string `toml:"cleanup_interval"`
	// PersonaMemoryCharLimit caps MEMORY.md size in characters. 0 = unlimited.
	PersonaMemoryCharLimit int `toml:"persona_memory_char_limit"`
	// PersonaUserCharLimit caps USER.md size in characters. 0 = unlimited.
	PersonaUserCharLimit int `toml:"persona_user_char_limit"`
}

// KVConfig configures the per-agent key-value store.
type KVConfig struct {
	// MaxKeysPerAgent limits the number of keys each agent can store (0 = unlimited).
	MaxKeysPerAgent int `toml:"max_keys_per_agent"`
	// MaxValueBytes limits the size of each value in bytes.
	MaxValueBytes int `toml:"max_value_bytes"`
	// CleanupInterval is how often expired keys are purged (Go duration string).
	CleanupInterval string `toml:"cleanup_interval"`
}

type LogConfig struct {
	Level  string `toml:"level"`
	Format string `toml:"format"`
}

// ScheduleConfig defines a single scheduled task entry.
//
// Schedule expression formats (schedule field):
//
//	Named shortcuts:  @hourly, @daily, @midnight, @weekly, @monthly, @yearly, @annually
//	Interval syntax:  @every <duration>  (e.g. @every 5m, @every 1h30m)
//	5-field cron:     <min> <hour> <dom> <month> <dow>  (e.g. "0 8 * * 1-5")
//
// Schedule types (type field):
//
//	"system"  Core system tasks (heartbeats, maintenance). Isolated from
//	          agent-created schedules and run with elevated priority.
//	"agent"   User-configured scheduled agent skill runs.
type ScheduleConfig struct {
	// Name is a unique identifier for this schedule. Required.
	Name string `toml:"name"`

	// Type classifies the schedule. Must be "system" or "agent". Required.
	Type string `toml:"type"`

	// Schedule is the timing expression. Required.
	Schedule string `toml:"schedule"`

	// Skill is the name of the skill to invoke on each run. Optional for
	// system schedules; typically required for agent schedules.
	Skill string `toml:"skill"`

	// SessionTier is the permission tier for the session spawned on each run.
	// Allowed values: "supervised" (default), "autonomous", "restricted".
	SessionTier string `toml:"session_tier"`

	// Channel is the adapter channel to deliver results to (e.g. "telegram:123456").
	Channel string `toml:"channel"`

	// SessionMode controls which conversation context is used for the scheduled run.
	// "shared" (default): reuses the channel's existing conversation history.
	// "isolated": creates a fresh conversation for each run with no prior context.
	SessionMode string `toml:"session_mode"`

	// Agent is the name of the agent that handles this schedule. Defaults to "default".
	Agent string `toml:"agent"`

	// Tags are freeform labels for organizing and filtering schedules.
	Tags []string `toml:"tags"`

	// Enabled controls whether this schedule is active. Use a pointer so that
	// an omitted field can be distinguished from an explicit false, allowing
	// applyDefaults to set the value to true when unspecified.
	Enabled *bool `toml:"enabled"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- config file path from CLI flag / env var
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	cfg := &Config{}

	// First try direct unmarshal. If it fails with a float→int coercion
	// error (common when TOML is generated by Helm's toToml, which renders
	// YAML integers as floats), normalise the data and retry.
	if err := toml.Unmarshal(data, cfg); err != nil {
		if !strings.Contains(err.Error(), "float cannot be assigned to") {
			return nil, fmt.Errorf("parsing config: %w", err)
		}
		normalised, nErr := normaliseFloatsToInts(data)
		if nErr != nil {
			return nil, fmt.Errorf("parsing config: %w (normalisation failed: %v)", err, nErr)
		}
		if err2 := toml.Unmarshal(normalised, cfg); err2 != nil {
			return nil, fmt.Errorf("parsing config: %w", err2)
		}
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// normaliseFloatsToInts round-trips through a generic map, converting float64
// values that are whole numbers to int64. This fixes TOML generated by Helm's
// toToml function, which renders YAML integers as TOML floats (e.g.
// allowed_users = [3.87956986e+08] instead of [387956986]).
func normaliseFloatsToInts(data []byte) ([]byte, error) {
	var generic map[string]any
	if err := toml.Unmarshal(data, &generic); err != nil {
		return nil, err
	}
	walkAndFixFloats(generic)
	return toml.Marshal(generic)
}

func walkAndFixFloats(m map[string]any) {
	for k, v := range m {
		switch val := v.(type) {
		case map[string]any:
			walkAndFixFloats(val)
		case float64:
			if val == math.Trunc(val) && !math.IsInf(val, 0) && !math.IsNaN(val) {
				m[k] = int64(val)
			}
		case []any:
			m[k] = fixSlice(val)
		}
	}
}

func fixSlice(s []any) []any {
	for i, v := range s {
		switch val := v.(type) {
		case map[string]any:
			walkAndFixFloats(val)
		case float64:
			if val == math.Trunc(val) && !math.IsInf(val, 0) && !math.IsNaN(val) {
				s[i] = int64(val)
			}
		case []any:
			s[i] = fixSlice(val)
		}
	}
	return s
}

func applyDefaults(cfg *Config) {
	resolveDataDir(cfg)
	applyScalarDefaults(cfg)

	userSetSoft := cfg.LLM.CostLimitSoft > 0
	userSetHard := cfg.LLM.CostLimitHard > 0 || cfg.LLM.MaxCostPerSession > 0
	userCostSoft := cfg.LLM.CostLimitSoft
	userCostHard := cfg.LLM.CostLimitHard
	if cfg.LLM.MaxCostPerSession > 0 && userCostHard == 0 {
		userCostHard = cfg.LLM.MaxCostPerSession
	}

	applyLLMDefaults(cfg)
	applyEnvOverrides(cfg)
	synthesizeLegacyProviders(cfg)
	resolveProviderOAuthTokens(cfg)
	migrateCostsToProviders(cfg, userSetSoft, userCostSoft, userSetHard, userCostHard)
	expandEnvVars(cfg)
	applyMiscDefaults(cfg)
	synthesizeDefaultAgent(cfg)
	applyAgentDefaults(cfg)
	synthesizeChannels(cfg)
	applyScheduleDefaults(cfg)
	applyToolDefaults(cfg)
}

// migrateCostsToProviders copies global cost limits onto providers that don't
// have their own overrides. Only runs when the user explicitly set values
// in the TOML (boolean flags distinguish "user set this" from "default applied").
func migrateCostsToProviders(cfg *Config, userSetSoft bool, softVal float64, userSetHard bool, hardVal float64) {
	if !userSetSoft && !userSetHard {
		return
	}
	for i := range cfg.LLM.Providers {
		p := &cfg.LLM.Providers[i]
		if userSetSoft && p.CostLimitSoft == nil {
			v := softVal
			p.CostLimitSoft = &v
		}
		if userSetHard && p.CostLimitHard == nil {
			v := hardVal
			p.CostLimitHard = &v
		}
	}
}

// synthesizeLegacyProviders converts the old-style [llm.openai], [llm.anthropic], etc.
// config sections into ProviderInstanceConfig entries. Legacy entries are only added
// if no [[llm.providers]] entry with the same name already exists. This allows users
// to migrate incrementally from the old single-slot syntax to the new array syntax.
func synthesizeLegacyProviders(cfg *Config) {
	add := func(p ProviderInstanceConfig) {
		if cfg.LLM.HasProvider(p.Name) {
			return
		}
		cfg.LLM.Providers = append(cfg.LLM.Providers, p)
	}

	// Always synthesize entries for legacy provider sections. Even if the API key
	// is missing, we need the entry so validation can produce a clear "requires
	// api_key" error rather than "provider not found". The key check happens in
	// validateProviderAPIKeys.
	if cfg.LLM.Anthropic.APIKey != "" || cfg.LLM.Anthropic.OAuthToken != "" ||
		cfg.LLM.Anthropic.Auth == AuthModeOAuth || IsProviderReferenced(cfg, "anthropic") {
		add(ProviderInstanceConfig{
			Name:       "anthropic",
			Type:       "anthropic",
			APIKey:     cfg.LLM.Anthropic.APIKey,
			BaseURL:    cfg.LLM.Anthropic.BaseURL,
			Auth:       cfg.LLM.Anthropic.Auth,
			OAuthToken: cfg.LLM.Anthropic.OAuthToken,
		})
	}
	if cfg.LLM.OpenRouter.APIKey != "" || IsProviderReferenced(cfg, "openrouter") {
		add(ProviderInstanceConfig{Name: "openrouter", Type: "openrouter", APIKey: cfg.LLM.OpenRouter.APIKey})
	}
	if cfg.LLM.OpenAI.APIKey != "" || IsProviderReferenced(cfg, "openai") {
		add(ProviderInstanceConfig{
			Name: "openai", Type: "openai", APIKey: cfg.LLM.OpenAI.APIKey,
			BaseURL: cfg.LLM.OpenAI.BaseURL, Organization: cfg.LLM.OpenAI.Organization,
		})
	}
	if cfg.LLM.Ollama.BaseURL != "" || IsProviderReferenced(cfg, "ollama") {
		add(ProviderInstanceConfig{Name: "ollama", Type: "ollama", BaseURL: cfg.LLM.Ollama.BaseURL})
	}
}

// resolveProviderOAuthTokens fills in the OAuth token for anthropic providers
// configured with auth = "oauth" but no token in TOML, from the environment.
// CLAUDE_CODE_OAUTH_TOKEN is the variable `claude setup-token` documents, so
// users who already export it (e.g. for Claude Code) get it picked up for free;
// DENKEEPER_LLM_ANTHROPIC_OAUTH_TOKEN is the Denkeeper-namespaced alias.
//
// Ordering note: this runs before expandEnvVars in applyDefaults. That is
// intentional — a token sourced from the environment here is a literal secret
// and must NOT be fed back through ${VAR} expansion. A token written in TOML as
// "${SOME_VAR}" is left untouched here (we only fill empties) and expanded later
// by expandEnvVars. Do not reorder these two passes.
func resolveProviderOAuthTokens(cfg *Config) {
	token := os.Getenv("DENKEEPER_LLM_ANTHROPIC_OAUTH_TOKEN")
	if token == "" {
		token = os.Getenv("CLAUDE_CODE_OAUTH_TOKEN")
	}
	if token == "" {
		return
	}
	for i := range cfg.LLM.Providers {
		p := &cfg.LLM.Providers[i]
		if p.Type == "anthropic" && p.IsOAuth() && p.OAuthToken == "" {
			p.OAuthToken = token
		}
	}
}

// IsProviderReferenced returns true if the named provider is referenced as the
// default provider, by any agent's llm_provider, or by any fallback rule.
func IsProviderReferenced(cfg *Config, name string) bool {
	if cfg.LLM.DefaultProvider == name {
		return true
	}
	for _, a := range cfg.Agents {
		if a.LLMProvider == name {
			return true
		}
	}
	for _, f := range cfg.LLM.Fallbacks {
		if f.Provider == name {
			return true
		}
	}
	return false
}

// resolveDataDir sets cfg.DataDir from DENKEEPER_DATA_DIR env var, the TOML
// data_dir field, or the default ~/.denkeeper. All other default paths are
// derived from this base directory.
func resolveDataDir(cfg *Config) {
	envOverride("DENKEEPER_DATA_DIR", &cfg.DataDir)
	if cfg.DataDir != "" {
		return
	}
	home, _ := os.UserHomeDir()
	cfg.DataDir = filepath.Join(home, ".denkeeper")
}

func applyScalarDefaults(cfg *Config) {
	if cfg.Memory.DBPath == "" {
		cfg.Memory.DBPath = filepath.Join(cfg.DataDir, "data", "memory.db")
	}
	if cfg.Memory.RetentionDays == 0 {
		cfg.Memory.RetentionDays = 90
	}
	if cfg.Memory.MaxConversations == 0 {
		cfg.Memory.MaxConversations = 10000
	}
	if cfg.Memory.CleanupInterval == "" {
		cfg.Memory.CleanupInterval = "1h"
	}
	if cfg.Memory.PersonaMemoryCharLimit == 0 {
		cfg.Memory.PersonaMemoryCharLimit = 2200
	}
	if cfg.Memory.PersonaUserCharLimit == 0 {
		cfg.Memory.PersonaUserCharLimit = 1375
	}
	if cfg.Agent.PersonaDir == "" {
		cfg.Agent.PersonaDir = filepath.Join(cfg.DataDir, "agents", "default")
	}
	if cfg.Agent.SkillsDir == "" {
		cfg.Agent.SkillsDir = filepath.Join(cfg.DataDir, "skills")
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	applySessionDefaults(&cfg.Session)
	if cfg.Sandbox.Runtime == "" {
		cfg.Sandbox.Runtime = "docker"
	}
	if cfg.Sandbox.Kubernetes.Namespace == "" {
		cfg.Sandbox.Kubernetes.Namespace = "denkeeper-sandboxes"
	}
	if cfg.OTel.ServiceName == "" {
		cfg.OTel.ServiceName = "denkeeper"
	}
	applyMCPDefaults(&cfg.MCP)
	applyMCPServerDefaults(&cfg.API.MCPServer)
	if cfg.API.Timezone == "" {
		cfg.API.Timezone = "UTC"
	}
	applyAuditDefaults(cfg)
}

func applyMCPDefaults(mcp *MCPConfig) {
	if mcp.RequestTimeoutSecs == 0 {
		mcp.RequestTimeoutSecs = 30
	}
	if mcp.SSEKeepAliveSecs == 0 {
		mcp.SSEKeepAliveSecs = 15
	}
	if mcp.AutoRestart == nil {
		t := true
		mcp.AutoRestart = &t
	}
	if mcp.MaxRestartAttempts == 0 {
		mcp.MaxRestartAttempts = 3
	}
	if mcp.RestartCooldown == "" {
		mcp.RestartCooldown = "5m"
	}
	if mcp.InitRetryAttempts == 0 {
		mcp.InitRetryAttempts = 5
	}
	if mcp.InitRetryBackoff == "" {
		mcp.InitRetryBackoff = "2s"
	}
}

func applyMCPServerDefaults(m *APIMCPServerConfig) {
	if m.SessionTimeout == "" {
		m.SessionTimeout = "30m"
	}
	if m.Transport == "" {
		m.Transport = "streamable"
	}
	if m.ChatTimeout == "" {
		m.ChatTimeout = "2m"
	}
}

func applyAuditDefaults(cfg *Config) {
	if cfg.Audit.RetentionDays == 0 {
		cfg.Audit.RetentionDays = 30
	}
	if cfg.Audit.CleanupInterval == "" {
		cfg.Audit.CleanupInterval = "1h"
	}
	if cfg.Audit.BufferSize == 0 {
		cfg.Audit.BufferSize = 1000
	}
	if v := os.Getenv("DENKEEPER_AUDIT_ENABLED"); v == "true" || v == "1" {
		t := true
		cfg.Audit.Enabled = &t
	} else if v == "false" || v == "0" {
		f := false
		cfg.Audit.Enabled = &f
	}
}

func applyLLMDefaults(cfg *Config) {
	if cfg.LLM.DefaultProvider == "" {
		cfg.LLM.DefaultProvider = "openrouter"
	}
	if cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = "anthropic/claude-sonnet-4-20250514"
	}

	// Migrate deprecated max_cost_per_session → cost_limit_hard.
	if cfg.LLM.MaxCostPerSession > 0 && cfg.LLM.CostLimitHard == 0 {
		cfg.LLM.CostLimitHard = cfg.LLM.MaxCostPerSession
	}
	// Default hard limit when nothing is configured.
	if cfg.LLM.CostLimitHard == 0 {
		cfg.LLM.CostLimitHard = 1.0
	}
	// Keep deprecated field in sync for backward compat.
	if cfg.LLM.MaxCostPerSession == 0 {
		cfg.LLM.MaxCostPerSession = cfg.LLM.CostLimitHard
	}

	// Default idle timeout for LLM SSE streams: 120s. This catches stalled
	// provider connections without affecting actively-streaming responses.
	if cfg.LLM.StreamIdleTimeoutSecs == 0 {
		cfg.LLM.StreamIdleTimeoutSecs = 120
	}

	migrateFallbacks("llm", cfg.LLM.Fallbacks)
	for i := range cfg.LLM.Fallbacks {
		if cfg.LLM.Fallbacks[i].Backoff == "" {
			cfg.LLM.Fallbacks[i].Backoff = "exponential"
		}
	}
}

// MigrateFallbacks is the exported entry point for rewriting legacy
// low_funds rules into the modern cost_limit/soft form. Used by the REST
// API to normalise PATCH bodies before validation.
func MigrateFallbacks(scope string, rules []FallbackConfig) {
	migrateFallbacks(scope, rules)
}

// migrateFallbacks rewrites legacy fallback definitions into the currently
// supported shape and logs a warning per migrated rule. Mutates rules in place.
func migrateFallbacks(scope string, rules []FallbackConfig) {
	for i := range rules {
		if rules[i].Trigger == "low_funds" {
			slog.Warn("migrating deprecated fallback rule",
				"scope", scope, "index", i,
				"from", "low_funds", "to", "cost_limit",
				"threshold_dropped", rules[i].Threshold)
			rules[i].Trigger = "cost_limit"
			if rules[i].Scope == "" {
				rules[i].Scope = "soft"
			}
			rules[i].Threshold = 0
		}

		if rules[i].Action == "switch_model" && rules[i].Provider != "" {
			slog.Warn("normalizing fallback rule: dropping unsupported provider field for switch_model",
				"scope", scope, "index", i,
				"provider", rules[i].Provider,
				"hint", "use switch_provider action to swap providers")
			rules[i].Provider = ""
		}
	}
}

// applyEnvOverrides allows specific config fields to be set via environment
// variables. This enables the standard Kubernetes pattern of injecting secrets
// via env vars while keeping non-secret config in a ConfigMap-mounted file.
// Only the explicitly listed DENKEEPER_* variables are read (allowlist).
// envOverride sets *target to the value of the named environment variable, if set.
func envOverride(name string, target *string) {
	if v := os.Getenv(name); v != "" {
		*target = v
	}
}

func envOverrideInt(name string, target *int) {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*target = n
		}
	}
}

func envOverrideBoolPtr(name string, target **bool) {
	if v := os.Getenv(name); v == "true" || v == "1" {
		t := true
		*target = &t
	} else if v == "false" || v == "0" {
		f := false
		*target = &f
	}
}

func applyEnvOverrides(cfg *Config) {
	envOverride("DENKEEPER_TELEGRAM_TOKEN", &cfg.Telegram.Token)
	envOverride("DENKEEPER_DISCORD_TOKEN", &cfg.Discord.Token)
	envOverride("DENKEEPER_LLM_PROVIDER", &cfg.LLM.DefaultProvider)
	envOverride("DENKEEPER_LLM_MODEL", &cfg.LLM.DefaultModel)
	envOverride("DENKEEPER_LLM_OPENROUTER_API_KEY", &cfg.LLM.OpenRouter.APIKey)
	envOverrideBoolPtr("DENKEEPER_LLM_OPENROUTER_REASONING_ENABLED", &cfg.LLM.OpenRouter.Reasoning.Enabled)
	envOverride("DENKEEPER_LLM_OPENROUTER_REASONING_EFFORT", &cfg.LLM.OpenRouter.Reasoning.Effort)
	envOverrideInt("DENKEEPER_LLM_OPENROUTER_REASONING_MAX_TOKENS", &cfg.LLM.OpenRouter.Reasoning.MaxTokens)
	envOverride("DENKEEPER_LLM_ANTHROPIC_API_KEY", &cfg.LLM.Anthropic.APIKey)
	envOverride("DENKEEPER_LLM_ANTHROPIC_BASE_URL", &cfg.LLM.Anthropic.BaseURL)
	envOverride("DENKEEPER_LLM_ANTHROPIC_OAUTH_TOKEN", &cfg.LLM.Anthropic.OAuthToken)
	envOverride("DENKEEPER_LLM_OLLAMA_BASE_URL", &cfg.LLM.Ollama.BaseURL)
	envOverride("DENKEEPER_LLM_OPENAI_API_KEY", &cfg.LLM.OpenAI.APIKey)
	envOverride("DENKEEPER_LLM_OPENAI_BASE_URL", &cfg.LLM.OpenAI.BaseURL)
	envOverride("DENKEEPER_VOICE_OPENAI_API_KEY", &cfg.Voice.OpenAI.APIKey)
	envOverride("DENKEEPER_LOG_LEVEL", &cfg.Log.Level)
	envOverride("DENKEEPER_LOG_FORMAT", &cfg.Log.Format)
	envOverride("DENKEEPER_MEMORY_DB_PATH", &cfg.Memory.DBPath)
	envOverrideInt("DENKEEPER_MEMORY_RETENTION_DAYS", &cfg.Memory.RetentionDays)
	envOverrideInt("DENKEEPER_MEMORY_MAX_CONVERSATIONS", &cfg.Memory.MaxConversations)
	envOverride("DENKEEPER_API_LISTEN", &cfg.API.Listen)
	envOverride("DENKEEPER_SESSION_TIER", &cfg.Session.Tier)
	envOverride("DENKEEPER_APPROVAL_TIMEOUT", &cfg.Session.ApprovalTimeout)
	envOverrideInt("DENKEEPER_APPROVAL_RETRIES", &cfg.Session.ApprovalRetries)
	envOverride("DENKEEPER_SEARCH_API_KEY", &cfg.Web.Search.APIKey)
	envOverride("DENKEEPER_OTEL_TRACES_ENDPOINT", &cfg.OTel.TracesEndpoint)
	if v := os.Getenv("DENKEEPER_OTEL_ENABLED"); v == "true" || v == "1" {
		cfg.OTel.Enabled = true
	} else if v == "false" || v == "0" {
		cfg.OTel.Enabled = false
	}
	envOverride("DENKEEPER_API_AUTH_SESSION_SECRET", &cfg.API.Auth.SessionSecret)
	envOverride("DENKEEPER_OIDC_CLIENT_ID", &cfg.API.Auth.OIDC.ClientID)
	envOverride("DENKEEPER_OIDC_CLIENT_SECRET", &cfg.API.Auth.OIDC.ClientSecret)
	envOverride("DENKEEPER_API_EXTERNAL_URL", &cfg.API.ExternalURL)
	envOverride("DENKEEPER_TIMEZONE", &cfg.API.Timezone)

	if v := os.Getenv("DENKEEPER_API_ENABLED"); v == "true" || v == "1" {
		t := true
		cfg.API.Enabled = &t
	} else if v == "false" || v == "0" {
		f := false
		cfg.API.Enabled = &f
	}

	if v := os.Getenv("DENKEEPER_API_WEBSOCKET_ENABLED"); v == "true" || v == "1" {
		t := true
		cfg.API.WebSocketEnabled = &t
	} else if v == "false" || v == "0" {
		f := false
		cfg.API.WebSocketEnabled = &f
	}

}

func expandEnvVars(cfg *Config) {
	for name, tc := range cfg.Tools {
		for k, v := range tc.Env {
			tc.Env[k] = os.ExpandEnv(v)
		}
		cfg.Tools[name] = tc
	}
	for name, pc := range cfg.Plugins {
		for k, v := range pc.Env {
			pc.Env[k] = os.ExpandEnv(v)
		}
		cfg.Plugins[name] = pc
	}
}

func applyMiscDefaults(cfg *Config) {
	if cfg.Voice.TTSVoice == "" && cfg.Voice.TTSProvider != "" {
		cfg.Voice.TTSVoice = "alloy"
	}
	if cfg.Security.AllowUnsigned == nil {
		t := true
		cfg.Security.AllowUnsigned = &t
	}
	if cfg.KV.MaxKeysPerAgent == 0 {
		cfg.KV.MaxKeysPerAgent = 1000
	}
	if cfg.KV.MaxValueBytes == 0 {
		cfg.KV.MaxValueBytes = 65536
	}
	if cfg.KV.CleanupInterval == "" {
		cfg.KV.CleanupInterval = "1h"
	}
	if cfg.API.Enabled == nil {
		t := true
		cfg.API.Enabled = &t
	}
	if cfg.API.IsEnabled() && cfg.API.Listen == "" {
		cfg.API.Listen = ":8080"
	}
	if cfg.API.WebSocketEnabled == nil {
		t := true
		cfg.API.WebSocketEnabled = &t
	}
	if cfg.API.WebSocketReplayBufferTTL == "" {
		cfg.API.WebSocketReplayBufferTTL = "5m"
	}
	applyAuthDefaults(cfg)
	applyWebDefaults(cfg)
	applyBrowserDefaults(cfg)
}

func applyAuthDefaults(cfg *Config) {
	if cfg.API.Auth.SessionMaxAge == "" {
		cfg.API.Auth.SessionMaxAge = "24h"
	}
	if cfg.API.Auth.PreferredLoginMethod == "" {
		cfg.API.Auth.PreferredLoginMethod = "auto"
	}
	if cfg.API.Auth.SessionRecordRetention == "" {
		cfg.API.Auth.SessionRecordRetention = "720h"
	}
	if cfg.API.Auth.OIDC.Enabled && len(cfg.API.Auth.OIDC.Scopes) == 0 {
		cfg.API.Auth.OIDC.Scopes = []string{"openid", "email", "profile"}
	}
}

func applyWebDefaults(cfg *Config) {
	if cfg.Web.Enabled == nil {
		trueVal := true
		cfg.Web.Enabled = &trueVal
	}
	if cfg.Web.Search.Provider == "" {
		cfg.Web.Search.Provider = "duckduckgo"
	}
	if cfg.Web.Search.MaxResults == 0 {
		cfg.Web.Search.MaxResults = 5
	}
	if cfg.Web.Fetch.Timeout == "" {
		cfg.Web.Fetch.Timeout = "30s"
	}
	if cfg.Web.Fetch.MaxSizeBytes == 0 {
		cfg.Web.Fetch.MaxSizeBytes = 5242880 // 5MB
	}
	if cfg.Web.Fetch.UserAgent == "" {
		cfg.Web.Fetch.UserAgent = "Denkeeper/1.0 (+https://denkeeper.io)"
	}
}

func applyBrowserDefaults(cfg *Config) {
	if cfg.Browser.Image == "" {
		cfg.Browser.Image = "ghcr.io/temikus/denkeeper-browser:latest"
	}
	if cfg.Browser.MemoryLimit == "" {
		cfg.Browser.MemoryLimit = "512m"
	}
	if cfg.Browser.CPULimit == "" {
		cfg.Browser.CPULimit = "1"
	}
	if cfg.Browser.ProfileDir == "" {
		cfg.Browser.ProfileDir = "data/browser-profiles"
	}
	if cfg.Browser.SessionTTL == "" {
		cfg.Browser.SessionTTL = "10m"
	}
	if cfg.Browser.MaxPages == 0 {
		cfg.Browser.MaxPages = 5
	}
}

// synthesizeDefaultAgent provides backward-compatible multi-agent support:
// if no [[agents]] defined AND at least one adapter token is configured
// (headless mode), synthesize a single "default" agent. When no adapters
// are configured the user is expected to create agents via the web wizard.
func synthesizeDefaultAgent(cfg *Config) {
	if len(cfg.Agents) != 0 {
		return
	}
	var defaultAdapters []string
	if cfg.Telegram.Token != "" {
		defaultAdapters = append(defaultAdapters, "telegram")
	}
	if cfg.Discord.Token != "" {
		defaultAdapters = append(defaultAdapters, "discord")
	}
	if len(defaultAdapters) == 0 {
		return
	}
	cfg.Agents = []AgentInstanceConfig{{
		Name:        "default",
		Description: "Default agent",
		PersonaDir:  cfg.Agent.PersonaDir,
		SkillsDir:   cfg.Agent.SkillsDir,
		Adapters:    defaultAdapters,
		SessionTier: cfg.Session.Tier,
	}}
}

func applyAgentDefaults(cfg *Config) {
	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if a.PersonaDir == "" {
			a.PersonaDir = filepath.Join(cfg.DataDir, "agents", a.Name)
		}
		migrateFallbacks("agent:"+a.Name, a.Fallbacks)
		for j := range a.Fallbacks {
			if a.Fallbacks[j].Backoff == "" {
				a.Fallbacks[j].Backoff = "exponential"
			}
		}
	}
}

// synthesizeChannels auto-generates channels from agent adapter bindings.
// When no explicit [[channels]] section exists, all agent bindings are
// synthesized. When explicit channels exist, any agent adapter bindings NOT
// already covered by an explicit channel are still synthesized as implicit
// channels, preventing silent loss of adapter routing.
func synthesizeChannels(cfg *Config) {
	// Collect adapter bindings and channel names already covered by explicit channels.
	covered := make(map[string]bool)
	usedNames := make(map[string]bool)
	for _, ch := range cfg.Channels {
		usedNames[ch.Name] = true
		for _, binding := range ch.Adapters {
			covered[binding] = true
		}
	}

	// Synthesize implicit channels for uncovered agent adapter bindings.
	for _, a := range cfg.Agents {
		for _, binding := range a.Adapters {
			if covered[binding] {
				continue
			}
			name := a.Name + ":" + binding
			if usedNames[name] {
				continue // avoid name collision with explicit channel
			}
			usedNames[name] = true
			cfg.Channels = append(cfg.Channels, ChannelConfig{
				Name:     name,
				Agent:    a.Name,
				Adapters: []string{binding},
				Implicit: true,
			})
		}
	}
}

func applyScheduleDefaults(cfg *Config) {
	trueVal := true
	for i := range cfg.Schedules {
		s := &cfg.Schedules[i]
		if s.Enabled == nil {
			s.Enabled = &trueVal
		}
		if s.SessionTier == "" {
			s.SessionTier = "supervised"
		}
		if s.SessionMode == "" {
			s.SessionMode = "shared"
		}
		if s.Agent == "" && len(cfg.Agents) > 0 {
			s.Agent = cfg.Agents[0].Name
		}
	}
}

func applyToolDefaults(cfg *Config) {
	for name, tc := range cfg.Tools {
		if tc.Enabled == nil {
			v := true
			tc.Enabled = &v
			cfg.Tools[name] = tc
		}
	}
}

// validTiers is the set of recognised permission tier names.
var validTiers = map[string]bool{
	"supervised": true,
	"autonomous": true,
	"restricted": true,
}

func applySessionDefaults(s *SessionConfig) {
	if s.Tier == "" {
		s.Tier = "supervised"
	}
	if s.ApprovalTimeout == "" {
		s.ApprovalTimeout = "5m"
	}
}

func validateSessionConfig(s *SessionConfig) error {
	if err := validateTier(s.Tier, "session.tier"); err != nil {
		return fmt.Errorf("validate session tier: %w", err)
	}
	if _, err := time.ParseDuration(s.ApprovalTimeout); err != nil {
		return fmt.Errorf("config: session.approval_timeout: invalid duration %q: %w", s.ApprovalTimeout, err)
	}
	if s.ApprovalRetries < 0 {
		return fmt.Errorf("config: session.approval_retries must be >= 0, got %d", s.ApprovalRetries)
	}
	return nil
}

func validateTier(tier, context string) error {
	if !validTiers[tier] {
		return fmt.Errorf("config: %s: invalid tier %q — must be one of: supervised, autonomous, restricted", context, tier)
	}
	return nil
}

// validProviderTypes is the set of recognized LLM provider types.
var validProviderTypes = map[string]bool{
	"anthropic": true, "openai": true, "openrouter": true, "ollama": true,
}

// ValidProviderType reports whether typ is a recognized LLM provider type.
func ValidProviderType(typ string) bool {
	return validProviderTypes[typ]
}

// Provider authentication modes.
const (
	// AuthModeAPIKey authenticates with a static API key via the X-Api-Key
	// header. This is the default for every provider type.
	AuthModeAPIKey = "api_key"
	// AuthModeOAuth authenticates with a Claude subscription OAuth token via the
	// Authorization: Bearer header. Anthropic provider only.
	AuthModeOAuth = "oauth"
)

// validAuthModes is the set of recognized provider authentication modes.
var validAuthModes = map[string]bool{
	AuthModeAPIKey: true, AuthModeOAuth: true,
}

// ValidAuthMode reports whether mode is a recognized provider auth mode.
// The empty string is accepted and treated as the default (api_key).
func ValidAuthMode(mode string) bool {
	return mode == "" || validAuthModes[mode]
}

// resourceNameRe is the shared pattern for agent, provider, and channel names:
// lowercase alphanumeric with hyphens, 1-64 chars.
var resourceNameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidResourceName reports whether name is a valid resource identifier
// (agent, provider, channel). Must be lowercase alphanumeric with hyphens,
// 1-64 characters.
func ValidResourceName(name string) bool {
	return len(name) > 0 && len(name) <= 64 && resourceNameRe.MatchString(name)
}

// providerNeedsAPIKey reports whether the given provider type requires an API key.
func providerNeedsAPIKey(typ string) bool {
	return typ != "ollama"
}

// validateProviderInstances checks provider names, types, and required fields.
func validateProviderInstances(cfg *Config) error {
	seen := make(map[string]bool)
	for i, p := range cfg.LLM.Providers {
		if p.Name == "" {
			return fmt.Errorf("config: llm.providers[%d]: name is required", i)
		}
		if !validProviderTypes[p.Type] {
			return fmt.Errorf("config: llm.providers[%d] %q: invalid type %q — must be one of: anthropic, openai, openrouter, ollama", i, p.Name, p.Type)
		}
		if !ValidAuthMode(p.Auth) {
			return fmt.Errorf("config: llm.providers[%d] %q: invalid auth %q — must be one of: api_key, oauth", i, p.Name, p.Auth)
		}
		if p.Auth == AuthModeOAuth && p.Type != "anthropic" {
			return fmt.Errorf("config: llm.providers[%d] %q: auth = \"oauth\" is only supported for type \"anthropic\" (got %q)", i, p.Name, p.Type)
		}
		if seen[p.Name] {
			return fmt.Errorf("config: llm.providers[%d]: duplicate provider name %q", i, p.Name)
		}
		seen[p.Name] = true
	}

	// Validate default_provider references an existing provider instance.
	if cfg.LLM.DefaultProvider != "" && !cfg.LLM.HasProvider(cfg.LLM.DefaultProvider) {
		return fmt.Errorf("config: llm.default_provider %q does not match any configured provider instance", cfg.LLM.DefaultProvider)
	}

	// Validate agent llm_provider references.
	for _, a := range cfg.Agents {
		if a.LLMProvider != "" && !cfg.LLM.HasProvider(a.LLMProvider) {
			return fmt.Errorf("config: agent %q: llm_provider %q does not match any configured provider instance", a.Name, a.LLMProvider)
		}
	}

	return nil
}

// validateProviderAPIKeys checks that referenced providers have required API keys.
func validateProviderAPIKeys(cfg *Config) error {
	// Collect all provider names that are actively referenced.
	referenced := make(map[string]bool)
	referenced[cfg.LLM.DefaultProvider] = true
	for _, f := range cfg.LLM.Fallbacks {
		if f.Provider != "" {
			referenced[f.Provider] = true
		}
	}
	for _, a := range cfg.Agents {
		if a.LLMProvider != "" {
			referenced[a.LLMProvider] = true
		}
	}

	for _, p := range cfg.LLM.Providers {
		if !referenced[p.Name] {
			continue
		}
		if p.IsOAuth() {
			if p.OAuthToken == "" {
				return fmt.Errorf("config: provider %q (auth oauth) requires an oauth_token — generate one with `claude setup-token` or set CLAUDE_CODE_OAUTH_TOKEN", p.Name)
			}
			continue
		}
		if providerNeedsAPIKey(p.Type) && p.APIKey == "" {
			return fmt.Errorf("config: provider %q (type %s) requires an api_key", p.Name, p.Type)
		}
	}
	return nil
}

// validateAdaptersAndProviders checks adapter tokens, allowed-user lists, and LLM provider keys.
func validateAdaptersAndProviders(cfg *Config) error {
	if cfg.Telegram.Token != "" && len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("config: telegram.allowed_users must not be empty when telegram.token is set (security requirement)")
	}
	if cfg.Telegram.Token == "" && cfg.Discord.Token == "" && !cfg.API.IsEnabled() {
		return fmt.Errorf("config: at least one adapter must be configured (telegram.token, discord.token, or api.enabled)")
	}
	if cfg.Discord.Token != "" && len(cfg.Discord.AllowedUsers) == 0 {
		return fmt.Errorf("config: discord.allowed_users must not be empty when discord.token is set (security requirement)")
	}
	if err := validateProviderInstances(cfg); err != nil {
		return err
	}
	if err := validateProviderAPIKeys(cfg); err != nil {
		return err
	}
	return nil
}

func validate(cfg *Config) error {
	if err := validateAdaptersAndProviders(cfg); err != nil {
		return fmt.Errorf("validate adapters/providers: %w", err)
	}
	if err := validateSessionConfig(&cfg.Session); err != nil {
		return err
	}
	if err := validateFallbacks(cfg.LLM.Fallbacks); err != nil {
		return fmt.Errorf("validate fallbacks: %w", err)
	}
	if err := validateAgentRouting(cfg); err != nil {
		return err
	}
	if err := validateMCP(&cfg.MCP); err != nil {
		return fmt.Errorf("validate mcp: %w", err)
	}
	validateTools(cfg)
	if err := validatePlugins(cfg.Plugins, cfg.Tools); err != nil {
		return fmt.Errorf("validate plugins: %w", err)
	}
	if err := validateVoice(&cfg.Voice); err != nil {
		return fmt.Errorf("validate voice: %w", err)
	}
	if err := validateCostLimits(cfg); err != nil {
		return fmt.Errorf("validate cost limits: %w", err)
	}
	if err := validateAPI(&cfg.API); err != nil {
		return fmt.Errorf("validate api: %w", err)
	}
	if err := validateWeb(&cfg.Web); err != nil {
		return fmt.Errorf("validate web: %w", err)
	}
	if err := validateSandbox(&cfg.Sandbox); err != nil {
		return fmt.Errorf("validate sandbox: %w", err)
	}
	if err := validateMemory(&cfg.Memory); err != nil {
		return fmt.Errorf("validate memory: %w", err)
	}
	if err := ValidateOpenRouterReasoning(&cfg.LLM.OpenRouter.Reasoning); err != nil {
		return fmt.Errorf("validate openrouter reasoning: %w", err)
	}
	return nil
}

// ValidateOpenRouterReasoning validates the OpenRouter reasoning config fields.
func ValidateOpenRouterReasoning(r *OpenRouterReasoningCfg) error {
	if r.Effort != "" && r.MaxTokens > 0 {
		return fmt.Errorf("effort and max_tokens are mutually exclusive")
	}
	if r.Effort != "" {
		valid := map[string]bool{
			"xhigh": true, "high": true, "medium": true,
			"low": true, "minimal": true, "none": true,
		}
		if !valid[r.Effort] {
			return fmt.Errorf("invalid effort %q, must be one of: xhigh, high, medium, low, minimal, none", r.Effort)
		}
	}
	if r.MaxTokens < 0 {
		return fmt.Errorf("max_tokens must be >= 0, got %d", r.MaxTokens)
	}
	return nil
}

func validateMemory(cfg *MemoryConfig) error {
	if cfg.RetentionDays < 0 {
		return fmt.Errorf("memory.retention_days must be >= 0, got %d", cfg.RetentionDays)
	}
	if cfg.MaxConversations < 0 {
		return fmt.Errorf("memory.max_conversations must be >= 0, got %d", cfg.MaxConversations)
	}
	if cfg.CleanupInterval != "" {
		if _, err := time.ParseDuration(cfg.CleanupInterval); err != nil {
			return fmt.Errorf("memory.cleanup_interval: %w", err)
		}
	}
	return nil
}

// validWebSearchProviders is the set of supported web search provider names.
var validWebSearchProviders = map[string]bool{
	"duckduckgo": true,
	"tavily":     true,
}

func validateCostLimits(cfg *Config) error {
	if err := validateGlobalCostLimits(&cfg.LLM); err != nil {
		return err
	}
	for _, p := range cfg.LLM.Providers {
		if err := validateProviderCostLimits(p); err != nil {
			return err
		}
		if err := validateProviderModelPrices(p.Name, p.ModelPrices); err != nil {
			return err
		}
	}
	for _, a := range cfg.Agents {
		if err := validateAgentCostLimits(a, cfg.LLM.CostLimitSoft, cfg.LLM.CostLimitHard); err != nil {
			return err
		}
		if len(a.Fallbacks) > 0 {
			if err := validateFallbacks(a.Fallbacks); err != nil {
				return fmt.Errorf("config: agents[%s]: %w", a.Name, err)
			}
		}
	}
	return nil
}

func validateGlobalCostLimits(llm *LLMConfig) error {
	if llm.CostLimitSoft < 0 {
		return fmt.Errorf("config: llm.cost_limit_soft must be non-negative, got %.2f", llm.CostLimitSoft)
	}
	if llm.CostLimitHard < 0 {
		return fmt.Errorf("config: llm.cost_limit_hard must be non-negative, got %.2f", llm.CostLimitHard)
	}
	if llm.CostLimitSoft > 0 && llm.CostLimitHard > 0 && llm.CostLimitSoft > llm.CostLimitHard {
		return fmt.Errorf("config: llm.cost_limit_soft ($%.2f) must not exceed cost_limit_hard ($%.2f)", llm.CostLimitSoft, llm.CostLimitHard)
	}
	return nil
}

func validateAgentCostLimits(a AgentInstanceConfig, globalSoft, globalHard float64) error {
	if a.CostLimitSoft != nil && *a.CostLimitSoft < 0 {
		return fmt.Errorf("config: agent %q: cost_limit_soft must be non-negative", a.Name)
	}
	if a.CostLimitHard != nil && *a.CostLimitHard < 0 {
		return fmt.Errorf("config: agent %q: cost_limit_hard must be non-negative", a.Name)
	}
	soft, hard := globalSoft, globalHard
	if a.CostLimitSoft != nil {
		soft = *a.CostLimitSoft
	}
	if a.CostLimitHard != nil {
		hard = *a.CostLimitHard
	}
	if soft > 0 && hard > 0 && soft > hard {
		return fmt.Errorf("config: agent %q: cost_limit_soft ($%.2f) must not exceed cost_limit_hard ($%.2f)", a.Name, soft, hard)
	}
	return nil
}

func validateProviderCostLimits(p ProviderInstanceConfig) error {
	if p.CostLimitSoft != nil && *p.CostLimitSoft < 0 {
		return fmt.Errorf("config: provider %q: cost_limit_soft must be non-negative", p.Name)
	}
	if p.CostLimitHard != nil && *p.CostLimitHard < 0 {
		return fmt.Errorf("config: provider %q: cost_limit_hard must be non-negative", p.Name)
	}
	if p.CostLimitSoft != nil && p.CostLimitHard != nil &&
		*p.CostLimitSoft > 0 && *p.CostLimitHard > 0 &&
		*p.CostLimitSoft > *p.CostLimitHard {
		return fmt.Errorf("config: provider %q: cost_limit_soft ($%.2f) must not exceed cost_limit_hard ($%.2f)",
			p.Name, *p.CostLimitSoft, *p.CostLimitHard)
	}
	if p.DefaultRatePerKTokens != nil && *p.DefaultRatePerKTokens < 0 {
		return fmt.Errorf("config: provider %q: default_rate_per_1k_tokens must be non-negative", p.Name)
	}
	return nil
}

func validateProviderModelPrices(provider string, prices map[string]ModelPriceConfig) error {
	for model, mp := range prices {
		if mp.InputPerMTok < 0 || mp.OutputPerMTok < 0 || mp.CachedInputPerMTok < 0 {
			return fmt.Errorf("config: provider %q model %q: rates must be non-negative", provider, model)
		}
	}
	return nil
}

func validateWeb(w *WebConfig) error {
	if w.Enabled != nil && !*w.Enabled {
		return nil
	}
	if !validWebSearchProviders[w.Search.Provider] {
		return fmt.Errorf("config: web.search.provider: unsupported provider %q — must be one of: duckduckgo, tavily", w.Search.Provider)
	}
	if w.Search.Provider == "tavily" && w.Search.APIKey == "" {
		return fmt.Errorf("config: web.search.api_key is required when using tavily provider")
	}
	if w.Search.MaxResults < 1 || w.Search.MaxResults > 20 {
		return fmt.Errorf("config: web.search.max_results must be between 1 and 20, got %d", w.Search.MaxResults)
	}
	return nil
}

func validateSandbox(s *SandboxConfig) error {
	switch s.Runtime {
	case "docker", "kubernetes":
		// valid
	default:
		return fmt.Errorf("config: sandbox.runtime: invalid value %q — must be \"docker\" or \"kubernetes\"", s.Runtime)
	}
	return nil
}

// validateAgents checks all agent instance entries. Returns the set of valid
// agent names for cross-referencing by other validators.
func validateAgents(agents []AgentInstanceConfig) (map[string]bool, error) {
	if len(agents) == 0 {
		return map[string]bool{}, nil
	}

	names := make(map[string]bool, len(agents))
	wildcards := make(map[string]string) // adapter → agent name (for conflict detection)

	for i, a := range agents {
		if a.Name == "" {
			return nil, fmt.Errorf("config: agents[%d]: name is required", i)
		}
		if names[a.Name] {
			return nil, fmt.Errorf("config: agents[%d]: duplicate agent name %q", i, a.Name)
		}
		names[a.Name] = true

		if a.PersonaDir == "" {
			return nil, fmt.Errorf("config: agent %q: persona_dir is required", a.Name)
		}

		if a.SessionTier != "" {
			if err := validateTier(a.SessionTier, fmt.Sprintf("agent %q: session_tier", a.Name)); err != nil {
				return nil, err
			}
		}

		if a.MaxContextMessages < 0 {
			return nil, fmt.Errorf("config: agent %q: max_context_messages must be >= 0 (0 = default)", a.Name)
		}

		if a.MaxToolRounds < 0 {
			return nil, fmt.Errorf("config: agent %q: max_tool_rounds must be >= 0 (0 = default)", a.Name)
		}

		if err := validateAgentBindings(a, wildcards); err != nil {
			return nil, err
		}
	}

	if err := validateSupervisorRefs(agents, names); err != nil {
		return nil, err
	}

	return names, nil
}

// validateAgentBindings checks adapter bindings for a single agent.
func validateAgentBindings(a AgentInstanceConfig, wildcards map[string]string) error {
	for _, binding := range a.Adapters {
		if binding == "" {
			return fmt.Errorf("config: agent %q: empty adapter binding", a.Name)
		}
		if !strings.Contains(binding, ":") {
			if prev, ok := wildcards[binding]; ok {
				return fmt.Errorf("config: agent %q: wildcard binding %q conflicts with agent %q", a.Name, binding, prev)
			}
			wildcards[binding] = a.Name
		}
	}
	return nil
}

// validateSupervisorRefs checks supervisor field references across agents.
func validateSupervisorRefs(agents []AgentInstanceConfig, names map[string]bool) error {
	agentByName := make(map[string]AgentInstanceConfig, len(agents))
	for _, a := range agents {
		agentByName[a.Name] = a
	}
	for _, a := range agents {
		if a.Supervisor == "" {
			continue
		}
		effectiveTier := a.SessionTier
		if effectiveTier == "" {
			effectiveTier = "autonomous" // default tier
		}
		if effectiveTier != "supervised" {
			return fmt.Errorf("config: agent %q: supervisor is only meaningful when session_tier = \"supervised\"", a.Name)
		}
		if !names[a.Supervisor] {
			return fmt.Errorf("config: agent %q: supervisor %q not found", a.Name, a.Supervisor)
		}
		if a.Supervisor == a.Name {
			return fmt.Errorf("config: agent %q: cannot supervise itself", a.Name)
		}
		sup := agentByName[a.Supervisor]
		if sup.Supervisor != "" {
			return fmt.Errorf("config: agent %q: supervisor %q itself has a supervisor — chaining is not supported", a.Name, a.Supervisor)
		}
		supTier := sup.SessionTier
		if supTier == "" {
			supTier = "autonomous"
		}
		if supTier == "supervised" {
			return fmt.Errorf("config: agent %q: supervisor %q must not use session_tier \"supervised\" (would deadlock)", a.Name, a.Supervisor)
		}
	}
	return nil
}

// validateAgentRouting validates agents, channels, and schedules together,
// since channels and schedules both reference agent names.
func validateAgentRouting(cfg *Config) error {
	agentNames, err := validateAgents(cfg.Agents)
	if err != nil {
		return fmt.Errorf("validate agents: %w", err)
	}
	if err := validateChannels(cfg.Channels, agentNames); err != nil {
		return fmt.Errorf("validate channels: %w", err)
	}
	channelNames := make(map[string]bool, len(cfg.Channels))
	for _, ch := range cfg.Channels {
		channelNames[ch.Name] = true
	}
	if err := validateSchedules(cfg.Schedules, agentNames, channelNames); err != nil {
		return fmt.Errorf("validate schedules: %w", err)
	}
	return nil
}

func validateChannels(channels []ChannelConfig, agentNames map[string]bool) error {
	names := make(map[string]bool, len(channels))
	specifics := make(map[string]string) // "adapter:externalID" → channel name
	wildcards := make(map[string]string) // "adapter" → channel name

	for i, ch := range channels {
		if ch.Name == "" {
			return fmt.Errorf("config: channels[%d]: name is required", i)
		}
		if names[ch.Name] {
			return fmt.Errorf("config: channels[%d]: duplicate channel name %q", i, ch.Name)
		}
		names[ch.Name] = true

		if ch.Agent == "" {
			return fmt.Errorf("config: channel %q: agent is required", ch.Name)
		}
		if !agentNames[ch.Agent] {
			return fmt.Errorf("config: channel %q: agent %q not found", ch.Name, ch.Agent)
		}

		if ch.Delivery != "" && ch.Delivery != "single" && ch.Delivery != "broadcast" {
			return fmt.Errorf("config: channel %q: delivery must be \"single\" or \"broadcast\"", ch.Name)
		}

		if ch.SessionMode != "" && ch.SessionMode != "persistent" && ch.SessionMode != "ephemeral" {
			return fmt.Errorf("config: channel %q: session_mode must be \"persistent\" or \"ephemeral\"", ch.Name)
		}

		if err := validateChannelBindings(ch, specifics, wildcards); err != nil {
			return err
		}
	}
	return nil
}

func validateChannelBindings(ch ChannelConfig, specifics, wildcards map[string]string) error {
	var specificCount int
	for _, binding := range ch.Adapters {
		if binding == "" {
			return fmt.Errorf("config: channel %q: empty adapter binding", ch.Name)
		}
		if strings.Contains(binding, ":") {
			specificCount++
			if prev, ok := specifics[binding]; ok {
				return fmt.Errorf("config: channel %q: adapter binding %q conflicts with channel %q", ch.Name, binding, prev)
			}
			specifics[binding] = ch.Name
		} else {
			if prev, ok := wildcards[binding]; ok {
				return fmt.Errorf("config: channel %q: wildcard binding %q conflicts with channel %q", ch.Name, binding, prev)
			}
			wildcards[binding] = ch.Name
		}
	}

	if ch.SessionMode == "ephemeral" && specificCount > 1 {
		return fmt.Errorf("config: channel %q: ephemeral channels cannot have multiple specific adapter bindings", ch.Name)
	}
	return nil
}

// validTTSVoices is the set of supported OpenAI TTS voice IDs.
var validTTSVoices = map[string]bool{
	"alloy": true, "echo": true, "fable": true,
	"onyx": true, "nova": true, "shimmer": true,
}

func validateVoice(v *VoiceConfig) error {
	if v.STTProvider != "" && v.STTProvider != "openai" {
		return fmt.Errorf("config: voice.stt_provider: unsupported provider %q — only \"openai\" is supported", v.STTProvider)
	}
	if v.TTSProvider != "" && v.TTSProvider != "openai" {
		return fmt.Errorf("config: voice.tts_provider: unsupported provider %q — only \"openai\" is supported", v.TTSProvider)
	}
	if (v.STTProvider == "openai" || v.TTSProvider == "openai") && v.OpenAI.APIKey == "" {
		return fmt.Errorf("config: voice.openai.api_key is required when using OpenAI voice providers")
	}
	if v.TTSProvider != "" && !validTTSVoices[v.TTSVoice] {
		return fmt.Errorf("config: voice.tts_voice: invalid voice %q — must be one of: alloy, echo, fable, onyx, nova, shimmer", v.TTSVoice)
	}
	return nil
}

// ValidateFallbacks checks that each fallback rule has valid trigger/action
// combinations and all required fields for its action type.
func ValidateFallbacks(fallbacks []FallbackConfig) error {
	return validateFallbacks(fallbacks)
}

func validateFallbacks(fallbacks []FallbackConfig) error {
	for i, f := range fallbacks {
		if err := validateFallback(i, f); err != nil {
			return err
		}
	}
	return nil
}

func validateFallback(i int, f FallbackConfig) error {
	switch f.Trigger {
	case "error", "rate_limit", "cost_limit":
	case "low_funds":
		// Tolerated at validation time — applyDefaults migrates these to
		// cost_limit before they ever reach the runtime. We accept the
		// legacy value so REST PATCH bodies posted by old clients still
		// pass validation.
	default:
		return fmt.Errorf("config: llm.fallback[%d]: invalid trigger %q", i, f.Trigger)
	}
	switch f.Action {
	case "switch_provider":
		if f.Provider == "" {
			return fmt.Errorf("config: llm.fallback[%d]: action \"switch_provider\" requires provider field", i)
		}
	case "switch_model":
		if f.Model == "" {
			return fmt.Errorf("config: llm.fallback[%d]: action \"switch_model\" requires model field", i)
		}
	case "wait_and_retry":
		if f.MaxRetries <= 0 {
			return fmt.Errorf("config: llm.fallback[%d]: action \"wait_and_retry\" requires max_retries > 0", i)
		}
	default:
		return fmt.Errorf("config: llm.fallback[%d]: invalid action %q", i, f.Action)
	}
	if f.Trigger == "cost_limit" {
		if err := validateFallbackScope(i, f.Scope); err != nil {
			return err
		}
	}
	if f.Backoff != "" && f.Backoff != "exponential" && f.Backoff != "constant" {
		return fmt.Errorf("config: llm.fallback[%d]: invalid backoff %q — must be \"exponential\" or \"constant\"", i, f.Backoff)
	}
	return nil
}

func validateFallbackScope(i int, scope string) error {
	switch scope {
	case "soft", "hard":
		return nil
	case "":
		return fmt.Errorf("config: llm.fallback[%d]: trigger \"cost_limit\" requires scope (\"soft\" or \"hard\")", i)
	default:
		return fmt.Errorf("config: llm.fallback[%d]: invalid scope %q — must be \"soft\" or \"hard\"", i, scope)
	}
}

// validateSchedules checks all schedule entries for structural correctness.
// Expression format validation (cron syntax, duration strings) is intentionally
// deferred to the scheduler at startup, keeping the config and scheduler packages
// independent.
func validateSchedules(schedules []ScheduleConfig, agentNames map[string]bool, channelNames map[string]bool) error {
	names := make(map[string]bool, len(schedules))
	for i, s := range schedules {
		if s.Name == "" {
			return fmt.Errorf("config: schedules[%d]: name is required", i)
		}
		if names[s.Name] {
			return fmt.Errorf("config: schedules[%d]: duplicate schedule name %q", i, s.Name)
		}
		names[s.Name] = true

		if s.Type == "" {
			return fmt.Errorf("config: schedule %q: type is required (must be \"system\" or \"agent\")", s.Name)
		}
		switch s.Type {
		case "system", "agent":
			// valid
		default:
			return fmt.Errorf("config: schedule %q: invalid type %q — must be \"system\" or \"agent\"", s.Name, s.Type)
		}

		if s.Schedule == "" {
			return fmt.Errorf("config: schedule %q: schedule expression is required", s.Name)
		}

		if s.SessionTier != "" {
			if err := validateTier(s.SessionTier, fmt.Sprintf("schedule %q: session_tier", s.Name)); err != nil {
				return err
			}
		}

		if s.SessionMode != "" {
			switch s.SessionMode {
			case "shared", "isolated":
				// valid
			default:
				return fmt.Errorf(
					"config: schedule %q: invalid session_mode %q — must be \"shared\" or \"isolated\"",
					s.Name, s.SessionMode,
				)
			}
		}

		if s.Agent != "" && !agentNames[s.Agent] {
			return fmt.Errorf("config: schedule %q: agent %q does not exist", s.Name, s.Agent)
		}

		if err := validateScheduleChannel(s.Name, s.Channel, channelNames); err != nil {
			return err
		}
	}
	return nil
}

// ParseChannel splits a channel string like "telegram:123456" into adapter
// name and external ID. Returns false if the format is invalid.
func ParseChannel(channel string) (adapterName, externalID string, ok bool) {
	idx := strings.IndexByte(channel, ':')
	if idx <= 0 || idx == len(channel)-1 {
		return "", "", false
	}
	return channel[:idx], channel[idx+1:], true
}

// IsChannelRef returns true when the channel string uses the @-prefix
// named channel syntax (e.g. "@work").
func IsChannelRef(channel string) bool {
	return strings.HasPrefix(channel, "@")
}

// ParseChannelRef extracts the channel name from an @-prefixed reference.
// Returns the name and true, or ("", false) if the string is not a channel ref.
func ParseChannelRef(channel string) (name string, ok bool) {
	if !IsChannelRef(channel) {
		return "", false
	}
	name = channel[1:]
	if name == "" {
		return "", false
	}
	return name, true
}

// validateScheduleChannel validates the channel format and cross-validates
// @channelname references against configured channels.
func validateScheduleChannel(scheduleName, channel string, channelNames map[string]bool) error {
	if err := validateChannel(scheduleName, channel); err != nil {
		return err
	}
	if name, ok := ParseChannelRef(channel); ok {
		if !channelNames[name] {
			return fmt.Errorf(
				"config: schedule %q: channel reference @%s does not match any configured [[channels]]",
				scheduleName, name,
			)
		}
	}
	return nil
}

func validateChannel(scheduleName, channel string) error {
	if channel == "" {
		return nil
	}
	// Accept @channelname references — cross-validation happens in validateSchedules.
	if IsChannelRef(channel) {
		if _, ok := ParseChannelRef(channel); !ok {
			return fmt.Errorf(
				"config: schedule %q: channel %q is an invalid channel reference (use \"@channelname\")",
				scheduleName, channel,
			)
		}
		return nil
	}
	if _, _, ok := ParseChannel(channel); !ok {
		return fmt.Errorf(
			"config: schedule %q: channel %q is not in adapter:externalID or @channelname format",
			scheduleName, channel,
		)
	}
	return nil
}

// validAPIScopes delegates to the canonical scope list so that config
// validation and the API server can never drift apart.
var validAPIScopes = scope.Valid

func validateAPI(api *APIConfig) error {
	if !api.IsEnabled() {
		return nil
	}
	if api.TLS {
		if api.CertFile == "" {
			return fmt.Errorf("config: api.cert_file is required when api.tls is true")
		}
		if api.KeyFile == "" {
			return fmt.Errorf("config: api.key_file is required when api.tls is true")
		}
	}
	if err := validateAPIKeys(api.Keys); err != nil {
		return err
	}
	if api.WebSocketReplayBufferTTL != "" {
		if _, err := time.ParseDuration(api.WebSocketReplayBufferTTL); err != nil {
			return fmt.Errorf("config: api.websocket_replay_buffer_ttl: invalid duration %q: %w", api.WebSocketReplayBufferTTL, err)
		}
	}
	if err := validateTimezone(api.Timezone); err != nil {
		return err
	}
	if err := validateAuth(&api.Auth); err != nil {
		return err
	}
	if err := validateMCPServer(&api.MCPServer); err != nil {
		return err
	}
	return nil
}

func validateMCPServer(m *APIMCPServerConfig) error {
	switch m.Transport {
	case "", "streamable", "sse":
	default:
		return fmt.Errorf("config: api.mcp_server.transport: must be \"streamable\" or \"sse\", got %q", m.Transport)
	}
	if m.SessionTimeout != "" {
		if _, err := time.ParseDuration(m.SessionTimeout); err != nil {
			return fmt.Errorf("config: api.mcp_server.session_timeout: invalid duration %q: %w", m.SessionTimeout, err)
		}
	}
	if m.ChatTimeout != "" {
		if _, err := time.ParseDuration(m.ChatTimeout); err != nil {
			return fmt.Errorf("config: api.mcp_server.chat_timeout: invalid duration %q: %w", m.ChatTimeout, err)
		}
	}
	return nil
}

func validateAPIKeys(keys []APIKeyConfig) error {
	names := make(map[string]bool, len(keys))
	for i, k := range keys {
		if k.Name == "" {
			return fmt.Errorf("config: api.keys[%d]: name is required", i)
		}
		if names[k.Name] {
			return fmt.Errorf("config: api.keys[%d]: duplicate key name %q", i, k.Name)
		}
		names[k.Name] = true
		if k.Key == "" {
			return fmt.Errorf("config: api.keys[%d] (%s): key is required", i, k.Name)
		}
		if len(k.Scopes) == 0 {
			return fmt.Errorf("config: api.keys[%d] (%s): at least one scope is required", i, k.Name)
		}
		for _, s := range k.Scopes {
			if _, ok := validAPIScopes[s]; !ok {
				return fmt.Errorf("config: api.keys[%d] (%s): invalid scope %q", i, k.Name, s)
			}
		}
	}
	return nil
}

func validateTimezone(tz string) error {
	if tz == "" {
		return nil
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return fmt.Errorf("config: api.timezone: invalid IANA timezone %q: %w", tz, err)
	}
	return nil
}

func validateAuth(auth *APIAuthConfig) error {
	hasPassword := auth.PasswordHash != ""
	hasOIDC := auth.OIDC.Enabled

	if !hasPassword && !hasOIDC {
		return nil // no auth configured — API key only
	}

	// Session secret required when either auth method is active.
	if auth.SessionSecret == "" {
		return fmt.Errorf("config: api.auth.session_secret is required when password or OIDC auth is configured")
	}

	if hasPassword {
		if !strings.HasPrefix(auth.PasswordHash, "$2a$") && !strings.HasPrefix(auth.PasswordHash, "$2b$") {
			return fmt.Errorf("config: api.auth.password_hash must be a bcrypt hash (starts with $2a$ or $2b$)")
		}
	}

	if hasOIDC {
		o := &auth.OIDC
		if o.Issuer == "" {
			return fmt.Errorf("config: api.auth.oidc.issuer is required when OIDC is enabled")
		}
		if o.ClientID == "" {
			return fmt.Errorf("config: api.auth.oidc.client_id is required when OIDC is enabled")
		}
		if o.ClientSecret == "" {
			return fmt.Errorf("config: api.auth.oidc.client_secret is required when OIDC is enabled")
		}
		if o.RedirectURL == "" {
			return fmt.Errorf("config: api.auth.oidc.redirect_url is required when OIDC is enabled")
		}
		if len(o.AllowedEmails) == 0 {
			return fmt.Errorf("config: api.auth.oidc.allowed_emails must not be empty when OIDC is enabled")
		}
	}

	if _, err := time.ParseDuration(auth.SessionMaxAge); err != nil {
		return fmt.Errorf("config: api.auth.session_max_age: invalid duration %q: %w", auth.SessionMaxAge, err)
	}

	return nil
}

func validateMCP(mcp *MCPConfig) error {
	if mcp.RequestTimeoutSecs < 0 {
		return fmt.Errorf("config: mcp.request_timeout_secs must be non-negative")
	}
	if mcp.SSEKeepAliveSecs < 0 {
		return fmt.Errorf("config: mcp.sse_keep_alive_secs must be non-negative")
	}
	if mcp.MaxRestartAttempts < 0 {
		return fmt.Errorf("config: mcp.max_restart_attempts must be non-negative")
	}
	if mcp.RestartCooldown != "" {
		if _, err := time.ParseDuration(mcp.RestartCooldown); err != nil {
			return fmt.Errorf("config: mcp.restart_cooldown: invalid duration %q: %w", mcp.RestartCooldown, err)
		}
	}
	if mcp.InitRetryAttempts < 1 {
		return fmt.Errorf("config: mcp.init_retry_attempts must be at least 1 (set to 1 to disable retries)")
	}
	if mcp.InitRetryBackoff != "" {
		if _, err := time.ParseDuration(mcp.InitRetryBackoff); err != nil {
			return fmt.Errorf("config: mcp.init_retry_backoff: invalid duration %q: %w", mcp.InitRetryBackoff, err)
		}
	}
	return nil
}

func validateTools(cfg *Config) {
	for name, tc := range cfg.Tools {
		if !tc.IsEnabled() {
			continue
		}
		if err := validateToolConfig(name, tc); err != nil {
			if cfg.ToolWarnings == nil {
				cfg.ToolWarnings = make(map[string]string)
			}
			cfg.ToolWarnings[name] = err.Error()
			v := false
			tc.Enabled = &v
			cfg.Tools[name] = tc
		}
	}
}

func validateToolConfig(name string, tc ToolConfig) error {
	if err := validateToolTransport(name, tc); err != nil {
		return err
	}
	return validateToolAuth(name, tc)
}

func validateToolTransport(name string, tc ToolConfig) error {
	transport := tc.Transport
	if transport == "" {
		transport = "stdio"
	}
	switch transport {
	case "stdio":
		if tc.Command == "" {
			return fmt.Errorf("config: tools.%s: command is required for stdio transport", name)
		}
		if tc.URL != "" {
			return fmt.Errorf("config: tools.%s: url must be empty for stdio transport", name)
		}
		if len(tc.Headers) > 0 {
			return fmt.Errorf("config: tools.%s: headers are not supported for stdio transport", name)
		}
		if tc.Auth != "" {
			return fmt.Errorf("config: tools.%s: auth is only supported for sse transport", name)
		}
	case "sse":
		if tc.URL == "" {
			return fmt.Errorf("config: tools.%s: url is required for sse transport", name)
		}
		if tc.Command != "" {
			return fmt.Errorf("config: tools.%s: command must be empty for sse transport", name)
		}
		if len(tc.Args) > 0 {
			return fmt.Errorf("config: tools.%s: args must be empty for sse transport", name)
		}
	default:
		return fmt.Errorf("config: tools.%s: unsupported transport %q (must be \"stdio\" or \"sse\")", name, transport)
	}
	return nil
}

func validateToolAuth(name string, tc ToolConfig) error {
	switch tc.Auth {
	case "", "oauth":
	default:
		return fmt.Errorf("config: tools.%s: unsupported auth %q (must be \"\" or \"oauth\")", name, tc.Auth)
	}
	if tc.Auth == "oauth" {
		hasID := tc.ClientID != ""
		hasSecret := tc.ClientSecret != ""
		if hasID != hasSecret {
			return fmt.Errorf("config: tools.%s: client_id and client_secret must both be set or both empty", name)
		}
	}
	if tc.Auth != "oauth" {
		if tc.ClientID != "" || tc.ClientSecret != "" {
			return fmt.Errorf("config: tools.%s: client_id and client_secret require auth = \"oauth\"", name)
		}
		if len(tc.Scopes) > 0 {
			return fmt.Errorf("config: tools.%s: scopes require auth = \"oauth\"", name)
		}
	}
	return nil
}

// validPluginTypes are the recognised type values for plugins.
var validPluginTypes = map[string]bool{"subprocess": true, "docker": true}

func validatePlugins(plugins map[string]PluginConfig, tools map[string]ToolConfig) error {
	for name, pc := range plugins {
		if pc.Type == "" {
			return fmt.Errorf("config: plugins.%s: type is required (\"subprocess\" or \"docker\")", name)
		}
		if !validPluginTypes[pc.Type] {
			return fmt.Errorf("config: plugins.%s: invalid type %q", name, pc.Type)
		}
		if pc.Type == "docker" {
			if pc.Image == "" {
				return fmt.Errorf("config: plugins.%s: image is required for docker plugins", name)
			}
		} else {
			if pc.Command == "" {
				return fmt.Errorf("config: plugins.%s: command is required", name)
			}
		}
		if _, exists := tools[name]; exists {
			return fmt.Errorf("config: plugins.%s: name conflicts with tools.%s", name, name)
		}
	}
	return nil
}

func DefaultConfigPath() string {
	return filepath.Join(defaultDataDir(), "denkeeper.toml")
}

// DefaultDBPath returns the default path for the SQLite database.
func DefaultDBPath() string {
	return filepath.Join(defaultDataDir(), "data", "memory.db")
}

// defaultDataDir returns the data directory from DENKEEPER_DATA_DIR or ~/.denkeeper.
func defaultDataDir() string {
	if v := os.Getenv("DENKEEPER_DATA_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".denkeeper")
}
