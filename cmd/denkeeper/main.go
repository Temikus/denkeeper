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
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	anthropicllm "github.com/Temikus/denkeeper/internal/llm/anthropic"
	"github.com/Temikus/denkeeper/internal/llm/ollama"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
	"github.com/Temikus/denkeeper/internal/persona"
	"github.com/Temikus/denkeeper/internal/plugin"
	"github.com/Temikus/denkeeper/internal/sandbox"
	"github.com/Temikus/denkeeper/internal/scheduler"
	"github.com/Temikus/denkeeper/internal/security"
	"github.com/Temikus/denkeeper/internal/skill"
	"github.com/Temikus/denkeeper/internal/tool"
	openaivoice "github.com/Temikus/denkeeper/internal/voice/openai"
	"github.com/Temikus/denkeeper/internal/web"
	"github.com/Temikus/denkeeper/internal/webfetch"
	"github.com/Temikus/denkeeper/internal/webmcp"
	"github.com/Temikus/denkeeper/internal/websearch"
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

	rootCmd.AddCommand(serveCmd, versionCmd, newKeysCmd(), newPluginCmd())

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

// stores holds the initialized persistence stores and the approval manager.
type stores struct {
	memory          agent.MemoryStore
	approvalStore   *approval.SQLiteStore
	approvalManager *approval.Manager
	kvStore         *kv.SQLiteStore
}

// initStores creates the memory, approval, and KV stores that share a single
// WAL SQLite database. It also expires stale pending approvals from any
// previous run.
func initStores(cfg *config.Config, logger *slog.Logger) (stores, error) {
	memory, err := agent.NewSQLiteMemoryStore(cfg.Memory.DBPath)
	if err != nil {
		return stores{}, fmt.Errorf("initializing memory store: %w", err)
	}

	approvalStore, err := approval.NewSQLiteStore(cfg.Memory.DBPath)
	if err != nil {
		_ = memory.Close()
		return stores{}, fmt.Errorf("initializing approval store: %w", err)
	}

	if n, expErr := approvalStore.ExpirePending(context.Background()); expErr != nil {
		logger.Warn("failed to expire pending approvals", "error", expErr)
	} else if n > 0 {
		logger.Info("expired pending approvals from previous run", "count", n)
	}

	kvStore, err := kv.NewSQLiteStore(cfg.Memory.DBPath,
		kv.WithMaxKeysPerAgent(cfg.KV.MaxKeysPerAgent),
		kv.WithMaxValueBytes(cfg.KV.MaxValueBytes),
	)
	if err != nil {
		_ = approvalStore.Close()
		_ = memory.Close()
		return stores{}, fmt.Errorf("initializing kv store: %w", err)
	}

	return stores{
		memory:          memory,
		approvalStore:   approvalStore,
		approvalManager: approval.NewManager(approvalStore, logger),
		kvStore:         kvStore,
	}, nil
}

