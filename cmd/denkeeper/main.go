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
	"github.com/Temikus/denkeeper/internal/adapter/discord"
	"github.com/Temikus/denkeeper/internal/adapter/telegram"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/llm"
	anthropicllm "github.com/Temikus/denkeeper/internal/llm/anthropic"
	"github.com/Temikus/denkeeper/internal/llm/ollama"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/plugin"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
	openaivoice "github.com/Temikus/denkeeper/internal/voice/openai"
	"github.com/Temikus/denkeeper/internal/web"
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

	rootCmd.AddCommand(serveCmd, versionCmd, newKeysCmd())

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

// llmClients holds the initialized LLM provider clients.
type llmClients struct {
	openRouter *openrouter.Client
	ollama     *ollama.Client
	anthropic  *anthropicllm.Client
	cost       *llm.CostTracker
	fallbacks  []llm.FallbackRule
}

func initLogger(cfg *config.Config) *slog.Logger {
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
	return slog.New(handler)
}

func initLLMClients(cfg *config.Config) llmClients {
	var orClient *openrouter.Client
	if cfg.LLM.OpenRouter.APIKey != "" {
		orClient = openrouter.New(cfg.LLM.OpenRouter.APIKey)
	}
	ollamaClient := ollama.New(cfg.LLM.Ollama.BaseURL)
	var anthropicClient *anthropicllm.Client
	if cfg.LLM.Anthropic.APIKey != "" {
		anthropicClient = anthropicllm.New(cfg.LLM.Anthropic.APIKey)
	}

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

	return llmClients{
		openRouter: orClient,
		ollama:     ollamaClient,
		anthropic:  anthropicClient,
		cost:       llm.NewCostTracker(cfg.LLM.MaxCostPerSession),
		fallbacks:  fallbackRules,
	}
}

func initVoiceOpts(cfg *config.Config, logger *slog.Logger) *telegram.VoiceOpts {
	if cfg.Voice.STTProvider == "" && cfg.Voice.TTSProvider == "" {
		return nil
	}
	voiceOpts := &telegram.VoiceOpts{
		TTSVoice:       cfg.Voice.TTSVoice,
		AutoVoiceReply: cfg.Voice.AutoVoiceReply,
	}
	if cfg.Voice.STTProvider == "openai" {
		voiceOpts.STT = openaivoice.New(cfg.Voice.OpenAI.APIKey)
	}
	if cfg.Voice.TTSProvider == "openai" {
		voiceOpts.TTS = openaivoice.New(cfg.Voice.OpenAI.APIKey)
	}
	logger.Info("voice support enabled",
		"stt_provider", cfg.Voice.STTProvider,
		"tts_provider", cfg.Voice.TTSProvider,
		"auto_voice_reply", cfg.Voice.AutoVoiceReply,
	)
	return voiceOpts
}

func initAdapters(cfg *config.Config, logger *slog.Logger, voiceOpts *telegram.VoiceOpts) ([]adapter.Adapter, *telegram.Adapter, error) {
	var adapters []adapter.Adapter

	var tgAdapter *telegram.Adapter
	if cfg.Telegram.Token != "" {
		var err error
		tgAdapter, err = telegram.New(cfg.Telegram.Token, cfg.Telegram.AllowedUsers, logger, voiceOpts)
		if err != nil {
			return nil, nil, fmt.Errorf("initializing telegram: %w", err)
		}
		adapters = append(adapters, tgAdapter)
	}

	if cfg.Discord.Token != "" {
		discordAdapter, err := discord.New(cfg.Discord.Token, cfg.Discord.AllowedUsers, logger)
		if err != nil {
			return nil, nil, fmt.Errorf("initializing discord: %w", err)
		}
		adapters = append(adapters, discordAdapter)
	}

	return adapters, tgAdapter, nil
}

// cleanupFunc is a function to be called on shutdown.
type cleanupFunc func()

