package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/adapter/telegram"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/llm"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
	openaivoice "github.com/Temikus/denkeeper/internal/voice/openai"
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

	// Init memory store (shared across all agents)
	memory, err := agent.NewSQLiteMemoryStore(cfg.Memory.DBPath)
	if err != nil {
		return fmt.Errorf("initializing memory store: %w", err)
	}
	defer func() { _ = memory.Close() }()

	// Init approval store (same WAL database file as memory store)
	approvalStore, err := approval.NewSQLiteStore(cfg.Memory.DBPath)
	if err != nil {
		return fmt.Errorf("initializing approval store: %w", err)
	}
	defer func() { _ = approvalStore.Close() }()

	// Expire any approvals left pending from a previous run.
	if n, expErr := approvalStore.ExpirePending(context.Background()); expErr != nil {
		logger.Warn("failed to expire pending approvals", "error", expErr)
	} else if n > 0 {
		logger.Info("expired pending approvals from previous run", "count", n)
	}

	approvalManager := approval.NewManager(approvalStore, logger)

	// Init LLM provider (shared across all agents)
	orClient := openrouter.New(cfg.LLM.OpenRouter.APIKey)
	costTracker := llm.NewCostTracker(cfg.LLM.MaxCostPerSession)

	// Parse fallback rules once (shared)
	var fallbackRules []llm.FallbackRule
	if len(cfg.LLM.Fallbacks) > 0 {
		fallbackRules = make([]llm.FallbackRule, len(cfg.LLM.Fallbacks))
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
	}

	// Init voice providers (if configured)
	var voiceOpts *telegram.VoiceOpts
	if cfg.Voice.STTProvider != "" || cfg.Voice.TTSProvider != "" {
		voiceOpts = &telegram.VoiceOpts{
			TTSVoice:       cfg.Voice.TTSVoice,
			AutoVoiceReply: cfg.Voice.AutoVoiceReply,
		}
		if cfg.Voice.STTProvider == "openai" {
			client := openaivoice.New(cfg.Voice.OpenAI.APIKey)
			voiceOpts.STT = client
		}
		if cfg.Voice.TTSProvider == "openai" {
			client := openaivoice.New(cfg.Voice.OpenAI.APIKey)
			voiceOpts.TTS = client
		}
		logger.Info("voice support enabled",
			"stt_provider", cfg.Voice.STTProvider,
			"tts_provider", cfg.Voice.TTSProvider,
			"auto_voice_reply", cfg.Voice.AutoVoiceReply,
		)
	}

	// Init Telegram adapter
	tgAdapter, err := telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, logger, voiceOpts)
	if err != nil {
		return fmt.Errorf("initializing telegram: %w", err)
	}

	// Setup graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Init tools (shared across all agents)
	var toolMgr *tool.Manager
	if len(cfg.Tools) > 0 {
		toolMgr = tool.NewManager(logger)
		for name, tc := range cfg.Tools {
			if err := toolMgr.RegisterServer(ctx, name, tc.Command, tc.Args, tc.Env); err != nil {
				return fmt.Errorf("initializing tool %q: %w", name, err)
			}
			logger.Info("tool server registered", "name", name, "command", tc.Command)
		}
		defer func() { _ = toolMgr.Close() }()
	}

	// Load global skills
	globalSkills, err := skill.LoadDir(cfg.Agent.SkillsDir, logger)
	if err != nil {
		logger.Warn("global skill loading error", "dir", cfg.Agent.SkillsDir, "error", err)
	} else if len(globalSkills) > 0 {
		logger.Info("global skills loaded", "dir", cfg.Agent.SkillsDir, "count", len(globalSkills))
	}

	// Build per-agent engines
	engines := make(map[string]*agent.Engine, len(cfg.Agents))
	var bindings []agent.Binding

	// Create dispatcher first (without engines) so we can reference SendFor.
	// We'll wire engines in after the loop.
	dispatcher := agent.NewDispatcher(nil, nil, []adapter.Adapter{tgAdapter}, logger)

	for _, ac := range cfg.Agents {
		// Per-agent persona
		var p *persona.Persona
		loadedPersona, pErr := persona.Load(ac.PersonaDir)
		if pErr != nil {
			logger.Warn("persona files not loaded, using default prompt", "agent", ac.Name, "dir", ac.PersonaDir, "error", pErr)
		} else {
			p = loadedPersona
			logger.Info("persona loaded", "agent", ac.Name, "dir", ac.PersonaDir)
		}

		// Per-agent skills: merge global + agent-specific (from persona_dir/skills/)
		agentSkillsDir := filepath.Join(ac.PersonaDir, "skills")
		agentSkills, sErr := skill.LoadDir(agentSkillsDir, logger)
		if sErr != nil {
			logger.Debug("no agent-specific skills", "agent", ac.Name, "dir", agentSkillsDir)
		}
		// If agent has a custom skills_dir override, load from there instead of global.
		effectiveGlobal := globalSkills
		if ac.SkillsDir != "" && ac.SkillsDir != cfg.Agent.SkillsDir {
			overrideSkills, oErr := skill.LoadDir(ac.SkillsDir, logger)
			if oErr != nil {
				logger.Warn("agent skills_dir loading error", "agent", ac.Name, "dir", ac.SkillsDir, "error", oErr)
			} else {
				effectiveGlobal = overrideSkills
			}
		}
		mergedSkills := skill.MergeSkills(effectiveGlobal, agentSkills)
		if len(mergedSkills) > 0 {
			logger.Info("skills loaded for agent", "agent", ac.Name, "count", len(mergedSkills))
		}

		// Per-agent permission tier (fall back to global session.tier)
		tier := cfg.Session.Tier
		if ac.SessionTier != "" {
			tier = ac.SessionTier
		}
		permissions, pErr := security.NewPermissionEngine(tier)
		if pErr != nil {
			return fmt.Errorf("initializing permissions for agent %q: %w", ac.Name, pErr)
		}

		// Per-agent LLM router (with optional model override)
		model := cfg.LLM.DefaultModel
		if ac.LLMModel != "" {
			model = ac.LLMModel
		}
		agentRouter := llm.NewRouter(cfg.LLM.DefaultProvider, model, costTracker)
		agentRouter.RegisterProvider(orClient)
		if len(fallbackRules) > 0 {
			agentRouter.SetFallbacks(fallbackRules)
		}
		if toolMgr != nil {
			agentRouter.SetTools(toolMgr.ToolDefs())
		}

		// Build engine
		e := agent.NewEngine(
			ac.Name,
			agentRouter,
			memory,
			dispatcher.SendFor("telegram"), // route responses through Telegram adapter
			permissions,
			p,
			persona.DefaultPrompt,
			mergedSkills,
			toolMgr,
			approvalManager,
			logger,
		)

		engines[ac.Name] = e

		for _, binding := range ac.Adapters {
			bindings = append(bindings, agent.Binding{Pattern: binding, AgentName: ac.Name})
		}

		logger.Info("agent initialized",
			"agent", ac.Name,
			"model", model,
			"tier", tier,
			"skills", len(mergedSkills),
		)
	}

	// Re-create dispatcher with the fully wired engines and bindings.
	dispatcher = agent.NewDispatcher(engines, bindings, []adapter.Adapter{tgAdapter}, logger)

	// Wire the callback resolver so Telegram inline keyboard buttons resolve approvals.
	tgAdapter.SetCallbackResolver(&callbackShim{
		manager: approvalManager,
		logger:  logger,
	})

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
			SkillName:  sc.Skill,
		}

		targetAgent := sc.Agent // defaults to "default" from applyDefaults

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
			if err := dispatcher.Dispatch(ctx, targetAgent, msg); err != nil {
				logger.Error("dispatching scheduled message", "name", entry.Name, "agent", targetAgent, "error", err)
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

	// Start API server (if enabled)
	if cfg.API.Enabled {
		apiServer := api.New(cfg.API, api.Deps{
			Dispatcher:  dispatcher,
			Scheduler:   sched,
			CostTracker: costTracker,
			Memory:      memory,
			Config:      cfg,
			Approvals:   approvalManager,
		}, logger)
		go func() {
			if err := apiServer.Run(ctx); err != nil && ctx.Err() == nil {
				logger.Error("api server error", "error", err)
			}
		}()
	}

	logger.Info("denkeeper starting",
		"agents", len(engines),
		"provider", cfg.LLM.DefaultProvider,
		"default_model", cfg.LLM.DefaultModel,
	)

	return dispatcher.Run(ctx)
}

// callbackShim implements adapter.CallbackResolver, bridging Telegram inline
// keyboard callbacks to the approval manager.
type callbackShim struct {
	manager *approval.Manager
	logger  *slog.Logger
}

func (s *callbackShim) Resolve(ctx context.Context, data string) (string, error) {
	if !strings.HasPrefix(data, "appr:") {
		return "", nil // not an approval callback
	}

	resolved, err := s.manager.ResolveByCallback(ctx, data, "telegram")
	if err != nil {
		if err == approval.ErrNotFound {
			s.logger.Warn("callback for unknown approval", "data", data)
			return "", nil
		}
		return fmt.Sprintf("Error processing request: %v", err), err
	}

	if strings.HasSuffix(data, ":approve") {
		return "✅ Approved: " + resolved.Summary, nil
	}
	return "❌ Denied: " + resolved.Summary, nil
}
