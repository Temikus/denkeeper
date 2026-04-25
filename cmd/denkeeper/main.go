package main

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed IANA timezone database for minimal Docker images

	"github.com/spf13/cobra"

	"github.com/Temikus/denkeeper/internal/adapter"
	"github.com/Temikus/denkeeper/internal/adapter/discord"
	"github.com/Temikus/denkeeper/internal/adapter/telegram"
	"github.com/Temikus/denkeeper/internal/agent"
	"github.com/Temikus/denkeeper/internal/api"
	"github.com/Temikus/denkeeper/internal/approval"
	"github.com/Temikus/denkeeper/internal/audit"
	"github.com/Temikus/denkeeper/internal/browser"
	"github.com/Temikus/denkeeper/internal/config"
	"github.com/Temikus/denkeeper/internal/configmcp"
	"github.com/Temikus/denkeeper/internal/kv"
	"github.com/Temikus/denkeeper/internal/llm"
	anthropicllm "github.com/Temikus/denkeeper/internal/llm/anthropic"
	"github.com/Temikus/denkeeper/internal/llm/ollama"
	openaillm "github.com/Temikus/denkeeper/internal/llm/openai"
	"github.com/Temikus/denkeeper/internal/llm/openrouter"
	"github.com/Temikus/denkeeper/internal/llm/pricing"
	dkotel "github.com/Temikus/denkeeper/internal/otel"
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

	rootCmd.AddCommand(serveCmd, versionCmd, newKeysCmd(), newPluginCmd(), newSessionsCmd(), newPasswdCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// parseChannel delegates to config.ParseChannel for backward compatibility.
func parseChannel(channel string) (adapterName, externalID string, ok bool) {
	return config.ParseChannel(channel)
}

// scheduleChannelResult holds the resolved delivery targets for a schedule.
type scheduleChannelResult struct {
	conversationID string
	targets        []agent.AdapterBinding
}

// resolveScheduleChannel resolves a schedule's channel string into delivery
// targets. Supports both "adapter:externalID" and "@channelname" formats.
// For broadcast channels, all specific bindings are returned.
func resolveScheduleChannel(channel string, d *agent.Dispatcher) (*scheduleChannelResult, error) {
	if channel == "" {
		return &scheduleChannelResult{}, nil
	}
	if channelName, isRef := config.ParseChannelRef(channel); isRef {
		channels := d.Channels()
		if channels == nil {
			return nil, fmt.Errorf("channel @%s not found: channels not configured", channelName)
		}
		ch, found := channels[channelName]
		if !found {
			return nil, fmt.Errorf("channel @%s not found", channelName)
		}
		convID := ch.ConversationID()
		if ch.IsBroadcast() {
			bindings := ch.ResolveAllBindings()
			if len(bindings) == 0 {
				return nil, fmt.Errorf("channel @%s has no specific adapter bindings for broadcast delivery", channelName)
			}
			return &scheduleChannelResult{conversationID: convID, targets: bindings}, nil
		}
		adapter, eid, wildcard, ok := ch.ResolveBinding()
		if !ok || wildcard {
			return nil, fmt.Errorf("channel @%s has only wildcard adapter bindings — schedules require a specific adapter:externalID binding", channelName)
		}
		return &scheduleChannelResult{
			conversationID: convID,
			targets:        []agent.AdapterBinding{{Adapter: adapter, ExternalID: eid}},
		}, nil
	}
	a, eid, ok := parseChannel(channel)
	if !ok {
		return nil, fmt.Errorf("invalid channel format %q", channel)
	}
	return &scheduleChannelResult{targets: []agent.AdapterBinding{{Adapter: a, ExternalID: eid}}}, nil
}

// buildChannelResolver returns a ChannelResolver that looks up channels from
// the dispatcher. For broadcast channels, all specific bindings are returned.
// For single-delivery channels (or unset), only the first specific binding is
// returned. Returns nil for channels that cannot be resolved.
func buildChannelResolver(d *agent.Dispatcher) configmcp.ChannelResolver {
	return func(name string) *configmcp.ChannelResolveResult {
		channels := d.Channels()
		if channels == nil {
			return nil
		}
		ch, found := channels[name]
		if !found {
			return nil
		}
		if ch.IsBroadcast() {
			bindings := ch.ResolveAllBindings()
			if len(bindings) == 0 {
				return nil
			}
			return &configmcp.ChannelResolveResult{
				ConversationID: ch.ConversationID(),
				Bindings:       bindings,
				Broadcast:      true,
			}
		}
		adapter, eid, wildcard, ok := ch.ResolveBinding()
		if !ok || wildcard {
			return nil
		}
		return &configmcp.ChannelResolveResult{
			ConversationID: ch.ConversationID(),
			Bindings:       []agent.AdapterBinding{{Adapter: adapter, ExternalID: eid}},
		}
	}
}

// llmClients holds the initialized LLM provider clients.
type llmClients struct {
	providers         map[string]llm.Provider // keyed by instance name
	cost              *llm.CostTracker
	fallbacks         []llm.FallbackRule
	pricing           *pricing.Registry
	streamIdleTimeout time.Duration
}

// stores holds the initialized persistence stores and the approval manager.
type stores struct {
	memory          agent.MemoryStore
	approvalStore   *approval.SQLiteStore
	approvalManager *approval.Manager
	kvStore         *kv.SQLiteStore
	auditStore      *audit.SQLiteStore
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

	// Audit store uses a separate DB file to avoid contention with the main store.
	var auditStore *audit.SQLiteStore
	if cfg.Audit.AuditEnabled() {
		auditDBPath := filepath.Join(filepath.Dir(cfg.Memory.DBPath), "audit.db")
		auditStore, err = audit.NewSQLiteStore(auditDBPath)
		if err != nil {
			_ = kvStore.Close()
			_ = approvalStore.Close()
			_ = memory.Close()
			return stores{}, fmt.Errorf("initializing audit store: %w", err)
		}
	}

	return stores{
		memory:          memory,
		approvalStore:   approvalStore,
		approvalManager: approval.NewManager(approvalStore, logger),
		kvStore:         kvStore,
		auditStore:      auditStore,
	}, nil
}

// closeStores closes all persistence stores in reverse order.
func (s stores) Close() {
	if s.auditStore != nil {
		_ = s.auditStore.Close()
	}
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
	providers := make(map[string]llm.Provider, len(cfg.LLM.Providers))
	for _, pc := range cfg.LLM.Providers {
		p := createProvider(pc, cfg)
		if p != nil {
			providers[pc.Name] = p
		}
	}

	fallbackRules := convertFallbacks(cfg.LLM.Fallbacks)

	// Build pricing registry with bundled defaults + operator overrides.
	reg := pricing.New()
	if cfg.Costs.DefaultRatePerKTokens > 0 {
		reg.SetFallbackRate(cfg.Costs.DefaultRatePerKTokens)
	}
	for prefix, mp := range cfg.Costs.ModelPrices {
		reg.RegisterPrefix(prefix, pricing.ModelPrice{
			InputPerMTok:       mp.InputPerMTok,
			OutputPerMTok:      mp.OutputPerMTok,
			CachedInputPerMTok: mp.CachedInputPerMTok,
		})
	}

	return llmClients{
		providers:         providers,
		cost:              buildCostTracker(cfg),
		fallbacks:         fallbackRules,
		pricing:           reg,
		streamIdleTimeout: time.Duration(cfg.LLM.StreamIdleTimeoutSecs) * time.Second,
	}
}

// createProvider instantiates an llm.Provider from a ProviderInstanceConfig.
func createProvider(pc config.ProviderInstanceConfig, cfg *config.Config) llm.Provider {
	switch pc.Type {
	case "anthropic":
		return anthropicllm.NewFull(pc.Name, pc.APIKey, pc.BaseURL)
	case "openai":
		return openaillm.NewFull(pc.Name, pc.APIKey, pc.BaseURL, pc.Organization)
	case "openrouter":
		client := openrouter.NewFull(pc.Name, pc.APIKey)
		r := &cfg.LLM.OpenRouter.Reasoning
		client.SetReasoning(r.Enabled, r.Effort, r.MaxTokens, r.Exclude)
		return client
	case "ollama":
		return ollama.NewFull(pc.Name, pc.BaseURL)
	default:
		return nil
	}
}

func buildCostTracker(cfg *config.Config) *llm.CostTracker {
	defaults := llm.SessionLimits{
		Soft: cfg.LLM.CostLimitSoft,
		Hard: cfg.LLM.CostLimitHard,
	}
	overrides := make(map[string]llm.SessionLimits)
	for _, a := range cfg.Agents {
		if a.CostLimitSoft != nil || a.CostLimitHard != nil {
			l := defaults // start from global defaults
			if a.CostLimitSoft != nil {
				l.Soft = *a.CostLimitSoft
			}
			if a.CostLimitHard != nil {
				l.Hard = *a.CostLimitHard
			}
			overrides[a.Name] = l
		}
	}
	return llm.NewCostTracker(defaults, overrides)
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

// initOAuthAndRegisterTools initialises OAuth support and registers the
// tools that were deferred during initSharedTools (those with auth=oauth).
func initOAuthAndRegisterTools(ctx context.Context, cfg *config.Config, mgr *tool.Manager, logger *slog.Logger) (*oauthState, *api.OAuthDeps, error) {
	oauthSt, oauthDeps, err := initOAuthSupport(ctx, cfg, mgr, logger)
	if err != nil {
		return nil, nil, fmt.Errorf("initializing OAuth support: %w", err)
	}
	if err := registerDeferredOAuthTools(ctx, cfg, mgr, logger); err != nil {
		oauthSt.Close()
		return nil, nil, err
	}
	return oauthSt, oauthDeps, nil
}

// registerWithInitRetry wraps mgr.RegisterServer with exponential-backoff
// retry for remote (sse/http) transports. Stdio transports register once —
// their lifecycle is owned by the subprocess we spawn, so retry makes no sense.
//
// registerWithInitRetry tries to connect to an MCP server. For remote
// transports (SSE/HTTP), the first attempt is synchronous (fast fail on
// connection-refused). If it fails and retries are configured, the tool is
// registered as "connecting" and remaining retries happen in a background
// goroutine so that startup is not blocked.
//
// Returns (true, nil) when background retries were launched, (false, nil)
// on immediate success, or (false, err) for a non-retryable failure.
func registerWithInitRetry(ctx context.Context, mgr *tool.Manager, mcpCfg config.MCPConfig, name string, tc config.ToolConfig, logger *slog.Logger) (backgroundRetry bool, err error) {
	isRemote := tc.Transport == "sse" || tc.Transport == "sse-legacy" || tc.Transport == "http"

	// First attempt — always synchronous.
	if err := mgr.RegisterServer(ctx, name, tc); err == nil {
		return false, nil
	} else if !isRemote || mcpCfg.InitRetryAttempts <= 1 {
		// Local (stdio) servers or no retries configured — fail immediately.
		return false, err
	} else {
		// Remote server failed; register placeholder and retry in background.
		mgr.RegisterPending(name, tc, err.Error())

		attempts := mcpCfg.InitRetryAttempts
		baseBackoff := 2 * time.Second
		if d, parseErr := time.ParseDuration(mcpCfg.InitRetryBackoff); parseErr == nil && d > 0 {
			baseBackoff = d
		}
		const maxBackoff = 30 * time.Second

		logger.Warn("tool server initial connection failed, retrying in background",
			"name", name, "remaining_attempts", attempts-1, "error", err)

		go func() {
			for attempt := 2; attempt <= attempts; attempt++ {
				shift := attempt - 1
				if shift >= 63 {
					shift = 63
				}
				delay := baseBackoff << shift
				if delay > maxBackoff || delay <= 0 {
					delay = maxBackoff
				}

				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}

				if err := mgr.RegisterServer(ctx, name, tc); err == nil {
					logger.Info("tool server connected (background retry)",
						"name", name, "attempt", attempt, "transport", tc.Transport)
					return
				} else {
					logger.Warn("tool server background retry failed",
						"name", name, "attempt", attempt, "max_attempts", attempts, "error", err)
				}
			}
			mgr.MarkDisabled(name)
			logger.Error("tool server disabled after background retries exhausted — fix the configuration and restart",
				"name", name, "attempts", attempts)
		}()

		return true, nil
	}
}

// registerNonOAuthTools registers tools that don't require OAuth at startup.
// OAuth tools are deferred until after initOAuthSupport wires the handler factory.
func registerNonOAuthTools(ctx context.Context, tools map[string]config.ToolConfig, mgr *tool.Manager, mcpCfg config.MCPConfig, logger *slog.Logger) error {
	for name, tc := range tools {
		if tc.Auth == "oauth" {
			logger.Info("tool server deferred (needs OAuth)", "name", name)
			continue
		}
		bg, err := registerWithInitRetry(ctx, mgr, mcpCfg, name, tc, logger)
		if err != nil {
			logger.Error("tool server disabled due to initialization error — fix the configuration and restart",
				"name", name, "error", err)
			continue
		}
		if !bg {
			logger.Info("tool server registered", "name", name, "transport", tc.Transport, "command", tc.Command)
		}
	}
	return nil
}

// registerDeferredOAuthTools registers tools with auth="oauth" that were
// skipped by initSharedTools. These must run after initOAuthSupport wires
// the OAuth handler factory into the tool manager.
func registerDeferredOAuthTools(ctx context.Context, cfg *config.Config, mgr *tool.Manager, logger *slog.Logger) error {
	for name, tc := range cfg.Tools {
		if tc.Auth != "oauth" {
			continue
		}
		bg, err := registerWithInitRetry(ctx, mgr, cfg.MCP, name, tc, logger)
		if err != nil {
			logger.Error("OAuth tool server disabled due to initialization error — fix the configuration and restart",
				"name", name, "error", err)
			continue
		}
		if !bg {
			logger.Info("tool server registered", "name", name, "transport", tc.Transport, "auth", tc.Auth)
		}
	}
	return nil
}

func initSharedTools(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*tool.Manager, string, []cleanupFunc, error) {
	var sharedToolMgr *tool.Manager
	var cleanups []cleanupFunc

	// Helper to ensure sharedToolMgr is initialised exactly once.
	ensureToolMgr := func() {
		if sharedToolMgr == nil {
			sharedToolMgr = tool.NewManager(logger, cfg.MCP)
			cleanups = append(cleanups, func() { _ = sharedToolMgr.Close() })
		}
	}

	if len(cfg.Tools) > 0 {
		ensureToolMgr()
		if err := registerNonOAuthTools(ctx, cfg.Tools, sharedToolMgr, cfg.MCP, logger); err != nil {
			return nil, "", cleanups, err
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
		if hasSandboxedPlugins(cfg.Plugins) {
			if err := ensureSandboxRT(); err != nil {
				return nil, "", cleanups, err
			}
		}
		if err := loadPlugins(ctx, cfg, sandboxRT, sharedToolMgr, logger); err != nil {
			return nil, "", cleanups, err
		}
	}

	// Browser automation — resolve profile dir and register as Docker-based MCP server.
	var browserProfileDir string
	if cfg.Browser.Enabled {
		ensureToolMgr()
		if err := ensureSandboxRT(); err != nil {
			return nil, "", cleanups, fmt.Errorf("browser sandbox runtime: %w", err)
		}
		var err error
		browserProfileDir, err = setupBrowser(ctx, cfg, sandboxRT, sharedToolMgr, logger)
		if err != nil {
			return nil, "", cleanups, err
		}
	}

	return sharedToolMgr, browserProfileDir, cleanups, nil
}

// loadPlugins initializes and starts configured plugins, registering their MCP tools.
func loadPlugins(ctx context.Context, cfg *config.Config, sandboxRT sandbox.Runtime, toolMgr *tool.Manager, logger *slog.Logger) error {
	existingToolNames := make(map[string]bool, len(cfg.Tools))
	for name := range cfg.Tools {
		existingToolNames[name] = true
	}

	var verifyOpts *plugin.VerifyOpts
	if len(cfg.Security.TrustedKeys) > 0 {
		trustedKeys, keyErr := security.LoadTrustedKeys(cfg.Security.TrustedKeys)
		if keyErr != nil {
			return fmt.Errorf("loading trusted plugin keys: %w", keyErr)
		}
		verifyOpts = &plugin.VerifyOpts{
			TrustedKeys:   trustedKeys,
			AllowUnsigned: cfg.Security.AllowUnsigned != nil && *cfg.Security.AllowUnsigned,
		}
	}

	pluginMgr := plugin.NewManager(logger, verifyOpts, sandboxRT)
	if err := pluginMgr.Load(cfg.Plugins, existingToolNames); err != nil {
		return fmt.Errorf("loading plugins: %w", err)
	}
	if err := pluginMgr.Start(ctx, toolMgr); err != nil {
		logger.Warn("one or more plugins failed to start", "error", err)
	}
	logger.Info("plugins loaded", "count", pluginMgr.Count())
	return nil
}

// resolveBrowserProfileDir resolves the browser profile base directory to an
// absolute path, creating it if necessary.
func resolveBrowserProfileDir(cfg *config.Config) (string, error) {
	profileDir := cfg.Browser.ProfileDir
	if !filepath.IsAbs(profileDir) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving browser profile dir: %w", err)
		}
		profileDir = filepath.Join(home, ".denkeeper", profileDir)
	}
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return "", fmt.Errorf("creating browser profile dir %q: %w", profileDir, err)
	}
	return profileDir, nil
}