func initSharedTools(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*tool.Manager, []cleanupFunc, error) {
	var sharedToolMgr *tool.Manager
	var cleanups []cleanupFunc

	if len(cfg.Tools) > 0 {
		sharedToolMgr = tool.NewManager(logger)
		cleanups = append(cleanups, func() { _ = sharedToolMgr.Close() })
		for name, tc := range cfg.Tools {
			if err := sharedToolMgr.RegisterServer(ctx, name, tc.Command, tc.Args, tc.Env); err != nil {
				return nil, cleanups, fmt.Errorf("initializing tool %q: %w", name, err)
			}
			logger.Info("tool server registered", "name", name, "command", tc.Command)
		}
	}

	if len(cfg.Plugins) > 0 {
		if sharedToolMgr == nil {
			sharedToolMgr = tool.NewManager(logger)
			cleanups = append(cleanups, func() { _ = sharedToolMgr.Close() })
		}

		existingToolNames := make(map[string]bool, len(cfg.Tools))
		for name := range cfg.Tools {
			existingToolNames[name] = true
		}

		var verifyOpts *plugin.VerifyOpts
		if len(cfg.Security.TrustedKeys) > 0 {
			trustedKeys, keyErr := security.LoadTrustedKeys(cfg.Security.TrustedKeys)
			if keyErr != nil {
				return nil, cleanups, fmt.Errorf("loading trusted plugin keys: %w", keyErr)
			}
			verifyOpts = &plugin.VerifyOpts{
				TrustedKeys:   trustedKeys,
				AllowUnsigned: cfg.Security.AllowUnsigned != nil && *cfg.Security.AllowUnsigned,
			}
		}

		pluginMgr := plugin.NewManager(logger, verifyOpts)
		if err := pluginMgr.Load(cfg.Plugins, existingToolNames); err != nil {
			return nil, cleanups, fmt.Errorf("loading plugins: %w", err)
		}
		if err := pluginMgr.Start(ctx, sharedToolMgr); err != nil {
			logger.Warn("one or more plugins failed to start", "error", err)
		}
		logger.Info("plugins loaded", "count", pluginMgr.Count())
	}

	return sharedToolMgr, cleanups, nil
}

// agentBuildCtx holds all the shared state needed to build per-agent engines.
type agentBuildCtx struct {
	cfg             *config.Config
	llm             llmClients
	memory          agent.MemoryStore
	sharedToolMgr   *tool.Manager
	approvalManager *approval.Manager
	globalSkills    []skill.Skill
	sched           *scheduler.Scheduler
	adapters        []adapter.Adapter
	dispatcher      *agent.Dispatcher
	logger          *slog.Logger
}