// closeStores closes all persistence stores in reverse order.
func (s stores) Close() {
	_ = s.kvStore.Close()
	_ = s.approvalStore.Close()
	_ = s.memory.Close()
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

	// Helper to ensure sharedToolMgr is initialised exactly once.
	ensureToolMgr := func() {
		if sharedToolMgr == nil {
			sharedToolMgr = tool.NewManager(logger)
			cleanups = append(cleanups, func() { _ = sharedToolMgr.Close() })
		}
	}

	if len(cfg.Tools) > 0 {
		ensureToolMgr()
		for name, tc := range cfg.Tools {
			if err := sharedToolMgr.RegisterServer(ctx, name, tc.Command, tc.Args, tc.Env); err != nil {
				return nil, cleanups, fmt.Errorf("initializing tool %q: %w", name, err)
			}
			logger.Info("tool server registered", "name", name, "command", tc.Command)
		}
	}

	// Shared sandbox runtime — created on first need (plugins or browser).
	var sandboxRT sandbox.Runtime
	ensureSandboxRT := func() error {
		if sandboxRT != nil {
			return nil
		}
		rt, rtErr := createSandboxRuntime(cfg, logger)
		if rtErr != nil {
			return rtErr
		}
		sandboxRT = rt
		cleanups = append(cleanups, func() {
			if err := rt.Close(); err != nil {
				logger.Error("closing sandbox runtime", "error", err)
			}
		})
		return nil
	}

	if len(cfg.Plugins) > 0 {
		ensureToolMgr()

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

		// Create sandbox runtime for Docker/K8s plugins.
		if hasSandboxedPlugins(cfg.Plugins) {
			if err := ensureSandboxRT(); err != nil {
				return nil, cleanups, err
			}
		}

		pluginMgr := plugin.NewManager(logger, verifyOpts, sandboxRT)
		if err := pluginMgr.Load(cfg.Plugins, existingToolNames); err != nil {
			return nil, cleanups, fmt.Errorf("loading plugins: %w", err)
		}
		if err := pluginMgr.Start(ctx, sharedToolMgr); err != nil {
			logger.Warn("one or more plugins failed to start", "error", err)
		}
		logger.Info("plugins loaded", "count", pluginMgr.Count())
	}

	// Browser automation — auto-register as a Docker-based MCP server.
	if cfg.Browser.Enabled {
		ensureToolMgr()
		if err := ensureSandboxRT(); err != nil {
			return nil, cleanups, fmt.Errorf("browser sandbox runtime: %w", err)
		}
		if err := registerBrowser(ctx, cfg, sandboxRT, sharedToolMgr, logger); err != nil {
			return nil, cleanups, err
		}
	}

	return sharedToolMgr, cleanups, nil
}

// registerBrowser spawns the browser automation container and registers its MCP tools.
func registerBrowser(ctx context.Context, cfg *config.Config, rt sandbox.Runtime, toolMgr *tool.Manager, logger *slog.Logger) error {
	proc, err := rt.Spawn(ctx, "denkeeper-browser", sandbox.SpawnOpts{
		Image:       cfg.Browser.Image,
		Args:        []string{"--headless", "--browser", "chromium", "--no-sandbox"},
		Network:     sandbox.NetworkEgress,
		MemoryLimit: cfg.Browser.MemoryLimit,
		CPULimit:    cfg.Browser.CPULimit,
	})
	if err != nil {
		return fmt.Errorf("starting browser plugin: %w", err)
	}
	if err := toolMgr.RegisterServer(ctx, "browser", proc.Command, proc.Args, proc.Env); err != nil {
		return fmt.Errorf("registering browser tools: %w", err)
	}
	logger.Info("browser automation registered", "image", cfg.Browser.Image)
	return nil
}

// hasSandboxedPlugins returns true if any plugin in the map uses the docker type
// (which is handled by the sandbox runtime — Docker or Kubernetes).
func hasSandboxedPlugins(plugins map[string]config.PluginConfig) bool {
	for _, pc := range plugins {
		if pc.Type == "docker" {
			return true
		}
	}
	return false
}

// createSandboxRuntime creates the appropriate sandbox.Runtime based on config.
func createSandboxRuntime(cfg *config.Config, logger *slog.Logger) (sandbox.Runtime, error) {
	switch cfg.Sandbox.Runtime {
	case "kubernetes":
		return sandbox.NewKubernetesRuntime(sandbox.KubernetesConfig{
			Namespace:    cfg.Sandbox.Kubernetes.Namespace,
			Kubeconfig:   cfg.Sandbox.Kubernetes.Kubeconfig,
			RuntimeClass: cfg.Sandbox.Kubernetes.RuntimeClass,
		}, logger)
	default: // "docker"
		return sandbox.NewDockerRuntime()
	}
}

