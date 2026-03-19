package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Telegram  TelegramConfig   `toml:"telegram"`
	LLM       LLMConfig        `toml:"llm"`
	Memory    MemoryConfig     `toml:"memory"`
	Log       LogConfig        `toml:"log"`
	Agent     AgentConfig      `toml:"agent"`
	Schedules []ScheduleConfig `toml:"schedules"`
}

type AgentConfig struct {
	PersonaDir string `toml:"persona_dir"`
}

type TelegramConfig struct {
	Token        string  `toml:"token"`
	AllowedUsers []int64 `toml:"allowed_users"`
}

type LLMConfig struct {
	DefaultProvider   string           `toml:"default_provider"`
	DefaultModel      string           `toml:"default_model"`
	OpenRouter        OpenRouterConfig `toml:"openrouter"`
	MaxCostPerSession float64          `toml:"max_cost_per_session"`
}

type OpenRouterConfig struct {
	APIKey string `toml:"api_key"`
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
		cfg.Memory.DBPath = filepath.Join(home, ".foxbox", "data", "memory.db")
	}
	if cfg.Agent.PersonaDir == "" {
		home, _ := os.UserHomeDir()
		cfg.Agent.PersonaDir = filepath.Join(home, ".foxbox", "agents", "default")
	}
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
	if cfg.Log.Format == "" {
		cfg.Log.Format = "text"
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
	}
}

func validate(cfg *Config) error {
	if cfg.Telegram.Token == "" {
		return fmt.Errorf("config: telegram.token is required")
	}
	if len(cfg.Telegram.AllowedUsers) == 0 {
		return fmt.Errorf("config: telegram.allowed_users must not be empty (security requirement)")
	}
	if cfg.LLM.OpenRouter.APIKey == "" {
		return fmt.Errorf("config: llm.openrouter.api_key is required")
	}
	if err := validateSchedules(cfg.Schedules); err != nil {
		return err
	}
	return nil
}

// validateSchedules checks all schedule entries for structural correctness.
// Expression format validation (cron syntax, duration strings) is intentionally
// deferred to the scheduler at startup, keeping the config and scheduler packages
// independent.
func validateSchedules(schedules []ScheduleConfig) error {
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
			switch s.SessionTier {
			case "supervised", "autonomous", "restricted":
				// valid
			default:
				return fmt.Errorf(
					"config: schedule %q: invalid session_tier %q — must be one of: supervised, autonomous, restricted",
					s.Name, s.SessionTier,
				)
			}
		}
	}
	return nil
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".foxbox", "foxbox.toml")
}
