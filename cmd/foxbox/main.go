package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/Temikus/foxbox/internal/adapter"
	"github.com/Temikus/foxbox/internal/adapter/telegram"
	"github.com/Temikus/foxbox/internal/agent"
	"github.com/Temikus/foxbox/internal/config"
	"github.com/Temikus/foxbox/internal/llm"
	"github.com/Temikus/foxbox/internal/llm/openrouter"
	"github.com/Temikus/foxbox/internal/persona"
	"github.com/Temikus/foxbox/internal/security"
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
		Use:   "foxbox",
		Short: "Foxbox — a security-first personal AI agent",
	}

	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Foxbox agent",
		RunE:  runServe,
	}
	serveCmd.Flags().StringVarP(&cfgFile, "config", "c", "", "config file path (default: ~/.foxbox/foxbox.toml)")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("foxbox version %s\n", version)
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

	// Init Telegram adapter
	tgAdapter, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, logger)
	if err != nil {
		return fmt.Errorf("initializing telegram: %w", err)
	}

	// Load persona
	systemPrompt := persona.DefaultPrompt
	p, err := persona.Load(cfg.Agent.PersonaDir)
	if err != nil {
		logger.Warn("persona files not loaded, using default prompt", "dir", cfg.Agent.PersonaDir, "error", err)
	} else {
		systemPrompt = p.SystemPrompt()
		logger.Info("persona loaded", "dir", cfg.Agent.PersonaDir)
	}

	// Init permissions
	permissions := security.NewPermissionEngine()

	// Init engine
	engine := agent.NewEngine(
		router,
		memory,
		[]adapter.Adapter{tgAdapter},
		permissions,
		systemPrompt,
		logger,
	)

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()

	logger.Info("foxbox starting",
		"provider", cfg.LLM.DefaultProvider,
		"model", cfg.LLM.DefaultModel,
		"permission_tier", permissions.Tier(),
	)

	return engine.Run(ctx)
}