// setupBrowser resolves the profile directory and registers the browser MCP server.
// Returns the resolved profile directory path.
func setupBrowser(ctx context.Context, cfg *config.Config, rt sandbox.Runtime, toolMgr *tool.Manager, logger *slog.Logger) (string, error) {
	profileDir, err := resolveBrowserProfileDir(cfg)
	if err != nil {
		return "", err
	}
	if err := registerBrowser(ctx, cfg, profileDir, rt, toolMgr, logger); err != nil {
		return "", err
	}
	return profileDir, nil
}

// registerBrowser spawns the browser automation container and registers its MCP tools.
func registerBrowser(ctx context.Context, cfg *config.Config, profileDir string, rt sandbox.Runtime, toolMgr *tool.Manager, logger *slog.Logger) error {
	env := map[string]string{}
	if len(cfg.Browser.URLAllowlist.Domains) > 0 {
		env["BROWSER_URL_ALLOWLIST"] = strings.Join(cfg.Browser.URLAllowlist.Domains, ",")
	}

	proc, err := rt.Spawn(ctx, "denkeeper-browser", sandbox.SpawnOpts{
		Image:       cfg.Browser.Image,
		Args:        []string{"--headless", "--browser", "chromium", "--no-sandbox"},
		Network:     sandbox.NetworkEgress,
		MemoryLimit: cfg.Browser.MemoryLimit,
		CPULimit:    cfg.Browser.CPULimit,
		Env:         env,
		Tmpfs:       []string{"/tmp:size=64m"},
		ShmSize:     "64m",
		Volumes:     []string{profileDir + ":/data/profile"},
	})
	if err != nil {
		return fmt.Errorf("starting browser plugin: %w", err)
	}
	if err := toolMgr.RegisterServer(ctx, "browser", config.ToolConfig{Command: proc.Command, Args: proc.Args, Env: proc.Env}); err != nil {
		return fmt.Errorf("registering browser tools: %w", err)
	}
	logger.Info("browser automation registered",
		"image", cfg.Browser.Image,
		"profile_dir", profileDir,
	)
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
	configPath      string
	llm             llmClients
	memory          agent.MemoryStore
	sharedToolMgr   *tool.Manager
	lifecycleMgr    *tool.LifecycleManager
	approvalManager *approval.Manager
	kvStore         kv.Store
	browserProfiles *browser.ProfileService
	globalSkills    []skill.Skill
	sched           *scheduler.Scheduler
	adapters        []adapter.Adapter
	dispatcher      *agent.Dispatcher
	auditor         audit.Emitter
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
		GetSkill:       e.GetSkill,
		UpdateSkill:    e.UpdateSkill,
		RemoveSkill:    e.RemoveSkill,
		Sched:          abc.sched,
		HandleMessage:  e.HandleMessage,
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
		BrowserProfiles:    abc.browserProfiles,
		GetPersonaSection:  e.PersonaSection,
		SavePersonaSection: e.SavePersonaSection,
		AppendMemoryEntry:  e.AppendMemoryEntry,
		RemoveMemoryEntry:  e.RemoveMemoryEntry,
		ConfigPath:         abc.configPath,
		ChannelResolver:    buildChannelResolver(abc.dispatcher),
		GetChannels: func() map[string]*agent.Channel {
			return abc.dispatcher.Channels()
		},
		SetActiveChannel:         abc.dispatcher.SetActiveChannelByKey,
		ActiveChannelsForChannel: abc.dispatcher.ActiveChannelsForChannel,
		Auditor:                  abc.auditor,
		Logger:                   abc.logger,
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
	if cfg.Web.Enabled != nil && !*cfg.Web.Enabled {
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

// agentSkillResult holds the resolved skills and directories for an agent.
type agentSkillResult struct {
	skills          []skill.Skill
	agentSkillsDir  string
	globalSkillsDir string
}

// loadAgentSkills merges global and agent-specific skills.
func loadAgentSkills(ac config.AgentInstanceConfig, abc agentBuildCtx) agentSkillResult {
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

	merged := skill.MergeSkills(effectiveGlobal, agentSkills)
	if len(merged) > 0 {
		abc.logger.Info("skills loaded for agent", "agent", ac.Name, "count", len(merged))
	}

	return agentSkillResult{
		skills:          merged,
		agentSkillsDir:  agentSkillsDir,
		globalSkillsDir: effectiveGlobalSkillsDir,
	}
}

// convertFallbacks converts config fallback rules to LLM router fallback rules.
func convertFallbacks(cfgs []config.FallbackConfig) []llm.FallbackRule {
	if len(cfgs) == 0 {
		return nil
	}
	rules := make([]llm.FallbackRule, len(cfgs))
	for i, f := range cfgs {
		rules[i] = llm.FallbackRule{
			Trigger:    f.Trigger,
			Action:     f.Action,
			Provider:   f.Provider,
			Model:      f.Model,
			Threshold:  f.Threshold,
			MaxRetries: f.MaxRetries,
			Backoff:    f.Backoff,
		}
	}
	return rules
}

// buildAgentRouter creates a per-agent LLM router with provider registrations.
func buildAgentRouter(provider, model string, abc agentBuildCtx) *llm.Router {
	router := llm.NewRouter(provider, model, abc.llm.cost)
	for _, p := range abc.llm.providers {
		router.RegisterProvider(p)
	}
	if len(abc.llm.fallbacks) > 0 {
		router.SetFallbacks(abc.llm.fallbacks)
	}
	if abc.llm.pricing != nil {
		router.SetPricing(abc.llm.pricing)
	}
	if abc.llm.streamIdleTimeout > 0 {
		router.SetStreamIdleTimeout(abc.llm.streamIdleTimeout)
	}
	return router
}

// buildChannels converts config.ChannelConfig entries into agent.Channel pointers.
// Channels are always present in cfg.Channels after applyDefaults runs —
// either explicitly defined by the user or synthesized from agent adapter bindings.
func buildChannels(cfg *config.Config) []*agent.Channel {
	channels := make([]*agent.Channel, 0, len(cfg.Channels))
	for _, cc := range cfg.Channels {
		channels = append(channels, &agent.Channel{
			Name:        cc.Name,
			AgentName:   cc.Agent,
			Adapters:    cc.Adapters,
			Delivery:    cc.Delivery,
			Implicit:    cc.Implicit,
			SessionMode: cc.SessionMode,
		})
	}
	return channels
}

// buildDispatcherWithChannels creates a Dispatcher wired with channel-based routing.
// It synthesizes channels from config, sets up the active channel store, loads
// persisted /session selections, and returns the ready-to-use dispatcher.
func buildDispatcherWithChannels(
	ctx context.Context,
	cfg *config.Config,
	engines map[string]*agent.Engine,
	bindings []agent.Binding,
	adapters []adapter.Adapter,
	memory agent.MemoryStore,
	logger *slog.Logger,
) *agent.Dispatcher {
	channels := buildChannels(cfg)
	var opts []agent.DispatcherOption
	if len(channels) > 0 {
		var activeStore agent.ActiveChannelStore
		if acs, ok := memory.(agent.ActiveChannelStore); ok {
			activeStore = acs
		}
		opts = append(opts, agent.WithChannels(channels, activeStore))
	}
	d := agent.NewDispatcher(engines, bindings, adapters, logger, opts...)
	if err := d.LoadActiveChannels(ctx); err != nil {
		logger.Warn("failed to load active channel selections", "error", err)
	}
	return d
}

// buildAllAgents creates an Engine for each configured agent and collects their bindings.
func buildAllAgents(ctx context.Context, agents []config.AgentInstanceConfig, abc agentBuildCtx) (map[string]*agent.Engine, []agent.Binding, error) {
	engines := make(map[string]*agent.Engine, len(agents))
	var bindings []agent.Binding
	for _, ac := range agents {
		e, agentBindings, buildErr := buildAgentEngine(ctx, ac, abc)
		if buildErr != nil {
			return nil, nil, buildErr
		}
		engines[ac.Name] = e
		bindings = append(bindings, agentBindings...)
	}
	return engines, bindings, nil
}

func buildAgentEngine(ctx context.Context, ac config.AgentInstanceConfig, abc agentBuildCtx) (*agent.Engine, []agent.Binding, error) {
	// Per-agent persona
	p, pErr := persona.Load(ac.PersonaDir)
	if pErr != nil {
		abc.logger.Warn("persona files not loaded, using empty persona", "agent", ac.Name, "dir", ac.PersonaDir, "error", pErr)
		p = persona.NewEmpty(ac.PersonaDir)
	} else {
		abc.logger.Info("persona loaded", "agent", ac.Name, "dir", ac.PersonaDir)
	}

	sr := loadAgentSkills(ac, abc)

	// Per-agent permission tier (fall back to global session.tier)
	tier := abc.cfg.Session.Tier
	if ac.SessionTier != "" {
		tier = ac.SessionTier
	}
	permissions, pErr := security.NewPermissionEngine(tier)
	if pErr != nil {
		return nil, nil, fmt.Errorf("initializing permissions for agent %q: %w", ac.Name, pErr)
	}

	agentToolMgr := tool.NewManager(abc.logger)
	if abc.sharedToolMgr != nil {
		agentToolMgr.AdoptFrom(abc.sharedToolMgr)
	}

	model := abc.cfg.LLM.DefaultModel
	if ac.LLMModel != "" {
		model = ac.LLMModel
	}
	provider := abc.cfg.LLM.DefaultProvider
	if ac.LLMProvider != "" {
		provider = ac.LLMProvider
	}
	agentRouter := buildAgentRouter(provider, model, abc)

	// Per-agent fallback overrides replace global rules when defined.
	if len(ac.Fallbacks) > 0 {
		agentRouter.SetFallbacks(convertFallbacks(ac.Fallbacks))
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
		sr.skills,
		agentToolMgr,
		abc.approvalManager,
		abc.logger,
	)
	e.SetMaxContextMessages(ac.MaxContextMessages)
	e.SetMaxToolRounds(ac.MaxToolRounds)
	e.SetSkillDirs(sr.agentSkillsDir, sr.globalSkillsDir)
	e.SetScheduler(abc.sched)
	e.SetAuditor(abc.auditor)
	approvalTimeout, _ := time.ParseDuration(abc.cfg.Session.ApprovalTimeout) // validated by config.Parse
	e.SetApprovalConfig(approvalTimeout, abc.cfg.Session.ApprovalRetries)

	if err := connectConfigMCP(ctx, ac.Name, sr.agentSkillsDir, e, agentRouter, agentToolMgr, abc); err != nil {
		return nil, nil, err
	}
	if err := connectWebMCP(ctx, ac.Name, abc.cfg, e.PermissionTier, agentToolMgr, abc.logger); err != nil {
		return nil, nil, err
	}

	agentRouter.SetTools(agentToolMgr.ToolDefs)

	var bindings []agent.Binding
	for _, binding := range ac.Adapters {
		bindings = append(bindings, agent.Binding{Pattern: binding, AgentName: ac.Name})
	}

	abc.logger.Info("agent initialized",
		"agent", ac.Name,
		"model", model,
		"tier", tier,
		"skills", len(sr.skills),
		"max_tool_rounds", e.MaxToolRounds(),
	)

	return e, bindings, nil
}

func registerSchedules(ctx context.Context, cfg *config.Config, sched *scheduler.Scheduler, dispatcher *agent.Dispatcher, auditor audit.Emitter, logger *slog.Logger) error {
	for _, sc := range cfg.Schedules {
		sc := sc // capture loop variable
		text := "[Scheduled trigger: " + sc.Name + "]"
		if sc.Skill != "" {
			text = "[Scheduled: " + sc.Skill + "]"
		}

		resolved, chanErr := resolveScheduleChannel(sc.Channel, dispatcher)
		if chanErr != nil {
			logger.Warn("schedule has invalid channel, skipping", "name", sc.Name, "channel", sc.Channel, "error", chanErr)
			continue
		}

		// Unknown agent = broken config → hard error. Missing skill = stale
		// reference that can be fixed at runtime → warn and skip.
		if sc.Skill != "" {
			eng := dispatcher.Agent(sc.Agent)
			if eng == nil {
				return fmt.Errorf("schedule %q targets unknown agent %q", sc.Name, sc.Agent)
			}
			if _, skillOK := eng.GetSkill(sc.Skill); !skillOK {
				logger.Error("schedule references missing skill, skipping", "name", sc.Name, "skill", sc.Skill, "agent", sc.Agent)
				continue
			}
		}

		targets := resolved.targets
		conversationID := resolved.conversationID
		broadcast := len(targets) > 1
		targetAgent := sc.Agent // defaults to "default" from applyDefaults

		if err := sched.Register(scheduler.Config{
			Name:        sc.Name,
			Type:        sc.Type,
			Schedule:    sc.Schedule,
			Skill:       sc.Skill,
			Agent:       sc.Agent,
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
			var failed, succeeded int
			var lastErr string
			for _, target := range targets {
				msg := adapter.IncomingMessage{
					Adapter:        target.Adapter,
					ExternalID:     target.ExternalID,
					ConversationID: conversationID,
					UserName:       "scheduler",
					Text:           text,
					SkillName:      sc.Skill,
					ScheduleName:   sc.Name,
					ScheduleCron:   sc.Schedule,
					IsScheduled:    true,
					Timestamp:      time.Now(),
				}
				if entry.SessionMode == "isolated" {
					msg.ConversationID = fmt.Sprintf("sched:%s:%d", entry.Name, time.Now().UnixNano())
				}
				if entry.SessionTier != "" {
					msg.SessionTier = entry.SessionTier
				}
				if err := dispatcher.Dispatch(ctx, targetAgent, msg); err != nil {
					failed++
					lastErr = err.Error()
					logger.Error("dispatching scheduled message", "name", entry.Name, "agent", targetAgent,
						"target", target.Adapter+":"+target.ExternalID, "error", err)
				} else {
					succeeded++
				}
			}
			configmcp.EmitBroadcastFailure(ctx, auditor, broadcast, entry.Name, sc.Channel, conversationID, succeeded, failed, lastErr)
		}); err != nil {
			return fmt.Errorf("registering schedule %q: %w", sc.Name, err)
		}
	}
	return nil
}

func otelMetricsHandler(cfg *config.Config) http.Handler {
	if cfg.OTel.Enabled {
		return dkotel.PrometheusHandler()
	}
	return nil
}

func startAPIServer(ctx context.Context, cfg *config.Config, deps api.Deps, logger *slog.Logger) (*api.Server, error) {
	keyStore, ksErr := api.NewKeyStore(cfg.Memory.DBPath)
	if ksErr != nil {
		return nil, fmt.Errorf("initializing api key store: %w", ksErr)
	}

	existingKeys, _ := keyStore.List(ctx)
	hasActiveKey := len(cfg.API.Keys) > 0
	for _, k := range existingKeys {
		if !k.Revoked {
			hasActiveKey = true
			break
		}
	}
	if !hasActiveKey && cfg.API.Auth.PasswordHash == "" {
		logger.Warn("no API keys or password found — open the web dashboard to complete setup")
	}

	deps.KeyStore = keyStore

	// Initialize auth subsystem if configured.
	if err := initAPIAuth(ctx, cfg, &deps, logger); err != nil {
		return nil, fmt.Errorf("initializing auth: %w", err)
	}

	// Generate a one-time setup PIN when no auth is configured yet.
	// The PIN secures the web-based account creation flow.
	if !hasActiveKey && cfg.API.Auth.PasswordHash == "" {
		pin, err := generateSetupPIN()
		if err != nil {
			return nil, fmt.Errorf("generating setup PIN: %w", err)
		}
		deps.SetupPIN = pin
		logger.Info("══════════════════════════════════════════════════")
		logger.Info("FIRST-RUN SETUP PIN", "pin", pin)
		logger.Info("Enter this PIN in the web dashboard to create your admin account.")
		logger.Info("══════════════════════════════════════════════════")
	}

	apiServer := api.New(cfg.API, deps, logger)
	go func() {
		if err := apiServer.Run(ctx); err != nil && ctx.Err() == nil {
			logger.Error("api server error", "error", err)
		}
	}()
	return apiServer, nil
}

// startAPIAndWireBroadcast starts the API server and wires the adapter→WebSocket
// broadcast so the web UI is notified when messages arrive via external adapters.
func startAPIAndWireBroadcast(ctx context.Context, cfg *config.Config, dispatcher *agent.Dispatcher, deps api.Deps, logger *slog.Logger) error {
	apiServer, err := startAPIServer(ctx, cfg, deps, logger)
	if err != nil {
		return err
	}

	hub := apiServer.WSHub()
	if hub != nil {
		dispatcher.OnBroadcast = func(agentName, convID, adapterName, channelName, summary string) {
			hub.Broadcast(api.ActivityFrame{
				Type:           api.FrameTypeActivity,
				ConversationID: convID,
				Agent:          agentName,
				Adapter:        adapterName,
				Channel:        channelName,
				Summary:        summary,
			})
		}
	}

	// Wire panic/resume hooks — pause scheduler and broadcast status.
	sched := deps.Scheduler
	dispatcher.OnPanic = func() {
		if sched != nil {
			sched.Pause()
		}
		if hub != nil {
			hub.Broadcast(api.PanicStatusFrame{
				Type:    api.FrameTypePanicStatus,
				Active:  true,
				Message: "Emergency stop triggered — all processing paused",
			})
		}
	}
	dispatcher.OnResume = func() {
		if sched != nil {
			sched.Resume()
		}
		if hub != nil {
			hub.Broadcast(api.PanicStatusFrame{
				Type:    api.FrameTypePanicStatus,
				Active:  false,
				Message: "Processing resumed",
			})
		}
	}

	return nil
}

// ensureSessionSecret auto-generates and persists a stable session secret when
// none is configured. This ensures OAuth MCP tools work out of the box on
// fresh installs without requiring the user to manually generate a key.
func ensureSessionSecret(cfg *config.Config, configPath string) error {
	if cfg.API.Auth.SessionSecret != "" {
		return nil
	}
	secret, err := generateSessionSecret()
	if err != nil {
		return fmt.Errorf("generating session secret: %w", err)
	}
	if err := tool.SetSessionSecret(configPath, secret); err != nil {
		return fmt.Errorf("persisting session secret: %w", err)
	}
	cfg.API.Auth.SessionSecret = secret
	return nil
}

// generateSessionSecret returns a hex-encoded 32-byte (AES-256) random key.
func generateSessionSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := cryptorand.Read(b); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// generateSetupPIN returns a cryptographically random 6-digit PIN string.
func generateSetupPIN() (string, error) {
	n, err := cryptorand.Int(cryptorand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generating random PIN: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// resolveConfigPath returns the config file path from the CLI flag, env var, or default.
func resolveConfigPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	if envPath := os.Getenv("DENKEEPER_CONFIG"); envPath != "" {
		return envPath
	}
	return config.DefaultConfigPath()
}

func runServe(_ *cobra.Command, _ []string) error {
	path := resolveConfigPath()

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	if err := ensureSessionSecret(cfg, path); err != nil {
		return err
	}

	logger := initLogger(cfg)

	otelShutdown, err := dkotel.Setup(dkotel.Config{
		Enabled:        cfg.OTel.Enabled,
		TracesEndpoint: cfg.OTel.TracesEndpoint,
		ServiceName:    cfg.OTel.ServiceName,
	}, logger)
	if err != nil {
		return fmt.Errorf("initializing otel: %w", err)
	}
	defer func() { _ = otelShutdown(context.Background()) }()

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

	startKVCleanupWorker(ctx, st.kvStore, kvCleanupDuration(cfg.KV.CleanupInterval), logger)
	startMemoryCleanupWorker(ctx, st.memory, &cfg.Memory, logger)

	// Audit emitter — wired into engines, dispatcher, scheduler, and tool manager.
	auditor, auditCloser := initAuditor(ctx, st.auditStore, cfg, logger)
	defer auditCloser()

	sharedToolMgr, browserProfileDir, cleanups, err := initSharedTools(ctx, cfg, logger)
	for _, fn := range cleanups {
		defer fn()
	}
	if err != nil {
		return err
	}

	// Ensure shared tool manager exists for the lifecycle manager.
	if sharedToolMgr == nil {
		sharedToolMgr = tool.NewManager(logger, cfg.MCP)
		defer func() { _ = sharedToolMgr.Close() }()
	}

	// Initialize OAuth support and register deferred OAuth tools.
	oauthSt, oauthDeps, err := initOAuthAndRegisterTools(ctx, cfg, sharedToolMgr, logger)
	if err != nil {
		return err
	}
	defer oauthSt.Close()

	lifecycleMgr := tool.NewLifecycleManager(sharedToolMgr, path, cfg.MaxTools, logger)

	// Track plugins loaded at startup so ListPlugins can report them.
	for name, pc := range cfg.Plugins {
		lifecycleMgr.TrackPlugin(name, pc)
	}

	globalSkills := loadGlobalSkills(cfg.Agent.SkillsDir, logger)

	// Timezone is validated by config.Load; LoadLocation cannot fail here.
	schedLoc, _ := time.LoadLocation(cfg.API.Timezone)
	sched := scheduler.New(logger, schedLoc)

	browserProfiles := newBrowserProfiles(cfg, browserProfileDir, logger)

	dispatcher := agent.NewDispatcher(nil, nil, adapters, logger)
	dispatcher.Auditor = auditor
	sched.Auditor = auditor
	sharedToolMgr.Auditor = auditor
	st.approvalManager.Auditor = auditor

	abc := agentBuildCtx{
		cfg:             cfg,
		configPath:      path,
		llm:             clients,
		memory:          st.memory,
		sharedToolMgr:   sharedToolMgr,
		lifecycleMgr:    lifecycleMgr,
		approvalManager: st.approvalManager,
		kvStore:         st.kvStore,
		browserProfiles: browserProfiles,
		globalSkills:    globalSkills,
		sched:           sched,
		adapters:        adapters,
		dispatcher:      dispatcher,
		auditor:         auditor,
		logger:          logger,
	}

	engines, bindings, err := buildAllAgents(ctx, cfg.Agents, abc)
	if err != nil {
		return err
	}

	// Re-create dispatcher with the fully wired engines, bindings, and channels.
	dispatcher = buildDispatcherWithChannels(ctx, cfg, engines, bindings, adapters, st.memory, logger)

	// Update abc.dispatcher so the AgentFactory closure captures the final
	// dispatcher (with channels and engines wired up).
	abc.dispatcher = dispatcher

	wireCallbackResolver(tgAdapter, st.approvalManager, logger)
	wireSkillCommands(tgAdapter, engines, logger)

	if err := registerSchedules(ctx, cfg, sched, dispatcher, auditor, logger); err != nil {
		return err
	}
	sched.Start()
	defer sched.Stop()

	handleShutdownSignals(cancel, logger)

	if cfg.API.IsEnabled() {
		if err := startAPIAndWireBroadcast(ctx, cfg, dispatcher, api.Deps{
			Dispatcher:        dispatcher,
			Scheduler:         sched,
			CostTracker:       clients.cost,
			Memory:            st.memory,
			Config:            cfg,
			Approvals:         st.approvalManager,
			LifecycleMgr:      lifecycleMgr,
			BrowserProfiles:   browserProfiles,
			WebHandler:        web.Handler(),
			MetricsHandler:    otelMetricsHandler(cfg),
			KVStore:           st.kvStore,
			AuditStore:        st.auditStore,
			Auditor:           auditor,
			ConfigPath:        path,
			ModelLister:       dispatcher.ListModels,
			ModelDetailLister: dispatcher.ListModelDetails,
			OAuthDeps:         oauthDeps,
			ReloadFunc:        buildReloadFunc(path, cfg, logger),
			RestartFunc:       selfRestartFunc,
			AgentFactory: func(ac config.AgentInstanceConfig) (*agent.Engine, []agent.Binding, error) {
				return buildAgentEngine(ctx, ac, abc)
			},
			Version:   version,
			Commit:    commit,
			BuildDate: date,
		}, logger); err != nil {
			return err
		}
	}

	// Start MCP server health checker (respects [mcp] auto_restart config).
	sharedToolMgr.StartHealthChecker(ctx, 30*time.Second)

	logger.Info("denkeeper starting",
		"agents", len(engines),
		"provider", cfg.LLM.DefaultProvider,
		"default_model", cfg.LLM.DefaultModel,
	)

	return dispatcher.Run(ctx)
}

// wireCallbackResolver sets the Telegram callback resolver when a Telegram adapter is active.
func wireCallbackResolver(tgAdapter *telegram.Adapter, approvalMgr *approval.Manager, logger *slog.Logger) {
	if tgAdapter != nil {
		tgAdapter.SetCallbackResolver(approval.NewCallbackHandler(approvalMgr, logger))
	}
}

// wireSkillCommands registers skill command triggers with the Telegram adapter
// so they appear in the Telegram command picker alongside built-in commands.
func wireSkillCommands(tgAdapter *telegram.Adapter, engines map[string]*agent.Engine, logger *slog.Logger) {
	if tgAdapter == nil {
		return
	}

	seen := make(map[string]bool)
	var cmds []telegram.SkillCommand
	for _, e := range engines {
		for _, s := range e.Skills() {
			for _, t := range s.ParsedTriggers {
				if t.Type == skill.TriggerCommand && !seen[t.Command] {
					seen[t.Command] = true
					desc := s.Description
					if desc == "" {
						desc = s.Name
					}
					cmds = append(cmds, telegram.SkillCommand{
						Command:     t.Command,
						Description: desc,
					})
				}
			}
		}
	}

	if len(cmds) > 0 {
		tgAdapter.RegisterSkillCommands(cmds)
		logger.Info("registered skill commands with telegram", "count", len(cmds))
	}
}

// buildReloadFunc returns a function that re-reads the config file from disk
// and overwrites cfg in place, allowing hot-reloading of most settings.
func buildReloadFunc(path string, cfg *config.Config, logger *slog.Logger) func() error {
	return func() error {
		newCfg, err := config.Load(path)
		if err != nil {
			return fmt.Errorf("reloading config: %w", err)
		}
		*cfg = *newCfg
		logger.Info("config reloaded from disk", "path", path)
		return nil
	}
}

// selfRestartFunc sends SIGTERM to the current process so that a process
// manager (systemd, Docker, K8s) can restart it.
func selfRestartFunc() error {
	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		return fmt.Errorf("finding own process: %w", err)
	}
	return p.Signal(syscall.SIGTERM)
}

// handleShutdownSignals starts a goroutine that cancels the context on SIGINT/SIGTERM.
func handleShutdownSignals(cancel context.CancelFunc, logger *slog.Logger) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("received signal, shutting down", "signal", sig)
		cancel()
	}()
}