func buildAgentEngine(ctx context.Context, ac config.AgentInstanceConfig, abc agentBuildCtx) (*agent.Engine, []agent.Binding, error) {
	// Per-agent persona
	var p *persona.Persona
	loadedPersona, pErr := persona.Load(ac.PersonaDir)
	if pErr != nil {
		abc.logger.Warn("persona files not loaded, using default prompt", "agent", ac.Name, "dir", ac.PersonaDir, "error", pErr)
	} else {
		p = loadedPersona
		abc.logger.Info("persona loaded", "agent", ac.Name, "dir", ac.PersonaDir)
	}

	// Per-agent skills: merge global + agent-specific (from persona_dir/skills/)
	agentSkillsDir := filepath.Join(ac.PersonaDir, "skills")
	agentSkills, sErr := skill.LoadDir(agentSkillsDir, abc.logger)
	if sErr != nil {
		abc.logger.Debug("no agent-specific skills", "agent", ac.Name, "dir", agentSkillsDir)
	}
	effectiveGlobal := abc.globalSkills
	effectiveGlobalSkillsDir := abc.cfg.Agent.SkillsDir
	if ac.SkillsDir != "" && ac.SkillsDir != abc.cfg.Agent.SkillsDir {
		overrideSkills, oErr := skill.LoadDir(ac.SkillsDir, abc.logger)
		if oErr != nil {
			abc.logger.Warn("agent skills_dir loading error", "agent", ac.Name, "dir", ac.SkillsDir, "error", oErr)
		} else {
			effectiveGlobal = overrideSkills
			effectiveGlobalSkillsDir = ac.SkillsDir
		}
	}
	mergedSkills := skill.MergeSkills(effectiveGlobal, agentSkills)
	if len(mergedSkills) > 0 {
		abc.logger.Info("skills loaded for agent", "agent", ac.Name, "count", len(mergedSkills))
	}

	// Per-agent permission tier (fall back to global session.tier)
	tier := abc.cfg.Session.Tier
	if ac.SessionTier != "" {
		tier = ac.SessionTier
	}
	permissions, pErr := security.NewPermissionEngine(tier)
	if pErr != nil {
		return nil, nil, fmt.Errorf("initializing permissions for agent %q: %w", ac.Name, pErr)
	}

	// Per-agent tool manager: adopt shared external tools then add the
	// per-agent Config MCP server so skill/schedule tools are agent-scoped.
	agentToolMgr := tool.NewManager(abc.logger)
	if abc.sharedToolMgr != nil {
		agentToolMgr.AdoptFrom(abc.sharedToolMgr)
	}

	// Per-agent LLM router (with optional model override)
	model := abc.cfg.LLM.DefaultModel
	if ac.LLMModel != "" {
		model = ac.LLMModel
	}
	agentRouter := llm.NewRouter(abc.cfg.LLM.DefaultProvider, model, abc.llm.cost)
	agentRouter.RegisterProvider(abc.llm.ollama)
	if abc.llm.openRouter != nil {
		agentRouter.RegisterProvider(abc.llm.openRouter)
	}
	if abc.llm.anthropic != nil {
		agentRouter.RegisterProvider(abc.llm.anthropic)
	}
	if len(abc.llm.fallbacks) > 0 {
		agentRouter.SetFallbacks(abc.llm.fallbacks)
	}

	sendVia := func(ctx context.Context, msg adapter.OutgoingMessage) error {
		name := msg.Adapter
		if name == "" && len(abc.adapters) > 0 {
			name = abc.adapters[0].Name()
		}
		return abc.dispatcher.SendVia(ctx, name, msg)
	}

	e := agent.NewEngine(
		ac.Name,
		agentRouter,
		abc.memory,
		sendVia,
		permissions,
		p,
		persona.DefaultPrompt,
		mergedSkills,
		agentToolMgr,
		abc.approvalManager,
		abc.logger,
	)
	e.SetSkillDirs(agentSkillsDir, effectiveGlobalSkillsDir)
	e.SetScheduler(abc.sched)

	// Connect per-agent Config MCP server and register its tools.
	cmcpSrv := configmcp.New(configmcp.Deps{
		AgentName:      ac.Name,
		AgentSkillsDir: agentSkillsDir,
		GetSkills:      e.Skills,
		AppendSkill:    e.AppendSkill,
		Sched:          abc.sched,
		HandleMessage:  e.HandleMessage,
		Approvals:      abc.approvalManager,
		PermissionTier: e.PermissionTier,
		Logger:         abc.logger,
	})
	cmcpSession, cmcpErr := cmcpSrv.Connect(ctx)
	if cmcpErr != nil {
		return nil, nil, fmt.Errorf("starting config MCP for agent %q: %w", ac.Name, cmcpErr)
	}
	if err := agentToolMgr.RegisterSession(ctx, "config-"+ac.Name, cmcpSession); err != nil {
		return nil, nil, fmt.Errorf("registering config MCP for agent %q: %w", ac.Name, err)
	}
	abc.logger.Info("config MCP registered", "agent", ac.Name, "tools", len(agentToolMgr.ToolDefs()))

	agentRouter.SetTools(agentToolMgr.ToolDefs())

	var bindings []agent.Binding
	for _, binding := range ac.Adapters {
		bindings = append(bindings, agent.Binding{Pattern: binding, AgentName: ac.Name})
	}

	abc.logger.Info("agent initialized",
		"agent", ac.Name,
		"model", model,
		"tier", tier,
		"skills", len(mergedSkills),
	)

	return e, bindings, nil
}

func registerSchedules(ctx context.Context, cfg *config.Config, sched *scheduler.Scheduler, dispatcher *agent.Dispatcher, logger *slog.Logger) error {
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
	return nil
}