// agentBuildCtx holds all the shared state needed to build per-agent engines.
type agentBuildCtx struct {
	cfg             *config.Config
	llm             llmClients
	memory          agent.MemoryStore
	sharedToolMgr   *tool.Manager
	lifecycleMgr    *tool.LifecycleManager
	approvalManager *approval.Manager
	kvStore         kv.Store
	globalSkills    []skill.Skill
	sched           *scheduler.Scheduler
	adapters        []adapter.Adapter
	dispatcher      *agent.Dispatcher
	logger          *slog.Logger
}

// connectConfigMCP creates the per-agent Config MCP server, connects it, and
// registers its tools into the agent's tool manager.
func connectConfigMCP(ctx context.Context, agentName, skillsDir string, e *agent.Engine, router *llm.Router, toolMgr *tool.Manager, abc agentBuildCtx) error {
	costTracker := abc.llm.cost
	cmcpSrv := configmcp.New(configmcp.Deps{
		AgentName:      agentName,
		AgentSkillsDir: skillsDir,
		GetSkills:      e.Skills,
		AppendSkill:    e.AppendSkill,
		Sched:          abc.sched,
		HandleMessage:  e.HandleMessage,
		Approvals:      abc.approvalManager,
		PermissionTier: e.PermissionTier,
		LifecycleMgr:   abc.lifecycleMgr,
		KVStore:        abc.kvStore,
		CostSummary: func() configmcp.CostSummaryData {
			return configmcp.CostSummaryData{
				GlobalCost:    costTracker.GlobalCost(),
				MaxPerSession: costTracker.MaxBudgetPerSession(),
				SessionCosts:  costTracker.AllSessionCosts(),
			}
		},
		SetFallbacks: func(rules []configmcp.FallbackRuleInput) {
			converted := make([]llm.FallbackRule, len(rules))
			for i, r := range rules {
				converted[i] = llm.FallbackRule{
					Trigger:    r.Trigger,
					Action:     r.Action,
					Provider:   r.Provider,
					Model:      r.Model,
					Threshold:  r.Threshold,
					MaxRetries: r.MaxRetries,
					Backoff:    r.Backoff,
				}
			}
			router.SetFallbacks(converted)
		},
		Logger: abc.logger,
	})
	session, err := cmcpSrv.Connect(ctx)
	if err != nil {
		return fmt.Errorf("starting config MCP for agent %q: %w", agentName, err)
	}
	if err := toolMgr.RegisterSession(ctx, "config-"+agentName, session); err != nil {
		return fmt.Errorf("registering config MCP for agent %q: %w", agentName, err)
	}
	abc.logger.Info("config MCP registered", "agent", agentName, "tools", len(toolMgr.ToolDefs()))
	return nil
}

// connectWebMCP creates the per-agent Web MCP server (search + fetch tools),
// connects it, and registers its tools into the agent's tool manager.
func connectWebMCP(ctx context.Context, agentName string, cfg *config.Config, permTier func() string, toolMgr *tool.Manager, logger *slog.Logger) error {
	if !cfg.Web.Enabled {
		return nil
	}

	var searchProvider websearch.Provider
	sp, err := websearch.NewProvider(cfg.Web.Search)
	if err != nil {
		logger.Warn("web search provider not available", "error", err)
	} else {
		searchProvider = sp
	}

	fetcher := buildWebFetcher(cfg.Web.Fetch, logger)

	srv := webmcp.New(webmcp.Deps{
		SearchProvider: searchProvider,
		Fetcher:        fetcher,
		PermissionTier: permTier,
		Logger:         logger,
	})
	session, err := srv.Connect(ctx)
	if err != nil {
		return fmt.Errorf("starting web MCP for agent %q: %w", agentName, err)
	}
	if err := toolMgr.RegisterSession(ctx, "web-"+agentName, session); err != nil {
		return fmt.Errorf("registering web MCP for agent %q: %w", agentName, err)
	}
	logger.Info("web MCP registered", "agent", agentName, "tools", len(toolMgr.ToolDefs()))
	return nil
}