// kvCleanupDuration parses a duration string and falls back to 1 hour.
func kvCleanupDuration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	if d <= 0 {
		return time.Hour
	}
	return d
}

// newBrowserProfiles creates a ProfileService when browser automation is
// enabled and a profile directory was resolved. Returns nil otherwise.
func newBrowserProfiles(cfg *config.Config, profileDir string, logger *slog.Logger) *browser.ProfileService {
	if cfg.Browser.Enabled && profileDir != "" {
		return browser.NewProfileService(profileDir, logger)
	}
	return nil
}

// loadGlobalSkills loads skills from the given directory, logging warnings on
// error. Returns nil if no skills are found or on error.
func loadGlobalSkills(dir string, logger *slog.Logger) []skill.Skill {
	skills, err := skill.LoadDir(dir, logger)
	if err != nil {
		logger.Warn("global skill loading error", "dir", dir, "error", err)
		return nil
	}
	if len(skills) > 0 {
		logger.Info("global skills loaded", "dir", dir, "count", len(skills))
	}
	return skills
}

// initAPIAuth sets up session-based auth (password + OIDC) on the API deps when configured.
func initAPIAuth(ctx context.Context, cfg *config.Config, deps *api.Deps, logger *slog.Logger) error {
	auth := cfg.API.Auth
	hasPassword := auth.PasswordHash != ""
	hasOIDC := auth.OIDC.Enabled

	if !hasPassword && !hasOIDC {
		return nil
	}

	maxAge, _ := time.ParseDuration(auth.SessionMaxAge) // validated in config
	secure := cfg.API.TLS                               // Secure cookies only when TLS is enabled
	sm, err := api.NewSessionManager(auth.SessionSecret, maxAge, secure)
	if err != nil {
		return fmt.Errorf("creating session manager: %w", err)
	}

	// Set up server-tracked session store.
	sessDBPath := filepath.Join(cfg.DataDir, "data", "sessions.db")
	sessStore, err := api.NewSessionStore(sessDBPath)
	if err != nil {
		return fmt.Errorf("creating session store: %w", err)
	}
	sm.Store = sessStore

	// Purge expired sessions periodically.
	go func() { //nolint:gosec // G118: long-running background goroutine is intentionally not request-scoped
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if n, purgeErr := sessStore.PurgeExpired(context.Background()); purgeErr != nil {
				logger.Warn("session purge failed", "error", purgeErr)
			} else if n > 0 {
				logger.Info("purged expired sessions", "count", n)
			}
		}
	}()

	deps.Sessions = sm

	if hasPassword {
		deps.PasswordHash = auth.PasswordHash
		logger.Info("dashboard password login enabled")
	}

	if hasOIDC {
		oidcCfg := auth.OIDC
		provider, oidcErr := api.NewOIDCProvider(ctx,
			oidcCfg.Issuer, oidcCfg.ClientID, oidcCfg.ClientSecret,
			oidcCfg.RedirectURL, oidcCfg.Scopes, oidcCfg.AllowedEmails,
			sm, logger)
		if oidcErr != nil {
			return fmt.Errorf("initializing OIDC provider: %w", oidcErr)
		}
		deps.OIDCProvider = provider
		logger.Info("dashboard OIDC login enabled", "issuer", oidcCfg.Issuer)
	}

	return nil
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

