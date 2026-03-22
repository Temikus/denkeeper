package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/adapter/telegram"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
)

// Build-time variables set via ldflags.
var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

var cfgFile string

func main() {
	rootCmd := &cobra.Command{
		Use:   "denkeeper",
		Short: "Denkeeper — a security-first personal AI agent",
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Denkeeper agent",
		RunE:  runServe,
	}
	serveCmd.Flags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.denkeeper/denkeeper.toml)")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("denkeeper version %s\n", version)
			fmt.Printf("  commit:    %s\n", commit)
			fmt.Printf("  built:     %s\n", date)
			fmt.Printf("  go:        %s\n", runtime.Version())
			fmt.Printf("  platform:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	}

	rootCmd.AddCommand(serveCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// parseChannel splits a "adapter:externalID" channel string into its parts.
// Returns ok=false if the format is invalid.
func parseChannel(channel string) (adapterName, externalID string, ok bool) {
	idx := strings.IndexByte(channel, ':')
	if idx <= 0 || idx == len(channel)-1 {
		return "", "", false
	}
	return channel[:idx], channel[idx+1:], true
}

func runServe(_ *cobra.Command, _ []string) error {
	// Determine config path
	path := cfgFile
	if path == "" {
		path = config.DefaultConfigPath()
	}

	// Load config
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Setup logger
	var logLevel slog.Level
	switch cfg.Log.Level {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	var handler slog.Handler
	if cfg.Log.Format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	}
	logger := slog.New(handler)

	// Init memory store
	memory, err := agent.NewSQLiteMemoryStore(cfg.Memory.DBPath)
	if err != nil {
		return fmt.Errorf("initializing memory store: %w", err)
	}
	defer func() { _ = memory.Close() }()

	// Init LLM
	orClient := openrouter.New(cfg.LLM.OpenRouter.APIKey)
	costTracker := llm.NewCostTracker(cfg.LLM.MaxCostPerSession)
	router := llm.NewRouter(cfg.LLM.DefaultProvider, cfg.LLM.DefaultModel, costTracker)
	router.RegisterProvider(orClient)

	if len(cfg.LLM.Fallbacks) > 0 {
		fallbackRules := make([]llm.FallbackRule, len(cfg.LLM.Fallbacks))
		for i, f := range cfg.LLM.Fallbacks {
			fallbackRules[i] = llm.FallbackRule{
				Trigger:    f.Trigger,
				Action:     f.Action,
				Provider:   f.Provider,
				Model:      f.Model,
				Threshold:  f.Threshold,
				MaxRetries: f.MaxRetries,
				Backoff:    f.Backoff,
			}
		}
		router.SetFallbacks(fallbackRules)
	}

	// Init Telegram adapter
	tgAdapter, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, logger)
	if err != nil {
		return fmt.Errorf("initializing telegram: %w", err)
	}

	// Load persona
	var p *persona.Persona
	loadedPersona, err := persona.Load(cfg.Agent.PersonaDir)
	if err != nil {
		logger.Warn("persona files not loaded, using default prompt", "dir", cfg.Agent.PersonaDir, "error", err)
	} else {
		p = loadedPersona
		logger.Info("persona loaded", "dir", cfg.Agent.PersonaDir)
	}

	// Load skills
	var skillsSuffix string
	skills, err := skill.LoadDir(cfg.Agent.SkillsDir, logger)
	if err != nil {
		logger.Warn("skill loading error", "dir", cfg.Agent.SkillsDir, "error", err)
	} else if len(skills) > 0 {
		skillsSuffix = skill.BuildPromptSection(skills)
		logger.Info("skills loaded", "dir", cfg.Agent.SkillsDir, "count", len(skills))
	}

	// Init permissions
	permissions, err := security.NewPermissionEngine(cfg.Session.Tier)
	if err != nil {
		return fmt.Errorf("initializing permissions: %w", err)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Init tools (if configured)
	var toolMgr *tool.Manager
	if len(cfg.Tools) > 0 {
		toolMgr = tool.NewManager(logger)
		for name, tc := range cfg.Tools {
			if err := toolMgr.RegisterServer(ctx, name, tc.Command, tc.Args, tc.Env); err != nil {
				return fmt.Errorf("initializing tool %q: %w", name, err)
			}
			logger.Info("tool server registered", "name", name, "command", tc.Command)
		}
		router.SetTools(toolMgr.ToolDefs())
		defer func() { _ = toolMgr.Close() }()
	}

	// Init engine
	engine := agent.NewEngine(
		router,
		memory,
		[]adapter.Adapter{tgAdapter},
		permissions,
		p,
		persona.DefaultPrompt,
		skillsSuffix,
		toolMgr,
		logger,
	)

	// Init and wire scheduler
	sched := scheduler.New(logger)
	for _, sc := range cfg.Schedules {
		sc := sc // capture loop variable
		text := "[Scheduled trigger: " + sc.Name + "]"
		if sc.Skill != "" {
			text = "[Scheduled: " + sc.Skill + "]"
		}

		adapterName, externalID, ok := parseChannel(sc.Channel)
		if !ok && sc.Channel != "" {
			logger.Warn("schedule has invalid channel format, skipping", "name", sc.Name, "channel", sc.Channel)
			continue
		}

		jobMsg := adapter.IncomingMessage{
			Adapter:    adapterName,
			ExternalID: externalID,
			UserName:   "scheduler",
			Text:       text,
		}

		if err := sched.Register(scheduler.Config{
			Name:        sc.Name,
			Type:        sc.Type,
			Schedule:    sc.Schedule,
			Skill:       sc.Skill,
			SessionTier: sc.SessionTier,
			SessionMode: sc.SessionMode,
			Channel:     sc.Channel,
			Tags:        sc.Tags,
			Enabled:     *sc.Enabled,
		}, func(entry scheduler.Entry) {
			if sc.Channel == "" {
				logger.Debug("schedule fired, no channel configured", "name", entry.Name)
				return
			}
			msg := jobMsg
			msg.Timestamp = time.Now()
			if entry.SessionMode == "isolated" {
				msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, time.Now().UnixNano())
			}
			if entry.SessionTier != "" {
				msg.SessionTier = entry.SessionTier
			}
			if err := engine.Dispatch(ctx, msg); err != nil {
				logger.Error("dispatching scheduled message", "name", entry.Name, "error", err)
			}
		}); err != nil {
			return fmt.Errorf("registering schedule %q: %w", sc.Name, err)
		}
	}
	sched.Start()
	defer sched.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("denkeeper starting",
		"provider", cfg.LLM.DefaultProvider,
		"model", cfg.LLM.DefaultModel,
		"permission_tier", permissions.Tier(),
	)

	return engine.Run(ctx)
}