// buildWebFetcher constructs a Fetcher (with optional Jina fallback chain) from config.
func buildWebFetcher(fc config.WebFetchConfig, logger *slog.Logger) webfetch.Fetcher {
	timeout, err := time.ParseDuration(fc.Timeout)
	if err != nil {
		timeout = 30 * time.Second
	}

	primary := webfetch.NewDefaultFetcher(webfetch.Options{
		Timeout:          timeout,
		MaxSizeBytes:     fc.MaxSizeBytes,
		UserAgent:        fc.UserAgent,
		RespectRobotsTxt: fc.RespectRobotsTxt,
		RespectAgentsTxt: fc.RespectAgentsTxt,
		Logger:           logger,
	})

	if !fc.Jina.Enabled {
		return primary
	}

	jina := webfetch.NewJinaFetcher(timeout, logger)
	return webfetch.NewChainFetcher(primary, jina, logger)
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
	if err := connectConfigMCP(ctx, ac.Name, agentSkillsDir, e, agentRouter, agentToolMgr, abc); err != nil {
		return nil, nil, err
	}

	// Connect per-agent Web MCP server (search + fetch) if enabled.
	if err := connectWebMCP(ctx, ac.Name, abc.cfg, e.PermissionTier, agentToolMgr, abc.logger); err != nil {
		return nil, nil, err
	}

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
		path = os.Getenv("DENKEEPER_CONFIG")
	}
	if path == "" {
		path = config.DefaultConfigPath()
	}

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	logger := initLogger(cfg)

	st, err := initStores(cfg, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	clients := initLLMClients(cfg)
	voiceOpts := initVoiceOpts(cfg, logger)

	adapters, tgAdapter, err := initAdapters(cfg, logger, voiceOpts)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st.approvalManager.StartExpiryWorker(ctx, time.Hour)

	kvCleanupInterval, _ := time.ParseDuration(cfg.KV.CleanupInterval)
	if kvCleanupInterval <= 0 {
		kvCleanupInterval = time.Hour
	}
	startKVCleanupWorker(ctx, st.kvStore, kvCleanupInterval, logger)

	sharedToolMgr, cleanups, err := initSharedTools(ctx, cfg, logger)
	for _, fn := range cleanups {
		defer fn()
	}
	if err != nil {
		return err
	}

	// Ensure shared tool manager exists for the lifecycle manager.
	if sharedToolMgr == nil {
		sharedToolMgr = tool.NewManager(logger)
		defer func() { _ = sharedToolMgr.Close() }()
	}

	lifecycleMgr := tool.NewLifecycleManager(sharedToolMgr, path, cfg.MaxTools, logger)

	// Track plugins loaded at startup so ListPlugins can report them.
	for name, pc := range cfg.Plugins {
		lifecycleMgr.TrackPlugin(name, pc)
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
		memory:          st.memory,
		sharedToolMgr:   sharedToolMgr,
		lifecycleMgr:    lifecycleMgr,
		approvalManager: st.approvalManager,
		kvStore:         st.kvStore,
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
		tgAdapter.SetCallbackResolver(approval.NewCallbackHandler(st.approvalManager, logger))
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
			Dispatcher:   dispatcher,
			Scheduler:    sched,
			CostTracker:  clients.cost,
			Memory:       st.memory,
			Config:       cfg,
			Approvals:    st.approvalManager,
			LifecycleMgr: lifecycleMgr,
			WebHandler:   web.Handler(),
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

// startKVCleanupWorker runs a background goroutine that periodically removes
// expired KV entries. Mirrors the approval expiry worker pattern.
func startKVCleanupWorker(ctx context.Context, store *kv.SQLiteStore, interval time.Duration, logger *slog.Logger) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := store.Cleanup(ctx); err != nil {
					logger.Warn("kv cleanup failed", "error", err)
				}
			}
		}
	}()
}