// startMemoryCleanupWorker runs a background goroutine that enforces
// retention policies (time-based and count-based) on stored conversations.
// No-op if the memory store does not support telemetry (PruneByCount).
func startMemoryCleanupWorker(ctx context.Context, mem agent.MemoryStore, cfg *config.MemoryConfig, logger *slog.Logger) {
	store, ok := mem.(*agent.SQLiteMemoryStore)
	if !ok {
		return
	}
	interval, err := time.ParseDuration(cfg.CleanupInterval)
	if err != nil {
		interval = time.Hour
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runMemoryCleanup(ctx, store, cfg, logger)
			}
		}
	}()
}

func runMemoryCleanup(ctx context.Context, store *agent.SQLiteMemoryStore, cfg *config.MemoryConfig, logger *slog.Logger) {
	if cfg.RetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(cfg.RetentionDays) * 24 * time.Hour)
		n, err := store.PruneConversations(ctx, cutoff)
		if err != nil {
			logger.Warn("memory retention prune failed", "error", err)
		} else if n > 0 {
			logger.Info("pruned old conversations", "count", n, "retention_days", cfg.RetentionDays)
		}
	}
	if cfg.MaxConversations > 0 {
		n, err := store.PruneByCount(ctx, cfg.MaxConversations)
		if err != nil {
			logger.Warn("memory count prune failed", "error", err)
		} else if n > 0 {
			logger.Info("pruned excess conversations", "count", n, "max", cfg.MaxConversations)
		}
	}
}

