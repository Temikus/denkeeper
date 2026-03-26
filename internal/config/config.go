package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Telegram  TelegramConfig        `toml:"telegram"`
	LLM       LLMConfig             `toml:"llm"`
	Memory    MemoryConfig          `toml:"memory"`
	Log       LogConfig             `toml:"log"`
	Agent     AgentConfig           `toml:"agent"`
	Session   SessionConfig         `toml:"session"`
	Agents    []AgentInstanceConfig `toml:"agents"`
	Schedules []ScheduleConfig      `toml:"schedules"`
	Tools     map[string]ToolConfig   `toml:"tools"`
	Plugins   map[string]PluginConfig `toml:"plugins"`
	Voice     VoiceConfig           `toml:"voice"`
	API       APIConfig             `toml:"api"`
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
}

// APIKeyConfig defines a single API key with named scopes.
type APIKeyConfig struct {
	// Name is a human-readable label for this key.
	Name string `toml:"name"`

	// Key is the secret API key value. Loaded from config or env.
	Key string `toml:"key"`

	// Scopes controls what this key can access.
	// Valid scopes: "chat", "sessions:read", "costs:read", "skills:read",
	// "skills:write", "schedules:read", "schedules:write", "health", "admin".
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
// checks and lifecycle management. Docker sandboxing is planned for a future release.
type PluginConfig struct {
	// Type is the execution strategy. Only "subprocess" is supported currently.
	// "docker" is reserved for future sandboxed execution.
	Type    string            `toml:"type"`
	Command string            `toml:"command"`
	Args    []string          `toml:"args"`
	Env     map[string]string `toml:"env"`
	// Capabilities declares contracts this plugin satisfies.
	// Currently only "tools" is meaningful — registers the plugin as an MCP server.
	Capabilities []string `toml:"capabilities"`
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
	MaxCostPerSession float64          `toml:"max_cost_per_session"`
	Fallbacks         []FallbackConfig `toml:"fallback"`
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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	cfg := &Config{}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	applyDefaults(cfg)

	if err := validate(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.LLM.DefaultProvider == "" {
		cfg.LLM.DefaultProvider = "openrouter"
	}
	if cfg.LLM.DefaultModel == "" {
		cfg.LLM.DefaultModel = "anthropic/claude-sonnet-4-20250514"
	}
	if cfg.LLM.MaxCostPerSession == 0 {
		cfg.LLM.MaxCostPerSession = 1.0
	}
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

	for i := range cfg.LLM.Fallbacks {
		if cfg.LLM.Fallbacks[i].Backoff == "" {
			cfg.LLM.Fallbacks[i].Backoff = "exponential"
		}
	}

	// Expand environment variables in tool env values.
	for name, tc := range cfg.Tools {
		for k, v := range tc.Env {
			tc.Env[k] = os.ExpandEnv(v)
		}
		cfg.Tools[name] = tc
	}

	// Expand environment variables in plugin env values.
	for name, pc := range cfg.Plugins {
		for k, v := range pc.Env {
			pc.Env[k] = os.ExpandEnv(v)
		}
		cfg.Plugins[name] = pc
	}

	if cfg.Voice.TTSVoice == "" && cfg.Voice.TTSProvider != "" {
		cfg.Voice.TTSVoice = "alloy"
	}

	if cfg.API.Enabled && cfg.API.Listen == "" {
		cfg.API.Listen = ":8080"
	}

	// Multi-agent backward compat: if no [[agents]] defined, synthesize a
	// single "default" agent from the legacy [agent]/[session] config.
	if len(cfg.Agents) == 0 {
		cfg.Agents = []AgentInstanceConfig{{
			Name:        "default",
			Description: "Default agent",
			PersonaDir:  cfg.Agent.PersonaDir,
			SkillsDir:   cfg.Agent.SkillsDir,
			Adapters:    []string{"telegram"},
			SessionTier: cfg.Session.Tier,
		}}
	}

	for i := range cfg.Agents {
		a := &cfg.Agents[i]
		if a.PersonaDir == "" {
			home, _ := os.UserHomeDir()
			a.PersonaDir = filepath.Join(home, ".denkeeper", "agents", a.Name)
		}
	}

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

func validate(cfg *Config) error {
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("config: telegram.token is required")
	}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("config: telegram.allowed_users must not be empty (security requirement)")
	}
	if needsOpenRouter(cfg) && cfg.LLM.OpenRouter.APIKey == "" {
		return fmt.Errorf("config: llm.openrouter.api_key is required when using openrouter provider")
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

// validAPIScopes is the set of recognised API key scopes.
var validAPIScopes = map[string]bool{
	"chat": true, "sessions:read": true, "costs:read": true,
	"skills:read": true, "skills:write": true,
	"schedules:read": true, "schedules:write": true,
	"approvals:read": true, "approvals:write": true,
	"health": true, "admin": true,
}

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
		for _, scope := range k.Scopes {
			if !validAPIScopes[scope] {
				return fmt.Errorf("config: api.keys[%d] (%s): invalid scope %q", i, k.Name, scope)
			}
		}
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
// "docker" is accepted in config (rejected at runtime) to ease future upgrades.
var validPluginTypes = map[string]bool{"subprocess": true, "docker": true}

func validatePlugins(plugins map[string]PluginConfig, tools map[string]ToolConfig) error {
	for name, pc := range plugins {
		if pc.Type == "" {
			return fmt.Errorf("config: plugins.%s: type is required (must be \"subprocess\")", name)
		}
		if !validPluginTypes[pc.Type] {
			return fmt.Errorf("config: plugins.%s: invalid type %q", name, pc.Type)
		}
		if pc.Command == "" {
			return fmt.Errorf("config: plugins.%s: command is required", name)
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