func startAPIServer(ctx context.Context, cfg *config.Config, deps api.Deps, logger *slog.Logger) error {
	keyStore, ksErr := api.NewKeyStore(cfg.Memory.DBPath)
	if ksErr != nil {
		return fmt.Errorf("initializing api key store: %w", ksErr)
	}

	existingKeys, _ := keyStore.List(ctx)
	hasActiveKey := len(cfg.API.Keys) > 0
	for _, k := range existingKeys {
		if !k.Revoked {
			hasActiveKey = true
			break
		}
	}
	if !hasActiveKey {
		logger.Warn("no API keys found — web dashboard login will fail",
			"hint", "run: denkeeper keys create <name>",
		)
	}

	deps.KeyStore = keyStore
	apiServer := api.New(cfg.API, deps, logger)
	go func() {
		if err := apiServer.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("api server error", "error", err)
		}
	}()
	return nil
}

func runServe(_ *cobra.Command, _ []string) error {
	path := cfgFile
	if path == "" {
		path = config.DefaultConfigPath()
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := initLogger(cfg)

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

	if n, expErr := approvalStore.ExpirePending(context.Background()); expErr != nil {
		logger.Warn("failed to expire pending approvals", "error", expErr)
	} else if n > 0 {
		logger.Info("expired pending approvals from previous run", "count", n)
	}

	approvalManager := approval.NewManager(approvalStore, logger)

	clients := initLLMClients(cfg)
	voiceOpts := initVoiceOpts(cfg, logger)

	adapters, tgAdapter, err := initAdapters(cfg, logger, voiceOpts)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	approvalManager.StartExpiryWorker(ctx, time.Hour)

	sharedToolMgr, cleanups, err := initSharedTools(ctx, cfg, logger)
	for _, fn := range cleanups {
		defer fn()
	}
	if err != nil {
		return err
	}

	// Load global skills
	globalSkills, err := skill.LoadDir(cfg.Agent.SkillsDir, logger)
	if err != nil {
		logger.Warn("global skill loading error", "dir", cfg.Agent.SkillsDir, "error", err)
	} else if len(globalSkills) > 0 {
		logger.Info("global skills loaded", "dir", cfg.Agent.SkillsDir, "count", len(globalSkills))
	}

	sched := scheduler.New(logger)

	// Build per-agent engines
	engines := make(map[string]*agent.Engine, len(cfg.Agents))
	var bindings []agent.Binding
	dispatcher := agent.NewDispatcher(nil, nil, adapters, logger)

	abc := agentBuildCtx{
		cfg:             cfg,
		llm:             clients,
		memory:          memory,
		sharedToolMgr:   sharedToolMgr,
		approvalManager: approvalManager,
		globalSkills:    globalSkills,
		sched:           sched,
		adapters:        adapters,
		dispatcher:      dispatcher,
		logger:          logger,
	}

	for _, ac := range cfg.Agents {
		e, agentBindings, buildErr := buildAgentEngine(ctx, ac, abc)
		if buildErr != nil {
			return buildErr
		}
		engines[ac.Name] = e
		bindings = append(bindings, agentBindings...)
	}

	// Re-create dispatcher with the fully wired engines and bindings.
	dispatcher = agent.NewDispatcher(engines, bindings, adapters, logger)

	if tgAdapter != nil {
		tgAdapter.SetCallbackResolver(approval.NewCallbackHandler(approvalManager, logger))
	}

	if err := registerSchedules(ctx, cfg, sched, dispatcher, logger); err != nil {
		return err
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

	if cfg.API.Enabled {
		if err := startAPIServer(ctx, cfg, api.Deps{
			Dispatcher:  dispatcher,
			Scheduler:   sched,
			CostTracker: clients.cost,
			Memory:      memory,
			Config:      cfg,
			Approvals:   approvalManager,
			WebHandler:  web.Handler(),
		}, logger); err != nil {
			return err
		}
	}

	logger.Info("denkeeper starting",
		"agents", len(engines),
		"provider", cfg.LLM.DefaultProvider,
		"default_model", cfg.LLM.DefaultModel,
	)

	return dispatcher.Run(ctx)
}