// initAuditor creates the audit emitter and returns it along with a cleanup function.
func initAuditor(ctx context.Context, store *audit.SQLiteStore, cfg *config.Config, logger *slog.Logger) (audit.Emitter, func()) {
	if store == nil {
		return audit.NopEmitter{}, func() {}
	}
	be := audit.NewBufferedEmitter(store, cfg.Audit.BufferSize, logger)
	be.Start(ctx)
	startAuditCleanupWorker(ctx, store, cfg.Audit.RetentionDays, cfg.Audit.CleanupInterval, logger)
	return be, be.Close
}

// startAuditCleanupWorker runs periodic retention enforcement on the audit store.
func startAuditCleanupWorker(ctx context.Context, store *audit.SQLiteStore, retentionDays int, interval string, logger *slog.Logger) {
	if retentionDays <= 0 {
		return // unlimited retention
	}
	dur, err := time.ParseDuration(interval)
	if err != nil {
		dur = time.Hour
	}

	go func() {
		ticker := time.NewTicker(dur)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cutoff := time.Now().UTC().Add(-time.Duration(retentionDays) * 24 * time.Hour)
				n, pruneErr := store.PruneBefore(ctx, cutoff)
				if pruneErr != nil {
					logger.Warn("audit retention prune failed", "error", pruneErr)
				} else if n > 0 {
					logger.Info("pruned audit events", "count", n, "retention_days", retentionDays)
				}
			}
		}
	}()
}
