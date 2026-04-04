package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/Temikus/denkeeper/internal/scope"
)

type Config struct {
	Telegram  TelegramConfig          `toml:"telegram"`
	Discord   DiscordConfig           `toml:"discord"`
	LLM       LLMConfig               `toml:"llm"`
	Memory    MemoryConfig            `toml:"memory"`
	Log       LogConfig               `toml:"log"`
	Agent     AgentConfig             `toml:"agent"`
	Session   SessionConfig           `toml:"session"`
	Agents    []AgentInstanceConfig   `toml:"agents"`
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
	// Enabled controls whether the API server starts. Default: false.
	Enabled bool `toml:"enabled"`

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
	// Valid scopes: "chat", "sessions:read", "costs:read", "skills:read",
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

	// LLMModel overrides the global default_model for this agent.
	LLMModel string `toml:"llm_model"`

	// SessionTier overrides the global session.tier for this agent.
	SessionTier string `toml:"session_tier"`

	// BrowserURLAllowlist overrides the global browser URL allowlist for this agent.
	// If set, only these domains are reachable. Supports wildcards: "*.example.com".
	BrowserURLAllowlist []string `toml:"browser_url_allowlist"`
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

// ToolConfig defines an MCP tool server to spawn.
type ToolConfig struct {
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
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
	DefaultProvider   string           `toml:"default_provider"`
	DefaultModel      string           `toml:"default_model"`
	OpenRouter        OpenRouterConfig `toml:"openrouter"`
	Ollama            OllamaConfig     `toml:"ollama"`
	Anthropic         AnthropicConfig  `toml:"anthropic"`
	OpenAI            OpenAIConfig     `toml:"openai"`
	MaxCostPerSession float64          `toml:"max_cost_per_session"`
	Fallbacks         []FallbackConfig `toml:"fallback"`
}

// AnthropicConfig configures the Anthropic direct LLM provider.
type AnthropicConfig struct {
	// APIKey is the Anthropic API key (sk-ant-...). Required to enable the provider.
	APIKey string `toml:"api_key"`
	// BaseURL overrides the default API endpoint. Useful for Bedrock/Vertex proxies.
	BaseURL string `toml:"base_url"`
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
	Trigger    string  `toml:"trigger"`     // "error" | "rate_limit" | "low_funds"
	Action     string  `toml:"action"`      // "switch_provider" | "switch_model" | "wait_and_retry"
	Provider   string  `toml:"provider"`    // required for switch_provider
	Model      string  `toml:"model"`       // required for switch_model; optional for switch_provider
	Threshold  float64 `toml:"threshold"`   // required for low_funds (USD remaining)
	MaxRetries int     `toml:"max_retries"` // required for wait_and_retry
	Backoff    string  `toml:"backoff"`     // "exponential" (default) | "constant"
}

type OpenRouterConfig struct {
	APIKey string `toml:"api_key"`
}

// OllamaConfig configures the local Ollama LLM provider.
type OllamaConfig struct {
	// BaseURL is the Ollama server address. Defaults to http://localhost:11434.
	BaseURL string `toml:"base_url"`
}

type MemoryConfig struct {
	DBPath string `toml:"db_path"`
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
	applyScalarDefaults(cfg)
	applyLLMDefaults(cfg)
	applyEnvOverrides(cfg)
	expandEnvVars(cfg)
	applyMiscDefaults(cfg)
	synthesizeDefaultAgent(cfg)
	applyAgentDefaults(cfg)
	applyScheduleDefaults(cfg)
}

func applyScalarDefaults(cfg *Config) {
	if cfg.Memory.DBPath == "" {
		home, _ := os.UserHomeDir()
		cfg.Memory.DBPath = filepath.Join(home, ".denkeeper", "data", "memory.db")
	}
	if cfg.Agent.PersonaDir == "" {
		home, _ := os.UserHomeDir()
		cfg.Agent.PersonaDir = filepath.Join(home, ".denkeeper", "agents", "default")
	}
	if cfg.Agent.SkillsDir == "" {
		home, _ := os.UserHomeDir()
		cfg.Agent.SkillsDir = filepath.Join(home, ".denkeeper", "skills")
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
	}
	if cfg.Session.Tier == "" {
		cfg.Session.Tier = "supervised"
	}
	if cfg.Sandbox.Runtime == "" {
		cfg.Sandbox.Runtime = "docker"
	}
	if cfg.Sandbox.Kubernetes.Namespace == "" {
		cfg.Sandbox.Kubernetes.Namespace = "denkeeper-sandboxes"
	}
	if cfg.OTel.ServiceName == "" {
		cfg.OTel.ServiceName = "denkeeper"
	}
}

func applyLLMDefaults(cfg *Config) {
	if cfg.LLM.DefaultProvider == "" {
		cfg.LLM.DefaultProvider = "openrouter"
	}
	if cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = "anthropic/claude-sonnet-4-20250514"
	}
	if cfg.LLM.MaxCostPerSession == 0 {
		cfg.LLM.MaxCostPerSession = 1.0
	}
	for i := range cfg.LLM.Fallbacks {
		if cfg.LLM.Fallbacks[i].Backoff == "" {
			cfg.LLM.Fallbacks[i].Backoff = "exponential"
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

func applyEnvOverrides(cfg *Config) {
	envOverride("DENKEEPER_TELEGRAM_TOKEN", &cfg.Telegram.Token)
	envOverride("DENKEEPER_DISCORD_TOKEN", &cfg.Discord.Token)
	envOverride("DENKEEPER_LLM_PROVIDER", &cfg.LLM.DefaultProvider)
	envOverride("DENKEEPER_LLM_MODEL", &cfg.LLM.DefaultModel)
	envOverride("DENKEEPER_LLM_OPENROUTER_API_KEY", &cfg.LLM.OpenRouter.APIKey)
	envOverride("DENKEEPER_LLM_ANTHROPIC_API_KEY", &cfg.LLM.Anthropic.APIKey)
	envOverride("DENKEEPER_LLM_ANTHROPIC_BASE_URL", &cfg.LLM.Anthropic.BaseURL)
	envOverride("DENKEEPER_LLM_OLLAMA_BASE_URL", &cfg.LLM.Ollama.BaseURL)
	envOverride("DENKEEPER_LLM_OPENAI_API_KEY", &cfg.LLM.OpenAI.APIKey)
	envOverride("DENKEEPER_LLM_OPENAI_BASE_URL", &cfg.LLM.OpenAI.BaseURL)
	envOverride("DENKEEPER_VOICE_OPENAI_API_KEY", &cfg.Voice.OpenAI.APIKey)
	envOverride("DENKEEPER_LOG_LEVEL", &cfg.Log.Level)
	envOverride("DENKEEPER_LOG_FORMAT", &cfg.Log.Format)
	envOverride("DENKEEPER_MEMORY_DB_PATH", &cfg.Memory.DBPath)
	envOverride("DENKEEPER_API_LISTEN", &cfg.API.Listen)
	envOverride("DENKEEPER_SESSION_TIER", &cfg.Session.Tier)
	envOverride("DENKEEPER_SEARCH_API_KEY", &cfg.Web.Search.APIKey)
	envOverride("DENKEEPER_OTEL_TRACES_ENDPOINT", &cfg.OTel.TracesEndpoint)
	envOverride("DENKEEPER_API_AUTH_SESSION_SECRET", &cfg.API.Auth.SessionSecret)
	envOverride("DENKEEPER_OIDC_CLIENT_ID", &cfg.API.Auth.OIDC.ClientID)
	envOverride("DENKEEPER_OIDC_CLIENT_SECRET", &cfg.API.Auth.OIDC.ClientSecret)

	if v := os.Getenv("DENKEEPER_API_ENABLED"); v == "true" || v == "1" {
		cfg.API.Enabled = true
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
	if cfg.API.Enabled && cfg.API.Listen == "" {
		cfg.API.Listen = ":8080"
	}
	applyAuthDefaults(cfg)
	applyWebDefaults(cfg)
	applyBrowserDefaults(cfg)
}

func applyAuthDefaults(cfg *Config) {
	if cfg.API.Auth.SessionMaxAge == "" {
		cfg.API.Auth.SessionMaxAge = "24h"
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
// if no [[agents]] defined, synthesize a single "default" agent from the
// legacy [agent]/[session] config.
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
		defaultAdapters = []string{"telegram"} // placeholder; validated later
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
			home, _ := os.UserHomeDir()
			a.PersonaDir = filepath.Join(home, ".denkeeper", "agents", a.Name)
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
		if s.Agent == "" {
			s.Agent = "default"
		}
	}
}

// validTiers is the set of recognised permission tier names.
var validTiers = map[string]bool{
	"supervised": true,
	"autonomous": true,
	"restricted": true,
}

func validateTier(tier, context string) error {
	if !validTiers[tier] {
		return fmt.Errorf("config: %s: invalid tier %q — must be one of: supervised, autonomous, restricted", context, tier)
	}
	return nil
}

// needsOpenRouter reports whether the config references the openrouter provider
// in either the default provider or any fallback rule, meaning an API key is required.
func needsOpenRouter(cfg *Config) bool {
	if cfg.LLM.DefaultProvider == "openrouter" {
		return true
	}
	for _, f := range cfg.LLM.Fallbacks {
		if f.Provider == "openrouter" {
			return true
		}
	}
	return false
}

// needsAnthropic reports whether the config's default provider is anthropic.
func needsAnthropic(cfg *Config) bool {
	return cfg.LLM.DefaultProvider == "anthropic"
}

// needsOpenAI reports whether the config references the openai provider
// in either the default provider or any fallback rule, meaning an API key is required.
func needsOpenAI(cfg *Config) bool {
	if cfg.LLM.DefaultProvider == "openai" {
		return true
	}
	for _, f := range cfg.LLM.Fallbacks {
		if f.Provider == "openai" {
			return true
		}
	}
	return false
}

// validateAdaptersAndProviders checks adapter tokens, allowed-user lists, and LLM provider keys.
func validateAdaptersAndProviders(cfg *Config) error {
	if cfg.Telegram.Token != "" && len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("config: telegram.allowed_users must not be empty when telegram.token is set (security requirement)")
	}
	if cfg.Telegram.Token == "" && cfg.Discord.Token == "" {
		return fmt.Errorf("config: at least one adapter must be configured (telegram.token or discord.token)")
	}
	if cfg.Discord.Token != "" && len(cfg.Discord.AllowedUsers) == 0 {
		return fmt.Errorf("config: discord.allowed_users must not be empty when discord.token is set (security requirement)")
	}
	if needsOpenRouter(cfg) && cfg.LLM.OpenRouter.APIKey == "" {
		return fmt.Errorf("config: llm.openrouter.api_key is required when using openrouter provider")
	}
	if needsAnthropic(cfg) && cfg.LLM.Anthropic.APIKey == "" {
		return fmt.Errorf("config: llm.anthropic.api_key is required when using anthropic provider")
	}
	if needsOpenAI(cfg) && cfg.LLM.OpenAI.APIKey == "" {
		return fmt.Errorf("config: llm.openai.api_key is required when using openai provider")
	}
	return nil
}

func validate(cfg *Config) error {
	if err := validateAdaptersAndProviders(cfg); err != nil {
		return err
	}
	if err := validateTier(cfg.Session.Tier, "session.tier"); err != nil {
		return err
	}
	if err := validateFallbacks(cfg.LLM.Fallbacks); err != nil {
		return err
	}
	agentNames, err := validateAgents(cfg.Agents)
	if err != nil {
		return err
	}
	if err := validateSchedules(cfg.Schedules, agentNames); err != nil {
		return err
	}
	if err := validateTools(cfg.Tools); err != nil {
		return err
	}
	if err := validatePlugins(cfg.Plugins, cfg.Tools); err != nil {
		return err
	}
	if err := validateVoice(&cfg.Voice); err != nil {
		return err
	}
	if err := validateAPI(&cfg.API); err != nil {
		return err
	}
	if err := validateWeb(&cfg.Web); err != nil {
		return err
	}
	return validateSandbox(&cfg.Sandbox)
}

// validWebSearchProviders is the set of supported web search provider names.
var validWebSearchProviders = map[string]bool{
	"duckduckgo": true,
	"tavily":     true,
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
		return nil, fmt.Errorf("config: at least one agent must be defined")
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

		for _, binding := range a.Adapters {
			if binding == "" {
				return nil, fmt.Errorf("config: agent %q: empty adapter binding", a.Name)
			}
			// Check for conflicting wildcard bindings (e.g. two agents both claim "telegram").
			if !strings.Contains(binding, ":") {
				if prev, ok := wildcards[binding]; ok {
					return nil, fmt.Errorf("config: agent %q: wildcard binding %q conflicts with agent %q", a.Name, binding, prev)
				}
				wildcards[binding] = a.Name
			}
		}
	}

	if !names["default"] {
		return nil, fmt.Errorf("config: exactly one agent must be named \"default\"")
	}

	return names, nil
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

func validateFallbacks(fallbacks []FallbackConfig) error {
	for i, f := range fallbacks {
		switch f.Trigger {
		case "error", "rate_limit", "low_funds":
		default:
			return fmt.Errorf("config: llm.fallback[%d]: invalid trigger %q", i, f.Trigger)
		}
		switch f.Action {
		case "switch_provider", "switch_model", "wait_and_retry":
		default:
			return fmt.Errorf("config: llm.fallback[%d]: invalid action %q", i, f.Action)
		}
		if f.Action == "switch_provider" && f.Provider == "" {
			return fmt.Errorf("config: llm.fallback[%d]: action \"switch_provider\" requires provider field", i)
		}
		if f.Action == "switch_model" && f.Model == "" {
			return fmt.Errorf("config: llm.fallback[%d]: action \"switch_model\" requires model field", i)
		}
		if f.Action == "wait_and_retry" && f.MaxRetries <= 0 {
			return fmt.Errorf("config: llm.fallback[%d]: action \"wait_and_retry\" requires max_retries > 0", i)
		}
		if f.Trigger == "low_funds" && f.Threshold <= 0 {
			return fmt.Errorf("config: llm.fallback[%d]: trigger \"low_funds\" requires threshold > 0", i)
		}
		if f.Backoff != "" && f.Backoff != "exponential" && f.Backoff != "constant" {
			return fmt.Errorf("config: llm.fallback[%d]: invalid backoff %q — must be \"exponential\" or \"constant\"", i, f.Backoff)
		}
	}
	return nil
}

// validateSchedules checks all schedule entries for structural correctness.
// Expression format validation (cron syntax, duration strings) is intentionally
// deferred to the scheduler at startup, keeping the config and scheduler packages
// independent.
func validateSchedules(schedules []ScheduleConfig, agentNames map[string]bool) error {
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
	}
	return nil
}

// validAPIScopes delegates to the canonical scope list so that config
// validation and the API server can never drift apart.
var validAPIScopes = scope.Valid

func validateAPI(api *APIConfig) error {
	if !api.Enabled {
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
	names := make(map[string]bool, len(api.Keys))
	for i, k := range api.Keys {
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
	if err := validateAuth(&api.Auth); err != nil {
		return err
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

func validateTools(tools map[string]ToolConfig) error {
	for name, tc := range tools {
		if tc.Command == "" {
			return fmt.Errorf("config: tools.%s: command is required", name)
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
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".denkeeper", "denkeeper.toml")
}

// DefaultDBPath returns the default path for the SQLite database.
func DefaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".denkeeper", "data", "memory.db")
}
